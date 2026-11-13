package core

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	key, err := s.newAccessKey("existing tests")
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler("")
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") && r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+key.Key)
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(h.Close)
	return s, h
}

func saveTestChannel(t *testing.T, s *Server, c Channel) Channel {
	t.Helper()
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func runSpeedTest(t *testing.T, h *httptest.Server, id int64) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post(h.URL+"/api/channels/"+strconv.FormatInt(id, 10)+"/speedtest", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

type speedResponse struct {
	DurationMS int    `json:"duration_ms"`
	Status     int    `json:"status"`
	Model      string `json:"model"`
}

func decodeSpeedResponse(t *testing.T, b []byte) speedResponse {
	t.Helper()
	var out speedResponse
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("invalid speed response %q: %v", b, err)
	}
	return out
}

func errorMessage(t *testing.T, b []byte) string {
	t.Helper()
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("invalid error response %q: %v", b, err)
	}
	return out.Error.Message
}

func TestChannelPersistenceAndSecret(t *testing.T) {
	s, h := newTestServer(t)
	c := Channel{Name: "one", BaseURL: "http://localhost:1000", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true}
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	res, err := http.Get(h.URL + "/api/channels")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if strings.Contains(string(b), "secret") {
		t.Fatal("secret leaked")
	}
	var cs []Channel
	if err = json.Unmarshal(b, &cs); err != nil || len(cs) != 1 {
		t.Fatalf("%s %v", b, err)
	}
}
func TestFailoverAndStreaming(t *testing.T) {
	var hits int
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/v1/chat/completions" {
			t.Error(r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		if !bytes.Contains(b, []byte(`"model":"upstream-deepseek"`)) {
			t.Error(string(b))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: hello\n\ndata: [DONE]\n\n")
	}))
	defer good.Close()
	s, h := newTestServer(t)
	for i, u := range []string{bad.URL + "/v1", good.URL + "/v1"} {
		c := Channel{Name: u, BaseURL: u, APIKey: "k", Model: "deepseek-flash", RemoteModel: "upstream-deepseek", Enabled: true, Priority: i}
		if err := s.SaveChannel(&c); err != nil {
			t.Fatal(err)
		}
	}
	res, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","stream":true,"messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(b), "[DONE]") || hits != 1 {
		t.Fatalf("%d %q hits %d", res.StatusCode, b, hits)
	}
}
func TestRejectOtherModel(t *testing.T) {
	_, h := newTestServer(t)
	r, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"gpt-test","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatalf("%d", r.StatusCode)
	}
}
func TestPaidChannelRequiresOptIn(t *testing.T) {
	s, h := newTestServer(t)
	c := Channel{Name: "paid", BaseURL: "http://localhost:1/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Paid: true}
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	r, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 503 {
		t.Fatalf("%d", r.StatusCode)
	}
}
func TestSearchForwarding(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/tools/search" || r.Header.Get("Authorization") != "Bearer search-key" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"results":[{"title":"result"}]}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := Channel{Name: "official", BaseURL: upstream.URL + "/v1", APIKey: "search-key", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Paid: true, AutoPaid: true}
	if e := s.SaveChannel(&c); e != nil {
		t.Fatal(e)
	}
	if got := historyRaw(t, h); strings.TrimSpace(string(got)) != "[]" {
		t.Fatalf("unexpected request history before search: %s", got)
	}
	res, e := http.Post(h.URL+"/v1/tools/search", "application/json", strings.NewReader(`{"query":"DeepSeek"}`))
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || calls != 1 || !strings.Contains(string(b), "result") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if got := historyRaw(t, h); strings.TrimSpace(string(got)) != "[]" {
		t.Fatalf("search wrote request history: %s", got)
	}
}
func TestEditingRetainsCredentialAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, e := New(path)
	if e != nil {
		t.Fatal(e)
	}
	c := Channel{Name: "old", BaseURL: "http://localhost:1/v1", APIKey: "keep", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true}
	if e = s.SaveChannel(&c); e != nil {
		t.Fatal(e)
	}
	c.Name = "new"
	c.APIKey = ""
	if e = s.SaveChannel(&c); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = New(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	cs, e := s.channels()
	if e != nil || len(cs) != 1 || cs[0].APIKey != "keep" || cs[0].Name != "new" {
		t.Fatalf("%+v %v", cs, e)
	}
}

type failingReader struct{ sent bool }

func (f *failingReader) Read(p []byte) (int, error) {
	if !f.sent {
		f.sent = true
		return copy(p, "partial"), nil
	}
	return 0, io.ErrUnexpectedEOF
}
func TestInterruptedStreamIsNotReportedAsComplete(t *testing.T) {
	w := httptest.NewRecorder()
	if err := copyStream(w, &failingReader{}); err == nil {
		t.Fatal("stream interruption was not reported")
	}
}
func TestSessionAffinityFollowsSuccessfulFallback(t *testing.T) {
	var a, b int
	firstHealthy := true
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a++
		if !firstHealthy {
			w.WriteHeader(429)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { b++; io.WriteString(w, `{"ok":true}`) }))
	defer second.Close()
	s, h := newTestServer(t)
	for i, u := range []string{first.URL + "/v1", second.URL + "/v1"} {
		c := Channel{Name: u, BaseURL: u, APIKey: "x", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Priority: i}
		if e := s.SaveChannel(&c); e != nil {
			t.Fatal(e)
		}
	}
	call := func() {
		req, _ := http.NewRequest("POST", h.URL+"/v1/chat/completions", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
		req.Header.Set("X-DeepSeek-Harness-Session-Id", "session-1")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode)
		}
	}
	call()
	firstHealthy = false
	call()
	firstHealthy = true
	call()
	if a != 2 || b != 2 {
		t.Fatalf("session did not stay on successful fallback: first=%d second=%d", a, b)
	}
}
func TestToolCallsUseOnlyConfiguredCapabilities(t *testing.T) {
	var unsupported, supported int
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { unsupported++; io.WriteString(w, `{}`) }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { supported++; io.WriteString(w, `{}`) }))
	defer second.Close()
	s, h := newTestServer(t)
	for i, u := range []string{first.URL + "/v1", second.URL + "/v1"} {
		c := Channel{Name: u, BaseURL: u, APIKey: "x", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Priority: i, SupportsTools: i == 1}
		if e := s.SaveChannel(&c); e != nil {
			t.Fatal(e)
		}
	}
	resp, e := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[],"tools":[{"type":"function","function":{"name":"test"}}]}`))
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || unsupported != 0 || supported != 1 {
		t.Fatalf("status=%d unsupported=%d supported=%d", resp.StatusCode, unsupported, supported)
	}
}
func TestUnconfirmedCapabilitiesAreNotRouted(t *testing.T) {
	s, h := newTestServer(t)
	c := Channel{Name: "no-caps", BaseURL: "http://localhost:1/v1", APIKey: "x", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true}
	if e := s.SaveChannel(&c); e != nil {
		t.Fatal(e)
	}
	cases := []struct{ path, body string }{
		{"/v1/responses", `{"model":"deepseek-flash","input":"hello"}`},
		{"/v1/chat/completions", `{"model":"deepseek-flash","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA"}}]}]}`},
	}
	for _, tc := range cases {
		r, e := http.Post(h.URL+tc.path, "application/json", strings.NewReader(tc.body))
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 503 {
			t.Errorf("%s got %d", tc.path, r.StatusCode)
		}
	}
}
func TestAnthropicMessagesPassThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/anthropic/v1/messages" || r.Header.Get("x-api-key") != "k" {
			t.Errorf("unexpected upstream request %s", r.URL.Path)
		}
		io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"ok"}]}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := Channel{Name: "official", BaseURL: upstream.URL + "/v1", MessagesBaseURL: upstream.URL + "/anthropic/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, SupportsMessages: true}
	if e := s.SaveChannel(&c); e != nil {
		t.Fatal(e)
	}
	resp, e := http.Post(h.URL+"/v1/messages", "application/json", strings.NewReader(`{"model":"deepseek-flash","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`))
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"text":"ok"`) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}
func TestOpenCodeGoSessionAndClientIdentity(t *testing.T) {
	var seen string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("x-opencode-session")
		if r.Header.Get("User-Agent") != "SeekWell/0.1" {
			t.Errorf("invalid client identity: %s", r.Header.Get("User-Agent"))
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := Channel{Name: "test", BaseURL: upstream.URL + "/v1", APIKey: "x", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true, Kind: "opencode_go"}
	if e := s.SaveChannel(&c); e != nil {
		t.Fatal(e)
	}
	send := func(session string) {
		req, _ := http.NewRequest("POST", h.URL+"/v1/chat/completions", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
		if session != "" {
			req.Header.Set("X-DeepSeek-Harness-Session-Id", session)
		}
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	send("dsh-session")
	if seen != "dsh-session" {
		t.Fatalf("session header = %q", seen)
	}
	send("")
	if seen == "" {
		t.Fatal("missing generated session")
	}
}

func TestOpenCodeNativeSessionHeader(t *testing.T) {
	var seen string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("x-opencode-session")
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := Channel{Name: "test", BaseURL: upstream.URL + "/v1", APIKey: "x", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true, Kind: "opencode_go"}
	if e := s.SaveChannel(&c); e != nil {
		t.Fatal(e)
	}
	req, _ := http.NewRequest("POST", h.URL+"/v1/chat/completions", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	req.Header.Set("X-Session-ID", "native-session")
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if seen != "native-session" {
		t.Fatalf("session header = %q", seen)
	}
}
func TestDiscoverDeepSeekModelsWithoutLeakingCredentials(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("unexpected request")
		}
		io.WriteString(w, `{"data":[{"id":"deepseek-v4.1-flash"},{"id":"gpt-test"},{"id":"deepseek-v4-pro"}]}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := Channel{Name: "test", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true, Kind: "opencode_go"}
	if e := s.SaveChannel(&c); e != nil {
		t.Fatal(e)
	}
	resp, e := http.Post(h.URL+"/api/channels/1/discover", "application/json", strings.NewReader(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte("deepseek-v4-pro")) || bytes.Contains(b, []byte("gpt-test")) || bytes.Contains(b, []byte("secret")) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

func TestSpeedTestReturnsDurationAndStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "test", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "upstream-deepseek", Enabled: true})
	resp, b := runSpeedTest(t, h, c.ID)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	got := decodeSpeedResponse(t, b)
	if got.DurationMS < 0 || got.Status != 200 || got.Model != c.RemoteModel {
		t.Fatalf("unexpected speed result: %+v", got)
	}
}

func TestSpeedTestSendsRealCompletionRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected authorization %q", r.Header.Get("Authorization"))
		}
		var payload struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			Stream    bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("invalid request body: %v", err)
		}
		if payload.Model != "upstream-deepseek" || payload.MaxTokens != 1 || payload.Stream {
			t.Errorf("unexpected payload: %+v", payload)
		}
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "test", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "upstream-deepseek", Enabled: true})
	resp, b := runSpeedTest(t, h, c.ID)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
}

func TestSpeedTestReportsUpstreamFailureAsResult(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":{"message":"down"}}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "test", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "upstream-deepseek", Enabled: true})
	resp, b := runSpeedTest(t, h, c.ID)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	got := decodeSpeedResponse(t, b)
	if got.Status != http.StatusServiceUnavailable || got.DurationMS < 0 {
		t.Fatalf("unexpected speed result: %+v", got)
	}
}

func TestSpeedTestTimesOutSeparately(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	s.speedTimeout = 50 * time.Millisecond
	c := saveTestChannel(t, s, Channel{Name: "test", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "upstream-deepseek", Enabled: true})
	resp, b := runSpeedTest(t, h, c.ID)
	if resp.StatusCode != 502 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	message := errorMessage(t, b)
	if !strings.Contains(message, "超时") || !strings.Contains(message, "已耗时") {
		t.Fatalf("unexpected timeout message %q", message)
	}
}

func TestSpeedTestOpenCodeGoHeaders(t *testing.T) {
	sessions := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessions = append(sessions, r.Header.Get("x-opencode-session"))
		if r.Header.Get("User-Agent") != "SeekWell/0.1" {
			t.Errorf("unexpected user agent %q", r.Header.Get("User-Agent"))
		}
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "test", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true, Kind: "opencode_go"})
	for i := 0; i < 2; i++ {
		resp, b := runSpeedTest(t, h, c.ID)
		if resp.StatusCode != 200 {
			t.Fatalf("status %d: %s", resp.StatusCode, b)
		}
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(sessions))
	}
	for _, session := range sessions {
		decoded, err := hex.DecodeString(session)
		if err != nil || len(decoded) != 16 {
			t.Fatalf("invalid session header %q", session)
		}
	}
	if sessions[0] == sessions[1] {
		t.Fatal("speed test reused the same opencode session")
	}
}

func TestSpeedTestIgnoresRoutingGates(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "paid-disabled", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "upstream-deepseek", Enabled: false, Paid: true, AutoPaid: false})
	resp, b := runSpeedTest(t, h, c.ID)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	if got := decodeSpeedResponse(t, b); got.Status != 200 {
		t.Fatalf("unexpected speed result: %+v", got)
	}
}

func TestSpeedTestDoesNotWriteRequestHistory(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "test", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "upstream-deepseek", Enabled: true})
	if resp, b := runSpeedTest(t, h, c.ID); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	resp, err := http.Get(h.URL + "/api/requests")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("speed test wrote request history: %s", b)
	}
}

func TestSpeedTestMissingChannel(t *testing.T) {
	_, h := newTestServer(t)
	resp, b := runSpeedTest(t, h, 999)
	if resp.StatusCode != 404 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	if message := errorMessage(t, b); message != "渠道不存在" {
		t.Fatalf("unexpected message %q", message)
	}
}

func TestSpeedTestConnectionFailure(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "test", BaseURL: closedURL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "upstream-deepseek", Enabled: true})
	resp, b := runSpeedTest(t, h, c.ID)
	if resp.StatusCode != 502 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	message := errorMessage(t, b)
	if !strings.Contains(message, "连接") || !strings.Contains(message, "已耗时") {
		t.Fatalf("unexpected connection message %q", message)
	}
}

type historyEntry struct {
	CreatedAt       string   `json:"created_at"`
	Model           string   `json:"model"`
	Channel         string   `json:"channel"`
	Kind            string   `json:"kind"`
	Status          int      `json:"status"`
	DurationMS      int      `json:"duration_ms"`
	InputTokens     *int64   `json:"input_tokens"`
	OutputTokens    *int64   `json:"output_tokens"`
	CacheHitTokens  *int64   `json:"cache_hit_tokens"`
	CacheMissTokens *int64   `json:"cache_miss_tokens"`
	CostCNY         *float64 `json:"cost_cny"`
	PricePeriod     *string  `json:"price_period"`
}

func historyRaw(t *testing.T, h *httptest.Server) []byte {
	t.Helper()
	resp, err := http.Get(h.URL + "/api/requests")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/requests = %d: %s", resp.StatusCode, b)
	}
	return b
}

func historyEntries(t *testing.T, h *httptest.Server) []historyEntry {
	t.Helper()
	var out []historyEntry
	if err := json.Unmarshal(historyRaw(t, h), &out); err != nil {
		t.Fatalf("invalid history response: %v", err)
	}
	return out
}

func TestRequestHistoryRecordsChannelKind(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "official", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek"})
	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	got := historyEntries(t, h)
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %+v", len(got), got)
	}
	if got[0].Kind != "deepseek" || got[0].Channel != c.Name || got[0].Status != 200 || got[0].Model != "deepseek-flash" {
		t.Fatalf("unexpected history entry: %+v", got[0])
	}
}

func TestRequestHistoryRecordsEachAttemptKind(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true}`) }))
	defer good.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "official", BaseURL: bad.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "opencode", BaseURL: good.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "opencode_go", Priority: 1})
	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	got := historyEntries(t, h)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(got), got)
	}
	if got[0].Channel != "opencode" || got[0].Kind != "opencode_go" || got[0].Status != 200 {
		t.Fatalf("unexpected newest entry: %+v", got[0])
	}
	if got[1].Channel != "official" || got[1].Kind != "deepseek" || got[1].Status != 429 {
		t.Fatalf("unexpected oldest entry: %+v", got[1])
	}
}

func TestRequestHistoryKindIsSnapshot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true}`) }))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "old-name", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek"})
	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	c.Name = "new-name"
	c.Kind = "compatible"
	c.APIKey = ""
	body, _ := json.Marshal(c)
	req, _ := http.NewRequest(http.MethodPut, h.URL+"/api/channels/"+strconv.FormatInt(c.ID, 10), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	updated, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, updated.Body)
	updated.Body.Close()
	if updated.StatusCode != 200 {
		t.Fatalf("update status %d", updated.StatusCode)
	}
	got := historyEntries(t, h)
	if len(got) != 1 || got[0].Channel != "old-name" || got[0].Kind != "deepseek" {
		t.Fatalf("history changed with channel config: %+v", got)
	}
}

func TestOldDatabaseMigratesRequestKind(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true}`) }))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE channels (id INTEGER PRIMARY KEY, name TEXT NOT NULL, base_url TEXT NOT NULL, api_key TEXT NOT NULL, model TEXT NOT NULL, remote_model TEXT NOT NULL, enabled INTEGER NOT NULL, paid INTEGER NOT NULL, auto_paid INTEGER NOT NULL, priority INTEGER NOT NULL, kind TEXT NOT NULL, supports_tools INTEGER NOT NULL DEFAULT 0, supports_images INTEGER NOT NULL DEFAULT 0, supports_responses INTEGER NOT NULL DEFAULT 0, supports_messages INTEGER NOT NULL DEFAULT 0, messages_base_url TEXT NOT NULL DEFAULT '');
CREATE TABLE requests (id INTEGER PRIMARY KEY, created_at TEXT NOT NULL, model TEXT NOT NULL, channel TEXT NOT NULL, status INTEGER NOT NULL, duration_ms INTEGER NOT NULL);`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO channels(id,name,base_url,api_key,model,remote_model,enabled,paid,auto_paid,priority,kind,supports_tools,supports_images,supports_responses,supports_messages,messages_base_url) VALUES(1,'legacy',?,'k','deepseek-flash','deepseek-flash',1,0,0,0,'deepseek',0,0,0,0,'')`, upstream.URL+"/v1")
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO requests(created_at,model,channel,status,duration_ms) VALUES(?,?,?,?,?)`, time.Now().UTC().Format(time.RFC3339), "deepseek-flash", "legacy", 429, 12)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := httptest.NewServer(s.Handler(""))
	defer h.Close()
	raw := historyRaw(t, h)
	if !bytes.Contains(raw, []byte(`"kind":""`)) {
		t.Fatalf("legacy kind is not serialized as empty string: %s", raw)
	}
	got := historyEntries(t, h)
	if len(got) != 1 || got[0].Channel != "legacy" || got[0].Kind != "" || got[0].Status != 429 {
		t.Fatalf("legacy history not preserved: %+v", got)
	}
	for _, column := range []string{"input_tokens", "output_tokens", "cache_hit_tokens", "cache_miss_tokens"} {
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('requests') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("legacy database missing requests.%s: %d %v", column, count, err)
		}
	}
	assertNilToken(t, "legacy input_tokens", got[0].InputTokens)
	assertNilToken(t, "legacy output_tokens", got[0].OutputTokens)
	assertNilToken(t, "legacy cache_hit_tokens", got[0].CacheHitTokens)
	assertNilToken(t, "legacy cache_miss_tokens", got[0].CacheMissTokens)
	key, err := s.newAccessKey("migration test")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", h.URL+"/v1/chat/completions", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+key.Key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	got = historyEntries(t, h)
	if len(got) != 2 || got[0].Kind != "deepseek" || got[0].Status != 200 || got[1].Kind != "" {
		t.Fatalf("unexpected history after migration: %+v", got)
	}
}

func TestEmptyChannelKindStillRecords(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true}`) }))
	defer upstream.Close()
	s, h := newTestServer(t)
	_, err := s.db.Exec(`INSERT INTO channels(name,base_url,api_key,model,remote_model,enabled,paid,auto_paid,priority,kind,supports_tools,supports_images,supports_responses,supports_messages,messages_base_url) VALUES('legacy-kind',?,'k','deepseek-flash','deepseek-flash',1,0,0,0,'',0,0,0,0,'')`, upstream.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	raw := historyRaw(t, h)
	if !bytes.Contains(raw, []byte(`"kind":""`)) {
		t.Fatalf("empty channel kind is not serialized as empty string: %s", raw)
	}
	got := historyEntries(t, h)
	if len(got) != 1 || got[0].Channel != "legacy-kind" || got[0].Kind != "" || got[0].Status != 200 {
		t.Fatalf("unexpected history entry: %+v", got)
	}
}

func TestRequestHistoryKindOnTransportFailure(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "gateway", BaseURL: closedURL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "compatible"})
	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	got := historyEntries(t, h)
	if len(got) != 1 || got[0].Status != 0 || got[0].Kind != "compatible" || got[0].Channel != "gateway" {
		t.Fatalf("unexpected transport failure entry: %+v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type interruptingReader struct{ sent bool }

func (i *interruptingReader) Read(p []byte) (int, error) {
	if !i.sent {
		i.sent = true
		return copy(p, "data: partial\n\n"), nil
	}
	return 0, io.ErrUnexpectedEOF
}
func (i *interruptingReader) Close() error { return nil }

func TestRequestHistoryKindOnInterruptedStream(t *testing.T) {
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "official", BaseURL: "http://upstream.invalid/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek"})
	s.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &interruptingReader{}}, nil
	})}
	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	got := historyEntries(t, h)
	if len(got) != 1 || got[0].Status != 0 || got[0].Kind != "deepseek" {
		t.Fatalf("unexpected interrupted stream entry: %+v", got)
	}
}

func TestRequestHistoryShapeAndLimit(t *testing.T) {
	s, h := newTestServer(t)
	if raw := historyRaw(t, h); strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("empty history must be []: %s", raw)
	}
	for i := 0; i < 105; i++ {
		_, err := s.db.Exec(`INSERT INTO requests(created_at,model,channel,status,duration_ms,kind) VALUES(?,?,?,?,?,?)`, time.Now().UTC().Format(time.RFC3339), "deepseek-flash", "channel", i, i*10, "deepseek")
		if err != nil {
			t.Fatal(err)
		}
	}
	raw := historyRaw(t, h)
	var shaped []map[string]any
	if err := json.Unmarshal(raw, &shaped); err != nil {
		t.Fatalf("invalid history response: %v", err)
	}
	if len(shaped) != 100 {
		t.Fatalf("expected 100 entries, got %d", len(shaped))
	}
	for _, key := range []string{"created_at", "model", "channel", "status", "duration_ms", "kind", "input_tokens", "output_tokens", "cache_hit_tokens", "cache_miss_tokens"} {
		if _, ok := shaped[0][key]; !ok {
			t.Fatalf("history entry missing %q: %v", key, shaped[0])
		}
	}
	for _, key := range []string{"input_tokens", "output_tokens", "cache_hit_tokens", "cache_miss_tokens"} {
		if shaped[0][key] != nil {
			t.Fatalf("history %q must be null when backfilled rows have no usage: %v", key, shaped[0])
		}
	}
	got := historyEntries(t, h)
	if got[0].Status != 104 || got[0].DurationMS != 1040 || got[0].Kind != "deepseek" || got[0].Model != "deepseek-flash" || got[0].Channel != "channel" || got[0].CreatedAt == "" {
		t.Fatalf("unexpected newest entry: %+v", got[0])
	}
	if got[99].Status != 5 {
		t.Fatalf("history is not newest-first or limited to 100: %+v", got[99])
	}
}

type modelTestResponse struct {
	Channel    string          `json:"channel"`
	Model      string          `json:"model"`
	Protocol   string          `json:"protocol"`
	Status     int             `json:"status"`
	DurationMS int             `json:"duration_ms"`
	Body       json.RawMessage `json:"body"`
	Truncated  bool            `json:"truncated"`
}

func runModelTest(t *testing.T, h *httptest.Server, id int64, payload string) (*http.Response, []byte) {
	t.Helper()
	return runModelTestWithHeaders(t, h, id, payload, nil)
}

func runModelTestWithHeaders(t *testing.T, h *httptest.Server, id int64, payload string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.URL+"/api/channels/"+strconv.FormatInt(id, 10)+"/test", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

func decodeModelTestResponse(t *testing.T, b []byte) modelTestResponse {
	t.Helper()
	var out modelTestResponse
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("invalid model test response %q: %v", b, err)
	}
	return out
}

func TestModelTestChatSuccess(t *testing.T) {
	var gotPath, gotAuth, gotContentType string
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("invalid upstream body: %v", err)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "official", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	resp, b := runModelTest(t, h, c.ID, `{"protocol":"chat","model":"deepseek-flash","prompt":"你好"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	got := decodeModelTestResponse(t, b)
	if got.Status != 200 || got.DurationMS < 0 || got.Channel != c.Name || got.Model != "deepseek-flash" || got.Protocol != "chat" || got.Truncated {
		t.Fatalf("unexpected model test result: %+v", got)
	}
	if !bytes.Contains(got.Body, []byte(`"choices"`)) || !bytes.Contains(got.Body, []byte(`"content":"ok"`)) {
		t.Fatalf("body was not passed through: %s", got.Body)
	}
	if gotPath != "/v1/chat/completions" || gotAuth != "Bearer secret" || gotContentType != "application/json" {
		t.Fatalf("unexpected upstream request path=%q auth=%q content-type=%q", gotPath, gotAuth, gotContentType)
	}
	if gotBody["model"] != "deepseek-flash" || gotBody["stream"] != false {
		t.Fatalf("unexpected upstream payload: %+v", gotBody)
	}
	messages, ok := gotBody["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("unexpected messages payload: %+v", gotBody["messages"])
	}
	message, _ := messages[0].(map[string]any)
	if message["role"] != "user" || message["content"] != "你好" {
		t.Fatalf("unexpected message payload: %+v", message)
	}
}

func TestModelTestResponsesShape(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		io.WriteString(w, `{"output":"ok"}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "official", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	resp, b := runModelTest(t, h, c.ID, `{"protocol":"responses","model":"deepseek-flash","prompt":"你好"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	got := decodeModelTestResponse(t, b)
	if got.Status != 200 || got.Protocol != "responses" {
		t.Fatalf("unexpected model test result: %+v", got)
	}
	if gotPath != "/v1/responses" || gotBody["input"] != "你好" || gotBody["stream"] != false || gotBody["model"] != "deepseek-flash" {
		t.Fatalf("unexpected upstream request path=%q body=%+v", gotPath, gotBody)
	}
	if !bytes.Contains(got.Body, []byte(`"output":"ok"`)) {
		t.Fatalf("body was not passed through: %s", got.Body)
	}
}

func TestModelTestMessagesShapeAndHeaders(t *testing.T) {
	paths := []string{}
	versions := []string{}
	betas := []string{}
	keys := []string{}
	bodies := []map[string]any{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		versions = append(versions, r.Header.Get("anthropic-version"))
		betas = append(betas, r.Header.Get("anthropic-beta"))
		keys = append(keys, r.Header.Get("x-api-key"))
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		io.WriteString(w, `{"type":"message"}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "official", BaseURL: upstream.URL + "/v1", MessagesBaseURL: upstream.URL + "/anthropic/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, SupportsMessages: true})
	payload := `{"protocol":"messages","model":"deepseek-flash","prompt":"你好"}`
	if resp, b := runModelTest(t, h, c.ID, payload); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	if resp, b := runModelTestWithHeaders(t, h, c.ID, payload, map[string]string{"anthropic-version": "2024-01-01", "anthropic-beta": "x"}); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	if len(paths) != 2 || paths[0] != "/anthropic/v1/messages" || paths[1] != "/anthropic/v1/messages" {
		t.Fatalf("unexpected upstream paths: %+v", paths)
	}
	if versions[0] != "2023-06-01" || versions[1] != "2024-01-01" {
		t.Fatalf("unexpected anthropic versions: %+v", versions)
	}
	if betas[0] != "" || betas[1] != "x" {
		t.Fatalf("unexpected anthropic betas: %+v", betas)
	}
	if keys[0] != "secret" || keys[1] != "secret" {
		t.Fatalf("unexpected x-api-key values: %+v", keys)
	}
	for _, body := range bodies {
		if body["max_tokens"] != float64(512) || body["model"] != "deepseek-flash" {
			t.Fatalf("unexpected messages payload: %+v", body)
		}
		messages, ok := body["messages"].([]any)
		if !ok || len(messages) != 1 {
			t.Fatalf("unexpected messages payload: %+v", body)
		}
		message, _ := messages[0].(map[string]any)
		if message["content"] != "你好" {
			t.Fatalf("unexpected message content: %+v", message)
		}
		if _, ok := body["stream"]; ok {
			t.Fatalf("messages test must not send stream: %+v", body)
		}
	}
}

func TestModelTestOpenCodeGoSessionAndIdentity(t *testing.T) {
	sessions := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessions = append(sessions, r.Header.Get("x-opencode-session"))
		if r.Header.Get("User-Agent") != "SeekWell/0.1" {
			t.Errorf("unexpected user agent %q", r.Header.Get("User-Agent"))
		}
		io.WriteString(w, `{"choices":[]}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "opencode", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true, Kind: "opencode_go"})
	for i := 0; i < 2; i++ {
		if resp, b := runModelTest(t, h, c.ID, `{"protocol":"chat","model":"deepseek-v4.1-flash","prompt":"hi"}`); resp.StatusCode != 200 {
			t.Fatalf("status %d: %s", resp.StatusCode, b)
		}
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(sessions))
	}
	for _, session := range sessions {
		decoded, err := hex.DecodeString(session)
		if err != nil || len(decoded) != 16 {
			t.Fatalf("invalid session header %q", session)
		}
	}
	if sessions[0] == sessions[1] {
		t.Fatal("model test reused the same opencode session")
	}
}

func TestModelTestDoesNotLeakSecret(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer super-secret" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected headers auth=%q content-type=%q", r.Header.Get("Authorization"), r.Header.Get("Content-Type"))
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "official", BaseURL: upstream.URL + "/v1", APIKey: "super-secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	resp, b := runModelTest(t, h, c.ID, `{"protocol":"chat","model":"deepseek-flash","prompt":"hi"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	if bytes.Contains(b, []byte("super-secret")) {
		t.Fatalf("model test response leaked the api key: %s", b)
	}
}

func TestModelTestIgnoresRoutingGates(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "gated", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: false, Paid: true, AutoPaid: false})
	if resp, b := runModelTest(t, h, c.ID, `{"protocol":"chat","model":"deepseek-flash","prompt":"hi"}`); resp.StatusCode != 200 {
		t.Fatalf("chat status %d: %s", resp.StatusCode, b)
	}
	c.MessagesBaseURL = upstream.URL + "/anthropic/v1"
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	if resp, b := runModelTest(t, h, c.ID, `{"protocol":"messages","model":"deepseek-flash","prompt":"hi"}`); resp.StatusCode != 200 {
		t.Fatalf("messages status %d: %s", resp.StatusCode, b)
	}
}

func TestModelTestUpstreamFailureIsAResult(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":{"message":"up"}}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "official", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	resp, b := runModelTest(t, h, c.ID, `{"protocol":"chat","model":"deepseek-flash","prompt":"hi"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	got := decodeModelTestResponse(t, b)
	if got.Status != http.StatusServiceUnavailable || !bytes.Contains(got.Body, []byte("up")) || got.DurationMS < 0 {
		t.Fatalf("unexpected model test result: %+v", got)
	}
}

func TestModelTestMissingChannel(t *testing.T) {
	_, h := newTestServer(t)
	resp, b := runModelTest(t, h, 999, `{"protocol":"chat","model":"deepseek-flash","prompt":"hi"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	if message := errorMessage(t, b); message != "渠道不存在" {
		t.Fatalf("unexpected message %q", message)
	}
}

func TestModelTestConnectionFailure(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "official", BaseURL: closedURL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	resp, b := runModelTest(t, h, c.ID, `{"protocol":"chat","model":"deepseek-flash","prompt":"hi"}`)
	if resp.StatusCode != 502 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	message := errorMessage(t, b)
	if !strings.Contains(message, "连接") || !strings.Contains(message, "已耗时") {
		t.Fatalf("unexpected connection message %q", message)
	}
}

func TestModelTestTimesOutSeparately(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	s.testTimeout = 50 * time.Millisecond
	c := saveTestChannel(t, s, Channel{Name: "official", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	start := time.Now()
	resp, b := runModelTest(t, h, c.ID, `{"protocol":"chat","model":"deepseek-flash","prompt":"hi"}`)
	elapsed := time.Since(start)
	if resp.StatusCode != 504 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	if message := errorMessage(t, b); !strings.Contains(message, "超时") || !strings.Contains(message, "已耗时") {
		t.Fatalf("unexpected timeout message %q", message)
	}
	if elapsed >= time.Second {
		t.Fatalf("model test did not honor its own timeout: %s", elapsed)
	}
}

func TestModelTestAllowsUnmappedModel(t *testing.T) {
	var received string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		received, _ = body["model"].(string)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "opencode", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
	resp, b := runModelTest(t, h, c.ID, `{"protocol":"chat","model":"deepseek-v4-pro","prompt":"hi"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	if got := decodeModelTestResponse(t, b); got.Model != "deepseek-v4-pro" {
		t.Fatalf("unexpected echoed model: %+v", got)
	}
	if received != "deepseek-v4-pro" {
		t.Fatalf("upstream received model %q", received)
	}
}

func TestModelTestValidationMatrix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "official", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	cases := []struct {
		name    string
		payload string
		reason  string
	}{
		{"empty prompt", `{"protocol":"chat","model":"deepseek-flash","prompt":""}`, "提示词"},
		{"blank prompt", `{"protocol":"chat","model":"deepseek-flash","prompt":"   "}`, "提示词"},
		{"oversized prompt", `{"protocol":"chat","model":"deepseek-flash","prompt":"` + strings.Repeat("x", 33*1024) + `"}`, "提示词"},
		{"empty model", `{"protocol":"chat","model":"","prompt":"hi"}`, "模型"},
		{"non-deepseek model", `{"protocol":"chat","model":"gpt-4o","prompt":"hi"}`, "模型"},
		{"unknown protocol", `{"protocol":"embeddings","model":"deepseek-flash","prompt":"hi"}`, "协议"},
		{"messages without address", `{"protocol":"messages","model":"deepseek-flash","prompt":"hi"}`, "Messages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, b := runModelTest(t, h, c.ID, tc.payload)
			if resp.StatusCode != 400 {
				t.Fatalf("status %d: %s", resp.StatusCode, b)
			}
			message := errorMessage(t, b)
			if !strings.Contains(message, tc.reason) {
				t.Fatalf("error %q does not mention %q", message, tc.reason)
			}
		})
	}
	if resp, b := runModelTest(t, h, c.ID, `{"protocol":"chat","model":"deepseek","prompt":"hi"}`); resp.StatusCode != 200 {
		t.Fatalf("minimal valid model was rejected: %d %s", resp.StatusCode, b)
	}
}

func TestModelTestTruncatesLargeResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("x", 2<<20))
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "official", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	resp, b := runModelTest(t, h, c.ID, `{"protocol":"chat","model":"deepseek-flash","prompt":"hi"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	got := decodeModelTestResponse(t, b)
	if !got.Truncated || got.Status != 200 {
		t.Fatalf("unexpected model test result: %+v", got)
	}
	if len(b) >= 3<<19 {
		t.Fatalf("model test response is not bounded: %d bytes", len(b))
	}
}

func TestModelTestDoesNotWriteRequestHistory(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "official", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	if resp, b := runModelTest(t, h, c.ID, `{"protocol":"chat","model":"deepseek-flash","prompt":"hi"}`); resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	if got := historyRaw(t, h); strings.TrimSpace(string(got)) != "[]" {
		t.Fatalf("model test wrote request history: %s", got)
	}
}

func TestModelTestConcurrentRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, Channel{Name: "official", BaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	results := make(chan string, 2)
	for i := 0; i < 2; i++ {
		go func() {
			req, _ := http.NewRequest(http.MethodPost, h.URL+"/api/channels/"+strconv.FormatInt(c.ID, 10)+"/test", strings.NewReader(`{"protocol":"chat","model":"deepseek-flash","prompt":"hi"}`))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				results <- err.Error()
				return
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			results <- strconv.Itoa(resp.StatusCode) + " " + string(b)
		}()
	}
	for i := 0; i < 2; i++ {
		if result := <-results; !strings.HasPrefix(result, "200 ") {
			t.Fatalf("concurrent model test failed: %s", result)
		}
	}
}

func postJSON(t *testing.T, url, body string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

func writeDimAuth(t *testing.T, token string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DIMCODE_HOME", dir)
	data := fmt.Sprintf(`{"nextApiOauth":{"type":"oauth","access":%q,"expires":%d}}`, token, time.Now().Add(time.Hour).UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func dimAgentDraft(name, baseURL, kind string) Channel {
	c := Channel{Name: name, BaseURL: baseURL, Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true, Paid: true, Kind: kind}
	if kind == "dimagent" {
		c.AuthMode = "cli_oauth"
	} else {
		c.AuthMode = "api_key"
		c.APIKey = "secret-key"
	}
	return c
}

func TestDimAgentKindRoundTripAndBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dimagent.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	c := dimAgentDraft("DimAgent", "https://dimagent.cn/v1", "dimagent")
	if err = s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	if c.ID <= 0 {
		t.Fatalf("channel was not persisted: %+v", c)
	}
	h := httptest.NewServer(s.Handler(""))
	resp, err := http.Get(h.URL + "/api/channels")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || bytes.Contains(raw, []byte("secret-key")) || bytes.Contains(raw, []byte(`"api_key":`)) {
		t.Fatalf("unexpected channels response %d: %s", resp.StatusCode, raw)
	}
	var listed []Channel
	if err = json.Unmarshal(raw, &listed); err != nil || len(listed) != 1 {
		t.Fatalf("invalid channels response %q: %v", raw, err)
	}
	if listed[0].Kind != "dimagent" || listed[0].HasKey || listed[0].AuthMode != "cli_oauth" || listed[0].APIKey != "" {
		t.Fatalf("dimagent kind did not round-trip: %+v", listed[0])
	}
	h.Close()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	persisted, err := s.channels()
	if err != nil || len(persisted) != 1 || persisted[0].Kind != "dimagent" {
		t.Fatalf("dimagent kind did not survive restart: %+v %v", persisted, err)
	}

	h = httptest.NewServer(s.Handler(""))
	defer h.Close()
	created, err := http.Post(h.URL+"/api/channels", "application/json", strings.NewReader(`{"name":"api-created","base_url":"https://dimagent.cn/v1","api_key":"api-key","model":"deepseek-flash","remote_model":"deepseek-v4.1-flash","enabled":true,"paid":true,"kind":"dimagent"}`))
	if err != nil {
		t.Fatal(err)
	}
	createdBody, _ := io.ReadAll(created.Body)
	created.Body.Close()
	if created.StatusCode != 201 || !bytes.Contains(createdBody, []byte(`"kind":"dimagent"`)) || !bytes.Contains(createdBody, []byte(`"auth_mode":"cli_oauth"`)) || !bytes.Contains(createdBody, []byte(`"has_key":false`)) || bytes.Contains(createdBody, []byte("api-key")) {
		t.Fatalf("POST /api/channels did not normalize dimagent: %d %s", created.StatusCode, createdBody)
	}
	var createdMode, createdKey string
	if err := s.db.QueryRow(`SELECT auth_mode,api_key FROM channels WHERE name='api-created'`).Scan(&createdMode, &createdKey); err != nil || createdMode != "cli_oauth" || createdKey != "" {
		t.Fatalf("POST persisted old credential: mode=%q key=%q err=%v", createdMode, createdKey, err)
	}
	if _, err := s.db.Exec(`UPDATE channels SET auth_mode='api_key',api_key='old-key' WHERE id=?`, c.ID); err != nil {
		t.Fatal(err)
	}
	c.AuthMode = "cli_oauth"
	c.APIKey = ""
	updateBody, _ := json.Marshal(c)
	updateReq, _ := http.NewRequest(http.MethodPut, h.URL+"/api/channels/"+strconv.FormatInt(c.ID, 10), bytes.NewReader(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updated, err := http.DefaultClient.Do(updateReq)
	if err != nil {
		t.Fatal(err)
	}
	updatedBody, _ := io.ReadAll(updated.Body)
	updated.Body.Close()
	if updated.StatusCode != 200 || !bytes.Contains(updatedBody, []byte(`"kind":"dimagent"`)) {
		t.Fatalf("PUT /api/channels rejected dimagent: %d %s", updated.StatusCode, updatedBody)
	}

	empty := dimAgentDraft("empty-kind", "https://dimagent.cn/v1", "")
	if err = s.SaveChannel(&empty); err != nil || empty.Kind != "compatible" {
		t.Fatalf("empty kind should default to compatible: %+v %v", empty, err)
	}
	for _, kind := range []string{"DimAgent", "gemini", " dimagent", "dimagent "} {
		rejected := dimAgentDraft("rejected-"+kind, "https://dimagent.cn/v1", kind)
		err = s.SaveChannel(&rejected)
		if err == nil || !strings.Contains(err.Error(), "不支持的渠道类型") {
			t.Errorf("kind %q should be rejected, got %v", kind, err)
		}
	}
}

func TestDimAgentKindWhitelistRegression(t *testing.T) {
	s, _ := newTestServer(t)
	for _, kind := range []string{"compatible_plus", "d imagent"} {
		c := dimAgentDraft("rejected-"+kind, "https://dimagent.cn/v1", kind)
		if err := s.SaveChannel(&c); err == nil || !strings.Contains(err.Error(), "不支持的渠道类型") {
			t.Errorf("kind %q should be rejected, got %v", kind, err)
		}
	}
	for _, kind := range []string{"deepseek", "opencode_go", "compatible"} {
		c := dimAgentDraft("accepted-"+kind, "https://dimagent.cn/v1", kind)
		if err := s.SaveChannel(&c); err != nil {
			t.Errorf("kind %q should still be accepted: %v", kind, err)
		}
	}
}

func TestDimAgentCapabilityGates(t *testing.T) {
	writeDimAuth(t, "test-oauth-token")
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, dimAgentDraft("DimAgent", upstream.URL+"/v1", "dimagent"))
	save := func() {
		t.Helper()
		c.APIKey = ""
		if err := s.SaveChannel(&c); err != nil {
			t.Fatal(err)
		}
	}
	chatWithTools := `{"model":"deepseek-flash","messages":[],"tools":[{"type":"function","function":{"name":"x"}}]}`
	chatWithImage := `{"model":"deepseek-flash","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA"}}]}]}`
	c.AutoPaid = true
	save()
	if status, body := postJSON(t, h.URL+"/v1/chat/completions", chatWithTools); status != 503 || hits != 0 {
		t.Fatalf("tools without capability: %d %s hits=%d", status, body, hits)
	}
	c.SupportsTools = true
	save()
	if status, body := postJSON(t, h.URL+"/v1/chat/completions", chatWithTools); status != 200 || hits != 1 {
		t.Fatalf("tools with capability: %d %s hits=%d", status, body, hits)
	}
	if status, body := postJSON(t, h.URL+"/v1/responses", `{"model":"deepseek-flash","input":"hi"}`); status != 503 || hits != 1 {
		t.Fatalf("responses without capability: %d %s hits=%d", status, body, hits)
	}
	c.SupportsResponses = true
	save()
	if status, body := postJSON(t, h.URL+"/v1/responses", `{"model":"deepseek-flash","input":"hi"}`); status != 200 || hits != 2 {
		t.Fatalf("responses with capability: %d %s hits=%d", status, body, hits)
	}
	if status, body := postJSON(t, h.URL+"/v1/messages", `{"model":"deepseek-flash","max_tokens":1,"messages":[]}`); status != 503 || hits != 2 {
		t.Fatalf("messages without capability: %d %s hits=%d", status, body, hits)
	}
	c.SupportsMessages = true
	c.MessagesBaseURL = upstream.URL + "/v1"
	save()
	if status, body := postJSON(t, h.URL+"/v1/messages", `{"model":"deepseek-flash","max_tokens":1,"messages":[]}`); status != 200 || hits != 3 {
		t.Fatalf("messages with capability: %d %s hits=%d", status, body, hits)
	}
	if status, body := postJSON(t, h.URL+"/v1/chat/completions", chatWithImage); status != 503 || hits != 3 {
		t.Fatalf("images without capability: %d %s hits=%d", status, body, hits)
	}
	c.SupportsImages = true
	save()
	if status, body := postJSON(t, h.URL+"/v1/chat/completions", chatWithImage); status != 200 || hits != 4 {
		t.Fatalf("images with capability: %d %s hits=%d", status, body, hits)
	}

	missingMessagesURL := dimAgentDraft("no-messages-url", upstream.URL+"/v1", "dimagent")
	missingMessagesURL.SupportsMessages = true
	if err := s.SaveChannel(&missingMessagesURL); err == nil || !strings.Contains(err.Error(), "Messages") {
		t.Fatalf("messages without address should be rejected: %v", err)
	}
	stored, err := s.channels()
	if err != nil || len(stored) != 1 {
		t.Fatalf("rejected channel was persisted: %+v %v", stored, err)
	}
}

func TestDimAgentPaidGate(t *testing.T) {
	writeDimAuth(t, "test-oauth-token")
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, dimAgentDraft("DimAgent", upstream.URL+"/v1", "dimagent"))
	save := func() {
		t.Helper()
		c.APIKey = ""
		if err := s.SaveChannel(&c); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"model":"deepseek-flash","messages":[]}`
	if status, resp := postJSON(t, h.URL+"/v1/chat/completions", body); status != 503 || hits != 0 {
		t.Fatalf("paid channel auto-used without opt-in: %d %s hits=%d", status, resp, hits)
	}
	c.AutoPaid = true
	save()
	if status, resp := postJSON(t, h.URL+"/v1/chat/completions", body); status != 200 || hits != 1 {
		t.Fatalf("paid channel not used after opt-in: %d %s hits=%d", status, resp, hits)
	}
	c.Enabled = false
	save()
	if status, resp := postJSON(t, h.URL+"/v1/chat/completions", body); status != 503 || hits != 1 {
		t.Fatalf("disabled channel was used: %d %s hits=%d", status, resp, hits)
	}
	c.Enabled = true
	c.Paid = false
	c.AutoPaid = false
	save()
	if status, resp := postJSON(t, h.URL+"/v1/chat/completions", body); status != 200 || hits != 2 {
		t.Fatalf("free channel required auto_paid: %d %s hits=%d", status, resp, hits)
	}
}

func TestDimAgentModelMappingAndHistory(t *testing.T) {
	writeDimAuth(t, "test-oauth-token")
	var gotModel, gotAuth, gotContentType string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotModel = payload.Model
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, dimAgentDraft("DimAgent 主账号", upstream.URL+"/v1", "dimagent"))
	c.AutoPaid = true
	c.APIKey = ""
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	status, body := postJSON(t, h.URL+"/v1/chat/completions", `{"model":"deepseek-flash","messages":[{"role":"user","content":"hi"}]}`)
	if status != 200 || !bytes.Contains(body, []byte(`"content":"ok"`)) {
		t.Fatalf("unexpected response %d: %s", status, body)
	}
	if gotModel != "deepseek-v4.1-flash" || gotAuth != "Bearer test-oauth-token" || gotContentType != "application/json" {
		t.Fatalf("unexpected upstream request model=%q auth=%q content-type=%q", gotModel, gotAuth, gotContentType)
	}
	entries := historyEntries(t, h)
	if len(entries) != 1 || entries[0].Channel != "DimAgent 主账号" || entries[0].Kind != "dimagent" || entries[0].Status != 200 || entries[0].DurationMS < 0 {
		t.Fatalf("unexpected history entry: %+v", entries)
	}

	slashed := dimAgentDraft("slash-mapping", upstream.URL+"/v1", "dimagent")
	slashed.Model = "deepseek-pro"
	slashed.RemoteModel = "deepseek/deepseek-v4.1-flash"
	slashed.AutoPaid = true
	saveTestChannel(t, s, slashed)
	if status, body := postJSON(t, h.URL+"/v1/chat/completions", `{"model":"deepseek-pro","messages":[]}`); status != 200 {
		t.Fatalf("slashed remote model was rejected: %d %s", status, body)
	}
	if gotModel != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("slashed remote model was not forwarded verbatim: %q", gotModel)
	}
}

func TestDimAgentDiscover(t *testing.T) {
	writeDimAuth(t, "test-oauth-token")
	var gotAuth, gotSession, gotUserAgent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotSession = r.Header.Get("x-opencode-session")
		gotUserAgent = r.Header.Get("User-Agent")
		io.WriteString(w, `{"data":[{"id":"deepseek-v4.1-flash"},{"id":"glm-5.3-flash"},{"id":"deepseek v4"},{"id":"deepseek-v4-pro"}]}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, dimAgentDraft("DimAgent", upstream.URL+"/v1", "dimagent"))
	status, body := postJSON(t, h.URL+"/api/channels/"+strconv.FormatInt(c.ID, 10)+"/discover", `{}`)
	if status != 200 {
		t.Fatalf("discover failed %d: %s", status, body)
	}
	var catalog struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		t.Fatalf("invalid discover response %s: %v", body, err)
	}
	if strings.Join(catalog.Models, ",") != "deepseek-v4.1-flash,deepseek-v4-pro" {
		t.Fatalf("unexpected models: %+v", catalog.Models)
	}
	if gotAuth != "Bearer test-oauth-token" {
		t.Fatalf("missing bearer token: %q", gotAuth)
	}
	if gotSession != "" || gotUserAgent != "DimAgent/0.5.27" {
		t.Fatalf("unexpected dimagent headers: session=%q user-agent=%q", gotSession, gotUserAgent)
	}
	if bytes.Contains(body, []byte("test-oauth-token")) {
		t.Fatalf("discover response leaked the oauth token: %s", body)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"未提供令牌"}}`)
	}))
	defer failing.Close()
	failingChannel := saveTestChannel(t, s, dimAgentDraft("failing", failing.URL+"/v1", "dimagent"))
	status, body = postJSON(t, h.URL+"/api/channels/"+strconv.FormatInt(failingChannel.ID, 10)+"/discover", `{}`)
	if status != 502 || !strings.Contains(errorMessage(t, body), "上游状态码 401") {
		t.Fatalf("unexpected upstream failure %d: %s", status, body)
	}
	if status, body = postJSON(t, h.URL+"/api/channels/999/discover", `{}`); status != 404 {
		t.Fatalf("missing channel discover returned %d: %s", status, body)
	}
}

func TestDimAgentDoesNotServeSearch(t *testing.T) {
	var dimAgentHits, deepseekHits int
	dimAgentUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dimAgentHits++
		io.WriteString(w, `{"results":[]}`)
	}))
	defer dimAgentUpstream.Close()
	deepseekUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deepseekHits++
		if r.URL.Path != "/v1/tools/search" || r.Header.Get("Authorization") != "Bearer deepseek-key" {
			t.Errorf("unexpected deepseek request %s", r.URL.Path)
		}
		io.WriteString(w, `{"results":[{"title":"result"}]}`)
	}))
	defer deepseekUpstream.Close()
	s, h := newTestServer(t)
	dimAgent := dimAgentDraft("DimAgent", dimAgentUpstream.URL+"/v1", "dimagent")
	dimAgent.AutoPaid = true
	saveTestChannel(t, s, dimAgent)
	for _, path := range []string{"/v1/tools/search", "/v1/tools/fetch"} {
		if status, body := postJSON(t, h.URL+path, `{"query":"DeepSeek"}`); status != 503 {
			t.Fatalf("%s used a dimagent channel: %d %s", path, status, body)
		}
	}
	if dimAgentHits != 0 {
		t.Fatalf("dimagent upstream received %d tool requests", dimAgentHits)
	}
	deepseek := Channel{Name: "official", BaseURL: deepseekUpstream.URL + "/v1", APIKey: "deepseek-key", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Paid: true, AutoPaid: true, Priority: 1}
	saveTestChannel(t, s, deepseek)
	if status, body := postJSON(t, h.URL+"/v1/tools/search", `{"query":"DeepSeek"}`); status != 200 || deepseekHits != 1 || !bytes.Contains(body, []byte("result")) {
		t.Fatalf("deepseek search regression: %d %s hits=%d", status, body, deepseekHits)
	}
}

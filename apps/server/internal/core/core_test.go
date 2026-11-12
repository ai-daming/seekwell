package core

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	h := httptest.NewServer(s.Handler(""))
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
	CreatedAt  string `json:"created_at"`
	Model      string `json:"model"`
	Channel    string `json:"channel"`
	Kind       string `json:"kind"`
	Status     int    `json:"status"`
	DurationMS int    `json:"duration_ms"`
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
	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
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
	for _, key := range []string{"created_at", "model", "channel", "status", "duration_ms", "kind"} {
		if _, ok := shaped[0][key]; !ok {
			t.Fatalf("history entry missing %q: %v", key, shaped[0])
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

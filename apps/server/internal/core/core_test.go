package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
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
	res, e := http.Post(h.URL+"/v1/tools/search", "application/json", strings.NewReader(`{"query":"DeepSeek"}`))
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || calls != 1 || !strings.Contains(string(b), "result") {
		t.Fatalf("%d %s", res.StatusCode, b)
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

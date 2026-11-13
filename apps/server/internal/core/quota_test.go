package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixedClock(t *testing.T, s *Server, at time.Time) *time.Time {
	t.Helper()
	clock := at
	s.now = func() time.Time { return clock }
	return &clock
}

func disableQuotaProbe(s *Server) {
	s.quotaProbeAsync = func(int64) {}
}

func mustQuotaState(t *testing.T, s *Server, id int64) quotaState {
	t.Helper()
	state, ok, err := s.quotaState(id)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected quota state for channel %d", id)
	}
	return state
}

func requireNoQuotaState(t *testing.T, s *Server, id int64) {
	t.Helper()
	_, ok, err := s.quotaState(id)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("unexpected quota state for channel %d", id)
	}
}

func postQuotaModel(t *testing.T, h *httptest.Server, body string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

func quotaHandler(t *testing.T, s *Server) *httptest.Server {
	t.Helper()
	key, err := s.newAccessKey("quota tests")
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
	return h
}

func TestQuotaSkipsExhaustedRequestModeChannel(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	var aHits, bHits int
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aHits++
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, `{"error":"insufficient_balance"}`)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bHits++
		io.WriteString(w, `{"from":"b"}`)
	}))
	defer b.Close()
	s, h := newTestServer(t)
	fixedClock(t, s, clock)
	ca := saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: a.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "backup-b", BaseURL: b.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 1})

	resp, raw := postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"from":"b"`) || resp.Header.Get("X-SeekWell-Channel") != "backup-b" {
		t.Fatalf("first status=%d channel=%q body=%q", resp.StatusCode, resp.Header.Get("X-SeekWell-Channel"), raw)
	}
	if aHits != 1 || bHits != 1 {
		t.Fatalf("first hits a=%d b=%d", aHits, bHits)
	}

	resp, raw = postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"from":"b"`) || resp.Header.Get("X-SeekWell-Channel") != "backup-b" {
		t.Fatalf("second status=%d channel=%q body=%q", resp.StatusCode, resp.Header.Get("X-SeekWell-Channel"), raw)
	}
	if aHits != 1 || bHits != 2 {
		t.Fatalf("second hits a=%d b=%d", aHits, bHits)
	}
	state := mustQuotaState(t, s, ca.ID)
	if state.ProbeMode != quotaProbeRequest || !state.NextProbeAt.Equal(clock.Add(10*time.Minute)) {
		t.Fatalf("unexpected state: %+v", state)
	}
	got := historyEntries(t, h)
	if len(got) != 3 || got[0].Channel != "backup-b" || got[1].Channel != "backup-b" || got[2].Channel != "quota-a" || got[2].Status != http.StatusPaymentRequired {
		t.Fatalf("unexpected history: %+v", got)
	}
}

func TestQuotaSkipsExhaustedChannelForStreaming(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	var aHits, bHits int
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aHits++
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bHits++
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: b\n\ndata: [DONE]\n\n")
	}))
	defer b.Close()
	s, h := newTestServer(t)
	fixedClock(t, s, clock)
	saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: a.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "backup-b", BaseURL: b.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 1})

	body := `{"model":"deepseek-flash","stream":true,"messages":[]}`
	resp, raw := postQuotaModel(t, h, body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "data: b") {
		t.Fatalf("first status=%d body=%q", resp.StatusCode, raw)
	}
	resp, raw = postQuotaModel(t, h, body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "[DONE]") || resp.Header.Get("X-SeekWell-Channel") != "backup-b" {
		t.Fatalf("second status=%d channel=%q body=%q", resp.StatusCode, resp.Header.Get("X-SeekWell-Channel"), raw)
	}
	if aHits != 1 || bHits != 2 {
		t.Fatalf("hits a=%d b=%d", aHits, bHits)
	}
}

func TestQuotaSkipDoesNotOverrideSessionAffinity(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	var aHits, bHits int
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aHits++
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bHits++
		io.WriteString(w, `{"from":"b"}`)
	}))
	defer b.Close()
	s, h := newTestServer(t)
	fixedClock(t, s, clock)
	ca := saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: a.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "backup-b", BaseURL: b.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 1})

	resp, _ := postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first status=%d", resp.StatusCode)
	}
	s.mu.Lock()
	s.sessions["deepseek-flash\x00session-1"] = sessionBinding{channel: ca.ID, expires: clock.Add(time.Hour)}
	s.mu.Unlock()

	req, err := http.NewRequest(http.MethodPost, h.URL+"/v1/chat/completions", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OpenCode-Session", "session-1")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"from":"b"`) || resp2.Header.Get("X-SeekWell-Channel") != "backup-b" {
		t.Fatalf("status=%d channel=%q body=%q", resp2.StatusCode, resp2.Header.Get("X-SeekWell-Channel"), raw)
	}
	if aHits != 1 || bHits != 2 {
		t.Fatalf("hits a=%d b=%d", aHits, bHits)
	}
	s.mu.Lock()
	binding := s.sessions["deepseek-flash\x00session-1"]
	s.mu.Unlock()
	if binding.channel == ca.ID {
		t.Fatal("session affinity still points at exhausted channel")
	}
}

func TestQuotaAllSkippedReturnsExplicit503(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	var aHits, bHits int
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aHits++
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bHits++
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer b.Close()
	s, h := newTestServer(t)
	fixedClock(t, s, clock)
	saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: a.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "quota-b", BaseURL: b.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 1})

	resp, _ := postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("first status=%d", resp.StatusCode)
	}
	resp, raw := postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second status=%d body=%q", resp.StatusCode, raw)
	}
	if msg := errorMessage(t, raw); msg != "所有匹配渠道均因额度耗尽暂时跳过；请稍后重试" {
		t.Fatalf("unexpected message %q", msg)
	}
	if aHits != 1 || bHits != 1 {
		t.Fatalf("hits a=%d b=%d", aHits, bHits)
	}
	got := historyEntries(t, h)
	if len(got) != 2 {
		t.Fatalf("skipped candidates wrote history: %+v", got)
	}
}

func TestQuotaRetryAfterDelaysRequestProbe(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	var aHits, bHits int
	aOK := false
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aHits++
		if aOK {
			io.WriteString(w, `{"from":"a"}`)
			return
		}
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bHits++
		io.WriteString(w, `{"from":"b"}`)
	}))
	defer b.Close()
	s, h := newTestServer(t)
	clockPtr := fixedClock(t, s, clock)
	ca := saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: a.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "backup-b", BaseURL: b.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 1})

	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	state := mustQuotaState(t, s, ca.ID)
	if !state.NextProbeAt.Equal(clock.Add(10*time.Minute)) || !state.ResetAt.Equal(clock.Add(10*time.Minute)) {
		t.Fatalf("unexpected retry-after state: %+v", state)
	}

	*clockPtr = clock.Add(9*time.Minute + 59*time.Second)
	resp, _ := postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if resp.StatusCode != http.StatusOK || aHits != 1 {
		t.Fatalf("early status=%d aHits=%d", resp.StatusCode, aHits)
	}

	*clockPtr = clock.Add(10 * time.Minute)
	aOK = true
	resp, raw := postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"from":"a"`) || resp.Header.Get("X-SeekWell-Channel") != "quota-a" {
		t.Fatalf("due status=%d channel=%q body=%q", resp.StatusCode, resp.Header.Get("X-SeekWell-Channel"), raw)
	}
	requireNoQuotaState(t, s, ca.ID)
}

func TestQuotaResetFromBodyAndFallbackInterval(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	reset := clock.Add(15 * time.Minute).Format(time.RFC3339)
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, `{"error":{"RESETS_AT":"`+reset+`"}}`)
	}))
	defer a.Close()
	s, h := newTestServer(t)
	fixedClock(t, s, clock)
	ca := saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: a.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek"})
	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	state := mustQuotaState(t, s, ca.ID)
	if !state.NextProbeAt.Equal(clock.Add(15*time.Minute)) || !state.ResetAt.Equal(clock.Add(15*time.Minute)) {
		t.Fatalf("unexpected reset state: %+v", state)
	}

	c := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, `{"error":"insufficient_balance"}`)
	}))
	defer c.Close()
	s2, h2 := newTestServer(t)
	fixedClock(t, s2, clock)
	cc := saveTestChannel(t, s2, Channel{Name: "quota-c", BaseURL: c.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek"})
	postQuotaModel(t, h2, `{"model":"deepseek-flash","messages":[]}`)
	state = mustQuotaState(t, s2, cc.ID)
	if !state.NextProbeAt.Equal(clock.Add(10*time.Minute)) || !state.ResetAt.IsZero() {
		t.Fatalf("unexpected fallback state: %+v", state)
	}
}

func TestQuotaProbeIntervalConfig(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
	}{
		{"", 10 * time.Minute},
		{"2m", 2 * time.Minute},
		{"600s", 10 * time.Minute},
		{"0s", 10 * time.Minute},
		{"-5m", 10 * time.Minute},
		{"not-a-duration", 10 * time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("SEEKWELL_QUOTA_PROBE_INTERVAL", tc.value)
			if got := quotaProbeIntervalFromEnv(); got != tc.want {
				t.Fatalf("interval=%v want %v", got, tc.want)
			}
		})
	}
}

func TestQuotaConfiguredIntervalControlsRequestProbe(t *testing.T) {
	t.Setenv("SEEKWELL_QUOTA_PROBE_INTERVAL", "2m")
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	var aHits int
	aOK := false
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aHits++
		if aOK {
			io.WriteString(w, `{"from":"a"}`)
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"from":"b"}`) }))
	defer b.Close()
	s, h := newTestServer(t)
	clockPtr := fixedClock(t, s, clock)
	ca := saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: a.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "backup-b", BaseURL: b.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 1})

	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if state := mustQuotaState(t, s, ca.ID); !state.NextProbeAt.Equal(clock.Add(2 * time.Minute)) {
		t.Fatalf("unexpected next probe: %+v", state)
	}
	*clockPtr = clock.Add(time.Minute)
	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if aHits != 1 {
		t.Fatalf("probed too early: %d", aHits)
	}
	*clockPtr = clock.Add(2 * time.Minute)
	aOK = true
	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if aHits != 2 {
		t.Fatalf("did not probe at interval: %d", aHits)
	}
	requireNoQuotaState(t, s, ca.ID)
}

func TestQuotaPastResetFallsBackToInterval(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Reset", "1")
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, `{"error":{"reset_at":"1999-01-01T00:00:00Z"}}`)
	}))
	defer a.Close()
	s, h := newTestServer(t)
	fixedClock(t, s, clock)
	ca := saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: a.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek"})
	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	state := mustQuotaState(t, s, ca.ID)
	if !state.NextProbeAt.Equal(clock.Add(10*time.Minute)) || !state.ResetAt.IsZero() {
		t.Fatalf("unexpected state: %+v", state)
	}
}

type quotaTransport struct {
	modelStatus int
	modelBodies []string
	modelHits   int
	usageHits   int
	usageBody   string
	usageStatus int
}

func (f *quotaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/zen/go/v1/usage" {
		f.usageHits++
		status := f.usageStatus
		if status == 0 {
			status = http.StatusOK
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(f.usageBody)), Request: r}, nil
	}
	f.modelHits++
	status := f.modelStatus
	if status == 0 {
		status = http.StatusOK
	}
	body := `{"ok":true}`
	if len(f.modelBodies) > 0 {
		body = f.modelBodies[0]
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func openCodeQuotaServer(t *testing.T, clock time.Time) (*Server, *httptest.Server, Channel, *quotaTransport) {
	t.Helper()
	s, h := newTestServer(t)
	fixedClock(t, s, clock)
	disableQuotaProbe(s)
	c := saveTestChannel(t, s, Channel{Name: "opencode-a", BaseURL: "https://opencode.ai/inference/go/v1", APIKey: "key", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "opencode_go"})
	transport := &quotaTransport{modelStatus: http.StatusPaymentRequired, modelBodies: []string{`{"error":"quota"}`}}
	s.client = &http.Client{Transport: transport}
	return s, h, c, transport
}

func TestOpenCodeQuotaUsageProbeExhaustedThenRecovered(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s, h, c, transport := openCodeQuotaServer(t, clock)
	reset := clock.Add(30 * time.Minute).Format(time.RFC3339)

	resp, _ := postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("mark status=%d", resp.StatusCode)
	}
	state := mustQuotaState(t, s, c.ID)
	if state.ProbeMode != quotaProbeUsage {
		t.Fatalf("unexpected mode: %+v", state)
	}

	transport.usageBody = `{"usage":{"rolling":{"percent":100,"resetsAt":"` + reset + `"},"weekly":{"percent":40,"resetsAt":"` + reset + `"}}}`
	if err := s.probeQuotaState(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	state = mustQuotaState(t, s, c.ID)
	if !state.NextProbeAt.Equal(clock.Add(30*time.Minute)) || !state.ResetAt.Equal(clock.Add(30*time.Minute)) || state.ProbeFailures != 0 {
		t.Fatalf("unexpected exhausted state: %+v", state)
	}

	transport.usageBody = `{"usage":{"rolling":{"percent":20,"resetsAt":"` + reset + `"},"weekly":{"percent":40,"resetsAt":"` + reset + `"}}}`
	if err := s.probeQuotaState(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	requireNoQuotaState(t, s, c.ID)
	if transport.modelHits != 1 {
		t.Fatalf("usage probe sent a model request: %d", transport.modelHits)
	}
	if got := historyEntries(t, h); len(got) != 1 {
		t.Fatalf("usage probe wrote history: %+v", got)
	}

	transport.modelStatus = http.StatusOK
	transport.modelBodies = nil
	resp, raw := postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"ok":true`) || resp.Header.Get("X-SeekWell-Channel") != "opencode-a" {
		t.Fatalf("recovered status=%d channel=%q body=%q", resp.StatusCode, resp.Header.Get("X-SeekWell-Channel"), raw)
	}
}

func floatPtr(v float64) *float64 { return &v }
func boolPtr(v bool) *bool        { return &v }

func TestEvaluateDimAgentQuotaUsage(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	future := now.Add(20 * time.Minute).Format(time.RFC3339)
	c := Channel{Kind: "dimagent"}
	tests := []struct {
		name        string
		usage       ChannelUsage
		recovered   bool
		exhausted   bool
		confirmed   bool
		nextMinutes int
	}{
		{name: "unlimited", usage: ChannelUsage{Status: usageOK, Credits: &UsageCredits{Unlimited: boolPtr(true)}}, recovered: true, confirmed: true},
		{name: "remaining positive", usage: ChannelUsage{Status: usageOK, Credits: &UsageCredits{Remaining: floatPtr(1)}}, recovered: true, confirmed: true},
		{name: "remaining zero with reset", usage: ChannelUsage{Status: usageOK, Credits: &UsageCredits{Remaining: floatPtr(0)}, Windows: []UsageWindow{{ResetsAt: future}}}, exhausted: true, confirmed: true, nextMinutes: 20},
		{name: "missing remaining with capped window", usage: ChannelUsage{Status: usageOK, Credits: &UsageCredits{}, Windows: []UsageWindow{{TokenUsed: floatPtr(10), TokenCap: floatPtr(10), ResetsAt: future}}}, exhausted: true, confirmed: true, nextMinutes: 20},
		{name: "unknown", usage: ChannelUsage{Status: usageOK, Credits: &UsageCredits{}}, confirmed: false},
		{name: "error", usage: ChannelUsage{Status: usageError}, confirmed: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := evaluateQuotaUsage(c, tc.usage, now, 10*time.Minute)
			if verdict.recovered != tc.recovered || verdict.exhausted != tc.exhausted || verdict.confirmed != tc.confirmed {
				t.Fatalf("unexpected verdict: %+v", verdict)
			}
			if tc.nextMinutes > 0 && !verdict.next.Equal(now.Add(time.Duration(tc.nextMinutes)*time.Minute)) {
				t.Fatalf("next=%v", verdict.next)
			}
		})
	}
}

func TestQuotaUsageProbeFailureDowngradesToRequestMode(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s, h, c, transport := openCodeQuotaServer(t, clock)
	transport.usageStatus = http.StatusInternalServerError
	transport.usageBody = `{"error":"boom"}`
	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)

	for i := 1; i <= 2; i++ {
		if err := s.probeQuotaState(context.Background(), c.ID); err != nil {
			t.Fatal(err)
		}
		state := mustQuotaState(t, s, c.ID)
		if state.ProbeMode != quotaProbeUsage || state.ProbeFailures != i {
			t.Fatalf("after %d failures: %+v", i, state)
		}
	}
	if err := s.probeQuotaState(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	state := mustQuotaState(t, s, c.ID)
	if state.ProbeMode != quotaProbeRequest || state.ProbeFailures != 3 {
		t.Fatalf("did not downgrade: %+v", state)
	}
	if !state.NextProbeAt.Equal(clock.Add(10 * time.Minute)) {
		t.Fatalf("unexpected next probe: %+v", state)
	}
	usageBefore := transport.usageHits
	if err := s.probeQuotaState(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	if transport.usageHits != usageBefore {
		t.Fatalf("request-mode state still called usage endpoint: %d -> %d", usageBefore, transport.usageHits)
	}
}

func TestQuotaUsageProbeIsReadOnly(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s, h, c, transport := openCodeQuotaServer(t, clock)
	transport.usageBody = `{"usage":{"rolling":{"percent":10,"resetsAt":"2030-01-01T01:00:00Z"}}}`
	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	before := historyEntries(t, h)
	modelHits := transport.modelHits
	if err := s.probeQuotaState(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	requireNoQuotaState(t, s, c.ID)
	if transport.modelHits != modelHits {
		t.Fatalf("probe sent model request: %d -> %d", modelHits, transport.modelHits)
	}
	if transport.usageHits != 1 {
		t.Fatalf("usage hits=%d", transport.usageHits)
	}
	after := historyEntries(t, h)
	if len(after) != len(before) {
		t.Fatalf("probe wrote history: %d -> %d", len(before), len(after))
	}
}

func TestQuotaStatePersistsAcrossRestart(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "persist.db")
	var aHits, bHits int
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aHits++
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bHits++
		io.WriteString(w, `{"from":"b"}`)
	}))
	defer b.Close()

	s1, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	fixedClock(t, s1, clock)
	ca := saveTestChannel(t, s1, Channel{Name: "quota-a", BaseURL: a.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s1, Channel{Name: "backup-b", BaseURL: b.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 1})
	h1 := quotaHandler(t, s1)
	postQuotaModel(t, h1, `{"model":"deepseek-flash","messages":[]}`)
	if aHits != 1 || bHits != 1 {
		t.Fatalf("first hits a=%d b=%d", aHits, bHits)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	fixedClock(t, s2, clock)
	h2 := quotaHandler(t, s2)
	resp, raw := postQuotaModel(t, h2, `{"model":"deepseek-flash","messages":[]}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"from":"b"`) || resp.Header.Get("X-SeekWell-Channel") != "backup-b" {
		t.Fatalf("after restart status=%d channel=%q body=%q", resp.StatusCode, resp.Header.Get("X-SeekWell-Channel"), raw)
	}
	if aHits != 1 || bHits != 2 {
		t.Fatalf("after restart hits a=%d b=%d", aHits, bHits)
	}
	if state := mustQuotaState(t, s2, ca.ID); state.ProbeMode != quotaProbeRequest {
		t.Fatalf("state not persisted: %+v", state)
	}
}

func TestQuotaStateSharedBetweenServersOnSameDatabase(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "shared.db")
	var aHits int
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aHits++
		io.WriteString(w, `{"from":"a"}`)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"from":"b"}`) }))
	defer b.Close()

	s1, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	fixedClock(t, s1, clock)
	ca := saveTestChannel(t, s1, Channel{Name: "quota-a", BaseURL: a.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s1, Channel{Name: "backup-b", BaseURL: b.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 1})
	if err := s1.markQuotaExhausted(ca, time.Time{}); err != nil {
		t.Fatal(err)
	}

	s2, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	fixedClock(t, s2, clock)
	h2 := quotaHandler(t, s2)
	resp, raw := postQuotaModel(t, h2, `{"model":"deepseek-flash","messages":[]}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `{"from":"b"}`) || resp.Header.Get("X-SeekWell-Channel") != "backup-b" {
		t.Fatalf("status=%d channel=%q body=%q", resp.StatusCode, resp.Header.Get("X-SeekWell-Channel"), raw)
	}
	if aHits != 0 {
		t.Fatalf("second server used exhausted channel: %d", aHits)
	}
}

func TestQuotaStateTableMigrationAndDeleteCleanup(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "migrate.db")
	s1, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	c := saveTestChannel(t, s1, Channel{Name: "quota-a", BaseURL: "http://quota-a.invalid/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek"})
	if _, err := s1.db.Exec(`DROP TABLE quota_states`); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	fixedClock(t, s2, clock)
	cs, err := s2.channels()
	if err != nil || len(cs) != 1 || cs[0].ID != c.ID {
		t.Fatalf("existing channels damaged: %+v %v", cs, err)
	}
	if err := s2.markQuotaExhausted(c, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s2.quotaState(c.ID); err != nil || !ok {
		t.Fatalf("state table not recreated: ok=%v err=%v", ok, err)
	}
	h := quotaHandler(t, s2)
	req, err := http.NewRequest(http.MethodDelete, h.URL+"/api/channels/"+strconv.FormatInt(c.ID, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d", resp.StatusCode)
	}
	requireNoQuotaState(t, s2, c.ID)
}

func TestSaveChannelClearsQuotaStateOnIdentityChange(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s, _ := newTestServer(t)
	fixedClock(t, s, clock)
	c := saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: "http://a.invalid/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek"})
	mark := func() {
		if err := s.markQuotaExhausted(c, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}

	mark()
	c.Name = "renamed"
	c.Priority = 5
	c.Enabled = false
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	mustQuotaState(t, s, c.ID)

	c.Enabled = true
	c.BaseURL = "http://b.invalid/v1"
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	requireNoQuotaState(t, s, c.ID)

	c.BaseURL = "http://a.invalid/v1"
	mark()
	c.APIKey = "new-key"
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	requireNoQuotaState(t, s, c.ID)

	c.APIKey = "new-key"
	mark()
	c.Kind = "compatible"
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	requireNoQuotaState(t, s, c.ID)
}

func TestQuotaRequestModeRecoversOnlyOnSuccess(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	var aHits int
	aStatus := http.StatusPaymentRequired
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aHits++
		if aStatus == http.StatusOK {
			io.WriteString(w, `{"from":"a"}`)
			return
		}
		w.WriteHeader(aStatus)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"from":"b"}`) }))
	defer b.Close()
	s, h := newTestServer(t)
	clockPtr := fixedClock(t, s, clock)
	ca := saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: a.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "compatible", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "backup-b", BaseURL: b.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 1})

	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	*clockPtr = clock.Add(5 * time.Minute)
	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if aHits != 1 {
		t.Fatalf("probed before interval: %d", aHits)
	}
	*clockPtr = clock.Add(10 * time.Minute)
	aStatus = http.StatusTooManyRequests
	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	state := mustQuotaState(t, s, ca.ID)
	if state.ProbeMode != quotaProbeRequest || !state.NextProbeAt.Equal(clock.Add(20*time.Minute)) {
		t.Fatalf("429 changed state: %+v", state)
	}
	if aHits != 2 {
		t.Fatalf("due probe not attempted: %d", aHits)
	}
	aStatus = http.StatusOK
	*clockPtr = clock.Add(20 * time.Minute)
	resp, raw := postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"from":"a"`) || resp.Header.Get("X-SeekWell-Channel") != "quota-a" {
		t.Fatalf("status=%d channel=%q body=%q", resp.StatusCode, resp.Header.Get("X-SeekWell-Channel"), raw)
	}
	if aHits != 3 {
		t.Fatalf("recovery request not sent: %d", aHits)
	}
	requireNoQuotaState(t, s, ca.ID)
}

func TestQuotaRequestMode402RemarksState(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "300")
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"from":"b"}`) }))
	defer b.Close()
	s, h := newTestServer(t)
	clockPtr := fixedClock(t, s, clock)
	ca := saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: a.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "compatible"})
	saveTestChannel(t, s, Channel{Name: "backup-b", BaseURL: b.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 1})

	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	*clockPtr = clock.Add(10 * time.Minute)
	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	state := mustQuotaState(t, s, ca.ID)
	if !state.NextProbeAt.Equal(clock.Add(15*time.Minute)) || !state.ResetAt.Equal(clock.Add(15*time.Minute)) {
		t.Fatalf("402 did not re-mark with new reset: %+v", state)
	}
}

func TestNonQuotaStatusesDoNotCreatePersistentState(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var hits int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				w.WriteHeader(status)
			}))
			defer upstream.Close()
			backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"from":"b"}`) }))
			defer backup.Close()
			s, h := newTestServer(t)
			fixedClock(t, s, clock)
			c := saveTestChannel(t, s, Channel{Name: "upstream-a", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
			saveTestChannel(t, s, Channel{Name: "backup-b", BaseURL: backup.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 1})
			postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
			postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
			requireNoQuotaState(t, s, c.ID)
			if hits != 2 {
				t.Fatalf("upstream was not retried after %d: hits=%d", status, hits)
			}
		})
	}
}

func TestQuotaSchedulerProbesDueUsageStates(t *testing.T) {
	clock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s, h, c, transport := openCodeQuotaServer(t, clock)
	transport.usageBody = `{"usage":{"rolling":{"percent":95,"resetsAt":"2030-01-01T01:00:00Z"}}}`
	postQuotaModel(t, h, `{"model":"deepseek-flash","messages":[]}`)
	state := mustQuotaState(t, s, c.ID)
	if err := s.setQuotaStateNextProbe(c.ID, state.Revision, clock.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.probeDueQuotas(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireNoQuotaState(t, s, c.ID)
	if transport.usageHits != 1 || transport.modelHits != 1 {
		t.Fatalf("unexpected hits usage=%d model=%d", transport.usageHits, transport.modelHits)
	}
}

func TestQuotaResetTimeParsing(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s := &Server{now: func() time.Time { return now }}
	future := now.Add(20 * time.Minute)
	tests := []struct {
		name   string
		header http.Header
		body   string
		want   time.Time
	}{
		{name: "retry after seconds", header: http.Header{"Retry-After": []string{"300"}}, want: now.Add(5 * time.Minute)},
		{name: "retry after http date", header: http.Header{"Retry-After": []string{future.Format(http.TimeFormat)}}, want: future},
		{name: "rate limit relative", header: http.Header{"RateLimit-Reset": []string{"120"}}, want: now.Add(2 * time.Minute)},
		{name: "x rate limit epoch seconds", header: http.Header{"X-RateLimit-Reset": []string{strconv.FormatInt(future.Unix(), 10)}}, want: future},
		{name: "x rate limit epoch millis", header: http.Header{"X-RateLimit-Reset": []string{strconv.FormatInt(future.UnixMilli(), 10)}}, want: future},
		{name: "x rate limit rfc3339", header: http.Header{"X-RateLimit-Reset": []string{future.Format(time.RFC3339)}}, want: future},
		{name: "body resets_at", body: `{"error":{"resets_at":"` + future.Format(time.RFC3339) + `"}}`, want: future},
		{name: "body retry_after_seconds", body: `{"retry_after_seconds":120}`, want: now.Add(2 * time.Minute)},
		{name: "body numeric reset", body: `{"reset_at":` + strconv.FormatInt(future.Unix(), 10) + `}`, want: future},
		{name: "earliest wins", header: http.Header{"Retry-After": []string{"600"}}, body: `{"resets_at":"` + now.Add(5*time.Minute).Format(time.RFC3339) + `"}`, want: now.Add(5 * time.Minute)},
		{name: "past ignored", header: http.Header{"X-RateLimit-Reset": []string{"1"}}, body: `{"reset_at":"1999-01-01T00:00:00Z"}`, want: time.Time{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			header := tc.header
			if header == nil {
				header = make(http.Header)
			}
			got := s.quotaResetFromResponse(header, []byte(tc.body), now)
			if !got.Equal(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

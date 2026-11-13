package core

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func quotaExhaustedServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestQuotaExhaustedFailoverNonStreaming(t *testing.T) {
	var aHits, bHits int
	quota := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aHits++
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, `{"error":"insufficient_balance"}`)
	}))
	defer quota.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bHits++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"from":"backup"}`)
	}))
	defer backup.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: quota.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "backup-b", BaseURL: backup.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "opencode_go", Priority: 1})

	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"from":"backup"`) || strings.Contains(string(b), "insufficient_balance") {
		t.Fatalf("status=%d body=%q", resp.StatusCode, b)
	}
	if resp.Header.Get("X-SeekWell-Channel") != "backup-b" {
		t.Fatalf("served by channel %q", resp.Header.Get("X-SeekWell-Channel"))
	}
	if aHits != 1 || bHits != 1 {
		t.Fatalf("hits quota=%d backup=%d", aHits, bHits)
	}
	got := historyEntries(t, h)
	if len(got) != 2 || got[0].Channel != "backup-b" || got[0].Status != http.StatusOK || got[1].Channel != "quota-a" || got[1].Status != http.StatusPaymentRequired {
		t.Fatalf("unexpected history: %+v", got)
	}
}

func TestQuotaExhaustedFailoverStreaming(t *testing.T) {
	var aHits, bHits int
	quota := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aHits++
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, `{"error":"insufficient_balance"}`)
	}))
	defer quota.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bHits++
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: backup\n\ndata: [DONE]\n\n")
	}))
	defer backup.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: quota.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "backup-b", BaseURL: backup.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "opencode_go", Priority: 1})

	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "data: backup") || !strings.Contains(string(b), "[DONE]") || strings.Contains(string(b), "insufficient_balance") {
		t.Fatalf("status=%d body=%q", resp.StatusCode, b)
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("X-SeekWell-Channel") != "backup-b" {
		t.Fatalf("content-type=%q channel=%q", resp.Header.Get("Content-Type"), resp.Header.Get("X-SeekWell-Channel"))
	}
	if aHits != 1 || bHits != 1 {
		t.Fatalf("hits quota=%d backup=%d", aHits, bHits)
	}
	got := historyEntries(t, h)
	if len(got) != 2 || got[0].Channel != "backup-b" || got[0].Status != http.StatusOK || got[1].Channel != "quota-a" || got[1].Status != http.StatusPaymentRequired {
		t.Fatalf("unexpected history: %+v", got)
	}
}

func TestQuotaExhaustedWithoutFallback(t *testing.T) {
	var hits int
	quota := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, `{"error":"insufficient_balance"}`)
	}))
	defer quota.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: quota.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek"})

	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d body=%q", resp.StatusCode, b)
	}
	if msg := errorMessage(t, b); msg != "所有匹配渠道均不可用；请检查渠道记录" {
		t.Fatalf("unexpected error message %q", msg)
	}
	if hits != 1 {
		t.Fatalf("hits=%d", hits)
	}
	got := historyEntries(t, h)
	if len(got) != 1 || got[0].Channel != "quota-a" || got[0].Status != http.StatusPaymentRequired {
		t.Fatalf("unexpected history: %+v", got)
	}
}

func TestQuotaExhaustedAllCandidatesFail(t *testing.T) {
	first := quotaExhaustedServer(t, `{"error":"insufficient_balance"}`)
	second := quotaExhaustedServer(t, `{"error":"insufficient_balance"}`)
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: first.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "quota-b", BaseURL: second.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "opencode_go", Priority: 1})

	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d body=%q", resp.StatusCode, b)
	}
	if msg := errorMessage(t, b); msg != "所有匹配渠道均不可用；请检查渠道记录" {
		t.Fatalf("unexpected error message %q", msg)
	}
	got := historyEntries(t, h)
	if len(got) != 2 || got[0].Channel != "quota-b" || got[0].Status != http.StatusPaymentRequired || got[1].Channel != "quota-a" || got[1].Status != http.StatusPaymentRequired {
		t.Fatalf("unexpected history: %+v", got)
	}
}

func TestQuotaExhaustedDoesNotBypassRoutingGates(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		backup Channel
	}{
		{name: "paid opt-in", body: `{"model":"deepseek-flash","messages":[]}`, backup: Channel{Paid: true}},
		{name: "tools capability", body: `{"model":"deepseek-flash","messages":[],"tools":[{"type":"function","function":{"name":"test"}}]}`, backup: Channel{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var aHits, bHits int
			quota := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				aHits++
				w.WriteHeader(http.StatusPaymentRequired)
			}))
			defer quota.Close()
			backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bHits++
				io.WriteString(w, `{"ok":true}`)
			}))
			defer backup.Close()
			s, h := newTestServer(t)
			saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: quota.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0, SupportsTools: true})
			c := tc.backup
			c.Name = "backup-b"
			c.BaseURL = backup.URL + "/v1"
			c.APIKey = "k"
			c.Model = "deepseek-flash"
			c.RemoteModel = "deepseek-flash"
			c.Enabled = true
			c.Kind = "opencode_go"
			c.Priority = 1
			saveTestChannel(t, s, c)

			resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("status=%d body=%q", resp.StatusCode, b)
			}
			if aHits != 1 || bHits != 0 {
				t.Fatalf("hits quota=%d backup=%d", aHits, bHits)
			}
			got := historyEntries(t, h)
			if len(got) != 1 || got[0].Channel != "quota-a" || got[0].Status != http.StatusPaymentRequired {
				t.Fatalf("unexpected history: %+v", got)
			}
		})
	}
}

func TestQuotaExhaustedDoesNotSwitchAfterOutputStarts(t *testing.T) {
	var bHits int
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "quota-a", BaseURL: "http://quota-a.invalid/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "backup-b", BaseURL: "http://backup-b.invalid/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "opencode_go", Priority: 1})
	s.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Host, "quota-a.") {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: &interruptingReader{}}, nil
		}
		bHits++
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})}

	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","stream":true,"messages":[]}`))
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if bHits != 0 {
		t.Fatalf("backup was called after output started: %d", bHits)
	}
	got := historyEntries(t, h)
	if len(got) != 1 || got[0].Channel != "quota-a" || got[0].Status != 0 {
		t.Fatalf("unexpected history: %+v", got)
	}
}

func TestNonQuotaClientErrorIsNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var aHits, bHits int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				aHits++
				w.WriteHeader(status)
				io.WriteString(w, `{"error":"client error"}`)
			}))
			defer upstream.Close()
			backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bHits++
				io.WriteString(w, `{"ok":true}`)
			}))
			defer backup.Close()
			s, h := newTestServer(t)
			saveTestChannel(t, s, Channel{Name: "strict-a", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek", Priority: 0})
			saveTestChannel(t, s, Channel{Name: "backup-b", BaseURL: backup.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "opencode_go", Priority: 1})

			resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-flash","messages":[]}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != status || !strings.Contains(string(b), "client error") {
				t.Fatalf("status=%d body=%q", resp.StatusCode, b)
			}
			if aHits != 1 || bHits != 0 {
				t.Fatalf("hits first=%d backup=%d", aHits, bHits)
			}
		})
	}
}

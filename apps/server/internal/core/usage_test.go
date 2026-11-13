package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func useUsageTransport(t *testing.T, s *Server, fn func(*http.Request) (*http.Response, error)) {
	t.Helper()
	s.client = &http.Client{Transport: roundTripFunc(fn)}
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func saveUsageChannel(t *testing.T, s *Server, c Channel) Channel {
	t.Helper()
	return saveTestChannel(t, s, c)
}

func fetchUsage(t *testing.T, h *httptest.Server) (int, []byte) {
	t.Helper()
	resp, err := http.Get(h.URL + "/api/channels/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

type usageList struct {
	Usage []ChannelUsage `json:"usage"`
}

func decodeUsage(t *testing.T, body []byte) usageList {
	t.Helper()
	var out usageList
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("invalid usage response %q: %v", body, err)
	}
	return out
}

func TestOpenCodeUsageSuccess(t *testing.T) {
	s, h := newTestServer(t)
	unix := int64(1763337600)
	millis := int64(1764547200000)
	var requests atomic.Int32
	useUsageTransport(t, s, func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("method: %s", r.Method)
		}
		if r.URL.String() != "https://opencode.ai/zen/go/v1/usage" {
			t.Errorf("url: %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer opencode-secret" {
			t.Errorf("authorization: %q", r.Header.Get("Authorization"))
		}
		return response(http.StatusOK, fmt.Sprintf(`{"usage":{"rolling":{"percent":12.5,"resetsAt":"2026-11-13T08:00:00Z"},"weekly":{"percent":34,"resetsAt":%d},"monthly":{"percent":56,"resetsAt":%d}}}`, unix, millis)), nil
	})
	c := saveUsageChannel(t, s, Channel{Name: "go", Kind: "opencode_go", BaseURL: "https://opencode.ai/inference/go/v1", APIKey: "opencode-secret", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
	status, body := fetchUsage(t, h)
	if status != http.StatusOK {
		t.Fatalf("status: %d %s", status, body)
	}
	if strings.Contains(string(body), "opencode-secret") {
		t.Fatal("usage response leaked the API key")
	}
	list := decodeUsage(t, body)
	if len(list.Usage) != 1 {
		t.Fatalf("usage: %s", body)
	}
	got := list.Usage[0]
	if got.ChannelID != c.ID || got.Kind != "opencode_go" || got.Status != "ok" || got.CheckedAt == "" {
		t.Fatalf("usage metadata: %+v", got)
	}
	wantResets := []string{
		"2026-11-13T08:00:00Z",
		time.Unix(unix, 0).UTC().Format(time.RFC3339),
		time.UnixMilli(millis).UTC().Format(time.RFC3339),
	}
	wantNames := []string{"five_hour", "weekly", "monthly"}
	if len(got.Windows) != 3 {
		t.Fatalf("windows: %+v", got.Windows)
	}
	for i, window := range got.Windows {
		if window.Window != wantNames[i] || window.UsedPercent == nil || *window.UsedPercent != []float64{12.5, 34, 56}[i] || window.ResetsAt != wantResets[i] {
			t.Fatalf("window %d: %+v", i, window)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests: %d", requests.Load())
	}
}

func TestOpenCodeUsagePartialWindows(t *testing.T) {
	s, h := newTestServer(t)
	useUsageTransport(t, s, func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"usage":{"rolling":{"percent":0,"resetsAt":"not-a-date"}}}`), nil
	})
	saveUsageChannel(t, s, Channel{Name: "go", Kind: "opencode_go", BaseURL: "https://opencode.ai/inference/go/v1", APIKey: "key", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
	status, body := fetchUsage(t, h)
	if status != http.StatusOK {
		t.Fatalf("status: %d %s", status, body)
	}
	list := decodeUsage(t, body)
	if len(list.Usage) != 1 || len(list.Usage[0].Windows) != 1 {
		t.Fatalf("partial windows: %s", body)
	}
	window := list.Usage[0].Windows[0]
	if window.Window != "five_hour" || window.UsedPercent == nil || *window.UsedPercent != 0 || window.ResetsAt != "" {
		t.Fatalf("partial window: %+v", window)
	}
}

func TestOpenCodeUsageFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", http.StatusUnauthorized, `{}`},
		{"forbidden", http.StatusForbidden, `{}`},
		{"rate limited", http.StatusTooManyRequests, `{}`},
		{"server error", http.StatusInternalServerError, `{}`},
		{"non json", http.StatusOK, `not json`},
		{"missing usage", http.StatusOK, `{}`},
		{"invalid percent", http.StatusOK, `{"usage":{"rolling":{"percent":"bad"}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, h := newTestServer(t)
			useUsageTransport(t, s, func(r *http.Request) (*http.Response, error) {
				return response(tc.status, tc.body), nil
			})
			saveUsageChannel(t, s, Channel{Name: "go", Kind: "opencode_go", BaseURL: "https://opencode.ai/inference/go/v1", APIKey: "opencode-secret", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
			status, body := fetchUsage(t, h)
			if status != http.StatusOK {
				t.Fatalf("status: %d %s", status, body)
			}
			list := decodeUsage(t, body)
			if len(list.Usage) != 1 || list.Usage[0].Status != "error" || list.Usage[0].Message == "" {
				t.Fatalf("failure result: %s", body)
			}
			if strings.Contains(string(body), "opencode-secret") {
				t.Fatal("usage response leaked the API key")
			}
		})
	}
}

func TestDimAgentUsageSuccess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DIMCODE_HOME", dir)
	data := fmt.Sprintf(`{"nextApiOauth":{"type":"oauth","access":"dim-oauth-secret","expires":%d}}`, time.Now().Add(time.Hour).UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	s, h := newTestServer(t)
	useUsageTransport(t, s, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.String() != "https://dimagent.cn/api/me/usage" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer dim-oauth-secret" {
			t.Errorf("authorization: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("User-Agent") != "DimAgent/0.5.27" || r.Header.Get("X-Title") != "DimCode" || r.Header.Get("HTTP-Referer") != "https://dimagent.com/" {
			t.Errorf("client identity headers: %v", r.Header)
		}
		return response(http.StatusOK, `{"success":true,"data":{"credits":{"total_units":11000,"used_units":7800,"remaining_units":3200,"unlimited":false,"expires_at":"2026-12-01T00:00:00Z","subscription_bucket":{"total_units":11000,"used_units":7800,"remaining_units":3200,"unlimited":false,"expires_at":"2026-12-01T00:00:00Z","window_states":[{"window_duration_hours":5,"window_token_cap":1000,"window_token_used":123,"window_started_at":"2026-11-13T03:00:00Z","window_expires_at":"2026-11-13T08:00:00Z"}]}}}}`), nil
	})
	c := saveUsageChannel(t, s, Channel{Name: "dim", Kind: "dimagent", AuthMode: "cli_oauth", BaseURL: "https://dimagent.cn/v1", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
	status, body := fetchUsage(t, h)
	if status != http.StatusOK {
		t.Fatalf("status: %d %s", status, body)
	}
	if strings.Contains(string(body), "dim-oauth-secret") {
		t.Fatal("usage response leaked the OAuth token")
	}
	list := decodeUsage(t, body)
	if len(list.Usage) != 1 {
		t.Fatalf("usage: %s", body)
	}
	got := list.Usage[0]
	if got.ChannelID != c.ID || got.Kind != "dimagent" || got.Status != "ok" || got.Credits == nil {
		t.Fatalf("usage metadata: %+v", got)
	}
	credits := got.Credits
	if credits.Total == nil || *credits.Total != 11000 || credits.Used == nil || *credits.Used != 7800 || credits.Remaining == nil || *credits.Remaining != 3200 {
		t.Fatalf("credits: %+v", credits)
	}
	if credits.Unlimited == nil || *credits.Unlimited || credits.Unit != "Credits" || credits.ExpiresAt != "2026-12-01T00:00:00Z" {
		t.Fatalf("credits metadata: %+v", credits)
	}
	if len(got.Windows) != 1 {
		t.Fatalf("windows: %+v", got.Windows)
	}
	window := got.Windows[0]
	if window.DurationHours == nil || *window.DurationHours != 5 || window.TokenUsed == nil || *window.TokenUsed != 123 || window.TokenCap == nil || *window.TokenCap != 1000 || window.ResetsAt != "2026-11-13T08:00:00Z" {
		t.Fatalf("window: %+v", window)
	}
}

func TestDimAgentUsageFallbacks(t *testing.T) {
	t.Run("top level summary", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("DIMCODE_HOME", dir)
		data := fmt.Sprintf(`{"nextApiOauth":{"type":"oauth","access":"token","expires":%d}}`, time.Now().Add(time.Hour).UnixMilli())
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		s, h := newTestServer(t)
		useUsageTransport(t, s, func(r *http.Request) (*http.Response, error) {
			return response(http.StatusOK, `{"success":true,"data":{"credits":{"total_units":500,"used_units":100,"remaining_units":400,"unlimited":true,"subscription_bucket":null}}}`), nil
		})
		saveUsageChannel(t, s, Channel{Name: "dim", Kind: "dimagent", AuthMode: "cli_oauth", BaseURL: "https://dimagent.cn/v1", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
		status, body := fetchUsage(t, h)
		if status != http.StatusOK {
			t.Fatalf("status: %d %s", status, body)
		}
		list := decodeUsage(t, body)
		if len(list.Usage) != 1 || list.Usage[0].Credits == nil {
			t.Fatalf("usage: %s", body)
		}
		credits := list.Usage[0].Credits
		if credits.Total == nil || *credits.Total != 500 || credits.Remaining == nil || *credits.Remaining != 400 || credits.Unlimited == nil || !*credits.Unlimited {
			t.Fatalf("top-level fallback: %+v", credits)
		}
	})
}

func TestDimAgentUsageFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", http.StatusUnauthorized, `{}`},
		{"success false", http.StatusOK, `{"success":false,"message":"usage denied"}`},
		{"missing data", http.StatusOK, `{"success":true}`},
		{"missing credits", http.StatusOK, `{"success":true,"data":{}}`},
		{"invalid windows", http.StatusOK, `{"success":true,"data":{"credits":{"total_units":1,"window_states":[{"window_duration_hours":"bad"}]}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("DIMCODE_HOME", dir)
			data := fmt.Sprintf(`{"nextApiOauth":{"type":"oauth","access":"dim-oauth-secret","expires":%d}}`, time.Now().Add(time.Hour).UnixMilli())
			if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			s, h := newTestServer(t)
			useUsageTransport(t, s, func(r *http.Request) (*http.Response, error) {
				return response(tc.status, tc.body), nil
			})
			saveUsageChannel(t, s, Channel{Name: "dim", Kind: "dimagent", AuthMode: "cli_oauth", BaseURL: "https://dimagent.cn/v1", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
			status, body := fetchUsage(t, h)
			if status != http.StatusOK {
				t.Fatalf("status: %d %s", status, body)
			}
			list := decodeUsage(t, body)
			if len(list.Usage) != 1 || list.Usage[0].Status != "error" || list.Usage[0].Message == "" {
				t.Fatalf("failure result: %s", body)
			}
			if strings.Contains(string(body), "dim-oauth-secret") {
				t.Fatal("usage response leaked the OAuth token")
			}
		})
	}
}

func TestDimAgentUsageAuthFailures(t *testing.T) {
	t.Run("missing auth file", func(t *testing.T) {
		t.Setenv("DIMCODE_HOME", t.TempDir())
		s, h := newTestServer(t)
		var calls atomic.Int32
		useUsageTransport(t, s, func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			return response(http.StatusOK, `{}`), nil
		})
		saveUsageChannel(t, s, Channel{Name: "dim", Kind: "dimagent", AuthMode: "cli_oauth", BaseURL: "https://dimagent.cn/v1", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
		status, body := fetchUsage(t, h)
		if status != http.StatusOK {
			t.Fatalf("status: %d %s", status, body)
		}
		list := decodeUsage(t, body)
		if len(list.Usage) != 1 || list.Usage[0].Status != "error" || !strings.Contains(list.Usage[0].Message, "dim auth login") {
			t.Fatalf("missing auth result: %s", body)
		}
		if calls.Load() != 0 {
			t.Fatal("sent a usage request without an authorization token")
		}
	})

	t.Run("refresh failure", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("DIMCODE_HOME", dir)
		t.Setenv("PATH", dir)
		expired := `{"nextApiOauth":{"type":"oauth","access":"expired-secret","expires":1}}`
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(expired), 0600); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\nexit 1\n"
		if err := os.WriteFile(filepath.Join(dir, "dim"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		s, h := newTestServer(t)
		useUsageTransport(t, s, func(r *http.Request) (*http.Response, error) {
			t.Fatal("usage request should not happen after refresh failure")
			return nil, nil
		})
		saveUsageChannel(t, s, Channel{Name: "dim", Kind: "dimagent", AuthMode: "cli_oauth", BaseURL: "https://dimagent.cn/v1", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
		status, body := fetchUsage(t, h)
		if status != http.StatusOK {
			t.Fatalf("status: %d %s", status, body)
		}
		list := decodeUsage(t, body)
		if len(list.Usage) != 1 || list.Usage[0].Status != "error" || list.Usage[0].Message == "" || strings.Contains(list.Usage[0].Message, "expired-secret") {
			t.Fatalf("refresh failure result: %s", body)
		}
	})
}

func TestUsageUnsupportedDoesNotSendCredential(t *testing.T) {
	s, h := newTestServer(t)
	var calls atomic.Int32
	useUsageTransport(t, s, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(http.StatusOK, `{}`), nil
	})
	saveUsageChannel(t, s, Channel{Name: "custom-go", Kind: "opencode_go", BaseURL: "https://example.com/v1", APIKey: "opencode-secret", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
	saveUsageChannel(t, s, Channel{Name: "custom-dim", Kind: "dimagent", AuthMode: "cli_oauth", BaseURL: "https://example.com/v1", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
	saveUsageChannel(t, s, Channel{Name: "http-go", Kind: "opencode_go", BaseURL: "http://opencode.ai/inference/go/v1", APIKey: "opencode-secret", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
	status, body := fetchUsage(t, h)
	if status != http.StatusOK {
		t.Fatalf("status: %d %s", status, body)
	}
	list := decodeUsage(t, body)
	if len(list.Usage) != 3 {
		t.Fatalf("usage: %s", body)
	}
	for _, got := range list.Usage {
		if got.Status != "unsupported" {
			t.Fatalf("unsupported result: %+v", got)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("sent %d requests to unsupported hosts", calls.Load())
	}
}

func TestUsageMixedResultsAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DIMCODE_HOME", dir)
	s, h := newTestServer(t)
	useUsageTransport(t, s, func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"usage":{"rolling":{"percent":50,"resetsAt":"2026-11-13T08:00:00Z"}}}`), nil
	})
	goChannel := saveUsageChannel(t, s, Channel{Name: "go", Kind: "opencode_go", BaseURL: "https://opencode.ai/inference/go/v1", APIKey: "key", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true})
	dimChannel := saveUsageChannel(t, s, Channel{Name: "dim", Kind: "dimagent", AuthMode: "cli_oauth", BaseURL: "https://dimagent.cn/v1", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: false})
	status, body := fetchUsage(t, h)
	if status != http.StatusOK {
		t.Fatalf("status: %d %s", status, body)
	}
	list := decodeUsage(t, body)
	if len(list.Usage) != 2 {
		t.Fatalf("usage: %s", body)
	}
	byID := map[int64]ChannelUsage{}
	for _, got := range list.Usage {
		byID[got.ChannelID] = got
	}
	if byID[goChannel.ID].Status != "ok" || byID[dimChannel.ID].Status != "error" {
		t.Fatalf("mixed results: %s", body)
	}
	var channels, requests int
	if err := s.db.QueryRow(`SELECT count(*) FROM channels`).Scan(&channels); err != nil || channels != 2 {
		t.Fatalf("channels changed: %d %v", channels, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM requests`).Scan(&requests); err != nil || requests != 0 {
		t.Fatalf("requests changed: %d %v", requests, err)
	}
}

func TestUsageEmptyForUnsupportedKinds(t *testing.T) {
	s, h := newTestServer(t)
	var calls atomic.Int32
	useUsageTransport(t, s, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(http.StatusOK, `{}`), nil
	})
	saveUsageChannel(t, s, Channel{Name: "deepseek", Kind: "deepseek", BaseURL: "https://api.deepseek.com/v1", APIKey: "key", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	saveUsageChannel(t, s, Channel{Name: "compatible", Kind: "compatible", BaseURL: "https://example.com/v1", APIKey: "key", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	status, body := fetchUsage(t, h)
	if status != http.StatusOK || strings.TrimSpace(string(body)) != `{"usage":[]}` {
		t.Fatalf("empty usage: %d %s", status, body)
	}
	if calls.Load() != 0 {
		t.Fatalf("sent %d requests for unsupported kinds", calls.Load())
	}
}

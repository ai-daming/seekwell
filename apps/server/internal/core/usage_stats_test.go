package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

type usageStatsOverallBody struct {
	RequestCount      int64    `json:"request_count"`
	SuccessCount      int64    `json:"success_count"`
	FailureCount      int64    `json:"failure_count"`
	UsageKnownCount   int64    `json:"usage_known_count"`
	UsageUnknownCount int64    `json:"usage_unknown_count"`
	InputTokens       *int64   `json:"input_tokens"`
	OutputTokens      *int64   `json:"output_tokens"`
	TotalTokens       *int64   `json:"total_tokens"`
	CostCNY           *float64 `json:"cost_cny"`
	CostKnownCount    int64    `json:"cost_known_count"`
	CostUnknownCount  int64    `json:"cost_unknown_count"`
	KeyCount          int64    `json:"key_count"`
	EnabledKeyCount   int64    `json:"enabled_key_count"`
	LastRequestAt     *string  `json:"last_request_at"`
}

type usageStatsKeyBody struct {
	ID                int64    `json:"id"`
	Name              string   `json:"name"`
	Prefix            string   `json:"prefix"`
	Enabled           bool     `json:"enabled"`
	CreatedAt         string   `json:"created_at"`
	RequestCount      int64    `json:"request_count"`
	SuccessCount      int64    `json:"success_count"`
	FailureCount      int64    `json:"failure_count"`
	UsageKnownCount   int64    `json:"usage_known_count"`
	UsageUnknownCount int64    `json:"usage_unknown_count"`
	InputTokens       *int64   `json:"input_tokens"`
	OutputTokens      *int64   `json:"output_tokens"`
	TotalTokens       *int64   `json:"total_tokens"`
	CostCNY           *float64 `json:"cost_cny"`
	CostKnownCount    int64    `json:"cost_known_count"`
	CostUnknownCount  int64    `json:"cost_unknown_count"`
	LastRequestAt     *string  `json:"last_request_at"`
}

type usageStatsDayBody struct {
	Date              string   `json:"date"`
	RequestCount      int64    `json:"request_count"`
	SuccessCount      int64    `json:"success_count"`
	FailureCount      int64    `json:"failure_count"`
	UsageKnownCount   int64    `json:"usage_known_count"`
	UsageUnknownCount int64    `json:"usage_unknown_count"`
	InputTokens       *int64   `json:"input_tokens"`
	OutputTokens      *int64   `json:"output_tokens"`
	TotalTokens       *int64   `json:"total_tokens"`
	CostCNY           *float64 `json:"cost_cny"`
	CostKnownCount    int64    `json:"cost_known_count"`
	CostUnknownCount  int64    `json:"cost_unknown_count"`
}

type usageStatsPricingBody struct {
	Currency     string   `json:"currency"`
	Unit         string   `json:"unit"`
	Source       string   `json:"source"`
	SourceURL    string   `json:"source_url"`
	LastSyncAt   *string  `json:"last_sync_at"`
	LastSyncOK   bool     `json:"last_sync_ok"`
	Stale        bool     `json:"stale"`
	Warning      string   `json:"warning"`
	PeakTimezone string   `json:"peak_timezone"`
	PeakWindows  []string `json:"peak_windows"`
	HolidayYears []int    `json:"holiday_years"`
}

type usageStatsBody struct {
	Overall usageStatsOverallBody `json:"overall"`
	Keys    []usageStatsKeyBody   `json:"keys"`
	Daily   []usageStatsDayBody   `json:"daily"`
	Pricing usageStatsPricingBody `json:"pricing"`
}

func newUsageStatsServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "usage-stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	h := httptest.NewServer(s.Handler(""))
	t.Cleanup(h.Close)
	return s, h
}

func fetchUsageStats(t *testing.T, h *httptest.Server) (int, []byte) {
	t.Helper()
	resp, err := http.Get(h.URL + "/api/usage/stats")
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

func decodeUsageStats(t *testing.T, status int, body []byte) usageStatsBody {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("usage stats status=%d body=%s", status, body)
	}
	var out usageStatsBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("invalid usage stats response %q: %v", body, err)
	}
	return out
}

func addUsageStatsKey(t *testing.T, s *Server, name string, enabled bool) createdKey {
	t.Helper()
	key, err := s.newAccessKey(name)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		if _, err := s.db.Exec(`UPDATE access_keys SET enabled=0 WHERE id=?`, key.ID); err != nil {
			t.Fatal(err)
		}
	}
	return key
}

func addUsageStatsRequest(t *testing.T, s *Server, keyID int64, at time.Time, status int, input, output *int64) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO access_requests(key_id,created_at,path,status,duration_ms,input_tokens,output_tokens) VALUES(?,?,?,?,?,?,?)`,
		keyID, at.UTC().Format(time.RFC3339), "/v1/chat/completions", status, 12, input, output)
	if err != nil {
		t.Fatal(err)
	}
}

func int64Ptr(value int64) *int64 { return &value }

func localAt(base time.Time, daysAgo int) time.Time {
	return time.Date(base.Year(), base.Month(), base.Day(), 12, 0, 0, 0, time.Local).AddDate(0, 0, -daysAgo)
}

func localDate(base time.Time, daysAgo int) string {
	return base.AddDate(0, 0, -daysAgo).Format("2006-01-02")
}

func assertNilToken(t *testing.T, label string, value *int64) {
	t.Helper()
	if value != nil {
		t.Fatalf("%s = %d, want null", label, *value)
	}
}

func assertToken(t *testing.T, label string, value *int64, want int64) {
	t.Helper()
	if value == nil || *value != want {
		t.Fatalf("%s = %v, want %d", label, value, want)
	}
}

func TestUsageStatsEmptyDatabase(t *testing.T) {
	s, h := newUsageStatsServer(t)
	_ = s
	status, body := fetchUsageStats(t, h)
	got := decodeUsageStats(t, status, body)

	if got.Overall.RequestCount != 0 || got.Overall.SuccessCount != 0 || got.Overall.FailureCount != 0 ||
		got.Overall.UsageKnownCount != 0 || got.Overall.UsageUnknownCount != 0 ||
		got.Overall.KeyCount != 0 || got.Overall.EnabledKeyCount != 0 {
		t.Fatalf("overall counts = %+v", got.Overall)
	}
	assertNilToken(t, "overall.input_tokens", got.Overall.InputTokens)
	assertNilToken(t, "overall.output_tokens", got.Overall.OutputTokens)
	assertNilToken(t, "overall.total_tokens", got.Overall.TotalTokens)
	if got.Overall.LastRequestAt != nil {
		t.Fatalf("overall.last_request_at = %q, want null", *got.Overall.LastRequestAt)
	}
	if len(got.Keys) != 0 {
		t.Fatalf("keys = %+v, want empty", got.Keys)
	}
	if len(got.Daily) != 7 {
		t.Fatalf("daily length = %d, want 7", len(got.Daily))
	}
	today := time.Now().In(time.Local)
	for index, day := range got.Daily {
		wantDate := localDate(today, 6-index)
		if day.Date != wantDate {
			t.Fatalf("daily[%d].date = %q, want %q", index, day.Date, wantDate)
		}
		if day.RequestCount != 0 || day.SuccessCount != 0 || day.FailureCount != 0 ||
			day.UsageKnownCount != 0 || day.UsageUnknownCount != 0 {
			t.Fatalf("daily[%d] counts = %+v", index, day)
		}
		assertNilToken(t, "daily.input_tokens", day.InputTokens)
		assertNilToken(t, "daily.output_tokens", day.OutputTokens)
		assertNilToken(t, "daily.total_tokens", day.TotalTokens)
	}
}

func TestUsageStatsAggregatesKeysAndTokens(t *testing.T) {
	s, h := newUsageStatsServer(t)
	base := time.Now().In(time.Local)
	a := addUsageStatsKey(t, s, "Alice", true)
	b := addUsageStatsKey(t, s, "Bob", false)
	c := addUsageStatsKey(t, s, "Carol", true)
	d := addUsageStatsKey(t, s, "Dave", true)

	atA1 := localAt(base, 0).Add(-3 * time.Hour)
	atA2 := localAt(base, 0).Add(-2 * time.Hour)
	atA3 := localAt(base, 0).Add(-time.Hour)
	addUsageStatsRequest(t, s, a.ID, atA1, 200, int64Ptr(10), int64Ptr(5))
	addUsageStatsRequest(t, s, a.ID, atA2, 500, int64Ptr(7), nil)
	addUsageStatsRequest(t, s, a.ID, atA3, 0, nil, int64Ptr(9))
	addUsageStatsRequest(t, s, b.ID, localAt(base, 0), 200, nil, nil)
	addUsageStatsRequest(t, s, c.ID, localAt(base, 0), 200, int64Ptr(3), nil)

	status, body := fetchUsageStats(t, h)
	got := decodeUsageStats(t, status, body)

	if got.Overall.RequestCount != 5 || got.Overall.SuccessCount != 3 || got.Overall.FailureCount != 2 {
		t.Fatalf("overall request/success/failure = %+v", got.Overall)
	}
	if got.Overall.UsageKnownCount != 1 || got.Overall.UsageUnknownCount != 4 {
		t.Fatalf("overall usage known/unknown = %+v", got.Overall)
	}
	assertToken(t, "overall.input_tokens", got.Overall.InputTokens, 20)
	assertToken(t, "overall.output_tokens", got.Overall.OutputTokens, 14)
	assertToken(t, "overall.total_tokens", got.Overall.TotalTokens, 34)
	if got.Overall.KeyCount != 4 || got.Overall.EnabledKeyCount != 3 {
		t.Fatalf("overall key counts = %+v", got.Overall)
	}
	latest := localAt(base, 0)
	if got.Overall.LastRequestAt == nil || *got.Overall.LastRequestAt != latest.UTC().Format(time.RFC3339) {
		t.Fatalf("overall.last_request_at = %v, want %s", got.Overall.LastRequestAt, latest.UTC().Format(time.RFC3339))
	}

	if len(got.Keys) != 4 {
		t.Fatalf("keys length = %d, want 4: %+v", len(got.Keys), got.Keys)
	}
	if got.Keys[0].ID != a.ID || got.Keys[1].ID != b.ID || got.Keys[2].ID != c.ID || got.Keys[3].ID != d.ID {
		t.Fatalf("key order = %+v, want Alice, Bob, Carol, Dave", got.Keys)
	}

	alice := got.Keys[0]
	if alice.RequestCount != 3 || alice.SuccessCount != 1 || alice.FailureCount != 2 ||
		alice.UsageKnownCount != 1 || alice.UsageUnknownCount != 2 {
		t.Fatalf("alice counts = %+v", alice)
	}
	assertToken(t, "alice.input_tokens", alice.InputTokens, 17)
	assertToken(t, "alice.output_tokens", alice.OutputTokens, 14)
	assertToken(t, "alice.total_tokens", alice.TotalTokens, 31)
	if alice.LastRequestAt == nil || *alice.LastRequestAt != atA3.UTC().Format(time.RFC3339) {
		t.Fatalf("alice.last_request_at = %v", alice.LastRequestAt)
	}

	bob := got.Keys[1]
	if bob.Enabled {
		t.Fatal("disabled key Bob is marked enabled")
	}
	if bob.RequestCount != 1 || bob.SuccessCount != 1 || bob.FailureCount != 0 ||
		bob.UsageKnownCount != 0 || bob.UsageUnknownCount != 1 {
		t.Fatalf("bob counts = %+v", bob)
	}
	assertNilToken(t, "bob.input_tokens", bob.InputTokens)
	assertNilToken(t, "bob.output_tokens", bob.OutputTokens)
	assertNilToken(t, "bob.total_tokens", bob.TotalTokens)

	carol := got.Keys[2]
	if carol.RequestCount != 1 || carol.UsageKnownCount != 0 || carol.UsageUnknownCount != 1 {
		t.Fatalf("carol counts = %+v", carol)
	}
	assertToken(t, "carol.input_tokens", carol.InputTokens, 3)
	assertNilToken(t, "carol.output_tokens", carol.OutputTokens)
	assertToken(t, "carol.total_tokens", carol.TotalTokens, 3)

	dave := got.Keys[3]
	if dave.RequestCount != 0 || dave.SuccessCount != 0 || dave.FailureCount != 0 ||
		dave.UsageKnownCount != 0 || dave.UsageUnknownCount != 0 {
		t.Fatalf("dave counts = %+v", dave)
	}
	assertNilToken(t, "dave.input_tokens", dave.InputTokens)
	assertNilToken(t, "dave.output_tokens", dave.OutputTokens)
	assertNilToken(t, "dave.total_tokens", dave.TotalTokens)
	if dave.LastRequestAt != nil {
		t.Fatalf("dave.last_request_at = %q, want null", *dave.LastRequestAt)
	}
}

func TestUsageStatsSevenDayLocalWindow(t *testing.T) {
	s, h := newUsageStatsServer(t)
	base := time.Now().In(time.Local)
	key := addUsageStatsKey(t, s, "Window", true)

	addUsageStatsRequest(t, s, key.ID, localAt(base, 0), 200, int64Ptr(10), int64Ptr(5))
	addUsageStatsRequest(t, s, key.ID, localAt(base, 1), 500, nil, int64Ptr(4))
	addUsageStatsRequest(t, s, key.ID, localAt(base, 7), 200, int64Ptr(1), int64Ptr(1))
	addUsageStatsRequest(t, s, key.ID, localAt(base, 8), 200, int64Ptr(2), int64Ptr(2))
	addUsageStatsRequest(t, s, key.ID, localAt(base, 30), 200, int64Ptr(3), int64Ptr(3))

	status, body := fetchUsageStats(t, h)
	got := decodeUsageStats(t, status, body)

	if got.Overall.RequestCount != 5 {
		t.Fatalf("overall requests = %d, want 5", got.Overall.RequestCount)
	}
	assertToken(t, "overall.input_tokens", got.Overall.InputTokens, 16)
	assertToken(t, "overall.output_tokens", got.Overall.OutputTokens, 15)
	assertToken(t, "overall.total_tokens", got.Overall.TotalTokens, 31)

	if len(got.Daily) != 7 {
		t.Fatalf("daily length = %d, want 7", len(got.Daily))
	}
	var dailyTotal int64
	for index, day := range got.Daily {
		wantDate := localDate(base, 6-index)
		if day.Date != wantDate {
			t.Fatalf("daily[%d].date = %q, want %q", index, day.Date, wantDate)
		}
		dailyTotal += day.RequestCount
	}
	if dailyTotal != 2 {
		t.Fatalf("daily total requests = %d, want 2", dailyTotal)
	}

	today := got.Daily[6]
	if today.RequestCount != 1 || today.SuccessCount != 1 || today.FailureCount != 0 ||
		today.UsageKnownCount != 1 || today.UsageUnknownCount != 0 {
		t.Fatalf("today counts = %+v", today)
	}
	assertToken(t, "today.input_tokens", today.InputTokens, 10)
	assertToken(t, "today.output_tokens", today.OutputTokens, 5)
	assertToken(t, "today.total_tokens", today.TotalTokens, 15)

	yesterday := got.Daily[5]
	if yesterday.RequestCount != 1 || yesterday.SuccessCount != 0 || yesterday.FailureCount != 1 ||
		yesterday.UsageKnownCount != 0 || yesterday.UsageUnknownCount != 1 {
		t.Fatalf("yesterday counts = %+v", yesterday)
	}
	assertNilToken(t, "yesterday.input_tokens", yesterday.InputTokens)
	assertToken(t, "yesterday.output_tokens", yesterday.OutputTokens, 4)
	assertToken(t, "yesterday.total_tokens", yesterday.TotalTokens, 4)

	for index := 0; index < 5; index++ {
		day := got.Daily[index]
		if day.RequestCount != 0 || day.SuccessCount != 0 || day.FailureCount != 0 ||
			day.UsageKnownCount != 0 || day.UsageUnknownCount != 0 {
			t.Fatalf("daily[%d] should be zero filled: %+v", index, day)
		}
		assertNilToken(t, "daily.input_tokens", day.InputTokens)
		assertNilToken(t, "daily.output_tokens", day.OutputTokens)
		assertNilToken(t, "daily.total_tokens", day.TotalTokens)
	}
}

func TestUsageStatsDoesNotLeakCredentials(t *testing.T) {
	s, h := newUsageStatsServer(t)
	key := addUsageStatsKey(t, s, "Secret", true)
	addUsageStatsRequest(t, s, key.ID, localAt(time.Now().In(time.Local), 0), 200, int64Ptr(1), int64Ptr(1))

	status, body := fetchUsageStats(t, h)
	_ = decodeUsageStats(t, status, body)

	if bytes.Contains(body, []byte(key.Key)) {
		t.Fatal("usage stats leaked the full access key")
	}
	if bytes.Contains(body, []byte(keyHash(key.Key))) {
		t.Fatal("usage stats leaked the key hash")
	}
	if bytes.Contains(body, []byte("key_hash")) {
		t.Fatal("usage stats exposed key_hash")
	}
	if !bytes.Contains(body, []byte(key.Key[:11])) {
		t.Fatal("usage stats did not include the key prefix")
	}
}

func TestUsageStatsIsReadOnly(t *testing.T) {
	s, h := newUsageStatsServer(t)
	key := addUsageStatsKey(t, s, "ReadOnly", true)
	addUsageStatsRequest(t, s, key.ID, localAt(time.Now().In(time.Local), 0), 200, int64Ptr(2), int64Ptr(3))
	addUsageStatsRequest(t, s, key.ID, localAt(time.Now().In(time.Local), 0), 500, nil, nil)

	counts := func() (access, calls int) {
		t.Helper()
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM access_requests`).Scan(&access); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM requests`).Scan(&calls); err != nil {
			t.Fatal(err)
		}
		return access, calls
	}
	beforeAccess, beforeCalls := counts()
	for i := 0; i < 2; i++ {
		status, body := fetchUsageStats(t, h)
		_ = decodeUsageStats(t, status, body)
	}
	afterAccess, afterCalls := counts()
	if beforeAccess != afterAccess || beforeCalls != afterCalls {
		t.Fatalf("stats endpoint wrote data: access %d->%d, requests %d->%d", beforeAccess, afterAccess, beforeCalls, afterCalls)
	}
}

func TestUsageStatsMatchesKeyStatsAndKeepsChannelUsage(t *testing.T) {
	s, h := newUsageStatsServer(t)
	key := addUsageStatsKey(t, s, "Parity", true)
	addUsageStatsRequest(t, s, key.ID, localAt(time.Now().In(time.Local), 0), 200, int64Ptr(8), int64Ptr(6))
	addUsageStatsRequest(t, s, key.ID, localAt(time.Now().In(time.Local), 0), 503, int64Ptr(4), nil)

	status, body := fetchUsageStats(t, h)
	stats := decodeUsageStats(t, status, body)

	resp, err := http.Get(h.URL + "/api/keys")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var keys []struct {
		RequestCount    int64  `json:"request_count"`
		SuccessCount    int64  `json:"success_count"`
		FailureCount    int64  `json:"failure_count"`
		UsageKnownCount int64  `json:"usage_known_count"`
		InputTokens     *int64 `json:"input_tokens"`
		OutputTokens    *int64 `json:"output_tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || len(stats.Keys) != 1 {
		t.Fatalf("keys length mismatch: api=%d stats=%d", len(keys), len(stats.Keys))
	}
	apiKey, statKey := keys[0], stats.Keys[0]
	if apiKey.RequestCount != statKey.RequestCount || apiKey.SuccessCount != statKey.SuccessCount ||
		apiKey.FailureCount != statKey.FailureCount || apiKey.UsageKnownCount != statKey.UsageKnownCount {
		t.Fatalf("counting rule mismatch: api=%+v stats=%+v", apiKey, statKey)
	}
	if apiKey.InputTokens == nil || statKey.InputTokens == nil || *apiKey.InputTokens != *statKey.InputTokens {
		t.Fatalf("input tokens mismatch: api=%v stats=%v", apiKey.InputTokens, statKey.InputTokens)
	}
	if apiKey.OutputTokens == nil || statKey.OutputTokens == nil || *apiKey.OutputTokens != *statKey.OutputTokens {
		t.Fatalf("output tokens mismatch: api=%v stats=%v", apiKey.OutputTokens, statKey.OutputTokens)
	}

	usageResp, err := http.Get(h.URL + "/api/channels/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer usageResp.Body.Close()
	if usageResp.StatusCode != http.StatusOK {
		t.Fatalf("channel usage status = %d", usageResp.StatusCode)
	}
}

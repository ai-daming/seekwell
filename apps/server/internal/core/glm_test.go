package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGLMChannelValidationMatrix(t *testing.T) {
	s, _ := newTestServer(t)
	accepted := []Channel{
		{Name: "glm", BaseURL: "https://opencode.ai/inference/go/v1", APIKey: "k", Model: "glm-5.3", RemoteModel: "glm-5.3", Enabled: true, Kind: "opencode_go"},
		{Name: "glm-flash", BaseURL: "https://opencode.ai/inference/go/v1", APIKey: "k", Model: "glm-5.3-flash", RemoteModel: "zhipuai/glm-5.3-flash", Enabled: true, Kind: "opencode_go"},
		{Name: "glm-case", BaseURL: "https://opencode.ai/inference/go/v1", APIKey: "k", Model: "GLM-5.3", RemoteModel: "GLM-5.3", Enabled: true, Kind: "opencode_go"},
		{Name: "mixed-family", BaseURL: "https://opencode.ai/inference/go/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "glm-5.3", Enabled: true, Kind: "opencode_go"},
		{Name: "deepseek", BaseURL: "https://opencode.ai/inference/go/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-v4.1-flash", Enabled: true, Kind: "opencode_go"},
	}
	for _, c := range accepted {
		channel := c
		if err := s.SaveChannel(&channel); err != nil {
			t.Fatalf("opencode_go channel %s should accept %s -> %s: %v", c.Name, c.Model, c.RemoteModel, err)
		}
	}

	for _, kind := range []string{"deepseek", "dimagent", "compatible", ""} {
		channel := Channel{Name: "reject-" + kind, BaseURL: "https://example.com/v1", Model: "glm-5.3", RemoteModel: "glm-5.3", Enabled: true, Kind: kind}
		if kind != "dimagent" {
			channel.APIKey = "k"
		}
		err := s.SaveChannel(&channel)
		if err == nil || (!strings.Contains(err.Error(), "GLM") && !strings.Contains(err.Error(), "OpenCode Go")) {
			t.Fatalf("kind %q must reject GLM with an explicit error, got %v", kind, err)
		}
	}

	for _, model := range []string{"glm 5.3", "glm-\n5.3", "glm-\t5.3"} {
		channel := Channel{Name: "bad-space", BaseURL: "https://opencode.ai/inference/go/v1", APIKey: "k", Model: model, RemoteModel: "glm-5.3", Enabled: true, Kind: "opencode_go"}
		if err := s.SaveChannel(&channel); err == nil {
			t.Fatalf("model %q with whitespace must be rejected", model)
		}
	}

	edit := Channel{Name: "edit", BaseURL: "https://opencode.ai/inference/go/v1", APIKey: "k", Model: "glm-5.3", RemoteModel: "glm-5.3", Enabled: true, Kind: "opencode_go"}
	if err := s.SaveChannel(&edit); err != nil {
		t.Fatal(err)
	}
	edit.Kind = "dimagent"
	if err := s.SaveChannel(&edit); err == nil {
		t.Fatal("changing an OpenCode Go GLM mapping to DimAgent must be rejected")
	}
}

func TestGLMModelDiscoveryByChannelKind(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"id":"deepseek-v4.1-flash"},{"id":"glm-5.3"},{"id":"glm-5.3-flash"},{"id":"glm-5.4"},{"id":"glm 5.4"},{"id":"gpt-test"}]}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	opencode := saveTestChannel(t, s, Channel{Name: "opencode", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "glm-5.3", RemoteModel: "glm-5.3", Enabled: true, Kind: "opencode_go"})
	compatible := saveTestChannel(t, s, Channel{Name: "compatible", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "compatible"})

	discoverModels := func(id int64) []string {
		status, body := postJSON(t, h.URL+"/api/channels/"+fmt.Sprint(id)+"/discover", `{}`)
		if status != 200 {
			t.Fatalf("discover %d failed: %d %s", id, status, body)
		}
		var catalog struct {
			Models []string `json:"models"`
		}
		if err := json.Unmarshal(body, &catalog); err != nil {
			t.Fatalf("invalid discover response %s: %v", body, err)
		}
		return catalog.Models
	}

	if got := strings.Join(discoverModels(opencode.ID), ","); got != "deepseek-v4.1-flash,glm-5.3,glm-5.3-flash,glm-5.4" {
		t.Fatalf("OpenCode Go catalog = %q", got)
	}
	if got := strings.Join(discoverModels(compatible.ID), ","); got != "deepseek-v4.1-flash" {
		t.Fatalf("compatible catalog = %q, want DeepSeek only", got)
	}
}

func TestGLMModelTestOnlyOpenCodeGo(t *testing.T) {
	var received, session string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		received, _ = body["model"].(string)
		session = r.Header.Get("x-opencode-session")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	opencode := saveTestChannel(t, s, Channel{Name: "opencode", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "glm-5.3", RemoteModel: "glm-5.3", Enabled: true, Kind: "opencode_go"})
	resp, body := runModelTest(t, h, opencode.ID, `{"protocol":"chat","model":"glm-5.3-flash","prompt":"hi"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("OpenCode Go GLM model test failed: %d %s", resp.StatusCode, body)
	}
	if received != "glm-5.3-flash" || session == "" {
		t.Fatalf("upstream model=%q session=%q", received, session)
	}

	for _, kind := range []string{"deepseek", "compatible", "dimagent"} {
		channel := Channel{Name: "reject-" + kind, BaseURL: upstream.URL + "/v1", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: kind}
		if kind != "dimagent" {
			channel.APIKey = "k"
		}
		channel = saveTestChannel(t, s, channel)
		resp, body := runModelTest(t, h, channel.ID, `{"protocol":"chat","model":"glm-5.3","prompt":"hi"}`)
		if resp.StatusCode != 400 || !strings.Contains(errorMessage(t, body), "模型") {
			t.Fatalf("%s model test must reject GLM: %d %s", kind, resp.StatusCode, body)
		}
	}
}

func TestGLMProxyRoutingAcrossProtocols(t *testing.T) {
	var models []string
	var sessions []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		models = append(models, fmt.Sprint(body["model"]))
		sessions = append(sessions, r.Header.Get("x-opencode-session"))
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "glm", BaseURL: upstream.URL + "/v1", MessagesBaseURL: upstream.URL + "/anthropic/v1", APIKey: "k", Model: "glm-5.3", RemoteModel: "glm-5.3", Enabled: true, Kind: "opencode_go", SupportsResponses: true, SupportsMessages: true})
	for _, tc := range []struct{ path, body string }{
		{"/v1/chat/completions", `{"model":"glm-5.3","messages":[]}`},
		{"/v1/responses", `{"model":"glm-5.3","input":"hi"}`},
		{"/v1/messages", `{"model":"glm-5.3","max_tokens":10,"messages":[]}`},
	} {
		status, body := postJSON(t, h.URL+tc.path, tc.body)
		if status != 200 {
			t.Fatalf("%s status=%d body=%s", tc.path, status, body)
		}
	}
	if strings.Join(models, ",") != "glm-5.3,glm-5.3,glm-5.3" {
		t.Fatalf("upstream models=%+v", models)
	}
	for _, session := range sessions {
		if session == "" {
			t.Fatalf("OpenCode Go GLM request missing session header: %+v", sessions)
		}
	}
	if got := channelModelNames(t, h); !strings.Contains(got, "glm-5.3") {
		t.Fatalf("/v1/models missing GLM: %s", got)
	}

	saveTestChannel(t, s, Channel{Name: "glm-close", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "glm-5.3-flash", RemoteModel: "glm-5.3-flash", Enabled: true, Kind: "opencode_go"})
	status, body := postJSON(t, h.URL+"/v1/chat/completions", `{"model":"glm-5.3-flash","messages":[],"tools":[{"type":"function","function":{"name":"x"}}]}`)
	if status != 503 {
		t.Fatalf("tool request must not route to a channel without tools: %d %s", status, body)
	}

	var forwarded int
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded++
		io.WriteString(w, `{"ok":true}`)
	}))
	defer good.Close()
	s2, h2 := newTestServer(t)
	saveTestChannel(t, s2, Channel{Name: "glm-bad", BaseURL: bad.URL + "/v1", APIKey: "k", Model: "glm-5.3", RemoteModel: "glm-5.3", Enabled: true, Kind: "opencode_go", Priority: 0})
	saveTestChannel(t, s2, Channel{Name: "glm-good", BaseURL: good.URL + "/v1", APIKey: "k", Model: "glm-5.3", RemoteModel: "glm-5.3", Enabled: true, Kind: "opencode_go", Priority: 1})
	if status, body := postJSON(t, h2.URL+"/v1/chat/completions", `{"model":"glm-5.3","messages":[]}`); status != 200 || forwarded != 1 {
		t.Fatalf("GLM failover status=%d body=%s forwarded=%d", status, body, forwarded)
	}
}

func channelModelNames(t *testing.T, h *httptest.Server) string {
	t.Helper()
	resp, err := http.Get(h.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("/v1/models = %d: %s", resp.StatusCode, body)
	}
	return string(body)
}

func TestGLMPricingFormulaAndNormalization(t *testing.T) {
	const million = int64(1_000_000)
	cases := []struct {
		model          string
		hit, miss, out int64
		wantNano       int64
	}{
		{"glm-5.3", million, 0, 0, 2_000_000_000},
		{"glm-5.3", 0, million, 0, 8_000_000_000},
		{"glm-5.3", 0, 0, million, 28_000_000_000},
		{"glm-5.3-flash", million, 0, 0, 230_000_000},
		{"glm-5.3-flash", 0, million, 0, 800_000_000},
		{"glm-5.3-flash", 0, 0, million, 2_800_000_000},
		{"glm-5.3", 1000, 2000, 300, 26_400_000},
	}
	for _, tc := range cases {
		rates, canonical, ok := pricingRatesFor(tc.model)
		if !ok {
			t.Fatalf("model %q not priced", tc.model)
		}
		if got := computeCostNano(rates, "flat", tc.hit, tc.miss, tc.out); got != tc.wantNano {
			t.Errorf("%s hit=%d miss=%d out=%d cost=%d want %d (canonical %s)", tc.model, tc.hit, tc.miss, tc.out, got, tc.wantNano, canonical)
		}
	}
	for _, model := range []string{" GLM-5.3 ", "zhipuai/glm-5.3", "zhipu/glm-5.3"} {
		_, canonical, ok := pricingRatesFor(model)
		if !ok || canonical != "glm-5.3" {
			t.Fatalf("model %q canonical=%q ok=%v", model, canonical, ok)
		}
	}
	for _, model := range []string{"glm-5.4", "glm-5.3-flashx", "glm-unknown"} {
		if _, _, ok := pricingRatesFor(model); ok {
			t.Fatalf("unknown GLM model %q must stay unpriced", model)
		}
	}
}

func TestGLMPriceAttemptFlatPeriodAndUnknowns(t *testing.T) {
	s, _ := newPricingServer(t)
	usage := tokensUsageAt(int64Ptr(1_000_000), int64Ptr(0), int64Ptr(0))
	for _, at := range []time.Time{
		beijingAt(2026, time.November, 13, 10, 0).UTC(), // Friday peak for DeepSeek
		beijingAt(2026, time.November, 13, 8, 0).UTC(),  // Friday offpeak for DeepSeek
	} {
		got := s.priceAttempt("opencode_go", "zhipuai/glm-5.3", 200, at, usage)
		if got.CostNano == nil || *got.CostNano != 2_000_000_000 || got.Period != "flat" || got.Model != "glm-5.3" || got.Reason != "" {
			t.Fatalf("GLM attempt at %s = %+v", at, got)
		}
	}
	if got := s.priceAttempt("deepseek", "deepseek-flash", 200, beijingAt(2026, time.November, 13, 10, 0).UTC(), usage); got.Period != "peak" {
		t.Fatalf("DeepSeek period changed: %+v", got)
	}

	var missingSplit usageTotals
	missingSplit.parse([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	got := s.priceAttempt("opencode_go", "glm-5.3", 200, beijingAt(2026, time.November, 13, 10, 0).UTC(), &missingSplit)
	if got.CostNano == nil || *got.CostNano != 10*8000+5*28000 || got.Period != "flat" {
		t.Fatalf("missing cache split GLM attempt = %+v", got)
	}
	if _, _, hit, miss := missingSplit.normalized(); hit != nil || miss != nil {
		t.Fatalf("missing cache split must stay unknown: hit=%v miss=%v", hit, miss)
	}

	if unknown := s.priceAttempt("opencode_go", "glm-5.4", 200, beijingAt(2026, time.November, 13, 10, 0).UTC(), usage); unknown.CostNano != nil || unknown.Reason != "unpriced" {
		t.Fatalf("unpriced GLM attempt = %+v", unknown)
	}
	if failed := s.priceAttempt("opencode_go", "glm-5.3", 429, beijingAt(2026, time.November, 13, 10, 0).UTC(), usage); failed.CostNano != nil || failed.Reason != "failed" {
		t.Fatalf("failed GLM attempt = %+v", failed)
	}
	zero := int64(0)
	if free := s.priceAttempt("opencode_go", "glm-5.3-flash", 200, beijingAt(2026, time.November, 13, 10, 0).UTC(), tokensUsageAt(&zero, &zero, &zero)); free.CostNano == nil || *free.CostNano != 0 || free.Period != "flat" {
		t.Fatalf("zero-token GLM attempt = %+v", free)
	}
}

func TestGLMRecordAndUsageStats(t *testing.T) {
	s, h := newUsageStatsServer(t)
	key := addUsageStatsKey(t, s, "glm", true)
	start := beijingAt(2026, time.November, 13, 10, 0).UTC()
	usage := tokensUsageAt(int64Ptr(1_000_000), int64Ptr(0), int64Ptr(0))
	channel := Channel{Name: "glm", Kind: "opencode_go", RemoteModel: "glm-5.3"}
	s.record("glm-5.3", channel, http.StatusOK, start, usage)
	entry := lastHistory(t, h)
	if entry.CostCNY == nil || math.Abs(*entry.CostCNY-2) > 1e-12 || entry.PricePeriod == nil || *entry.PricePeriod != "flat" || entry.PricingModel == nil || *entry.PricingModel != "glm-5.3" {
		t.Fatalf("GLM history entry = %+v", entry)
	}

	w := &usageWriter{ResponseWriter: httptest.NewRecorder(), channelKind: "opencode_go", channelModel: "glm-5.3", channelName: "glm"}
	w.parse([]byte(`{"usage":{"prompt_tokens":1000000,"completion_tokens":0,"prompt_cache_hit_tokens":1000000,"prompt_cache_miss_tokens":0}}`))
	s.recordAccess(key.ID, "/v1/chat/completions", http.StatusOK, start, w)
	var period *string
	var cost *int64
	if err := s.db.QueryRow(`SELECT price_period,cost_nano_cny FROM access_requests ORDER BY id DESC LIMIT 1`).Scan(&period, &cost); err != nil {
		t.Fatal(err)
	}
	if period == nil || *period != "flat" || cost == nil || *cost != 2_000_000_000 {
		t.Fatalf("GLM access cost=%v period=%v", cost, period)
	}

	status, body := fetchUsageStats(t, h)
	stats := decodeUsageStats(t, status, body)
	assertCost(t, "overall GLM cost", stats.Overall.CostCNY, 2_000_000_000)
	if stats.Overall.CostKnownCount != 1 || stats.Overall.CostUnknownCount != 0 {
		t.Fatalf("GLM cost counts = %d/%d", stats.Overall.CostKnownCount, stats.Overall.CostUnknownCount)
	}
	glm := findUsageStatsRate(stats.Pricing.Models, "glm-5.3")
	if glm == nil || glm.Source != pricingSourceZhipu || !strings.Contains(glm.SourceURL, "docs.bigmodel.cn") || !glm.FlatPeriod {
		t.Fatalf("GLM pricing metadata = %+v", glm)
	}
}

func findUsageStatsRate(rates []usageStatsPricingRateBody, model string) *usageStatsPricingRateBody {
	for i := range rates {
		if rates[i].Model == model {
			return &rates[i]
		}
	}
	return nil
}

func TestGLMPricingSnapshotSyncMergeAndHistory(t *testing.T) {
	s, _ := newPricingServer(t)
	info := s.pricingInfo()
	if len(info.Models) != 4 {
		t.Fatalf("bundled models = %+v, want DeepSeek and GLM", info.Models)
	}
	glm := findPricingRate(info.Models, "glm-5.3")
	if glm == nil {
		t.Fatalf("missing GLM bundled rate: %+v", info.Models)
	}
	if glm.Source != pricingSourceZhipu || !strings.Contains(glm.SourceURL, "docs.bigmodel.cn") || !glm.FlatPeriod {
		t.Fatalf("GLM source/flat metadata = %+v", glm)
	}
	assertUnitRate(t, "glm hit", glm.HitOff, 2)
	assertUnitRate(t, "glm miss", glm.MissOff, 8)
	assertUnitRate(t, "glm output", glm.OutOff, 28)
	if glm.HitOff != glm.HitPeak || glm.MissOff != glm.MissPeak || glm.OutOff != glm.OutPeak {
		t.Fatalf("GLM must be flat across periods: %+v", glm)
	}
	flash := findPricingRate(info.Models, "glm-5.3-flash")
	if flash == nil {
		t.Fatalf("missing GLM flash bundled rate: %+v", info.Models)
	}
	assertUnitRate(t, "glm-flash hit", flash.HitOff, 0.23)
	assertUnitRate(t, "glm-flash miss", flash.MissOff, 0.8)
	assertUnitRate(t, "glm-flash output", flash.OutOff, 2.8)
	if flash.Source != pricingSourceZhipu || !flash.FlatPeriod {
		t.Fatalf("GLM flash metadata = %+v", flash)
	}
	deepseek := findPricingRate(info.Models, "deepseek-flash")
	if deepseek == nil || deepseek.Source != pricingSourceDeepSeek || deepseek.FlatPeriod {
		t.Fatalf("DeepSeek source/flat metadata = %+v", deepseek)
	}

	changed := strings.Replace(standardPricingDoc(), "0.04元", "0.10元", 1)
	pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, changed)
	}))
	if err := s.SyncPricing(context.Background()); err != nil {
		t.Fatal(err)
	}
	info = s.pricingInfo()
	if got := findPricingRate(info.Models, "deepseek-flash"); got == nil || math.Abs(got.HitPeak-0.1) > 1e-12 {
		t.Fatalf("DeepSeek remote rate not applied: %+v", got)
	}
	if got := findPricingRate(info.Models, "glm-5.3"); got == nil || math.Abs(got.MissOff-8) > 1e-12 {
		t.Fatalf("DeepSeek sync overwrote GLM: %+v", got)
	}
	if len(info.History) != 1 {
		t.Fatalf("history = %+v, want one snapshot", info.History)
	}
	historyGLM := findPricingRate(info.History[0].Models, "glm-5.3")
	if historyGLM == nil || historyGLM.Source != pricingSourceZhipu || info.History[0].Source != "remote" {
		t.Fatalf("history GLM source = %+v in snapshot source %q", historyGLM, info.History[0].Source)
	}
	if got := s.priceAttempt("opencode_go", "glm-5.3", 200, beijingAt(2026, time.November, 13, 10, 0).UTC(), tokensUsageAt(int64Ptr(1_000_000), int64Ptr(0), int64Ptr(0))); got.CostNano == nil || *got.CostNano != 2_000_000_000 {
		t.Fatalf("GLM not priceable after DeepSeek sync: %+v", got)
	}

	pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	if err := s.SyncPricing(context.Background()); err == nil {
		t.Fatal("expected failed sync")
	}
	info = s.pricingInfo()
	if got := findPricingRate(info.Models, "glm-5.3"); got == nil || math.Abs(got.MissOff-8) > 1e-12 {
		t.Fatalf("GLM lost after failed DeepSeek sync: %+v", got)
	}
	if got := findPricingRate(info.Models, "deepseek-flash"); got == nil || math.Abs(got.HitPeak-0.1) > 1e-12 {
		t.Fatalf("previous DeepSeek snapshot lost after failed sync: %+v", got)
	}
}

func TestGLMPricingUpgradeFromDeepSeekOnlySnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old-pricing.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := bundledPricingSnapshot()
	delete(snapshot.Rates, "glm-5.3")
	delete(snapshot.Rates, "glm-5.3-flash")
	snapshot.Source = "remote"
	snapshot.Version = "remote-20261113T120000Z"
	synced := time.Date(2026, time.November, 13, 12, 0, 0, 0, time.UTC)
	snapshot.SyncedAt = &synced
	body, err := json.Marshal(pricingStored{Snapshot: snapshot, LastSyncOK: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM pricing_history`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO pricing_state(id,body,updated_at) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,updated_at=excluded.updated_at`, string(body), synced.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	s.Close()

	reopened, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	info := reopened.pricingInfo()
	if got := findPricingRate(info.Models, "glm-5.3-flash"); got == nil {
		t.Fatalf("upgraded database did not add bundled GLM rates: %+v", info.Models)
	}
	if got := reopened.priceAttempt("opencode_go", "glm-5.3", 200, beijingAt(2026, time.November, 13, 10, 0).UTC(), tokensUsageAt(int64Ptr(1_000_000), int64Ptr(0), int64Ptr(0))); got.CostNano == nil {
		t.Fatalf("upgraded database cannot price GLM: %+v", got)
	}
}

func TestGLMDoesNotChangeSearchRouting(t *testing.T) {
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "glm", BaseURL: "http://127.0.0.1:1/v1", APIKey: "k", Model: "glm-5.3", RemoteModel: "glm-5.3", Enabled: true, Kind: "opencode_go", Paid: false})
	status, body := postJSON(t, h.URL+"/v1/tools/search", `{"query":"x"}`)
	if status != 503 {
		t.Fatalf("Search must not use GLM channels: %d %s", status, body)
	}
}

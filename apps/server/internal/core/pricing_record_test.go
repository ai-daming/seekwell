package core

import (
	"bytes"
	"database/sql"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// S6 请求记录：DeepSeek 官方渠道成功请求按开始时段计价并持久化。
func TestHistoryRecordsCost(t *testing.T) {
	s, h := newTestServer(t)
	channel := Channel{Name: "official", Kind: "deepseek", RemoteModel: "deepseek-flash"}
	usage := tokensUsageAt(int64Ptr(1_000_000), int64Ptr(0), int64Ptr(0))
	start := beijingAt(2026, time.November, 13, 10, 0).UTC()
	s.record("deepseek-flash", channel, http.StatusOK, start, usage)

	got := lastHistory(t, h)
	if got.CostCNY == nil {
		t.Fatalf("history cost is null: %+v", got)
	}
	if math.Abs(*got.CostCNY-0.04) > 1e-12 {
		t.Fatalf("history cost=%v want 0.04", *got.CostCNY)
	}
	if got.PricePeriod == nil || *got.PricePeriod != "peak" {
		t.Fatalf("price_period=%v want peak", got.PricePeriod)
	}

	offpeak := beijingAt(2026, time.November, 13, 8, 0).UTC()
	s.record("deepseek-flash", channel, http.StatusOK, offpeak, usage)
	got = lastHistory(t, h)
	if got.CostCNY == nil || math.Abs(*got.CostCNY-0.02) > 1e-12 || got.PricePeriod == nil || *got.PricePeriod != "offpeak" {
		t.Fatalf("offpeak history cost=%v period=%v", got.CostCNY, got.PricePeriod)
	}
}

// S6b 费用未知写入 NULL，不参与已知合计，并通过 API 暴露原因。
func TestHistoryCostUnknownCases(t *testing.T) {
	s, h := newTestServer(t)
	usage := tokensUsageAt(int64Ptr(10), int64Ptr(20), int64Ptr(5))
	start := beijingAt(2026, time.November, 13, 10, 0).UTC()
	cases := []struct {
		name   string
		kind   string
		model  string
		status int
		usage  *usageTotals
		reason string
	}{
		{"unknown model", "deepseek", "deepseek-mystery", 200, usage, "unpriced"},
		{"failed attempt", "deepseek", "deepseek-flash", 429, usage, "failed"},
		{"missing output", "deepseek", "deepseek-flash", 200, tokensUsageAt(nil, nil, int64Ptr(5)), "no_usage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s.record("deepseek-flash", Channel{Name: "c", Kind: tc.kind, RemoteModel: tc.model}, tc.status, start, tc.usage)
			raw := historyRaw(t, h)
			if !bytes.Contains(raw, []byte(`"cost_cny":null`)) || !bytes.Contains(raw, []byte(`"price_period":null`)) {
				t.Fatalf("history cost not null: %s", raw)
			}
			got := lastHistory(t, h)
			if got.CostUnknownReason == nil || *got.CostUnknownReason != tc.reason {
				t.Fatalf("cost_unknown_reason=%v want %q", got.CostUnknownReason, tc.reason)
			}
			if got.PricingModel != nil {
				t.Fatalf("pricing_model=%v want null", got.PricingModel)
			}
		})
	}
}

// S6b2 非官方渠道和缺缓存拆分写入非空费用；缺缓存拆分不伪造缓存 token。
func TestHistoryCostCompatibleAndMissingCacheSplit(t *testing.T) {
	s, h := newTestServer(t)
	start := beijingAt(2026, time.November, 13, 10, 0).UTC()
	full := tokensUsageAt(int64Ptr(10), int64Ptr(20), int64Ptr(5))
	s.record("deepseek-flash", Channel{Name: "compat", Kind: "compatible", RemoteModel: "deepseek-flash"}, http.StatusOK, start, full)
	got := lastHistory(t, h)
	if got.CostCNY == nil || got.PricingModel == nil || *got.PricingModel != "deepseek-flash" {
		t.Fatalf("compatible history cost/model: %+v", got)
	}
	want := nanoToCNY(10*40 + 20*2000 + 5*8000)
	if math.Abs(*got.CostCNY-want) > 1e-12 {
		t.Fatalf("compatible history cost=%v want %v", *got.CostCNY, want)
	}
	if got.CostUnknownReason != nil {
		t.Fatalf("priced entry reason=%v want null", *got.CostUnknownReason)
	}

	var usage usageTotals
	usage.parse([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	s.record("deepseek-flash", Channel{Name: "compat", Kind: "compatible", RemoteModel: "deepseek-flash"}, http.StatusOK, start, &usage)
	got = lastHistory(t, h)
	if got.CostCNY == nil || got.PricingModel == nil || *got.PricingModel != "deepseek-flash" {
		t.Fatalf("missing-cache history cost/model: %+v", got)
	}
	if got.CacheHitTokens != nil || got.CacheMissTokens != nil {
		t.Fatalf("cache split must stay null: hit=%v miss=%v", got.CacheHitTokens, got.CacheMissTokens)
	}
	want = nanoToCNY(10*2000 + 5*8000)
	if math.Abs(*got.CostCNY-want) > 1e-12 {
		t.Fatalf("missing-cache cost=%v want %v", *got.CostCNY, want)
	}
}

// S6c 访问 Key 记录实际渠道、模型、缓存拆分和费用；非官方渠道为 NULL。
func TestAccessRequestRecordsCost(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "access-cost.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key, err := s.newAccessKey("cost")
	if err != nil {
		t.Fatal(err)
	}
	start := beijingAt(2026, time.November, 13, 8, 0).UTC() // offpeak
	w := &usageWriter{ResponseWriter: httptest.NewRecorder(), channelKind: "deepseek", channelModel: "deepseek-flash", channelName: "official"}
	w.parse([]byte(`{"usage":{"prompt_tokens":1000,"completion_tokens":0,"prompt_cache_hit_tokens":1000,"prompt_cache_miss_tokens":0}}`))
	s.recordAccess(key.ID, "/v1/chat/completions", http.StatusOK, start, w)

	var model, kind, period *string
	var hit, miss, cost *int64
	err = s.db.QueryRow(`SELECT model,channel,kind,cache_hit_tokens,cache_miss_tokens,cost_nano_cny,price_period FROM access_requests WHERE key_id=?`, key.ID).
		Scan(&model, new(string), &kind, &hit, &miss, &cost, &period)
	if err != nil {
		t.Fatal(err)
	}
	if model == nil || *model != "deepseek-flash" || kind == nil || *kind != "deepseek" {
		t.Fatalf("channel/model not recorded: %v %v", model, kind)
	}
	if hit == nil || *hit != 1000 || miss == nil || *miss != 0 {
		t.Fatalf("cache split not recorded: %v %v", hit, miss)
	}
	if cost == nil || *cost != 1000*20 || period == nil || *period != "offpeak" {
		t.Fatalf("cost=%v period=%v want 20000/offpeak", cost, period)
	}

	compatible := &usageWriter{ResponseWriter: httptest.NewRecorder(), channelKind: "compatible", channelModel: "deepseek-flash", channelName: "compat"}
	compatible.parse([]byte(`{"usage":{"prompt_tokens":1000,"completion_tokens":0,"prompt_cache_hit_tokens":1000,"prompt_cache_miss_tokens":0}}`))
	s.recordAccess(key.ID, "/v1/chat/completions", http.StatusOK, start, compatible)
	var compatibleCost *int64
	if err := s.db.QueryRow(`SELECT cost_nano_cny FROM access_requests ORDER BY id DESC LIMIT 1`).Scan(&compatibleCost); err != nil {
		t.Fatal(err)
	}
	if compatibleCost == nil || *compatibleCost != 1000*20 {
		t.Fatalf("compatible channel cost=%v want 20000", compatibleCost)
	}

	unknown := &usageWriter{ResponseWriter: httptest.NewRecorder(), channelKind: "compatible", channelModel: "deepseek-mystery", channelName: "compat"}
	unknown.parse([]byte(`{"usage":{"prompt_tokens":1000,"completion_tokens":0,"prompt_cache_hit_tokens":1000,"prompt_cache_miss_tokens":0}}`))
	s.recordAccess(key.ID, "/v1/chat/completions", http.StatusOK, start, unknown)
	var unknownCost *int64
	if err := s.db.QueryRow(`SELECT cost_nano_cny FROM access_requests ORDER BY id DESC LIMIT 1`).Scan(&unknownCost); err != nil {
		t.Fatal(err)
	}
	if unknownCost != nil {
		t.Fatalf("unknown model cost=%d want null", *unknownCost)
	}
}

// S8 旧库迁移：新列自动补齐，旧行为 NULL，扫描不报错。
func TestPricingMigrationFromOldSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE channels (id INTEGER PRIMARY KEY, name TEXT NOT NULL, base_url TEXT NOT NULL, api_key TEXT NOT NULL, model TEXT NOT NULL, remote_model TEXT NOT NULL, enabled INTEGER NOT NULL, paid INTEGER NOT NULL, auto_paid INTEGER NOT NULL, priority INTEGER NOT NULL, kind TEXT NOT NULL);
		CREATE TABLE requests (id INTEGER PRIMARY KEY, created_at TEXT NOT NULL, model TEXT NOT NULL, channel TEXT NOT NULL, status INTEGER NOT NULL, duration_ms INTEGER NOT NULL, kind TEXT NOT NULL DEFAULT '', input_tokens INTEGER, output_tokens INTEGER, cache_hit_tokens INTEGER, cache_miss_tokens INTEGER);
		CREATE TABLE access_keys (id INTEGER PRIMARY KEY, name TEXT NOT NULL, key_hash TEXT NOT NULL UNIQUE, prefix TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL);
		CREATE TABLE access_requests (id INTEGER PRIMARY KEY, key_id INTEGER NOT NULL, created_at TEXT NOT NULL, path TEXT NOT NULL, status INTEGER NOT NULL, duration_ms INTEGER NOT NULL, input_tokens INTEGER, output_tokens INTEGER);
		INSERT INTO requests(created_at,model,channel,status,duration_ms,kind,input_tokens,output_tokens) VALUES('2026-11-01T00:00:00Z','deepseek-flash','old',200,10,'deepseek',10,2);
		INSERT INTO access_keys(id,name,key_hash,prefix,enabled,created_at) VALUES(1,'old','hash','sw-old',1,'2026-11-01T00:00:00Z');
		INSERT INTO access_requests(key_id,created_at,path,status,duration_ms,input_tokens,output_tokens) VALUES(1,'2026-11-01T00:00:00Z','/v1/chat/completions',200,10,10,2);`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, column := range []string{"cost_nano_cny", "price_period", "pricing_model", "pricing_version"} {
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('requests') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("requests missing column %s: %d %v", column, count, err)
		}
	}
	for _, column := range []string{"model", "channel", "kind", "cache_hit_tokens", "cache_miss_tokens", "cost_nano_cny", "price_period", "pricing_model", "pricing_version"} {
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('access_requests') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("access_requests missing column %s: %d %v", column, count, err)
		}
	}
	var cost *int64
	var period *string
	if err := s.db.QueryRow(`SELECT cost_nano_cny,price_period FROM requests WHERE id=1`).Scan(&cost, &period); err != nil {
		t.Fatal(err)
	}
	if cost != nil || period != nil {
		t.Fatalf("old request got guessed cost: %v %v", cost, period)
	}
	if err := s.db.QueryRow(`SELECT cost_nano_cny FROM access_requests WHERE id=1`).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if cost != nil {
		t.Fatalf("old access request got guessed cost: %v", cost)
	}

	overall, err := s.overallUsage()
	if err != nil {
		t.Fatal(err)
	}
	if overall.CostKnownCount != 0 || overall.CostUnknownCount != 1 || overall.CostCNY != nil {
		t.Fatalf("old stats cost: %+v", overall.UsageTotals)
	}
	var historyCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pricing_history`).Scan(&historyCount); err != nil || historyCount != 0 {
		t.Fatalf("old database history count=%d err=%v", historyCount, err)
	}
	if info := s.pricingInfo(); info.Source != "bundled" || len(info.History) != 0 {
		t.Fatalf("old database pricing info: %+v", info)
	}

	// New requests still price and write on an upgraded database.
	start := beijingAt(2026, time.November, 13, 10, 0).UTC()
	s.record("deepseek-flash", testChannel(), 200, start, tokensUsageAt(int64Ptr(1_000_000), int64Ptr(0), int64Ptr(0)))
	var newCost *int64
	if err := s.db.QueryRow(`SELECT cost_nano_cny FROM requests ORDER BY id DESC LIMIT 1`).Scan(&newCost); err != nil {
		t.Fatal(err)
	}
	if newCost == nil || *newCost != 40_000_000 {
		t.Fatalf("upgraded database cannot price: %v", newCost)
	}
}

// S6d 真实代理链路：成功安装 usage 的 DeepSeek 官方请求在访问 Key 记录上带费用。
func TestAccessRequestCostFromProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":10,"prompt_cache_hit_tokens":600,"prompt_cache_miss_tokens":400}}`))
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "official", Kind: "deepseek", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true})
	postModel(t, h, "/v1/chat/completions", `{"model":"deepseek-flash","messages":[]}`)

	var model, channel, kind, period *string
	var hit, miss, cost *int64
	err := s.db.QueryRow(`SELECT model,channel,kind,cache_hit_tokens,cache_miss_tokens,cost_nano_cny,price_period FROM access_requests ORDER BY id DESC LIMIT 1`).
		Scan(&model, &channel, &kind, &hit, &miss, &cost, &period)
	if err != nil {
		t.Fatal(err)
	}
	if model == nil || *model != "deepseek-flash" || channel == nil || *channel != "official" || kind == nil || *kind != "deepseek" {
		t.Fatalf("access channel/model: %v %v %v", model, channel, kind)
	}
	if hit == nil || *hit != 600 || miss == nil || *miss != 400 {
		t.Fatalf("access cache split: %v %v", hit, miss)
	}
	if cost == nil || period == nil {
		t.Fatalf("access cost=%v period=%v", cost, period)
	}
	rates, _, ok := pricingRatesFor("deepseek-flash")
	if !ok {
		t.Fatal("missing bundled rates")
	}
	want := computeCostNano(rates, *period, 600, 400, 10)
	if *cost != want {
		t.Fatalf("access cost=%d want %d (%s)", *cost, want, *period)
	}
}

// S7d 非官方渠道的官方等价费用计入 overall / key / daily，并与逐条记录一致。
func TestAccessRequestCompatibleCostAggregation(t *testing.T) {
	s, h := newUsageStatsServer(t)
	key, err := s.newAccessKey("compatible-cost")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for _, body := range []string{
		`{"usage":{"prompt_tokens":1000,"completion_tokens":10,"prompt_cache_hit_tokens":600,"prompt_cache_miss_tokens":400}}`,
		`{"usage":{"prompt_tokens":100,"completion_tokens":5}}`,
	} {
		w := &usageWriter{ResponseWriter: httptest.NewRecorder(), channelKind: "compatible", channelModel: "deepseek-v4.1-flash", channelName: "compat"}
		w.parse([]byte(body))
		s.recordAccess(key.ID, "/v1/chat/completions", http.StatusOK, start, w)
	}

	var expectedNano int64
	if err := s.db.QueryRow(`SELECT SUM(cost_nano_cny) FROM access_requests WHERE key_id=?`, key.ID).Scan(&expectedNano); err != nil {
		t.Fatal(err)
	}
	if expectedNano <= 0 {
		t.Fatal("compatible requests were not priced")
	}
	status, body := fetchUsageStats(t, h)
	got := decodeUsageStats(t, status, body)
	assertCost(t, "overall compatible cost", got.Overall.CostCNY, expectedNano)
	if got.Overall.CostKnownCount != 2 || got.Overall.CostUnknownCount != 0 {
		t.Fatalf("overall cost counts=%d/%d", got.Overall.CostKnownCount, got.Overall.CostUnknownCount)
	}
	if len(got.Keys) != 1 {
		t.Fatalf("keys=%+v", got.Keys)
	}
	assertCost(t, "key compatible cost", got.Keys[0].CostCNY, expectedNano)
	if got.Keys[0].CostKnownCount != 2 || got.Keys[0].CostUnknownCount != 0 {
		t.Fatalf("key cost counts=%d/%d", got.Keys[0].CostKnownCount, got.Keys[0].CostUnknownCount)
	}
	today := time.Now().Format("2006-01-02")
	found := false
	for _, day := range got.Daily {
		if day.Date == today {
			found = true
			assertCost(t, "daily compatible cost", day.CostCNY, expectedNano)
			if day.CostKnownCount != 2 || day.CostUnknownCount != 0 {
				t.Fatalf("daily cost counts=%d/%d", day.CostKnownCount, day.CostUnknownCount)
			}
		}
	}
	if !found {
		t.Fatalf("today missing from daily: %+v", got.Daily)
	}
}

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

// S6b 未知费用写入 NULL，不参与已知合计。
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
	}{
		{"compatible channel", "compatible", "deepseek-flash", 200, usage},
		{"unknown model", "deepseek", "deepseek-mystery", 200, usage},
		{"failed attempt", "deepseek", "deepseek-flash", 429, usage},
		{"missing cache split", "deepseek", "deepseek-flash", 200, tokensUsageAt(nil, nil, int64Ptr(5))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s.record("deepseek-flash", Channel{Name: "c", Kind: tc.kind, RemoteModel: tc.model}, tc.status, start, tc.usage)
			raw := historyRaw(t, h)
			if !bytes.Contains(raw, []byte(`"cost_cny":null`)) || !bytes.Contains(raw, []byte(`"price_period":null`)) {
				t.Fatalf("history cost not null: %s", raw)
			}
		})
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

	unknown := &usageWriter{ResponseWriter: httptest.NewRecorder(), channelKind: "compatible", channelModel: "deepseek-flash", channelName: "compat"}
	unknown.parse([]byte(`{"usage":{"prompt_tokens":1000,"completion_tokens":0,"prompt_cache_hit_tokens":1000,"prompt_cache_miss_tokens":0}}`))
	s.recordAccess(key.ID, "/v1/chat/completions", http.StatusOK, start, unknown)
	var unknownCost *int64
	if err := s.db.QueryRow(`SELECT cost_nano_cny FROM access_requests ORDER BY id DESC LIMIT 1`).Scan(&unknownCost); err != nil {
		t.Fatal(err)
	}
	if unknownCost != nil {
		t.Fatalf("compatible channel cost=%d want null", *unknownCost)
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

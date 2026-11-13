package core

import (
	"math"
	"testing"
	"time"
)

func addCostAccessRequest(t *testing.T, s *Server, keyID int64, at time.Time, status int, costNano *int64) {
	t.Helper()
	var cost any
	if costNano != nil {
		cost = *costNano
	}
	_, err := s.db.Exec(`INSERT INTO access_requests(key_id,created_at,path,status,duration_ms,input_tokens,output_tokens,cost_nano_cny) VALUES(?,?,?,?,?,?,?,?)`,
		keyID, at.UTC().Format(time.RFC3339), "/v1/chat/completions", status, 12, int64Ptr(10), int64Ptr(2), cost)
	if err != nil {
		t.Fatal(err)
	}
}

func assertCost(t *testing.T, label string, value *float64, wantNano int64) {
	t.Helper()
	if value == nil {
		t.Fatalf("%s = null, want %v", label, nanoToCNY(wantNano))
	}
	if math.Abs(*value-nanoToCNY(wantNano)) > 1e-12 {
		t.Fatalf("%s = %v, want %v", label, *value, nanoToCNY(wantNano))
	}
}

func assertNilCost(t *testing.T, label string, value *float64) {
	t.Helper()
	if value != nil {
		t.Fatalf("%s = %v, want null", label, *value)
	}
}

// S7 统计费用聚合：overall / keys / daily 同口径，只累加已知项并统计未知覆盖。
func TestUsageStatsCostAggregation(t *testing.T) {
	s, h := newUsageStatsServer(t)
	key1 := addUsageStatsKey(t, s, "Alice", true)
	key2 := addUsageStatsKey(t, s, "Bob", true)
	base := time.Now()
	addCostAccessRequest(t, s, key1.ID, localAt(base, 0), 200, int64Ptr(1_000_000))
	addCostAccessRequest(t, s, key1.ID, localAt(base, 0), 200, nil)
	addCostAccessRequest(t, s, key2.ID, localAt(base, 0), 200, int64Ptr(0))
	addCostAccessRequest(t, s, key1.ID, localAt(base, 1), 200, int64Ptr(2_500_000))
	addCostAccessRequest(t, s, key1.ID, localAt(base, 8), 200, int64Ptr(9_000_000))

	status, body := fetchUsageStats(t, h)
	got := decodeUsageStats(t, status, body)

	assertCost(t, "overall.cost_cny", got.Overall.CostCNY, 12_500_000)
	if got.Overall.CostKnownCount != 4 || got.Overall.CostUnknownCount != 1 {
		t.Fatalf("overall cost counts = %d/%d, want 4/1", got.Overall.CostKnownCount, got.Overall.CostUnknownCount)
	}
	if got.Overall.CostKnownCount+got.Overall.CostUnknownCount != got.Overall.RequestCount {
		t.Fatalf("overall cost counts do not add up: %+v", got.Overall)
	}

	var alice, bob *usageStatsKeyBody
	for i := range got.Keys {
		switch got.Keys[i].Name {
		case "Alice":
			alice = &got.Keys[i]
		case "Bob":
			bob = &got.Keys[i]
		}
	}
	if alice == nil || bob == nil {
		t.Fatalf("keys: %+v", got.Keys)
	}
	assertCost(t, "alice.cost_cny", alice.CostCNY, 12_500_000)
	if alice.CostKnownCount != 3 || alice.CostUnknownCount != 1 {
		t.Fatalf("alice cost counts = %d/%d, want 3/1", alice.CostKnownCount, alice.CostUnknownCount)
	}
	assertCost(t, "bob.cost_cny", bob.CostCNY, 0)
	if bob.CostKnownCount != 1 || bob.CostUnknownCount != 0 {
		t.Fatalf("bob cost counts = %d/%d, want 1/0", bob.CostKnownCount, bob.CostUnknownCount)
	}

	today := localDate(base, 0)
	yesterday := localDate(base, 1)
	for _, day := range got.Daily {
		switch day.Date {
		case today:
			assertCost(t, "daily.today.cost_cny", day.CostCNY, 1_000_000)
			if day.CostKnownCount != 2 || day.CostUnknownCount != 1 {
				t.Fatalf("today cost counts = %d/%d", day.CostKnownCount, day.CostUnknownCount)
			}
		case yesterday:
			assertCost(t, "daily.yesterday.cost_cny", day.CostCNY, 2_500_000)
			if day.CostKnownCount != 1 || day.CostUnknownCount != 0 {
				t.Fatalf("yesterday cost counts = %d/%d", day.CostKnownCount, day.CostUnknownCount)
			}
		default:
			assertNilCost(t, "daily."+day.Date+".cost_cny", day.CostCNY)
		}
	}
}

// S7b 没有任何可计价请求时 cost_cny 为 null，不是 0。
func TestUsageStatsCostNullWithoutKnownRequests(t *testing.T) {
	s, h := newUsageStatsServer(t)
	key := addUsageStatsKey(t, s, "Alice", true)
	addCostAccessRequest(t, s, key.ID, localAt(time.Now(), 0), 200, nil)

	status, body := fetchUsageStats(t, h)
	got := decodeUsageStats(t, status, body)
	assertNilCost(t, "overall.cost_cny", got.Overall.CostCNY)
	if got.Overall.CostKnownCount != 0 || got.Overall.CostUnknownCount != 1 {
		t.Fatalf("overall cost counts = %d/%d", got.Overall.CostKnownCount, got.Overall.CostUnknownCount)
	}
	assertNilCost(t, "key.cost_cny", got.Keys[0].CostCNY)
}

// S7c 统计接口的价格快照信息：内置快照、币种单位、高峰规则、节假日年份和提示。
func TestUsageStatsPricingInfo(t *testing.T) {
	_, h := newUsageStatsServer(t)
	status, body := fetchUsageStats(t, h)
	got := decodeUsageStats(t, status, body)
	p := got.Pricing
	if p.Currency != "CNY" || p.Unit != "1M tokens" {
		t.Fatalf("currency/unit: %+v", p)
	}
	if p.Source != "bundled" || p.LastSyncAt != nil || p.LastSyncOK || !p.Stale {
		t.Fatalf("bundled pricing state: %+v", p)
	}
	if p.Warning == "" {
		t.Fatal("bundled pricing must explain itself")
	}
	if p.PeakTimezone != "Asia/Shanghai" || len(p.PeakWindows) != 2 {
		t.Fatalf("peak rules: %+v", p)
	}
	found := false
	for _, year := range p.HolidayYears {
		if year == 2026 {
			found = true
		}
	}
	if !found {
		t.Fatalf("holiday years: %+v", p.HolidayYears)
	}
	if p.SourceURL == "" {
		t.Fatal("source url missing")
	}
}

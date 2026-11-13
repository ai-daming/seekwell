package core

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testBeijing = time.FixedZone("Asia/Shanghai", 8*60*60)

func beijingAt(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, testBeijing)
}

// S1 高峰/空闲边界：北京时间工作日，用 UTC 输入也按固定 UTC+8 判断。
func TestPricePeriodBoundaries(t *testing.T) {
	cases := []struct {
		hour, minute int
		want         string
	}{
		{8, 59, "offpeak"},
		{9, 0, "peak"},
		{11, 59, "peak"},
		{12, 0, "offpeak"},
		{13, 59, "offpeak"},
		{14, 0, "peak"},
		{17, 59, "peak"},
		{18, 0, "offpeak"},
		{23, 59, "offpeak"},
	}
	for _, tc := range cases {
		at := beijingAt(2026, time.November, 13, tc.hour, tc.minute).UTC()
		if got := pricePeriodAt(at); got != tc.want {
			t.Errorf("Friday %02d:%02d UTC=%s period=%q want %q", tc.hour, tc.minute, at, got, tc.want)
		}
	}
}

// S1b 周末与内置节假日全天空闲。
func TestPricePeriodWeekendAndHoliday(t *testing.T) {
	for _, at := range []time.Time{
		beijingAt(2026, time.November, 14, 10, 0), // Saturday
		beijingAt(2026, time.November, 15, 15, 0), // Sunday
		beijingAt(2026, time.October, 1, 10, 0),   // National Day, Thursday
		beijingAt(2026, time.October, 1, 15, 0),
	} {
		if got := pricePeriodAt(at.UTC()); got != "offpeak" {
			t.Errorf("%s period=%q want offpeak", at, got)
		}
	}
	if got := pricePeriodAt(beijingAt(2026, time.November, 12, 10, 0).UTC()); got != "peak" {
		t.Fatalf("regular Thursday period=%q want peak", got)
	}
}

// S1c 节假日表未覆盖年份仍按工作日规则，但给出口径提示。
func TestPriceHolidayCoverage(t *testing.T) {
	if warning := pricingHolidayWarning(beijingAt(2026, time.November, 12, 10, 0)); warning != "" {
		t.Fatalf("covered year warning=%q want empty", warning)
	}
	if warning := pricingHolidayWarning(beijingAt(2030, time.November, 12, 10, 0)); !strings.Contains(warning, "节假日") {
		t.Fatalf("uncovered year warning=%q want holiday hint", warning)
	}
	if got := pricePeriodAt(beijingAt(2030, time.November, 12, 10, 0).UTC()); got != "peak" {
		t.Fatalf("uncovered year period=%q want weekday peak", got)
	}
}

// S2 费用公式：官方单价 × 命中/未命中/输出，nano-CNY 精确整数。
func TestPricingFormula(t *testing.T) {
	const million = int64(1_000_000)
	cases := []struct {
		model, period  string
		hit, miss, out int64
		wantNano       int64
	}{
		{"deepseek-flash", "offpeak", million, 0, 0, 20_000_000},
		{"deepseek-flash", "offpeak", 0, million, 0, 1_000_000_000},
		{"deepseek-flash", "offpeak", 0, 0, million, 4_000_000_000},
		{"deepseek-flash", "peak", million, 0, 0, 40_000_000},
		{"deepseek-flash", "peak", 0, million, 0, 2_000_000_000},
		{"deepseek-flash", "peak", 0, 0, million, 8_000_000_000},
		{"deepseek-v4-pro", "offpeak", million, 0, 0, 150_000_000},
		{"deepseek-v4-pro", "offpeak", 0, million, 0, 4_500_000_000},
		{"deepseek-v4-pro", "offpeak", 0, 0, million, 13_500_000_000},
		{"deepseek-v4-pro", "peak", million, 0, 0, 300_000_000},
		{"deepseek-v4-pro", "peak", 0, million, 0, 9_000_000_000},
		{"deepseek-v4-pro", "peak", 0, 0, million, 27_000_000_000},
		{"deepseek-flash", "offpeak", 1000, 2000, 300, 3_220_000},
		{"deepseek-v4-flash", "offpeak", million, 0, 0, 20_000_000},
		{"deepseek-v4-flash-vision-exp", "peak", 0, 0, million, 8_000_000_000},
	}
	for _, tc := range cases {
		rates, canonical, ok := pricingRatesFor(tc.model)
		if !ok {
			t.Fatalf("model %q not priced", tc.model)
		}
		got := computeCostNano(rates, tc.period, tc.hit, tc.miss, tc.out)
		if got != tc.wantNano {
			t.Errorf("%s/%s hit=%d miss=%d out=%d cost=%d want %d (canonical %s)", tc.model, tc.period, tc.hit, tc.miss, tc.out, got, tc.wantNano, canonical)
		}
	}
	if _, _, ok := pricingRatesFor("deepseek-unknown"); ok {
		t.Fatal("unknown model must not have official pricing")
	}
}

// S3 未知费用：失败、缺用量、未知模型仍为 nil，并给出可展示的原因。
func TestPricingUnknownCost(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "pricing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := beijingAt(2026, time.November, 13, 10, 0).UTC()
	usageWith := func(body string) *usageTotals {
		var usage usageTotals
		usage.parse([]byte(body))
		return &usage
	}
	hit, miss, out := int64(10), int64(20), int64(5)
	cases := []struct {
		name   string
		kind   string
		model  string
		status int
		usage  *usageTotals
		reason string
	}{
		{"unknown remote model", "deepseek", "deepseek-mystery", 200, tokensUsageAt(&hit, &miss, &out), "unpriced"},
		{"unknown compatible model", "compatible", "deepseek-mystery", 200, tokensUsageAt(&hit, &miss, &out), "unpriced"},
		{"missing output", "deepseek", "deepseek-flash", 200, usageWith(`{"usage":{"prompt_tokens":10,"prompt_cache_hit_tokens":4,"prompt_cache_miss_tokens":6}}`), "no_usage"},
		{"only output", "compatible", "deepseek-flash", 200, usageWith(`{"usage":{"completion_tokens":5}}`), "no_usage"},
		{"no usage", "deepseek", "deepseek-flash", 200, nil, "no_usage"},
		{"failed attempt", "deepseek", "deepseek-flash", 429, tokensUsageAt(&hit, &miss, &out), "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.priceAttempt(tc.kind, tc.model, tc.status, at, tc.usage)
			if got.CostNano != nil || got.Period != "" || got.Model != "" {
				t.Fatalf("cost=%v period=%q model=%q want unknown", got.CostNano, got.Period, got.Model)
			}
			if got.Reason != tc.reason {
				t.Fatalf("reason=%q want %q", got.Reason, tc.reason)
			}
		})
	}
}

// S3a 非官方渠道与缺缓存拆分也按官方价推算；缺少缓存拆分按全部未命中估算。
func TestPricingNonOfficialAndMissingCacheSplit(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "pricing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := beijingAt(2026, time.November, 13, 10, 0).UTC() // Friday peak
	complete := tokensUsageAt(int64Ptr(10), int64Ptr(20), int64Ptr(5))
	for _, kind := range []string{"compatible", "opencode_go", "dimagent"} {
		t.Run(kind+" complete usage", func(t *testing.T) {
			got := s.priceAttempt(kind, "deepseek-flash", 200, at, complete)
			if got.CostNano == nil || *got.CostNano != 10*40+20*2000+5*8000 {
				t.Fatalf("cost=%v want peak formula", got.CostNano)
			}
			if got.Model != "deepseek-flash" || got.Period != "peak" || got.Reason != "" {
				t.Fatalf("attempt price=%+v", got)
			}
		})
	}

	var usage usageTotals
	usage.parse([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	got := s.priceAttempt("compatible", "deepseek-flash", 200, at, &usage)
	if got.CostNano == nil || *got.CostNano != 10*2000+5*8000 {
		t.Fatalf("missing cache split cost=%v want input as miss + output", got.CostNano)
	}
	if got.Model != "deepseek-flash" || got.Period != "peak" {
		t.Fatalf("missing cache split attempt=%+v", got)
	}
}

// S3c 渠道上游模型名按官方别名归一；直接命中官方价目键优先于别名。
func TestPricingModelAliases(t *testing.T) {
	cases := []struct {
		name, model, want string
	}{
		{"opencode flash", "deepseek-v4.1-flash", "deepseek-flash"},
		{"opencode dated flash", "deepseek-v4.1-flash-1017", "deepseek-flash"},
		{"slash prefix", "deepseek/deepseek-v4.1-flash", "deepseek-flash"},
		{"case and spaces", "  DeepSeek/DeepSeek-V4.1-Flash  ", "deepseek-flash"},
		{"dimagent pro", "deepseek-pro", "deepseek-v4-pro"},
		{"alternate pro", "deepseek-v4.1-pro", "deepseek-v4-pro"},
		{"legacy flash", "deepseek-v4-flash", "deepseek-flash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, canonical, ok := pricingRatesFor(tc.model)
			if !ok || canonical != tc.want {
				t.Fatalf("model=%q canonical=%q ok=%v want %q", tc.model, canonical, ok, tc.want)
			}
		})
	}

	rates := map[string]priceRates{
		"deepseek-v4.1-flash": {OutOff: 123},
		"deepseek-flash":      {OutOff: 20},
	}
	rate, canonical, ok := ratesForMap(rates, "deepseek-v4.1-flash")
	if !ok || canonical != "deepseek-v4.1-flash" || rate.OutOff != 123 {
		t.Fatalf("direct official key must win over alias: %+v %q %v", rate, canonical, ok)
	}
}

// S3d 非官方渠道的别名模型按当时官方价算出金额。
func TestPricingAliasedChannelCost(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "pricing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := beijingAt(2026, time.November, 13, 10, 0).UTC()
	usage := tokensUsageAt(int64Ptr(0), int64Ptr(1_000_000), int64Ptr(0))
	for _, tc := range []struct{ kind, model string }{
		{"opencode_go", "deepseek-v4.1-flash"},
		{"dimagent", "deepseek-pro"},
	} {
		got := s.priceAttempt(tc.kind, tc.model, 200, at, usage)
		if got.CostNano == nil {
			t.Fatalf("%s/%s not priced", tc.kind, tc.model)
		}
		if tc.model == "deepseek-pro" && *got.CostNano != 9_000_000_000 {
			t.Fatalf("pro cost=%d want 9 CNY worth of nano", *got.CostNano)
		}
	}
}

func tokensUsageAt(hit, miss, out *int64) *usageTotals {
	usage := &usageTotals{}
	if hit != nil {
		usage.cacheHit, usage.hasCacheHit = *hit, true
	}
	if miss != nil {
		usage.cacheMiss, usage.hasCacheMiss = *miss, true
	}
	if out != nil {
		usage.output, usage.hasOutput = *out, true
	}
	if hit != nil && miss != nil {
		usage.input, usage.hasInput = *hit+*miss, true
	}
	return usage
}

// S3b 费用为 0 与极小费用：已知 0 不是未知，1 token 也不会被抹成 0。
func TestPricingZeroAndTinyCost(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "pricing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := beijingAt(2026, time.November, 13, 10, 0).UTC()
	zero := int64(0)
	got := s.priceAttempt("deepseek", "deepseek-flash", 200, at, tokensUsageAt(&zero, &zero, &zero))
	if got.CostNano == nil || *got.CostNano != 0 || got.Period != "peak" {
		t.Fatalf("zero tokens cost=%v period=%q, want known 0/peak", got.CostNano, got.Period)
	}
	one, zero2 := int64(1), int64(0)
	tiny := s.priceAttempt("deepseek", "deepseek-flash", 200, at, tokensUsageAt(&one, &zero2, &zero2))
	if tiny.CostNano == nil || *tiny.CostNano != 40 {
		t.Fatalf("1 hit token peak cost=%v, want 40 nano", tiny.CostNano)
	}
	if value := nanoToCNY(*tiny.CostNano); math.Abs(value-0.00000004) > 1e-18 {
		t.Fatalf("1 hit token cny=%v", value)
	}
}

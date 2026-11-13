package core

import (
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	pricingCurrency = "CNY"
	pricingUnit     = "1M tokens"
	pricingURL      = "https://api-docs.deepseek.com/zh-cn/quick_start/pricing.md"
	peakTimezone    = "Asia/Shanghai"
)

// Beijing time has no daylight saving time; use a fixed offset so pricing never
// depends on the host tzdata.
var beijingZone = time.FixedZone(peakTimezone, 8*60*60)

var peakWindows = []string{"09:00-12:00", "14:00-18:00"}

// priceRates holds the official price in nano-CNY per token for one model.
// 1 CNY = 1e9 nano-CNY, 1M tokens -> price * 1000 nano per token.
type priceRates struct {
	HitOff   int64 `json:"hit_off"`
	HitPeak  int64 `json:"hit_peak"`
	MissOff  int64 `json:"miss_off"`
	MissPeak int64 `json:"miss_peak"`
	OutOff   int64 `json:"out_off"`
	OutPeak  int64 `json:"out_peak"`
}

type pricingSnapshot struct {
	Version   string                `json:"version"`
	Currency  string                `json:"currency"`
	Unit      string                `json:"unit"`
	Source    string                `json:"source"`
	SourceURL string                `json:"source_url"`
	SyncedAt  *time.Time            `json:"synced_at,omitempty"`
	Rates     map[string]priceRates `json:"rates"`
}

type pricingState struct {
	mu            sync.Mutex
	snapshot      pricingSnapshot
	lastAttemptAt time.Time
	lastSyncOK    bool
	lastError     string
	syncing       bool
}

// PricingInfo is the read-only pricing block in GET /api/usage/stats.
type PricingInfo struct {
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

// pricingModelAliases maps the legacy Flash names that the official page still
// bills as deepseek-flash. Comparison is trim + ASCII lowercase; no fuzzy match.
var pricingModelAliases = map[string]string{
	"deepseek-v4-flash":            "deepseek-flash",
	"deepseek-v4-flash-vision-exp": "deepseek-flash",
}

func bundledPricingRates() map[string]priceRates {
	return map[string]priceRates{
		"deepseek-flash": {
			HitOff: 20, HitPeak: 40,
			MissOff: 1000, MissPeak: 2000,
			OutOff: 4000, OutPeak: 8000,
		},
		"deepseek-v4-pro": {
			HitOff: 150, HitPeak: 300,
			MissOff: 4500, MissPeak: 9000,
			OutOff: 13500, OutPeak: 27000,
		},
	}
}

func bundledPricingSnapshot() pricingSnapshot {
	return pricingSnapshot{
		Version:   "bundled-2026-11-13",
		Currency:  pricingCurrency,
		Unit:      pricingUnit,
		Source:    "bundled",
		SourceURL: pricingURL,
		Rates:     bundledPricingRates(),
	}
}

// holidayCalendar is a built-in copy of China's statutory holidays. It is
// shipped with the release and never inferred from the price document.
var holidayCalendar = map[int]map[string]bool{
	2026: holidaySet(
		"2026-01-01", "2026-01-02", "2026-01-03",
		"2026-02-15", "2026-02-16", "2026-02-17", "2026-02-18", "2026-02-19", "2026-02-20", "2026-02-21",
		"2026-04-04", "2026-04-05", "2026-04-06",
		"2026-05-01", "2026-05-02", "2026-05-03", "2026-05-04", "2026-05-05",
		"2026-06-19", "2026-06-20", "2026-06-21",
		"2026-09-25", "2026-09-26", "2026-09-27",
		"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04", "2026-10-05", "2026-10-06", "2026-10-07",
	),
}

func holidaySet(dates ...string) map[string]bool {
	out := make(map[string]bool, len(dates))
	for _, date := range dates {
		out[date] = true
	}
	return out
}

func holidayYears() []int {
	years := make([]int, 0, len(holidayCalendar))
	for year := range holidayCalendar {
		years = append(years, year)
	}
	sort.Ints(years)
	return years
}

func localHoliday(at time.Time) bool {
	local := at.In(beijingZone)
	days := holidayCalendar[local.Year()]
	return days != nil && days[local.Format("2006-01-02")]
}

// pricePeriodAt applies the official rule to the request start time: peak on
// Beijing weekdays 09:00-12:00 and 14:00-18:00, excluding statutory holidays.
func pricePeriodAt(at time.Time) string {
	local := at.In(beijingZone)
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday || localHoliday(at) {
		return "offpeak"
	}
	minutes := local.Hour()*60 + local.Minute()
	if (minutes >= 9*60 && minutes < 12*60) || (minutes >= 14*60 && minutes < 18*60) {
		return "peak"
	}
	return "offpeak"
}

func pricingHolidayWarning(at time.Time) string {
	if holidayCalendar[at.In(beijingZone).Year()] != nil {
		return ""
	}
	return "节假日表未覆盖当前年份，节假日费用可能偏高"
}

func normalizePricingModel(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func canonicalPricingModel(rates map[string]priceRates, name string) (string, bool) {
	normalized := normalizePricingModel(name)
	if alias, ok := pricingModelAliases[normalized]; ok {
		normalized = alias
	}
	_, ok := rates[normalized]
	return normalized, ok
}

// pricingRatesFor looks up the built-in snapshot; tests and offline use share
// this path. Runtime lookups use the active snapshot via Server.ratesFor.
func pricingRatesFor(model string) (priceRates, string, bool) {
	return ratesForMap(bundledPricingRates(), model)
}

func ratesForMap(rates map[string]priceRates, model string) (priceRates, string, bool) {
	canonical, ok := canonicalPricingModel(rates, model)
	if !ok {
		return priceRates{}, "", false
	}
	return rates[canonical], canonical, true
}

func computeCostNano(rates priceRates, period string, hit, miss, output int64) int64 {
	hitPrice, missPrice, outPrice := rates.HitOff, rates.MissOff, rates.OutOff
	if period == "peak" {
		hitPrice, missPrice, outPrice = rates.HitPeak, rates.MissPeak, rates.OutPeak
	}
	return hit*hitPrice + miss*missPrice + output*outPrice
}

func nanoToCNY(nano int64) float64 {
	return float64(nano) / 1e9
}

type attemptPrice struct {
	CostNano *int64
	Period   string
	Model    string
	Version  string
}

func (s *Server) ratesFor(model string) (priceRates, string, bool) {
	return ratesForMap(s.activePricing().Rates, model)
}

func (s *Server) activePricing() pricingSnapshot {
	s.pricing.mu.Lock()
	defer s.pricing.mu.Unlock()
	return s.pricing.snapshot
}

// priceAttempt prices one upstream attempt. Only DeepSeek official channels with
// a known model, a successful status and a complete usage split are priced;
// everything else stays unknown (nil) rather than zero.
func (s *Server) priceAttempt(kind, remoteModel string, status int, at time.Time, usage *usageTotals) attemptPrice {
	if kind != "deepseek" || usage == nil || status < 200 || status >= 300 {
		return attemptPrice{}
	}
	snapshot := s.activePricing()
	rates, canonical, ok := ratesForMap(snapshot.Rates, remoteModel)
	if !ok {
		return attemptPrice{}
	}
	_, output, hit, miss := usage.normalized()
	if hit == nil || miss == nil || output == nil {
		return attemptPrice{}
	}
	period := pricePeriodAt(at)
	cost := computeCostNano(rates, period, *hit, *miss, *output)
	return attemptPrice{CostNano: &cost, Period: period, Model: canonical, Version: snapshot.Version}
}

func (s *Server) pricingInfo() PricingInfo {
	s.pricing.mu.Lock()
	snapshot := s.pricing.snapshot
	lastAttempt := s.pricing.lastAttemptAt
	lastSyncOK := s.pricing.lastSyncOK
	s.pricing.mu.Unlock()

	info := PricingInfo{
		Currency:     snapshot.Currency,
		Unit:         snapshot.Unit,
		Source:       snapshot.Source,
		SourceURL:    snapshot.SourceURL,
		LastSyncOK:   lastSyncOK,
		PeakTimezone: peakTimezone,
		PeakWindows:  append([]string(nil), peakWindows...),
		HolidayYears: holidayYears(),
	}
	if info.SourceURL == "" {
		info.SourceURL = pricingURL
	}
	if snapshot.SyncedAt != nil {
		value := snapshot.SyncedAt.UTC().Format(time.RFC3339)
		info.LastSyncAt = &value
	}
	now := s.now()
	info.Stale = snapshot.SyncedAt == nil || now.Sub(snapshot.SyncedAt.UTC()) > 7*24*time.Hour
	if !lastSyncOK && !lastAttempt.IsZero() {
		info.Stale = true
	}

	warnings := []string{}
	switch {
	case snapshot.Source != "remote" && !lastSyncOK && !lastAttempt.IsZero():
		warnings = append(warnings, "官方价格同步失败，当前使用内置价格快照")
	case snapshot.Source != "remote":
		warnings = append(warnings, "尚未成功同步官方价格，当前使用内置价格快照")
	case !lastSyncOK && !lastAttempt.IsZero():
		warnings = append(warnings, "价格自动同步失败，使用上次成功快照")
	}
	if snapshot.SyncedAt != nil && now.Sub(snapshot.SyncedAt.UTC()) > 7*24*time.Hour {
		warnings = append(warnings, "价格快照已超过 7 天未更新")
	}
	if holiday := pricingHolidayWarning(now); holiday != "" {
		warnings = append(warnings, holiday)
	}
	info.Warning = strings.Join(warnings, "；")
	return info
}

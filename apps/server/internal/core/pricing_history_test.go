package core

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func findPricingRate(rates []PricingRate, model string) *PricingRate {
	for i := range rates {
		if rates[i].Model == model {
			return &rates[i]
		}
	}
	return nil
}

func assertUnitRate(t *testing.T, label string, got float64, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("%s=%v want %v", label, got, want)
	}
}

// S9 新库：当前价目始终有六项单价（内置价），历史为空数组。
func TestPricingHistoryInitialState(t *testing.T) {
	s, _ := newPricingServer(t)
	info := s.pricingInfo()
	if len(info.Models) != 2 {
		t.Fatalf("models=%+v want two bundled models", info.Models)
	}
	flash := findPricingRate(info.Models, "deepseek-flash")
	if flash == nil {
		t.Fatalf("missing bundled flash: %+v", info.Models)
	}
	assertUnitRate(t, "flash.hit_off", flash.HitOff, 0.02)
	assertUnitRate(t, "flash.hit_peak", flash.HitPeak, 0.04)
	assertUnitRate(t, "flash.miss_off", flash.MissOff, 1)
	assertUnitRate(t, "flash.miss_peak", flash.MissPeak, 2)
	assertUnitRate(t, "flash.out_off", flash.OutOff, 4)
	assertUnitRate(t, "flash.out_peak", flash.OutPeak, 8)
	if info.History == nil || len(info.History) != 0 {
		t.Fatalf("history=%+v want empty non-nil", info.History)
	}
}

// S9a 首次成功同步即使与内置价相同也留一条基线；相同价目重复同步不重复记录。
func TestPricingHistoryFirstSyncAndRepeatedSync(t *testing.T) {
	s, _ := newPricingServer(t)
	pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, standardPricingDoc())
	}))
	if err := s.SyncPricing(context.Background()); err != nil {
		t.Fatal(err)
	}
	info := s.pricingInfo()
	if len(info.History) != 1 {
		t.Fatalf("history after first sync=%+v want 1", info.History)
	}
	entry := info.History[0]
	if entry.Source != "remote" || entry.Version == "" || entry.ObservedAt == "" {
		t.Fatalf("first history entry=%+v", entry)
	}
	if info.LastSyncAt == nil || entry.ObservedAt != *info.LastSyncAt {
		t.Fatalf("observed_at=%q last_sync_at=%v", entry.ObservedAt, info.LastSyncAt)
	}
	flash := findPricingRate(entry.Models, "deepseek-flash")
	if flash == nil {
		t.Fatalf("history missing flash: %+v", entry.Models)
	}
	assertUnitRate(t, "history flash hit_peak", flash.HitPeak, 0.04)

	if err := s.SyncPricing(context.Background()); err != nil {
		t.Fatal(err)
	}
	if again := s.pricingInfo(); len(again.History) != 1 {
		t.Fatalf("same rates must not append history: %+v", again.History)
	}
}

// S9b 调价或新增模型追加历史；旧请求费用不变，新请求使用新价；失败不追加。
func TestPricingHistoryChangesKeepOldCost(t *testing.T) {
	s, _ := newPricingServer(t)
	at := beijingAt(2026, time.November, 13, 10, 0).UTC()
	usage := tokensUsageAt(int64Ptr(1_000_000), int64Ptr(0), int64Ptr(0))
	s.record("deepseek-flash", testChannel(), 200, at, usage)
	oldCost := historyCostNano(t, s)

	pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, standardPricingDoc())
	}))
	if err := s.SyncPricing(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := historyCostNano(t, s); got != oldCost {
		t.Fatalf("first sync changed old request cost %d -> %d", oldCost, got)
	}

	changed := strings.Replace(standardPricingDoc(), "0.04元", "0.20元", 1)
	pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, changed)
	}))
	if err := s.SyncPricing(context.Background()); err != nil {
		t.Fatal(err)
	}
	info := s.pricingInfo()
	if len(info.History) != 2 {
		t.Fatalf("history after price change=%+v want 2", info.History)
	}
	if findPricingRate(info.History[0].Models, "deepseek-flash") == nil || findPricingRate(info.History[1].Models, "deepseek-flash") == nil {
		t.Fatalf("history model snapshots: %+v", info.History)
	}
	assertUnitRate(t, "newest hit_peak", findPricingRate(info.History[0].Models, "deepseek-flash").HitPeak, 0.2)
	assertUnitRate(t, "older hit_peak", findPricingRate(info.History[1].Models, "deepseek-flash").HitPeak, 0.04)
	if current := findPricingRate(info.Models, "deepseek-flash"); current == nil || math.Abs(current.HitPeak-0.2) > 1e-12 {
		t.Fatalf("current rates not updated: %+v", info.Models)
	}

	s.record("deepseek-flash", testChannel(), 200, at, usage)
	var newestCost *int64
	if err := s.db.QueryRow(`SELECT cost_nano_cny FROM requests ORDER BY id DESC LIMIT 1`).Scan(&newestCost); err != nil {
		t.Fatal(err)
	}
	if newestCost == nil || *newestCost != 1_000_000*200 {
		t.Fatalf("new request cost=%v want new peak price", newestCost)
	}
	var persistedOld *int64
	if err := s.db.QueryRow(`SELECT cost_nano_cny FROM requests ORDER BY id ASC LIMIT 1`).Scan(&persistedOld); err != nil {
		t.Fatal(err)
	}
	if persistedOld == nil || *persistedOld != oldCost {
		t.Fatalf("old request cost changed %d -> %v", oldCost, persistedOld)
	}

	pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	if err := s.SyncPricing(context.Background()); err == nil {
		t.Fatal("expected failed sync")
	}
	if after := s.pricingInfo(); len(after.History) != 2 {
		t.Fatalf("failed sync appended history: %+v", after.History)
	}
}

// S9c 从 #24 已有的 remote pricing_state 升级时，以该快照作为历史基线。
func TestPricingHistorySeedsExistingRemoteSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pricing.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := bundledPricingSnapshot()
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
	if len(info.History) != 1 {
		t.Fatalf("upgraded history=%+v want one baseline", info.History)
	}
	entry := info.History[0]
	if entry.Source != "remote" || entry.Version != snapshot.Version || entry.ObservedAt != synced.Format(time.RFC3339) {
		t.Fatalf("baseline entry=%+v", entry)
	}
	if len(entry.Models) != 2 {
		t.Fatalf("baseline models=%+v", entry.Models)
	}
}

// S9d 损坏的历史行跳过，不影响当前价目与统计接口。
func TestPricingHistoryCorruptRowIsSkipped(t *testing.T) {
	s, h := newUsageStatsServer(t)
	if _, err := s.db.Exec(`INSERT INTO pricing_history(version,source,observed_at,rates_json) VALUES('bad','remote','2026-11-13T12:00:00Z','not-json')`); err != nil {
		t.Fatal(err)
	}
	info := s.pricingInfo()
	if len(info.History) != 0 {
		t.Fatalf("corrupt history=%+v want skipped", info.History)
	}
	if len(info.Models) != 2 {
		t.Fatalf("current rates lost: %+v", info.Models)
	}
	status, body := fetchUsageStats(t, h)
	got := decodeUsageStats(t, status, body)
	if len(got.Pricing.Models) != 2 || len(got.Pricing.History) != 0 {
		t.Fatalf("stats pricing after corrupt row: %+v", got.Pricing)
	}
}

// S9e 历史写入失败时当前价目不切换到未留档的新价，并返回同步失败。
func TestPricingHistoryWriteFailureKeepsSnapshot(t *testing.T) {
	s, _ := newPricingServer(t)
	before := s.activePricing()
	pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Replace(standardPricingDoc(), "0.04元", "0.20元", 1))
	}))
	if _, err := s.db.Exec(`DROP TABLE pricing_history`); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncPricing(context.Background()); err == nil {
		t.Fatal("expected sync failure when history cannot be written")
	}
	after := s.activePricing()
	if after.Version != before.Version || !pricingRatesEqual(after.Rates, before.Rates) {
		t.Fatalf("snapshot switched without history: before=%+v after=%+v", before, after)
	}
	if info := s.pricingInfo(); info.LastSyncOK || info.Warning == "" {
		t.Fatalf("failure not exposed: %+v", info)
	}
}

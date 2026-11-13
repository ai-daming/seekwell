package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixturePricingRow struct {
	kind   string
	period string
	values []string
}

func fixturePricingDoc(names []string, rows []fixturePricingRow) string {
	var b strings.Builder
	b.WriteString("# 模型 & 价格\n\n下表所列模型价格以“百万 tokens”为单位。\n\n")
	b.WriteString("| 模型 | 模型 | 模型 | " + strings.Join(names, " | ") + " |\n")
	b.WriteString("| --- | --- | --- | " + strings.Repeat("--- | ", len(names)) + "\n")
	for _, row := range rows {
		b.WriteString("| 价格(2) | " + row.kind + " | " + row.period + " | " + strings.Join(row.values, " | ") + " |\n")
	}
	b.WriteString("\n(2) 空闲时段价格为高峰时段价格的一半。\n")
	return b.String()
}

func standardPricingRows(flash, pro []string) []fixturePricingRow {
	return []fixturePricingRow{
		{"百万tokens输入 / （缓存命中）", "空闲时段", []string{flash[0], pro[0]}},
		{"百万tokens输入 / （缓存命中）", "高峰时段", []string{flash[1], pro[1]}},
		{"百万tokens输入 / （缓存未命中）", "空闲时段", []string{flash[2], pro[2]}},
		{"百万tokens输入 / （缓存未命中）", "高峰时段", []string{flash[3], pro[3]}},
		{"百万tokens输出", "空闲时段", []string{flash[4], pro[4]}},
		{"百万tokens输出", "高峰时段", []string{flash[5], pro[5]}},
	}
}

func standardPricingDoc() string {
	return fixturePricingDoc(
		[]string{"deepseek-flash(1)", "deepseek-v4-pro"},
		standardPricingRows(
			[]string{"0.02元", "0.04元", "1元", "2元", "4元", "8元"},
			[]string{"0.15元", "0.30元", "4.5元", "9.0元", "13.5元", "27.0元"},
		),
	)
}

func newPricingServer(t *testing.T) (*Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pricing.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func pointPricingTo(t *testing.T, s *Server, handler http.Handler) {
	t.Helper()
	h := httptest.NewServer(handler)
	t.Cleanup(h.Close)
	s.pricingURL = h.URL
	s.pricingClient = h.Client()
}

func testChannel() Channel {
	return Channel{Name: "official", Kind: "deepseek", RemoteModel: "deepseek-flash", Model: "deepseek-flash", Enabled: true}
}

func historyCostNano(t *testing.T, s *Server) int64 {
	t.Helper()
	var cost *int64
	if err := s.db.QueryRow(`SELECT cost_nano_cny FROM requests ORDER BY id DESC LIMIT 1`).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if cost == nil {
		t.Fatal("history cost is null")
	}
	return *cost
}

// S4 自动同步成功：新快照持久化、新请求用新价、旧请求费用不变、新模型列被识别。
func TestPricingSyncSuccess(t *testing.T) {
	s, path := newPricingServer(t)
	oldStart := beijingAt(2026, time.November, 13, 10, 0).UTC() // Friday 10:00 peak
	usage := tokensUsageAt(int64Ptr(1000), int64Ptr(0), int64Ptr(0))
	s.record("deepseek-flash", testChannel(), 200, oldStart, usage)
	oldCost := historyCostNano(t, s)

	doc := fixturePricingDoc(
		[]string{"deepseek-flash(1)", "deepseek-v4-pro", "deepseek-v4-lite"},
		[]fixturePricingRow{
			{"百万tokens输入 / （缓存命中）", "空闲时段", []string{"0.10元", "0.30元", "0.5元"}},
			{"百万tokens输入 / （缓存命中）", "高峰时段", []string{"0.20元", "0.60元", "1元"}},
			{"百万tokens输入 / （缓存未命中）", "空闲时段", []string{"2元", "6元", "7元"}},
			{"百万tokens输入 / （缓存未命中）", "高峰时段", []string{"4元", "12元", "14元"}},
			{"百万tokens输出", "空闲时段", []string{"5元", "20元", "21元"}},
			{"百万tokens输出", "高峰时段", []string{"10元", "40元", "42元"}},
		},
	)
	pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, doc)
	}))
	if err := s.SyncPricing(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	info := s.pricingInfo()
	if info.Source != "remote" || !info.LastSyncOK || info.LastSyncAt == nil {
		t.Fatalf("pricing info after sync: %+v", info)
	}
	if info.Currency != "CNY" || info.Unit != "1M tokens" {
		t.Fatalf("pricing unit: %+v", info)
	}

	if oldCost != 1000*40 {
		t.Fatalf("old cost=%d want %d", oldCost, 1000*40)
	}
	var persistedOld *int64
	if err := s.db.QueryRow(`SELECT cost_nano_cny FROM requests ORDER BY id ASC LIMIT 1`).Scan(&persistedOld); err != nil {
		t.Fatal(err)
	}
	if persistedOld == nil || *persistedOld != oldCost {
		t.Fatalf("old request cost changed from %d to %v", oldCost, persistedOld)
	}

	s.record("deepseek-flash", testChannel(), 200, oldStart, usage)
	if got := historyCostNano(t, s); got != 1000*200 {
		t.Fatalf("new cost=%d want %d after sync", got, 1000*200)
	}

	next := s.priceAttempt("deepseek", "deepseek-v4-lite", 200, oldStart, usage)
	if next.CostNano == nil || *next.CostNano != 1000*1000 {
		t.Fatalf("new model pricing: %+v", next)
	}

	s.Close()
	reopened, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if info := reopened.pricingInfo(); info.Source != "remote" || info.LastSyncAt == nil || !info.LastSyncOK {
		t.Fatalf("snapshot not persisted: %+v", info)
	}
	if got := reopened.priceAttempt("deepseek", "deepseek-flash", 200, oldStart, usage); got.CostNano == nil || *got.CostNano != 1000*200 {
		t.Fatalf("persisted rate not used: %+v", got)
	}
}

func failingSyncDoc(mutate func(string) (string, int)) (string, int) {
	doc := standardPricingDoc()
	status := http.StatusOK
	if mutate != nil {
		doc, status = mutate(doc)
	}
	return doc, status
}

// S5 自动同步失败保留上一份快照，状态可读且不会半更新；首次失败用内置价。
func TestPricingSyncFailuresKeepPreviousSnapshot(t *testing.T) {
	mutators := map[string]func(string) (string, int){
		"non 200": func(doc string) (string, int) { return doc, http.StatusInternalServerError },
		"not markdown": func(string) (string, int) {
			return "# 模型 & 价格\n\n暂时没有价格表。\n", http.StatusOK
		},
		"missing output row": func(doc string) (string, int) {
			return strings.Replace(doc, "| 价格(2) | 百万tokens输出 | 高峰时段 | 8元 | 27.0元 |\n", "", 1), http.StatusOK
		},
		"zero price": func(doc string) (string, int) {
			return strings.Replace(doc, "0.02元", "0元", 1), http.StatusOK
		},
		"negative price": func(doc string) (string, int) {
			return strings.Replace(doc, "0.02元", "-0.02元", 1), http.StatusOK
		},
		"peak lower than offpeak": func(doc string) (string, int) {
			return strings.Replace(doc, "0.04元", "0.01元", 1), http.StatusOK
		},
		"non numeric price": func(doc string) (string, int) {
			return strings.Replace(doc, "0.02元", "零点零二元", 1), http.StatusOK
		},
	}
	for name, mutate := range mutators {
		t.Run(name, func(t *testing.T) {
			s, _ := newPricingServer(t)
			pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, standardPricingDoc())
			}))
			if err := s.SyncPricing(context.Background()); err != nil {
				t.Fatalf("baseline sync: %v", err)
			}
			before := s.pricingInfo()

			doc, status := failingSyncDoc(mutate)
			pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				io.WriteString(w, doc)
			}))
			if err := s.SyncPricing(context.Background()); err == nil {
				t.Fatal("expected sync failure")
			}
			after := s.pricingInfo()
			if after.Source != "remote" || after.LastSyncAt == nil || *after.LastSyncAt != *before.LastSyncAt {
				t.Fatalf("previous snapshot lost: before=%+v after=%+v", before, after)
			}
			if after.LastSyncOK || after.Warning == "" || !after.Stale {
				t.Fatalf("failure status not exposed: %+v", after)
			}
			usage := tokensUsageAt(int64Ptr(1000), int64Ptr(0), int64Ptr(0))
			if got := s.priceAttempt("deepseek", "deepseek-flash", 200, beijingAt(2026, time.November, 13, 10, 0).UTC(), usage); got.CostNano == nil || *got.CostNano != 1000*40 {
				t.Fatalf("old snapshot no longer prices requests: %+v", got)
			}
		})
	}
}

// S5b 从未成功同步时使用内置价格并明确提示。
func TestPricingSyncFailureUsesBundled(t *testing.T) {
	s, _ := newPricingServer(t)
	pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	if err := s.SyncPricing(context.Background()); err == nil {
		t.Fatal("expected sync failure")
	}
	info := s.pricingInfo()
	if info.Source != "bundled" || info.LastSyncAt != nil || info.LastSyncOK || info.Warning == "" || !info.Stale {
		t.Fatalf("bundled fallback info: %+v", info)
	}
	usage := tokensUsageAt(int64Ptr(1_000_000), int64Ptr(0), int64Ptr(0))
	if got := s.priceAttempt("deepseek", "deepseek-flash", 200, beijingAt(2026, time.November, 13, 10, 0).UTC(), usage); got.CostNano == nil || *got.CostNano != 40_000_000 {
		t.Fatalf("bundled snapshot cannot price: %+v", got)
	}
}

// S5c 超时按失败处理，不写半份价格。
func TestPricingSyncTimeout(t *testing.T) {
	s, _ := newPricingServer(t)
	pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		io.WriteString(w, standardPricingDoc())
	}))
	s.pricingClient.Timeout = 30 * time.Millisecond
	if err := s.SyncPricing(context.Background()); err == nil {
		t.Fatal("expected timeout failure")
	}
	if info := s.pricingInfo(); info.Source != "bundled" || info.LastSyncOK {
		t.Fatalf("timeout state: %+v", info)
	}
}

// S5d 快照超过 7 天标记 stale。
func TestPricingStaleAfterSevenDays(t *testing.T) {
	s, _ := newPricingServer(t)
	pointPricingTo(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, standardPricingDoc())
	}))
	if err := s.SyncPricing(context.Background()); err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	s.now = func() time.Time { return base.Add(8 * 24 * time.Hour) }
	if info := s.pricingInfo(); !info.Stale {
		t.Fatalf("snapshot older than 7 days not stale: %+v", info)
	}
	s.now = func() time.Time { return base.Add(24 * time.Hour) }
	if info := s.pricingInfo(); info.Stale {
		t.Fatalf("fresh snapshot marked stale: %+v", info)
	}
}

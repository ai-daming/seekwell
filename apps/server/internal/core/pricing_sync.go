package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	pricingSyncTimeout  = 10 * time.Second
	pricingSyncInterval = 24 * time.Hour
	pricingBodyLimit    = 2 << 20
)

type pricingStored struct {
	Snapshot      pricingSnapshot `json:"snapshot"`
	LastAttemptAt *time.Time      `json:"last_attempt_at,omitempty"`
	LastSyncOK    bool            `json:"last_sync_ok"`
	LastError     string          `json:"last_error,omitempty"`
}

func loadPricingState(db *sql.DB) (*pricingState, error) {
	state := &pricingState{snapshot: bundledPricingSnapshot()}
	var body string
	err := db.QueryRow(`SELECT body FROM pricing_state WHERE id=1`).Scan(&body)
	if err == sql.ErrNoRows {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	var stored pricingStored
	if json.Unmarshal([]byte(body), &stored) != nil || len(stored.Snapshot.Rates) == 0 || stored.Snapshot.Currency != pricingCurrency {
		// A corrupt snapshot falls back to the bundled one instead of failing startup.
		return state, nil
	}
	state.snapshot = stored.Snapshot
	state.lastSyncOK = stored.LastSyncOK
	state.lastError = stored.LastError
	if stored.LastAttemptAt != nil {
		state.lastAttemptAt = *stored.LastAttemptAt
	}
	return state, nil
}

func (s *Server) persistPricingLocked() error {
	stored := pricingStored{
		Snapshot:   s.pricing.snapshot,
		LastSyncOK: s.pricing.lastSyncOK,
		LastError:  s.pricing.lastError,
	}
	if !s.pricing.lastAttemptAt.IsZero() {
		at := s.pricing.lastAttemptAt
		stored.LastAttemptAt = &at
	}
	body, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO pricing_state(id,body,updated_at) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,updated_at=excluded.updated_at`,
		string(body), s.now().UTC().Format(time.RFC3339))
	return err
}

var pricingFootnote = regexp.MustCompile(`\s*\(\d+\)\s*$`)
var pricingDecimal = regexp.MustCompile(`^\d+(?:\.\d+)?$`)

func cleanMarkdownCell(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "**")
	value = strings.TrimSuffix(value, "**")
	value = strings.Trim(value, "`")
	return strings.TrimSpace(value)
}

type pricingField int

const (
	pricingHit pricingField = iota
	pricingMiss
	pricingOutput
	pricingFieldCount
)

var pricingFieldNames = [pricingFieldCount]string{"hit", "miss", "output"}

// yuanPerMillionToNanoPerToken converts an official CNY-per-million-tokens
// price to integer nano-CNY per token without binary floating point.
func yuanPerMillionToNanoPerToken(text string) (int64, error) {
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "元"))
	if !pricingDecimal.MatchString(text) {
		return 0, fmt.Errorf("无法解析价格 %q", text)
	}
	parts := strings.SplitN(text, ".", 2)
	digits := parts[0]
	scale := 3
	if len(parts) == 2 {
		digits += parts[1]
		scale -= len(parts[1])
	}
	value, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("无法解析价格 %q", text)
	}
	if scale >= 0 {
		for i := 0; i < scale; i++ {
			value *= 10
		}
		return value, nil
	}
	divisor := int64(1)
	for i := 0; i < -scale; i++ {
		divisor *= 10
	}
	if value%divisor != 0 {
		return 0, fmt.Errorf("价格 %q 超出 nano-CNY 精度", text)
	}
	return value / divisor, nil
}

// parsePricingDocument parses the official Markdown pricing table and validates
// the whole table. Any structural or numeric problem rejects the sync as a whole.
func parsePricingDocument(body []byte, now time.Time) (pricingSnapshot, error) {
	text := string(body)
	lowerText := strings.ToLower(text)
	if !strings.Contains(text, "百万") || !strings.Contains(text, "元") || !strings.Contains(lowerText, "token") {
		return pricingSnapshot{}, errors.New("价格文档缺少 CNY / 百万 tokens 单位")
	}

	lines := strings.Split(text, "\n")
	modelNames := []string{}
	rows := [][3]string{}
	values := [][]string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		raw := strings.Split(strings.Trim(trimmed, "|"), "|")
		cells := make([]string, len(raw))
		for i, cell := range raw {
			cells[i] = cleanMarkdownCell(cell)
		}
		leadingModels := 0
		for leadingModels < len(cells) && cells[leadingModels] == "模型" {
			leadingModels++
		}
		if leadingModels >= 1 && len(cells) > leadingModels {
			modelNames = append([]string(nil), cells[leadingModels:]...)
			continue
		}
		if len(cells) < 3 || !strings.HasPrefix(cells[0], "价格") {
			continue
		}
		rows = append(rows, [3]string{cells[0], cells[1], cells[2]})
		values = append(values, append([]string(nil), cells[3:]...))
	}
	if len(modelNames) == 0 {
		return pricingSnapshot{}, errors.New("价格文档缺少模型列")
	}
	if len(rows) == 0 {
		return pricingSnapshot{}, errors.New("价格文档缺少价格行")
	}

	rates := make(map[string]priceRates, len(modelNames))
	seen := make(map[string]bool, len(modelNames)*6)
	for i, name := range modelNames {
		name = pricingFootnote.ReplaceAllString(name, "")
		name = normalizePricingModel(name)
		if name == "" {
			return pricingSnapshot{}, errors.New("模型名为空")
		}
		modelNames[i] = name
		rates[name] = priceRates{}
	}

	for index, row := range rows {
		if len(values[index]) < len(modelNames) {
			return pricingSnapshot{}, errors.New("价格行列数与模型数不一致")
		}
		var field pricingField
		switch {
		case strings.Contains(row[1], "未命中"):
			field = pricingMiss
		case strings.Contains(row[1], "缓存命中"):
			field = pricingHit
		case strings.Contains(row[1], "输出"):
			field = pricingOutput
		default:
			continue
		}
		peak := false
		switch {
		case strings.Contains(row[2], "空闲"):
			peak = false
		case strings.Contains(row[2], "高峰"):
			peak = true
		default:
			return pricingSnapshot{}, fmt.Errorf("无法识别价格时段 %q", row[2])
		}
		rowValues := values[index][len(values[index])-len(modelNames):]
		for i, name := range modelNames {
			nano, err := yuanPerMillionToNanoPerToken(rowValues[i])
			if err != nil {
				return pricingSnapshot{}, err
			}
			ratesForModel := rates[name]
			switch field {
			case pricingHit:
				if peak {
					ratesForModel.HitPeak = nano
				} else {
					ratesForModel.HitOff = nano
				}
			case pricingMiss:
				if peak {
					ratesForModel.MissPeak = nano
				} else {
					ratesForModel.MissOff = nano
				}
			case pricingOutput:
				if peak {
					ratesForModel.OutPeak = nano
				} else {
					ratesForModel.OutOff = nano
				}
			}
			rates[name] = ratesForModel
			seen[name+"\x00"+pricingFieldNames[field]+"\x00"+strconv.FormatBool(peak)] = true
		}
	}

	for _, name := range modelNames {
		rate := rates[name]
		for field := pricingHit; field < pricingFieldCount; field++ {
			for _, peak := range []bool{false, true} {
				if !seen[name+"\x00"+pricingFieldNames[field]+"\x00"+strconv.FormatBool(peak)] {
					return pricingSnapshot{}, fmt.Errorf("模型 %s 缺少 %s %s价格", name, pricingFieldNames[field], map[bool]string{false: "空闲", true: "高峰"}[peak])
				}
			}
		}
		if rate.HitOff <= 0 || rate.HitPeak <= 0 || rate.MissOff <= 0 || rate.MissPeak <= 0 || rate.OutOff <= 0 || rate.OutPeak <= 0 {
			return pricingSnapshot{}, fmt.Errorf("模型 %s 价格必须为正数", name)
		}
		if rate.HitPeak < rate.HitOff || rate.MissPeak < rate.MissOff || rate.OutPeak < rate.OutOff {
			return pricingSnapshot{}, fmt.Errorf("模型 %s 高峰价低于空闲价", name)
		}
	}

	snapshot := pricingSnapshot{
		Version:   "remote-" + now.UTC().Format("20060102T150405Z"),
		Currency:  pricingCurrency,
		Unit:      pricingUnit,
		Source:    "remote",
		SourceURL: pricingURL,
		SyncedAt:  &now,
		Rates:     rates,
	}
	return snapshot, nil
}

func (s *Server) fetchPricing(ctx context.Context) (pricingSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, pricingSyncTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.pricingURL, nil)
	if err != nil {
		return pricingSnapshot{}, err
	}
	req.Header.Set("User-Agent", "SeekWell/0.1")
	resp, err := s.pricingClient.Do(req)
	if err != nil {
		return pricingSnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return pricingSnapshot{}, fmt.Errorf("官方价格页返回状态码 %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, pricingBodyLimit))
	if err != nil {
		return pricingSnapshot{}, err
	}
	return parsePricingDocument(body, s.now())
}

// SyncPricing fetches and validates the official price document. A failure keeps
// the previous snapshot untouched.
func (s *Server) SyncPricing(ctx context.Context) error {
	s.pricing.mu.Lock()
	if s.pricing.syncing {
		s.pricing.mu.Unlock()
		return errors.New("价格同步已在进行中")
	}
	s.pricing.syncing = true
	s.pricing.lastAttemptAt = s.now()
	s.pricing.mu.Unlock()

	snapshot, fetchErr := s.fetchPricing(ctx)

	s.pricing.mu.Lock()
	defer s.pricing.mu.Unlock()
	s.pricing.syncing = false
	s.pricing.lastSyncOK = fetchErr == nil
	if fetchErr != nil {
		s.pricing.lastError = fetchErr.Error()
		_ = s.persistPricingLocked()
		return fetchErr
	}
	s.pricing.snapshot = snapshot
	s.pricing.lastError = ""
	return s.persistPricingLocked()
}

func (s *Server) syncPricingIfDue(ctx context.Context) {
	s.pricing.mu.Lock()
	last := s.pricing.lastAttemptAt
	s.pricing.mu.Unlock()
	if !last.IsZero() && s.now().Sub(last) < pricingSyncInterval {
		return
	}
	_ = s.SyncPricing(ctx)
}

// StartPricingSync tries once at startup and then at most once every 24 hours,
// without blocking HTTP listening.
func (s *Server) StartPricingSync(ctx context.Context) {
	go func() {
		s.syncPricingIfDue(ctx)
		ticker := time.NewTicker(pricingSyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.syncPricingIfDue(ctx)
			}
		}
	}()
}

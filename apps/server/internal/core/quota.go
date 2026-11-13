package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	quotaProbeUsage       = "usage"
	quotaProbeRequest     = "request"
	defaultQuotaInterval  = 10 * time.Minute
	quotaProbeFailureStop = 3
	quotaResetBodyLimit   = 64 << 10
	quotaSchedulerTick    = 30 * time.Second
)

type quotaState struct {
	ChannelID     int64
	ProbeMode     string
	NextProbeAt   time.Time
	ResetAt       time.Time
	ProbeFailures int
	LastCheckedAt time.Time
	UpdatedAt     time.Time
	Revision      int64
}

type quotaUsageVerdict struct {
	recovered bool
	exhausted bool
	confirmed bool
	next      time.Time
	reset     time.Time
}

func quotaProbeIntervalFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("SEEKWELL_QUOTA_PROBE_INTERVAL"))
	if raw == "" {
		return defaultQuotaInterval
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		log.Printf("SEEKWELL_QUOTA_PROBE_INTERVAL=%q 无效，使用默认值 %s", raw, defaultQuotaInterval)
		return defaultQuotaInterval
	}
	return interval
}

func formatQuotaTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseQuotaTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

type quotaScanner interface {
	Scan(dest ...any) error
}

func scanQuotaState(row quotaScanner) (quotaState, error) {
	var state quotaState
	var next, reset, checked, updated string
	err := row.Scan(&state.ChannelID, &state.ProbeMode, &next, &reset, &state.ProbeFailures, &checked, &updated, &state.Revision)
	if err != nil {
		return quotaState{}, err
	}
	state.NextProbeAt = parseQuotaTime(next)
	state.ResetAt = parseQuotaTime(reset)
	state.LastCheckedAt = parseQuotaTime(checked)
	state.UpdatedAt = parseQuotaTime(updated)
	return state, nil
}

func (s *Server) quotaStates() (map[int64]quotaState, error) {
	rows, err := s.db.Query(`SELECT channel_id,probe_mode,next_probe_at,reset_at,probe_failures,last_checked_at,updated_at,revision FROM quota_states`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make(map[int64]quotaState)
	for rows.Next() {
		state, err := scanQuotaState(rows)
		if err != nil {
			return nil, err
		}
		states[state.ChannelID] = state
	}
	return states, rows.Err()
}

func (s *Server) quotaState(channelID int64) (quotaState, bool, error) {
	row := s.db.QueryRow(`SELECT channel_id,probe_mode,next_probe_at,reset_at,probe_failures,last_checked_at,updated_at,revision FROM quota_states WHERE channel_id=?`, channelID)
	state, err := scanQuotaState(row)
	if err == sql.ErrNoRows {
		return quotaState{}, false, nil
	}
	if err != nil {
		return quotaState{}, false, err
	}
	return state, true, nil
}

func (s *Server) markQuotaExhausted(c Channel, reset time.Time) error {
	now := s.now().UTC()
	existing, ok, err := s.quotaState(c.ID)
	if err != nil {
		return err
	}
	mode := quotaProbeRequest
	if ok {
		mode = existing.ProbeMode
	} else if _, supported := usageURL(c); supported {
		mode = quotaProbeUsage
	}
	next := now.Add(s.quotaProbeInterval)
	resetAt := time.Time{}
	if reset.After(now) {
		resetAt = reset.UTC()
		next = resetAt
	}
	nowText := formatQuotaTime(now)
	_, err = s.db.Exec(`INSERT INTO quota_states(channel_id,probe_mode,next_probe_at,reset_at,probe_failures,last_checked_at,updated_at,revision) VALUES(?,?,?,?,0,?,?,0)
		ON CONFLICT(channel_id) DO UPDATE SET probe_mode=excluded.probe_mode,next_probe_at=excluded.next_probe_at,reset_at=excluded.reset_at,probe_failures=0,last_checked_at=excluded.last_checked_at,updated_at=excluded.updated_at,revision=quota_states.revision+1`,
		c.ID, mode, formatQuotaTime(next), formatQuotaTime(resetAt), nowText, nowText)
	if err != nil {
		return err
	}
	if mode == quotaProbeUsage && s.quotaProbeAsync != nil {
		s.quotaProbeAsync(c.ID)
	}
	return nil
}

func (s *Server) claimQuotaProbe(channelID int64, mode string, now time.Time) (int64, bool, error) {
	now = now.UTC()
	var revision int64
	err := s.db.QueryRow(`UPDATE quota_states SET next_probe_at=?,updated_at=?,revision=revision+1 WHERE channel_id=? AND probe_mode=? AND next_probe_at<=? RETURNING revision`,
		formatQuotaTime(now.Add(s.quotaProbeInterval)), formatQuotaTime(now), channelID, mode, formatQuotaTime(now)).Scan(&revision)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return revision, true, nil
}

func (s *Server) clearQuotaState(channelID, revision int64) error {
	_, err := s.db.Exec(`DELETE FROM quota_states WHERE channel_id=? AND revision=?`, channelID, revision)
	return err
}

func (s *Server) setQuotaStateNextProbe(channelID, revision int64, next time.Time) error {
	_, err := s.db.Exec(`UPDATE quota_states SET next_probe_at=?,updated_at=?,revision=revision+1 WHERE channel_id=? AND revision=?`,
		formatQuotaTime(next), formatQuotaTime(s.now()), channelID, revision)
	return err
}

func (s *Server) channelByID(id int64) (Channel, bool, error) {
	channels, err := s.channels()
	if err != nil {
		return Channel{}, false, err
	}
	for _, c := range channels {
		if c.ID == id {
			return c, true, nil
		}
	}
	return Channel{}, false, nil
}

func (s *Server) probeQuotaState(ctx context.Context, channelID int64) error {
	state, ok, err := s.quotaState(channelID)
	if err != nil || !ok || state.ProbeMode != quotaProbeUsage {
		return err
	}
	channel, ok, err := s.channelByID(channelID)
	if err != nil {
		return err
	}
	if !ok {
		return s.clearQuotaState(channelID, state.Revision)
	}
	usage := s.usageForChannel(ctx, channel)
	verdict := evaluateQuotaUsage(channel, usage, s.now().UTC(), s.quotaProbeInterval)
	return s.applyQuotaVerdict(state, verdict)
}

func (s *Server) applyQuotaVerdict(state quotaState, verdict quotaUsageVerdict) error {
	now := s.now().UTC()
	nowText := formatQuotaTime(now)
	if verdict.recovered {
		return s.clearQuotaState(state.ChannelID, state.Revision)
	}
	if verdict.exhausted {
		next := verdict.next
		if !next.After(now) {
			next = now.Add(s.quotaProbeInterval)
		}
		_, err := s.db.Exec(`UPDATE quota_states SET next_probe_at=?,reset_at=?,probe_failures=0,last_checked_at=?,updated_at=?,revision=revision+1 WHERE channel_id=? AND probe_mode=? AND revision=?`,
			formatQuotaTime(next), formatQuotaTime(verdict.reset), nowText, nowText, state.ChannelID, quotaProbeUsage, state.Revision)
		return err
	}
	if verdict.confirmed {
		return nil
	}
	failures := state.ProbeFailures + 1
	mode := quotaProbeUsage
	if failures >= quotaProbeFailureStop {
		mode = quotaProbeRequest
	}
	next := now.Add(s.quotaProbeInterval)
	if state.ResetAt.After(now) {
		next = state.ResetAt
	}
	_, err := s.db.Exec(`UPDATE quota_states SET probe_mode=?,next_probe_at=?,probe_failures=?,last_checked_at=?,updated_at=?,revision=revision+1 WHERE channel_id=? AND probe_mode=? AND revision=?`,
		mode, formatQuotaTime(next), failures, nowText, nowText, state.ChannelID, quotaProbeUsage, state.Revision)
	return err
}

func evaluateQuotaUsage(c Channel, usage ChannelUsage, now time.Time, interval time.Duration) quotaUsageVerdict {
	if usage.Status != usageOK {
		return quotaUsageVerdict{}
	}
	switch c.Kind {
	case "opencode_go":
		windows := 0
		maxed := false
		resets := []string{}
		for _, window := range usage.Windows {
			if window.UsedPercent == nil {
				continue
			}
			windows++
			if *window.UsedPercent >= 100 {
				maxed = true
				resets = append(resets, window.ResetsAt)
			}
		}
		if windows == 0 {
			return quotaUsageVerdict{}
		}
		if !maxed {
			return quotaUsageVerdict{recovered: true, confirmed: true}
		}
		next := earliestFutureQuotaTime(resets, now)
		reset := next
		if next.IsZero() {
			next = now.Add(interval)
		}
		return quotaUsageVerdict{exhausted: true, confirmed: true, next: next, reset: reset}
	case "dimagent":
		credits := usage.Credits
		if credits == nil {
			return quotaUsageVerdict{}
		}
		if credits.Unlimited != nil && *credits.Unlimited {
			return quotaUsageVerdict{recovered: true, confirmed: true}
		}
		if credits.Remaining != nil {
			if *credits.Remaining > 0 {
				return quotaUsageVerdict{recovered: true, confirmed: true}
			}
			resets := make([]string, 0, len(usage.Windows))
			for _, window := range usage.Windows {
				resets = append(resets, window.ResetsAt)
			}
			next := earliestFutureQuotaTime(resets, now)
			reset := next
			if next.IsZero() {
				next = now.Add(interval)
			}
			return quotaUsageVerdict{exhausted: true, confirmed: true, next: next, reset: reset}
		}
		resets := []string{}
		capped := false
		for _, window := range usage.Windows {
			if window.TokenCap == nil || window.TokenUsed == nil || *window.TokenCap <= 0 || *window.TokenUsed < *window.TokenCap {
				continue
			}
			capped = true
			resets = append(resets, window.ResetsAt)
		}
		if !capped {
			return quotaUsageVerdict{}
		}
		next := earliestFutureQuotaTime(resets, now)
		reset := next
		if next.IsZero() {
			next = now.Add(interval)
		}
		return quotaUsageVerdict{exhausted: true, confirmed: true, next: next, reset: reset}
	default:
		return quotaUsageVerdict{}
	}
}

func earliestFutureQuotaTime(values []string, now time.Time) time.Time {
	var earliest time.Time
	for _, value := range values {
		parsed := parseQuotaTime(value)
		if parsed.IsZero() || !parsed.After(now) {
			continue
		}
		if earliest.IsZero() || parsed.Before(earliest) {
			earliest = parsed
		}
	}
	return earliest
}

func (s *Server) StartQuotaProbes(ctx context.Context) {
	go func() {
		_ = s.probeDueQuotas(ctx)
		ticker := time.NewTicker(quotaSchedulerTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.probeDueQuotas(ctx)
			}
		}
	}()
}

func (s *Server) probeDueQuotas(ctx context.Context) error {
	now := s.now().UTC()
	rows, err := s.db.Query(`SELECT channel_id FROM quota_states WHERE probe_mode=? AND next_probe_at<=? ORDER BY next_probe_at,channel_id`, quotaProbeUsage, formatQuotaTime(now))
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var firstErr error
	for _, id := range ids {
		_, claimed, err := s.claimQuotaProbe(id, quotaProbeUsage, now)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !claimed {
			continue
		}
		if err := s.probeQuotaState(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func quotaHeaderValue(header http.Header, key string) string {
	for name, values := range header {
		if len(values) > 0 && strings.EqualFold(name, key) {
			return values[0]
		}
	}
	return header.Get(key)
}

func (s *Server) quotaResetFromResponse(header http.Header, body []byte, now time.Time) time.Time {
	now = now.UTC()
	candidates := []time.Time{}
	addFuture := func(t time.Time) {
		if !t.IsZero() && t.After(now) {
			candidates = append(candidates, t.UTC())
		}
	}
	if value := strings.TrimSpace(quotaHeaderValue(header, "Retry-After")); value != "" {
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
			addFuture(now.Add(time.Duration(seconds) * time.Second))
		} else if parsed, err := http.ParseTime(value); err == nil {
			addFuture(parsed)
		}
	}
	if value := strings.TrimSpace(quotaHeaderValue(header, "X-RateLimit-Reset")); value != "" {
		addFuture(parseQuotaAbsoluteValue(value, now))
	}
	if value := strings.TrimSpace(quotaHeaderValue(header, "RateLimit-Reset")); value != "" {
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
			addFuture(now.Add(time.Duration(seconds) * time.Second))
		}
	}
	if len(body) > 0 {
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) == nil {
			collectQuotaResetFields(value, now, &candidates)
		}
	}
	var earliest time.Time
	for _, candidate := range candidates {
		if !candidate.After(now) {
			continue
		}
		if earliest.IsZero() || candidate.Before(earliest) {
			earliest = candidate
		}
	}
	return earliest
}

func collectQuotaResetFields(value any, now time.Time, candidates *[]time.Time) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			relative := strings.HasPrefix(normalized, "retry_after")
			absolute := normalized == "resets_at" || normalized == "reset_at" || normalized == "reset_time" || normalized == "x-ratelimit-reset"
			if relative {
				if seconds, ok := quotaNumericValue(child); ok && seconds >= 0 {
					*candidates = append(*candidates, now.Add(time.Duration(seconds)*time.Second))
				}
			} else if absolute {
				if parsed := parseQuotaAbsoluteValue(child, now); !parsed.IsZero() {
					*candidates = append(*candidates, parsed)
				}
			}
			collectQuotaResetFields(child, now, candidates)
		}
	case []any:
		for _, child := range typed {
			collectQuotaResetFields(child, now, candidates)
		}
	}
}

func quotaNumericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return number, err == nil
	default:
		return 0, false
	}
}

func parseQuotaAbsoluteValue(value any, now time.Time) time.Time {
	switch typed := value.(type) {
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return time.Time{}
		}
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return parsed.UTC()
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return time.Time{}
		}
		return quotaEpochTime(number)
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return time.Time{}
		}
		return quotaEpochTime(number)
	case float64:
		return quotaEpochTime(typed)
	case int64:
		return quotaEpochTime(float64(typed))
	case int:
		return quotaEpochTime(float64(typed))
	default:
		return time.Time{}
	}
}

func quotaEpochTime(value float64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value > 1e12 {
		return time.UnixMilli(int64(value)).UTC()
	}
	return time.Unix(int64(value), 0).UTC()
}

func readQuotaErrorBody(resp *http.Response) []byte {
	if resp.Body == nil {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, quotaResetBodyLimit))
	resp.Body.Close()
	return body
}

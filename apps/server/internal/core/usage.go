package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	usageOK          = "ok"
	usageError       = "error"
	usageUnsupported = "unsupported"
)

type ChannelUsage struct {
	ChannelID int64         `json:"channel_id"`
	Kind      string        `json:"kind"`
	Status    string        `json:"status"`
	CheckedAt string        `json:"checked_at,omitempty"`
	Message   string        `json:"message,omitempty"`
	Credits   *UsageCredits `json:"credits,omitempty"`
	Windows   []UsageWindow `json:"windows,omitempty"`
}

type UsageCredits struct {
	Total     *float64 `json:"total,omitempty"`
	Used      *float64 `json:"used,omitempty"`
	Remaining *float64 `json:"remaining,omitempty"`
	Unlimited *bool    `json:"unlimited,omitempty"`
	Unit      string   `json:"unit,omitempty"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

type UsageWindow struct {
	Window        string   `json:"window,omitempty"`
	UsedPercent   *float64 `json:"used_percent,omitempty"`
	ResetsAt      string   `json:"resets_at,omitempty"`
	DurationHours *float64 `json:"duration_hours,omitempty"`
	TokenUsed     *float64 `json:"token_used,omitempty"`
	TokenCap      *float64 `json:"token_cap,omitempty"`
}

func (s *Server) channelUsage(w http.ResponseWriter, r *http.Request) {
	channels, err := s.channels()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取渠道失败")
		return
	}
	queryable := make([]Channel, 0, len(channels))
	for _, c := range channels {
		if c.Kind == "opencode_go" || c.Kind == "dimagent" {
			queryable = append(queryable, c)
		}
	}
	results := make([]ChannelUsage, len(queryable))
	var wg sync.WaitGroup
	for i, c := range queryable {
		wg.Add(1)
		go func(index int, channel Channel) {
			defer wg.Done()
			results[index] = s.usageForChannel(r.Context(), channel)
		}(i, c)
	}
	wg.Wait()
	jsonOut(w, http.StatusOK, map[string]any{"usage": results})
}

func (s *Server) usageForChannel(ctx context.Context, c Channel) ChannelUsage {
	switch c.Kind {
	case "opencode_go":
		return s.openCodeUsage(ctx, c)
	case "dimagent":
		return s.dimAgentUsage(ctx, c)
	default:
		return ChannelUsage{ChannelID: c.ID, Kind: c.Kind, Status: usageUnsupported}
	}
}

func usageResult(c Channel, status string) ChannelUsage {
	return ChannelUsage{ChannelID: c.ID, Kind: c.Kind, Status: status}
}

func usageFailure(c Channel, message string) ChannelUsage {
	result := usageResult(c, usageError)
	result.Message = truncateUsageMessage(message)
	return result
}

func usageSuccess(c Channel) ChannelUsage {
	result := usageResult(c, usageOK)
	result.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	return result
}

func truncateUsageMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "用量查询失败"
	}
	runes := []rune(message)
	if len(runes) > 240 {
		return string(runes[:240]) + "…"
	}
	return message
}

func sanitizeUsageMessage(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[已隐藏]")
		}
	}
	return truncateUsageMessage(message)
}

func usageTimeout() time.Duration {
	return 10 * time.Second
}

func usageURL(c Channel) (string, bool) {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	switch c.Kind {
	case "opencode_go":
		if host == "opencode.ai" {
			return "https://opencode.ai/zen/go/v1/usage", true
		}
	case "dimagent":
		if host == "dimagent.cn" || host == "www.dimagent.cn" {
			return "https://dimagent.cn/api/me/usage", true
		}
	}
	return "", false
}

func readUsageBody(resp *http.Response) ([]byte, error) {
	if resp.Body == nil {
		return nil, errors.New("empty response")
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

type openCodeUsageWindow struct {
	Percent  *float64        `json:"percent"`
	ResetsAt json.RawMessage `json:"resetsAt"`
}

type openCodeUsageResponse struct {
	Usage struct {
		Rolling *openCodeUsageWindow `json:"rolling"`
		Weekly  *openCodeUsageWindow `json:"weekly"`
		Monthly *openCodeUsageWindow `json:"monthly"`
	} `json:"usage"`
}

func (s *Server) openCodeUsage(parent context.Context, c Channel) ChannelUsage {
	endpoint, supported := usageURL(c)
	if !supported {
		return usageResult(c, usageUnsupported)
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return usageFailure(c, "OpenCode Go 渠道缺少 API Key，无法查询用量")
	}
	ctx, cancel := context.WithTimeout(parent, usageTimeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return usageFailure(c, "无法创建 OpenCode Go 用量请求")
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return usageFailure(c, "OpenCode Go 用量查询超时")
		}
		return usageFailure(c, "无法连接 OpenCode Go 用量接口")
	}
	body, err := readUsageBody(resp)
	if err != nil {
		return usageFailure(c, "读取 OpenCode Go 用量响应失败")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return usageFailure(c, fmt.Sprintf("OpenCode Go 用量接口返回 %d", resp.StatusCode))
	}
	var payload openCodeUsageResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return usageFailure(c, sanitizeUsageMessage("OpenCode Go 用量响应无法解析", c.APIKey))
	}
	windows := make([]UsageWindow, 0, 3)
	for _, item := range []struct {
		name   string
		window *openCodeUsageWindow
	}{
		{"five_hour", payload.Usage.Rolling},
		{"weekly", payload.Usage.Weekly},
		{"monthly", payload.Usage.Monthly},
	} {
		if item.window == nil || item.window.Percent == nil {
			continue
		}
		windows = append(windows, UsageWindow{
			Window:      item.name,
			UsedPercent: item.window.Percent,
			ResetsAt:    parseUsageTime(item.window.ResetsAt),
		})
	}
	if len(windows) == 0 {
		return usageFailure(c, "OpenCode Go 用量响应中没有可展示的数据")
	}
	result := usageSuccess(c)
	result.Windows = windows
	return result
}

func parseUsageTime(raw json.RawMessage) string {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return ""
	}
	if strings.HasPrefix(value, `"`) {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return ""
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return ""
		}
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
		if number, err := strconv.ParseInt(text, 10, 64); err == nil {
			return formatEpoch(number)
		}
		return ""
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return ""
	}
	return formatEpoch(int64(number))
}

func formatEpoch(value int64) string {
	if value <= 0 {
		return ""
	}
	if value > 1e12 {
		return time.UnixMilli(value).UTC().Format(time.RFC3339)
	}
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}

type dimUsageWindow struct {
	DurationHours *float64 `json:"window_duration_hours"`
	TokenCap      *float64 `json:"window_token_cap"`
	TokenUsed     *float64 `json:"window_token_used"`
	StartedAt     string   `json:"window_started_at"`
	ExpiresAt     string   `json:"window_expires_at"`
}

type dimUsageCredits struct {
	TotalUnits         *float64         `json:"total_units"`
	UsedUnits          *float64         `json:"used_units"`
	RemainingUnits     *float64         `json:"remaining_units"`
	Unlimited          *bool            `json:"unlimited"`
	ExpiresAt          string           `json:"expires_at"`
	WindowStates       []dimUsageWindow `json:"window_states"`
	SubscriptionBucket *dimUsageCredits `json:"subscription_bucket"`
}

type dimUsageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    *struct {
		Credits *dimUsageCredits `json:"credits"`
	} `json:"data"`
}

func (s *Server) dimAgentUsage(parent context.Context, c Channel) ChannelUsage {
	endpoint, supported := usageURL(c)
	if !supported {
		return usageResult(c, usageUnsupported)
	}
	token, err := s.credential(c)
	if err != nil {
		return usageFailure(c, err.Error())
	}
	ctx, cancel := context.WithTimeout(parent, usageTimeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return usageFailure(c, "无法创建 DimAgent 用量请求")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	applyDimOAuthHeaders(req, c)
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return usageFailure(c, "DimAgent 用量查询超时")
		}
		return usageFailure(c, "无法连接 DimAgent 用量接口")
	}
	body, err := readUsageBody(resp)
	if err != nil {
		return usageFailure(c, "读取 DimAgent 用量响应失败")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return usageFailure(c, fmt.Sprintf("DimAgent 用量接口返回 %d", resp.StatusCode))
	}
	var payload dimUsageResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return usageFailure(c, sanitizeUsageMessage("DimAgent 用量响应无法解析", token))
	}
	if !payload.Success {
		message := sanitizeUsageMessage(payload.Message, token)
		if strings.TrimSpace(payload.Message) == "" {
			message = "DimAgent 用量接口返回失败"
		}
		return usageFailure(c, message)
	}
	if payload.Data == nil || payload.Data.Credits == nil {
		return usageFailure(c, "DimAgent 用量响应中没有 Credits 数据")
	}
	credits := mergeDimCredits(payload.Data.Credits)
	if credits.empty() {
		return usageFailure(c, "DimAgent 用量响应中没有可展示的数据")
	}
	result := usageSuccess(c)
	result.Credits = credits.toUsageCredits()
	result.Windows = credits.usageWindows()
	return result
}

func firstFloat(values ...*float64) *float64 {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstBool(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstWindows(values ...[]dimUsageWindow) []dimUsageWindow {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func mergeDimCredits(top *dimUsageCredits) *dimUsageCredits {
	if top == nil {
		return &dimUsageCredits{}
	}
	bucket := top.SubscriptionBucket
	return &dimUsageCredits{
		TotalUnits:     firstFloat(valueOrNil(bucket, func(c *dimUsageCredits) *float64 { return c.TotalUnits }), top.TotalUnits),
		UsedUnits:      firstFloat(valueOrNil(bucket, func(c *dimUsageCredits) *float64 { return c.UsedUnits }), top.UsedUnits),
		RemainingUnits: firstFloat(valueOrNil(bucket, func(c *dimUsageCredits) *float64 { return c.RemainingUnits }), top.RemainingUnits),
		Unlimited:      firstBool(valueOrBool(bucket, func(c *dimUsageCredits) *bool { return c.Unlimited }), top.Unlimited),
		ExpiresAt:      firstString(valueOrString(bucket, func(c *dimUsageCredits) string { return c.ExpiresAt }), top.ExpiresAt),
		WindowStates:   firstWindows(valueOrWindows(bucket), top.WindowStates),
	}
}

func valueOrNil(value *dimUsageCredits, get func(*dimUsageCredits) *float64) *float64 {
	if value == nil {
		return nil
	}
	return get(value)
}

func valueOrBool(value *dimUsageCredits, get func(*dimUsageCredits) *bool) *bool {
	if value == nil {
		return nil
	}
	return get(value)
}

func valueOrString(value *dimUsageCredits, get func(*dimUsageCredits) string) string {
	if value == nil {
		return ""
	}
	return get(value)
}

func valueOrWindows(value *dimUsageCredits) []dimUsageWindow {
	if value == nil {
		return nil
	}
	return value.WindowStates
}

func (c *dimUsageCredits) empty() bool {
	return c.TotalUnits == nil && c.UsedUnits == nil && c.RemainingUnits == nil && c.Unlimited == nil && len(c.WindowStates) == 0
}

func (c *dimUsageCredits) toUsageCredits() *UsageCredits {
	return &UsageCredits{
		Total:     c.TotalUnits,
		Used:      c.UsedUnits,
		Remaining: c.RemainingUnits,
		Unlimited: c.Unlimited,
		Unit:      "Credits",
		ExpiresAt: normalizeUsageTime(c.ExpiresAt),
	}
}

func (c *dimUsageCredits) usageWindows() []UsageWindow {
	windows := make([]UsageWindow, 0, len(c.WindowStates))
	for _, window := range c.WindowStates {
		if window.DurationHours == nil && window.TokenUsed == nil && window.TokenCap == nil && window.ExpiresAt == "" {
			continue
		}
		windows = append(windows, UsageWindow{
			DurationHours: window.DurationHours,
			TokenUsed:     window.TokenUsed,
			TokenCap:      window.TokenCap,
			ResetsAt:      normalizeUsageTime(window.ExpiresAt),
		})
	}
	if len(windows) == 0 {
		return nil
	}
	return windows
}

func normalizeUsageTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return ""
}

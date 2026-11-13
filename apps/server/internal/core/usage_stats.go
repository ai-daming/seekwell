package core

import (
	"database/sql"
	"net/http"
	"time"
)

// UsageTotals is the shared counting shape for overall, per-key and daily
// access usage. Token fields stay null when the upstream never returned them.
type UsageTotals struct {
	RequestCount      int64  `json:"request_count"`
	SuccessCount      int64  `json:"success_count"`
	FailureCount      int64  `json:"failure_count"`
	UsageKnownCount   int64  `json:"usage_known_count"`
	UsageUnknownCount int64  `json:"usage_unknown_count"`
	InputTokens       *int64 `json:"input_tokens"`
	OutputTokens      *int64 `json:"output_tokens"`
	TotalTokens       *int64 `json:"total_tokens"`
}

type OverallUsage struct {
	UsageTotals
	KeyCount        int64   `json:"key_count"`
	EnabledKeyCount int64   `json:"enabled_key_count"`
	LastRequestAt   *string `json:"last_request_at"`
}

type KeyUsage struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UsageTotals
	LastRequestAt *string `json:"last_request_at"`
}

type DailyUsage struct {
	Date string `json:"date"`
	UsageTotals
}

type UsageStats struct {
	Overall OverallUsage `json:"overall"`
	Keys    []KeyUsage   `json:"keys"`
	Daily   []DailyUsage `json:"daily"`
}

func usageTotalTokens(input, output *int64) *int64 {
	if input == nil && output == nil {
		return nil
	}
	var total int64
	if input != nil {
		total += *input
	}
	if output != nil {
		total += *output
	}
	return &total
}

func newUsageTotals(requestCount, successCount, knownCount int64, input, output *int64) UsageTotals {
	return UsageTotals{
		RequestCount:      requestCount,
		SuccessCount:      successCount,
		FailureCount:      requestCount - successCount,
		UsageKnownCount:   knownCount,
		UsageUnknownCount: requestCount - knownCount,
		InputTokens:       input,
		OutputTokens:      output,
		TotalTokens:       usageTotalTokens(input, output),
	}
}

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func (s *Server) usageStats(w http.ResponseWriter, r *http.Request) {
	overall, err := s.overallUsage()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取使用统计失败")
		return
	}
	keys, err := s.keyUsage()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取使用统计失败")
		return
	}
	daily, err := s.dailyUsage()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取使用统计失败")
		return
	}
	jsonOut(w, http.StatusOK, UsageStats{Overall: overall, Keys: keys, Daily: daily})
}

func (s *Server) overallUsage() (OverallUsage, error) {
	var requestCount, successCount, knownCount int64
	var input, output sql.NullInt64
	var lastRequestAt sql.NullString
	err := s.db.QueryRow(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN status BETWEEN 200 AND 299 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN input_tokens IS NOT NULL AND output_tokens IS NOT NULL THEN 1 ELSE 0 END),0),
		SUM(input_tokens), SUM(output_tokens), MAX(created_at)
		FROM access_requests`).Scan(&requestCount, &successCount, &knownCount, &input, &output, &lastRequestAt)
	if err != nil {
		return OverallUsage{}, err
	}
	var keyCount, enabledKeyCount int64
	err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN enabled=1 THEN 1 ELSE 0 END),0) FROM access_keys`).Scan(&keyCount, &enabledKeyCount)
	if err != nil {
		return OverallUsage{}, err
	}
	return OverallUsage{
		UsageTotals:     newUsageTotals(requestCount, successCount, knownCount, nullableInt64(input), nullableInt64(output)),
		KeyCount:        keyCount,
		EnabledKeyCount: enabledKeyCount,
		LastRequestAt:   nullableString(lastRequestAt),
	}, nil
}

func (s *Server) keyUsage() ([]KeyUsage, error) {
	rows, err := s.db.Query(`SELECT k.id,k.name,k.prefix,k.enabled,k.created_at,
		COUNT(r.id),
		COALESCE(SUM(CASE WHEN r.status BETWEEN 200 AND 299 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN r.input_tokens IS NOT NULL AND r.output_tokens IS NOT NULL THEN 1 ELSE 0 END),0),
		SUM(r.input_tokens), SUM(r.output_tokens), MAX(r.created_at)
		FROM access_keys k LEFT JOIN access_requests r ON r.key_id=k.id
		GROUP BY k.id
		ORDER BY COUNT(r.id) DESC, k.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []KeyUsage{}
	for rows.Next() {
		var item KeyUsage
		var requestCount, successCount, knownCount int64
		var input, output sql.NullInt64
		var lastRequestAt sql.NullString
		if err = rows.Scan(&item.ID, &item.Name, &item.Prefix, &item.Enabled, &item.CreatedAt,
			&requestCount, &successCount, &knownCount, &input, &output, &lastRequestAt); err != nil {
			return nil, err
		}
		item.UsageTotals = newUsageTotals(requestCount, successCount, knownCount, nullableInt64(input), nullableInt64(output))
		item.LastRequestAt = nullableString(lastRequestAt)
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type dailyUsageAccumulator struct {
	requestCount int64
	successCount int64
	knownCount   int64
	input        int64
	output       int64
	hasInput     bool
	hasOutput    bool
}

func (s *Server) dailyUsage() ([]DailyUsage, error) {
	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	start := today.AddDate(0, 0, -6)
	end := today.AddDate(0, 0, 1)
	rows, err := s.db.Query(`SELECT created_at,status,input_tokens,output_tokens FROM access_requests WHERE created_at>=? AND created_at<?`,
		start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	acc := map[string]*dailyUsageAccumulator{}
	for rows.Next() {
		var createdAt string
		var status int
		var input, output sql.NullInt64
		if err = rows.Scan(&createdAt, &status, &input, &output); err != nil {
			return nil, err
		}
		at, parseErr := time.Parse(time.RFC3339, createdAt)
		if parseErr != nil {
			continue
		}
		day := at.In(time.Local).Format("2006-01-02")
		item := acc[day]
		if item == nil {
			item = &dailyUsageAccumulator{}
			acc[day] = item
		}
		item.requestCount++
		if status >= 200 && status <= 299 {
			item.successCount++
		}
		if input.Valid {
			item.input += input.Int64
			item.hasInput = true
		}
		if output.Valid {
			item.output += output.Int64
			item.hasOutput = true
		}
		if input.Valid && output.Valid {
			item.knownCount++
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	out := make([]DailyUsage, 0, 7)
	for i := 0; i < 7; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		item := acc[date]
		if item == nil {
			out = append(out, DailyUsage{Date: date, UsageTotals: newUsageTotals(0, 0, 0, nil, nil)})
			continue
		}
		var input, output *int64
		if item.hasInput {
			value := item.input
			input = &value
		}
		if item.hasOutput {
			value := item.output
			output = &value
		}
		out = append(out, DailyUsage{
			Date:        date,
			UsageTotals: newUsageTotals(item.requestCount, item.successCount, item.knownCount, input, output),
		})
	}
	return out, nil
}

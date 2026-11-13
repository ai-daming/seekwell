package core

import (
	"database/sql"
	"encoding/json"
	"time"
)

const createPricingHistorySQL = `CREATE TABLE IF NOT EXISTS pricing_history (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	version TEXT NOT NULL,
	source TEXT NOT NULL,
	observed_at TEXT NOT NULL,
	rates_json TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pricing_history_observed_at ON pricing_history(observed_at);`

func ensurePricingHistory(db *sql.DB) error {
	_, err := db.Exec(createPricingHistorySQL)
	return err
}

// seedPricingHistory keeps the last remote snapshot from #24 as the first
// observation when upgrading. Bundled rates wait for the first successful sync.
func seedPricingHistory(db *sql.DB, snapshot pricingSnapshot) error {
	if snapshot.Source != "remote" || snapshot.SyncedAt == nil {
		return nil
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pricing_history`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return insertPricingHistory(db, snapshot)
}

func insertPricingHistory(db *sql.DB, snapshot pricingSnapshot) error {
	observed := snapshot.SyncedAt
	if observed == nil {
		now := time.Now()
		observed = &now
	}
	body, err := json.Marshal(snapshot.Rates)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO pricing_history(version,source,observed_at,rates_json) VALUES(?,?,?,?)`,
		snapshot.Version, snapshot.Source, observed.UTC().Format(time.RFC3339), string(body))
	return err
}

func latestPricingHistoryRates(db *sql.DB) (map[string]priceRates, bool, error) {
	var body string
	err := db.QueryRow(`SELECT rates_json FROM pricing_history ORDER BY observed_at DESC, id DESC LIMIT 1`).Scan(&body)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var rates map[string]priceRates
	if json.Unmarshal([]byte(body), &rates) != nil || len(rates) == 0 {
		return nil, false, nil
	}
	return rates, true, nil
}

func pricingRatesEqual(a, b map[string]priceRates) bool {
	if len(a) != len(b) {
		return false
	}
	for model, rate := range a {
		if other, ok := b[model]; !ok || other != rate {
			return false
		}
	}
	return true
}

func (s *Server) appendPricingHistoryIfChanged(snapshot pricingSnapshot) error {
	latest, ok, err := latestPricingHistoryRates(s.db)
	if err != nil {
		return err
	}
	if ok && pricingRatesEqual(latest, snapshot.Rates) {
		return nil
	}
	return insertPricingHistory(s.db, snapshot)
}

// pricingHistory returns newest-first observed snapshots. Corrupt rows are
// skipped so a damaged history never breaks the stats endpoint.
func (s *Server) pricingHistory() []PricingHistory {
	out := []PricingHistory{}
	rows, err := s.db.Query(`SELECT version,source,observed_at,rates_json FROM pricing_history ORDER BY observed_at DESC, id DESC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var version, source, observedAt, body string
		if err := rows.Scan(&version, &source, &observedAt, &body); err != nil {
			continue
		}
		var rates map[string]priceRates
		if json.Unmarshal([]byte(body), &rates) != nil || len(rates) == 0 {
			continue
		}
		out = append(out, PricingHistory{
			Version:    version,
			ObservedAt: observedAt,
			Source:     source,
			Models:     pricingRatesAPI(rates),
		})
	}
	return out
}

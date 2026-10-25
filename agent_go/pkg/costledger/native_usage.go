package costledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// NativeCoverage is an accounting receipt, not another cost ledger. Positions
// name the source bytes already owned by a foreground or native transaction.
type NativeCoverage struct {
	Positions         map[string]int64 `json:"positions"`
	Fallback          map[string]Entry `json:"foreground_covered_sources,omitempty"`
	Ready             bool             `json:"ready"`
	ForegroundThrough time.Time        `json:"foreground_through,omitempty"`
}

func (l *Ledger) NativeCoverage(runtime string) (NativeCoverage, error) {
	var c NativeCoverage
	if l == nil {
		return c, fmt.Errorf("native accounting needs a ledger")
	}
	store, ok := l.db.(*sqliteLedger)
	if !ok {
		return c, fmt.Errorf("native accounting needs SQLite")
	}
	if err := ensureNativeCoverageTable(store); err != nil {
		return c, err
	}
	var raw string
	err := store.db.QueryRow(`SELECT receipt FROM native_cost_coverage WHERE runtime_key=?`, runtime).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(raw), &c)
	return c, err
}

func ensureNativeCoverageTable(s *sqliteLedger) error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS native_cost_coverage(runtime_key TEXT PRIMARY KEY,receipt TEXT NOT NULL)`)
	return err
}

// CommitNativeUsage commits the charge and source receipt together. A crash
// before the caller saves its read cursor only replays the same source keys.
func (l *Ledger) CommitNativeUsage(runtime string, entries []Entry, c NativeCoverage) error {
	if l == nil {
		return fmt.Errorf("native accounting needs a ledger")
	}
	store, ok := l.db.(*sqliteLedger)
	if !ok {
		return fmt.Errorf("native accounting needs SQLite")
	}
	if runtime == "" {
		return fmt.Errorf("native runtime identity required")
	}
	if err := ensureNativeCoverageTable(store); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, e := range entries {
		native, _ := e.OperationMetadata["native_usage"].(bool)
		if err := appendCostEntry(tx, e, native); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO native_cost_coverage(runtime_key,receipt) VALUES(?,?) ON CONFLICT(runtime_key) DO UPDATE SET receipt=excluded.receipt`, runtime, string(raw))
	if err != nil {
		return err
	}
	return tx.Commit()
}

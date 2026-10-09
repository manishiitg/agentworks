package server

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// renameCostLedgerKeys re-files a moved Crew's cost rows under its new path. The cost ledger (<docs>/_system/costs.sqlite)
// keys every row by workflow_id = the path the turn ran in, and the Costs popup reads only the Crew's current path, so
// after a move the history from before it showed "No cost data found" (server B 2026-10-05). It is idempotent, a missing
// ledger is not an error, and the caller treats a failure as a warning: the Crew itself has already moved.
func renameCostLedgerKeys(docsRoot, oldKey, newKey string) (int64, error) {
	db := filepath.Join(docsRoot, "_system", "costs.sqlite")
	if _, err := os.Stat(db); err != nil {
		return 0, nil
	}
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(db)+"?_pragma=busy_timeout("+url.QueryEscape("60000")+")")
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	res, err := conn.Exec(`UPDATE cost_events SET workflow_id=? WHERE workflow_id=?`, newKey, oldKey)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Package sqliteopen builds SQLite connection strings for workflow database
// files shared by more than one process/goroutine. Every caller here must
// agree on the same two pragmas or reintroduce SQLITE_BUSY under concurrent
// load: journal_mode=WAL, so a writer's transaction doesn't take an
// exclusive lock against every concurrent reader (the default
// rollback-journal mode does exactly that), and busy_timeout, so a genuine
// writer-vs-writer collision retries for a few seconds instead of failing
// instantly. Both must be embedded in the DSN, not set via a one-time
// runtime PRAGMA call: a *sql.DB with no SetMaxOpenConns cap can silently
// open a second physical connection for a concurrent query, and a pragma
// set on connection #1 does not apply to connection #2.
package sqliteopen

import (
	"net/url"
	"path/filepath"
)

// DSN returns a "sqlite" driver connection string for path with WAL mode
// and a 5s busy_timeout embedded. Callers needing additional pragmas (e.g.
// query_only(true) for a strictly read-only connection) should append
// "&_pragma=..." to the result themselves.
func DSN(path string) string {
	// A Windows path becomes file:///C:/dir/file: forward slashes, and a leading slash before the drive letter.
	slashed := filepath.ToSlash(path)
	if len(slashed) >= 2 && slashed[1] == ':' {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String() + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
}

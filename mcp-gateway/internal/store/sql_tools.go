package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
	"github.com/manishiitg/coding-agent-loop/workspace/sqlpolicy"
)

// Public SQL tables contain governance metadata. Credentials and key hashes
// remain only in the encrypted configuration row, with the key outside chat.
var publicSchema = []string{
	`CREATE TABLE IF NOT EXISTS workspaces(id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, email TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS groups(id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, name TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS group_members(group_id TEXT NOT NULL,user_id TEXT NOT NULL,PRIMARY KEY(group_id,user_id))`,
	`CREATE TABLE IF NOT EXISTS connectors(id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL,provider TEXT NOT NULL,label TEXT NOT NULL,status TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS tools(public_name TEXT PRIMARY KEY,connector_id TEXT NOT NULL,upstream_name TEXT NOT NULL,description TEXT NOT NULL,input_schema TEXT NOT NULL,fingerprint TEXT NOT NULL,status TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS user_tool_grants(user_id TEXT NOT NULL,public_name TEXT NOT NULL,PRIMARY KEY(user_id,public_name))`,
	`CREATE TABLE IF NOT EXISTS group_tool_grants(group_id TEXT NOT NULL,public_name TEXT NOT NULL,PRIMARY KEY(group_id,public_name))`,
	`CREATE TABLE IF NOT EXISTS group_server_grants(group_id TEXT NOT NULL,connector_id TEXT NOT NULL,PRIMARY KEY(group_id,connector_id))`,
	`CREATE TABLE IF NOT EXISTS permission_drafts(id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL,group_id TEXT NOT NULL,name TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 0,rules_json TEXT NOT NULL CHECK(json_valid(rules_json)))`,
	`CREATE TABLE IF NOT EXISTS published_permissions(id TEXT PRIMARY KEY,group_id TEXT NOT NULL,name TEXT NOT NULL,version INTEGER NOT NULL,status TEXT NOT NULL,rules_json TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS policy_history(sequence INTEGER PRIMARY KEY,event_json TEXT NOT NULL)`,
}
var publicTables = []string{"workspaces", "users", "groups", "group_members", "connectors", "tools", "user_tool_grants", "group_tool_grants", "group_server_grants", "permission_drafts", "published_permissions", "policy_history"}
var editableTables = map[string]bool{"groups": true, "group_members": true, "user_tool_grants": true, "group_tool_grants": true, "permission_drafts": true}

// EnableSQLWorkspace binds this project's SQL surface to one workspace. SQL
// callers cannot select a different database or tenant through arguments.
func (s *MemoryStore) EnableSQLWorkspace(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.persistence
	if p == nil {
		return errors.New("SQLite storage is required")
	}
	if _, ok := s.workspaces[id]; !ok {
		return errors.New("unknown SQL workspace")
	}
	p.sqlWorkspace = id
	for _, q := range publicSchema {
		if _, err := p.db.Exec(q); err != nil {
			return err
		}
	}
	for _, table := range publicTables {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			q := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS "caplayer_%s_%s" AFTER %s ON "%s" BEGIN UPDATE gateway_configuration SET revision=revision+1 WHERE id=1; END`, table, op, op, table)
			if _, err := p.db.Exec(q); err != nil {
				return err
			}
		}
	}
	data, err := json.Marshal(s.durableState())
	if err != nil {
		return err
	}
	if err = p.commitState(context.Background(), data, s.durableState()); err != nil {
		p.err = err
		return err
	}
	return nil
}

func (p *sqlitePersistence) project(tx *sql.Tx, state durableState) error {
	if p.sqlWorkspace == "" {
		return nil
	}
	for _, table := range publicTables {
		if _, err := tx.Exec(`DELETE FROM "` + table + `"`); err != nil {
			return err
		}
	}
	exec := func(q string, args ...any) error { _, err := tx.Exec(q, args...); return err }
	w := p.sqlWorkspace
	if v, ok := state.Workspaces[w]; ok {
		if err := exec(`INSERT INTO workspaces VALUES(?,?)`, v.ID, v.Name); err != nil {
			return err
		}
	}
	for _, v := range state.Users {
		if v.WorkspaceID == w {
			if err := exec(`INSERT INTO users VALUES(?,?,?)`, v.ID, v.WorkspaceID, v.Email); err != nil {
				return err
			}
		}
	}
	for _, v := range state.Groups {
		if v.WorkspaceID != w {
			continue
		}
		if err := exec(`INSERT INTO groups VALUES(?,?,?)`, v.ID, v.WorkspaceID, v.Name); err != nil {
			return err
		}
		for uid := range state.Members[v.ID] {
			if err := exec(`INSERT INTO group_members VALUES(?,?)`, v.ID, uid); err != nil {
				return err
			}
		}
		for tool := range state.GroupGrants[v.ID] {
			if err := exec(`INSERT INTO group_tool_grants VALUES(?,?)`, v.ID, tool); err != nil {
				return err
			}
		}
		for cid := range state.GroupServers[v.ID] {
			if err := exec(`INSERT INTO group_server_grants VALUES(?,?)`, v.ID, cid); err != nil {
				return err
			}
		}
	}
	for _, v := range state.Connectors {
		if v.WorkspaceID == w {
			if err := exec(`INSERT INTO connectors VALUES(?,?,?,?,?)`, v.ID, v.WorkspaceID, v.Provider, v.Label, string(v.Status)); err != nil {
				return err
			}
		}
	}
	for _, v := range state.Tools {
		if v.WorkspaceID == w {
			if err := exec(`INSERT INTO tools VALUES(?,?,?,?,?,?,?)`, v.PublicName, v.ConnectorID, v.UpstreamName, v.Description, string(v.InputSchema), v.Fingerprint, string(v.Status)); err != nil {
				return err
			}
		}
	}
	for uid, grants := range state.Grants {
		if state.Users[uid].WorkspaceID == w {
			for name := range grants {
				if err := exec(`INSERT INTO user_tool_grants VALUES(?,?)`, uid, name); err != nil {
					return err
				}
			}
		}
	}
	for _, v := range state.Drafts {
		if v.WorkspaceID == w {
			rules, _ := json.Marshal(v.Rules)
			if err := exec(`INSERT INTO permission_drafts VALUES(?,?,?,?,?,?)`, v.ID, v.WorkspaceID, v.GroupID, v.Name, v.Version, string(rules)); err != nil {
				return err
			}
		}
	}
	for _, v := range state.Live {
		if v.WorkspaceID == w {
			rules, _ := json.Marshal(v.Rules)
			if err := exec(`INSERT INTO published_permissions VALUES(?,?,?,?,?,?)`, v.ID, v.GroupID, v.Name, v.Version, v.Status, string(rules)); err != nil {
				return err
			}
		}
	}
	for i, v := range state.History[w] {
		b, _ := json.Marshal(v)
		if err := exec(`INSERT INTO policy_history VALUES(?,?)`, i+1, string(b)); err != nil {
			return err
		}
	}
	return nil
}
func (p *sqlitePersistence) finishTx(ctx context.Context, tx *sql.Tx, data []byte, state durableState) error {
	sealed, err := p.seal(data)
	if err != nil {
		return err
	}
	// The SQL transaction already owns the writer lock and checked its starting revision.
	if _, err = tx.ExecContext(ctx, `UPDATE gateway_configuration SET payload=?,revision=revision+1 WHERE id=1`, sealed); err != nil {
		return err
	}
	if err = p.project(tx, state); err != nil {
		return err
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM gateway_configuration WHERE id=1`).Scan(&revision); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	p.saved = data
	p.revision = revision
	return nil
}
func (p *sqlitePersistence) beginTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM gateway_configuration WHERE id=1`).Scan(&revision); err != nil {
		tx.Rollback()
		return nil, err
	}
	if revision != p.revision {
		tx.Rollback()
		return nil, errors.New("gateway configuration changed outside its storage owner")
	}
	return tx, nil
}
func (p *sqlitePersistence) commitState(ctx context.Context, data []byte, state durableState) error {
	tx, err := p.beginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return p.finishTx(ctx, tx, data, state)
}

type SQLQuery struct {
	Action  string `json:"action"`
	Table   string `json:"table"`
	SQL     string `json:"sql"`
	Query   string `json:"query"`
	Params  []any  `json:"params"`
	MaxRows int    `json:"max_rows"`
}
type SQLStatement struct {
	SQL    string `json:"sql"`
	Params []any  `json:"params"`
}
type SQLMutation struct {
	SQL        string         `json:"sql"`
	Params     []any          `json:"params"`
	Statements []SQLStatement `json:"statements"`
}
type SQLRows struct {
	Columns   []string         `json:"columns"`
	Rows      []map[string]any `json:"rows"`
	Truncated bool             `json:"truncated"`
}
type SQLMutationResult struct {
	Results           []SQLStatementResult `json:"results"`
	TotalRowsAffected int64                `json:"total_rows_affected"`
}
type SQLStatementResult struct {
	RowsAffected int64 `json:"rows_affected"`
	LastInsertID int64 `json:"last_insert_id"`
}

func (s *MemoryStore) sqlReady(workspaceID string) error {
	p := s.persistence
	if p == nil || p.sqlWorkspace == "" {
		return errors.New("project SQL database is not configured")
	}
	if p.err != nil {
		return p.err
	}
	if p.sqlWorkspace != workspaceID {
		return errors.New("SQL workspace mismatch")
	}
	var revision int64
	if err := p.db.QueryRow(`SELECT revision FROM gateway_configuration WHERE id=1`).Scan(&revision); err != nil {
		return err
	}
	if revision != p.revision {
		p.err = errors.New("database modified outside gateway transaction; restart required")
		return p.err
	}
	return nil
}
func (s *MemoryStore) QuerySQL(ctx context.Context, workspaceID string, req SQLQuery) (SQLRows, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.sqlReady(workspaceID); err != nil {
		return SQLRows{}, err
	}
	q := strings.TrimSpace(req.SQL)
	alias := strings.TrimSpace(req.Query)
	if q != "" && alias != "" && q != alias {
		return SQLRows{}, errors.New("sql and query must match")
	}
	if q == "" {
		q = alias
	}
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "", "query":
	case "describe":
		if q != "" {
			return SQLRows{}, errors.New("describe does not accept SQL")
		}
		if req.Table == "" {
			q = `SELECT name,type,sql FROM sqlite_master WHERE type='table' AND name IN ('` + strings.Join(publicTables, "','") + `') ORDER BY name`
		} else {
			found := false
			for _, table := range publicTables {
				if table == req.Table {
					found = true
				}
			}
			if !found {
				return SQLRows{}, errors.New("unknown governance table")
			}
			q = `PRAGMA table_info("` + req.Table + `")`
		}
	case "integrity_check":
		if q != "" {
			return SQLRows{}, errors.New("integrity_check does not accept SQL")
		}
		q = `PRAGMA integrity_check`
	default:
		return SQLRows{}, errors.New("unknown query action")
	}
	if err := sqlpolicy.ValidateRead(q); err != nil {
		return SQLRows{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// query_only is an engine-level protection, including WITH ... UPDATE bypasses.
	if _, err := s.persistence.db.ExecContext(ctx, `PRAGMA query_only=ON`); err != nil {
		return SQLRows{}, err
	}
	defer func() {
		if _, err := s.persistence.db.Exec(`PRAGMA query_only=OFF`); err != nil {
			s.persistence.err = err
		}
	}()
	rows, err := s.persistence.db.QueryContext(ctx, q, req.Params...)
	if err != nil {
		return SQLRows{}, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return SQLRows{}, err
	}
	result := SQLRows{Columns: cols, Rows: []map[string]any{}}
	limit := req.MaxRows
	if limit <= 0 {
		limit = 500
	}
	if limit > 1000 {
		limit = 1000
	}
	bytes := 0
	for rows.Next() {
		if len(result.Rows) >= limit {
			result.Truncated = true
			break
		}
		values := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range values {
			ptr[i] = &values[i]
		}
		if err := rows.Scan(ptr...); err != nil {
			return SQLRows{}, err
		}
		row := map[string]any{}
		for i, key := range cols {
			v := values[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row[key] = v
		}
		encoded, _ := json.Marshal(row)
		bytes += len(encoded)
		if bytes > 1024*1024 {
			return SQLRows{}, errors.New("query response exceeds 1 MiB; select fewer columns/rows")
		}
		result.Rows = append(result.Rows, row)
	}
	return result, rows.Err()
}

func (s *MemoryStore) MutateSQL(ctx context.Context, workspaceID, actor string, req SQLMutation) (SQLMutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	empty := SQLMutationResult{}
	if err := s.sqlReady(workspaceID); err != nil {
		return empty, err
	}
	statements := req.Statements
	if len(statements) > 0 && (req.SQL != "" || len(req.Params) > 0) {
		return empty, errors.New("use sql or statements, not both")
	}
	if len(statements) == 0 && req.SQL != "" {
		statements = []SQLStatement{{SQL: req.SQL, Params: req.Params}}
	}
	if len(statements) < 1 || len(statements) > 20 {
		return empty, errors.New("provide 1 to 20 statements")
	}
	for _, statement := range statements {
		table, err := sqlpolicy.MutationTarget(statement.SQL)
		if err != nil {
			return empty, err
		}
		if !editableTables[table] {
			return empty, fmt.Errorf("%s is read-only; mutable tables: groups, group_members, user_tool_grants, group_tool_grants, permission_drafts", table)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p := s.persistence
	tx, err := p.beginTx(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	result := SQLMutationResult{Results: []SQLStatementResult{}}
	for _, statement := range statements {
		out, err := tx.ExecContext(ctx, statement.SQL, statement.Params...)
		if err != nil {
			return empty, err
		}
		affected, err := out.RowsAffected()
		if err != nil {
			return empty, err
		}
		id, _ := out.LastInsertId()
		result.Results = append(result.Results, SQLStatementResult{affected, id})
		result.TotalRowsAffected += affected
	}
	data, err := json.Marshal(s.durableState())
	if err != nil {
		return empty, err
	}
	var state durableState
	if err = json.Unmarshal(data, &state); err != nil {
		return empty, err
	}
	if err = s.importSQL(tx, workspaceID, &state); err != nil {
		return empty, err
	}
	if result.TotalRowsAffected > 0 {
		state.History[workspaceID] = append(state.History[workspaceID], PolicyEvent{At: time.Now().UTC(), Actor: actor, Action: "sql_mutation"})
		if len(state.History[workspaceID]) > 10000 {
			state.History[workspaceID] = state.History[workspaceID][len(state.History[workspaceID])-10000:]
		}
	}
	data, err = json.Marshal(state)
	if err != nil {
		return empty, err
	}
	if err = p.finishTx(ctx, tx, data, state); err != nil {
		p.err = fmt.Errorf("configuration SQL commit failed: %w", err)
		return empty, p.err
	}
	if err = s.restore(data); err != nil {
		p.err = err
		return empty, err
	}
	return result, nil
}

// importSQL validates all editable rows against the same state used by runtime
// authorization before the SQL transaction and encrypted snapshot are committed.
func (s *MemoryStore) importSQL(tx *sql.Tx, w string, state *durableState) error {
	old := s.durableState()
	groups := map[string]Group{}
	rows, err := tx.Query(`SELECT id,workspace_id,name FROM groups`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var g Group
		if err = rows.Scan(&g.ID, &g.WorkspaceID, &g.Name); err != nil {
			rows.Close()
			return err
		}
		if !sqlIdentity.MatchString(g.ID) || g.WorkspaceID != w || strings.TrimSpace(g.Name) == "" || len(g.Name) > 100 {
			rows.Close()
			return errors.New("invalid group identity/name/workspace")
		}
		if previous, ok := old.Groups[g.ID]; ok && previous.WorkspaceID != w {
			rows.Close()
			return errors.New("group ID belongs to another workspace")
		}
		groups[g.ID] = g
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for id, g := range state.Groups {
		if g.WorkspaceID != w {
			continue
		}
		delete(state.Groups, id)
		state.Members[id] = map[string]bool{}
		state.GroupGrants[id] = map[string]bool{}
		if _, exists := groups[id]; !exists {
			delete(state.Members, id)
			delete(state.GroupGrants, id)
			delete(state.GroupServers, id)
			for key, k := range state.Keys {
				if k.GroupID == id {
					delete(state.Keys, key)
				}
			}
			for pid, p := range state.Live {
				if p.GroupID == id {
					p.Status = "revoked"
					p.Version++
					p.Rules = nil
					state.Live[pid] = p
				}
			}
		}
	}
	for id, g := range groups {
		state.Groups[id] = g
		if state.Members[id] == nil {
			state.Members[id] = map[string]bool{}
		}
		if state.GroupGrants[id] == nil {
			state.GroupGrants[id] = map[string]bool{}
		}
	}
	for uid, u := range state.Users {
		if u.WorkspaceID == w {
			state.Grants[uid] = map[string]bool{}
		}
	}
	for _, table := range []string{"group_members", "group_tool_grants", "user_tool_grants"} {
		column := "group_id"
		other := "public_name"
		if table == "group_members" {
			other = "user_id"
		}
		if table == "user_tool_grants" {
			column = "user_id"
		}
		rows, err := tx.Query(`SELECT ` + column + `,` + other + ` FROM ` + table)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, value string
			if err = rows.Scan(&id, &value); err != nil {
				rows.Close()
				return err
			}
			// Cascading removal of a deleted group's rows is reconciled here.
			if table != "user_tool_grants" {
				if _, ok := groups[id]; !ok {
					if _, oldGroup := old.Groups[id]; oldGroup && old.Groups[id].WorkspaceID == w {
						continue
					}
					rows.Close()
					return errors.New("unknown group")
				}
			} else if u, ok := state.Users[id]; !ok || u.WorkspaceID != w {
				rows.Close()
				return errors.New("unknown user")
			}
			if table == "group_members" {
				if u, ok := state.Users[value]; !ok || u.WorkspaceID != w {
					rows.Close()
					return errors.New("unknown group member")
				}
				state.Members[id][value] = true
				continue
			}
			tool, ok := state.Tools[value]
			if !ok || tool.WorkspaceID != w {
				rows.Close()
				return errors.New("unknown tool")
			}
			wasGranted := old.GroupGrants[id][value]
			if table == "user_tool_grants" {
				wasGranted = old.Grants[id][value]
			}
			if !wasGranted && (tool.Status != StatusActive || tool.Fingerprint != tool.ApprovedFingerprint || state.Governed[w][value]) {
				rows.Close()
				return errors.New("new grants require an approved active tool without a governing policy; edit a permission draft instead")
			}
			if table == "user_tool_grants" {
				state.Grants[id][value] = true
			} else {
				state.GroupGrants[id][value] = true
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	for id, p := range state.Drafts {
		if p.WorkspaceID == w {
			delete(state.Drafts, id)
		}
	}
	rows, err = tx.Query(`SELECT id,workspace_id,group_id,name,version,rules_json FROM permission_drafts`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p access.Package
		var rules string
		if err = rows.Scan(&p.ID, &p.WorkspaceID, &p.GroupID, &p.Name, &p.Version, &rules); err != nil {
			rows.Close()
			return err
		}
		if _, ok := groups[p.GroupID]; !ok {
			if g, existed := old.Groups[p.GroupID]; existed && g.WorkspaceID == w {
				continue
			}
		}
		if err = json.Unmarshal([]byte(rules), &p.Rules); err != nil {
			rows.Close()
			return err
		}
		p.Status = "draft"
		if previous, exists := old.Drafts[p.ID]; exists && previous.WorkspaceID != w {
			rows.Close()
			return errors.New("draft belongs to another workspace")
		}
		if previous, exists := old.Live[p.ID]; exists && previous.WorkspaceID != w {
			rows.Close()
			return errors.New("policy belongs to another workspace")
		}
		previous := old.Drafts[p.ID]
		version := previous.Version
		if live := old.Live[p.ID]; live.Version > version {
			version = live.Version
		}
		if p.Version != version {
			rows.Close()
			return errors.New("draft version changed; query current version before editing")
		}
		if previous.ID != "" && reflect.DeepEqual(p, previous) {
			state.Drafts[p.ID] = previous
			continue
		}
		if err = validatePackageState(p, w, *state); err != nil {
			rows.Close()
			return err
		}
		p.Version = version + 1
		state.Drafts[p.ID] = p
	}
	err = rows.Err()
	rows.Close()
	return err
}

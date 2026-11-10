package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// conversationRegistryRows is deliberately unwired. The initial migration
// review compares these reads with the existing JSON store using isolated
// snapshots; no live constructor, import, fallback or reader switch exists.
// Its namespace is the resolved per-user registry file, not a session ID.
var errAmbiguousConversationRegistry = errors.New("this chat has multiple saved run bindings; ask the owner to repair its chat bindings")

type conversationRegistryRows struct {
	db        *sql.DB
	namespace string
}

type conversationRegistryRow struct {
	key    string
	record ProductConversationRecord
}

func decodeConversationRegistrySnapshot(raw string) (productConversationRegistryDocument, error) {
	document := productConversationRegistryDocument{Version: productConversationRegistryVersion, Entries: map[string]ProductConversationRecord{}}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &document); err != nil {
			return document, fmt.Errorf("decode conversation registry snapshot: %w", err)
		}
	}
	return document, nil
}

func initializeConversationRegistryRows(ctx context.Context, db *sql.DB) error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS registry_imports(namespace TEXT PRIMARY KEY,cleanup_pending INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS registry_versions(namespace TEXT PRIMARY KEY,version INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS registry_entries(namespace TEXT NOT NULL,entry_key TEXT NOT NULL,profile_id TEXT NOT NULL,session_id TEXT NOT NULL,workspace TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(namespace,entry_key))`,
		`CREATE INDEX IF NOT EXISTS registry_session ON registry_entries(namespace,profile_id,session_id,workspace)`,
		`CREATE TABLE IF NOT EXISTS registry_previous(namespace TEXT NOT NULL,entry_key TEXT NOT NULL,ordinal INTEGER NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(namespace,entry_key,ordinal))`,
		`CREATE TABLE IF NOT EXISTS registry_agents(namespace TEXT NOT NULL,entry_key TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(namespace,entry_key))`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	return nil
}

func (r conversationRegistryRows) importSnapshot(ctx context.Context, raw string) error {
	document, err := decodeConversationRegistrySnapshot(raw)
	if err != nil {
		return err
	}
	if err = initializeConversationRegistryRows(ctx, r.db); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = r.writeSnapshot(ctx, tx, document, raw); err != nil {
		return err
	}
	return tx.Commit()
}

func (r conversationRegistryRows) writeSnapshot(ctx context.Context, tx *sql.Tx, document productConversationRegistryDocument, raw string) error {
	// Preserve per-row source JSON, including explicit empty optional arrays
	// and unknown fields. Re-marshalling typed records here would omit [] and
	// make a first read differ from the existing registry before any edit.
	var encoded struct {
		Entries  map[string]json.RawMessage   `json:"entries"`
		Previous map[string][]json.RawMessage `json:"previous"`
		Agents   map[string]json.RawMessage   `json:"agents"`
	}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &encoded); err != nil {
			return err
		}
	}
	var err error
	for _, table := range []string{"registry_entries", "registry_previous", "registry_agents"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE namespace=?`, r.namespace); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO registry_versions(namespace,version) VALUES (?,?) ON CONFLICT(namespace) DO UPDATE SET version=excluded.version`, r.namespace, document.Version); err != nil {
		return err
	}
	for key, record := range document.Entries {
		payload, ok := encoded.Entries[key]
		if !ok {
			return errors.New("registry snapshot entry is unavailable")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO registry_entries(namespace,entry_key,profile_id,session_id,workspace,payload) VALUES (?,?,?,?,?,?)`, r.namespace, key, record.ProfileID, record.SessionID, record.WorkspacePath, string(payload)); err != nil {
			return err
		}
	}
	for key, records := range document.Previous {
		if len(records) != len(encoded.Previous[key]) {
			return errors.New("registry snapshot history is unavailable")
		}
		for ordinal := range records {
			payload := encoded.Previous[key][ordinal]
			if _, err = tx.ExecContext(ctx, `INSERT INTO registry_previous(namespace,entry_key,ordinal,payload) VALUES (?,?,?,?)`, r.namespace, key, ordinal, string(payload)); err != nil {
				return err
			}
		}
	}
	for key := range document.Agents {
		payload, ok := encoded.Agents[key]
		if !ok {
			return errors.New("registry snapshot identity is unavailable")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO registry_agents(namespace,entry_key,payload) VALUES (?,?,?)`, r.namespace, key, string(payload)); err != nil {
			return err
		}
	}
	return nil
}

func (r conversationRegistryRows) entries(ctx context.Context, predicate string, args ...any) ([]conversationRegistryRow, error) {
	values := append([]any{r.namespace}, args...)
	rows, err := r.db.QueryContext(ctx, `SELECT entry_key,payload FROM registry_entries WHERE namespace=? AND (`+predicate+`) ORDER BY entry_key`, values...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []conversationRegistryRow
	for rows.Next() {
		var row conversationRegistryRow
		var payload string
		if err = rows.Scan(&row.key, &payload); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(payload), &row.record); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r conversationRegistryRows) agentIdentity(ctx context.Context, profile, key string) (ProductAgentIdentity, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, `SELECT payload FROM registry_agents WHERE namespace=? AND entry_key=?`, r.namespace, productConversationRegistryEntryKey(profile, strings.TrimSpace(key))).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ProductAgentIdentity{}, nil
	}
	if err != nil {
		return ProductAgentIdentity{}, err
	}
	var identity ProductAgentIdentity
	err = json.Unmarshal([]byte(raw), &identity)
	return identity, err
}

func (r conversationRegistryRows) agentIdentities(ctx context.Context, profile, project string) (map[string]ProductAgentIdentity, error) {
	main := productConversationRegistryEntryKey(profile, strings.TrimSpace(project))
	side := main + ":chat:"
	schedule := main + scheduleOwnChatMarker
	rows, err := r.db.QueryContext(ctx, `SELECT entry_key,payload FROM registry_agents WHERE namespace=? AND (entry_key=? OR (entry_key>=? AND entry_key<?) OR (entry_key>=? AND entry_key<?))`, r.namespace, main, side, main+":chat;", schedule, main+":schedule;")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ProductAgentIdentity{}
	for rows.Next() {
		var key, payload string
		var identity ProductAgentIdentity
		if err = rows.Scan(&key, &payload); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(payload), &identity); err != nil {
			return nil, err
		}
		out[strings.TrimPrefix(key, strings.TrimSpace(profile)+"/")] = identity
	}
	return out, rows.Err()
}

func (r conversationRegistryRows) history(ctx context.Context, profile, key string) (current ProductConversationRecord, ok bool, previous []ProductConversationRecord, err error) {
	entry := productConversationRegistryEntryKey(profile, key)
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return current, false, nil, err
	}
	defer tx.Rollback()
	var payload string
	err = tx.QueryRowContext(ctx, `SELECT payload FROM registry_entries WHERE namespace=? AND entry_key=?`, r.namespace, entry).Scan(&payload)
	if err == nil {
		if err = json.Unmarshal([]byte(payload), &current); err != nil {
			return current, false, nil, err
		}
		ok = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return current, false, nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM registry_previous WHERE namespace=? AND entry_key=? ORDER BY ordinal`, r.namespace, entry)
	if err != nil {
		return current, ok, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var record ProductConversationRecord
		if err = rows.Scan(&payload); err != nil {
			return current, ok, nil, err
		}
		if err = json.Unmarshal([]byte(payload), &record); err != nil {
			return current, ok, nil, err
		}
		previous = append(previous, record)
	}
	return current, ok, previous, rows.Err()
}

func (r conversationRegistryRows) liveSessionIDs(ctx context.Context, profile string) (map[string]bool, error) {
	// Key authority matches the old registry; a mismatched record.ProfileID must
	// not silently move the record into another profile's visible set.
	prefix := strings.TrimSpace(profile) + "/"
	rows, err := r.db.QueryContext(ctx, `SELECT session_id FROM registry_entries WHERE namespace=? AND entry_key>=? AND entry_key<?`, r.namespace, prefix, strings.TrimSpace(profile)+"0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var session string
		if err = rows.Scan(&session); err != nil {
			return nil, err
		}
		if strings.TrimSpace(session) != "" {
			out[session] = true
		}
	}
	return out, rows.Err()
}

func (r conversationRegistryRows) projectChats(ctx context.Context, profile, project string, read func(context.Context, string) (string, bool, error)) ([]ProductConversationRecord, error) {
	project = strings.TrimSpace(project)
	if project == "" || strings.Contains(project, ":") {
		return nil, errors.New("project id is required")
	}
	main := productConversationRegistryEntryKey(profile, project)
	side := main + ":chat:"
	selected, err := r.entries(ctx, `entry_key=? OR (entry_key>=? AND entry_key<?)`, main, side, main+":chat;")
	if err != nil {
		return nil, err
	}
	// Presence of the old main slot is a rule even when it has no session.
	ownRoot := false
	for _, row := range selected {
		if row.key == main {
			ownRoot = true
			break
		}
	}
	mainKeys := map[string]string{}
	var chats []ProductConversationRecord
	for _, row := range selected {
		record := row.record
		if strings.TrimSpace(record.SessionID) == "" {
			continue
		}
		if row.key != main && (len(row.key) <= len(side) || strings.Contains(row.key[len(side):], ":")) {
			continue
		}
		mainKey, known := mainKeys[record.WorkspacePath]
		if !known {
			mainKey = project
			raw, exists, e := read(ctx, strings.TrimSuffix(record.WorkspacePath, "/")+"/product.json")
			if e != nil {
				return nil, e
			}
			if exists {
				var manifest productProjectManifest
				if e = json.Unmarshal([]byte(raw), &manifest); e != nil {
					return nil, e
				}
				if manifest.MainChatKey != "" && !ownRoot {
					mainKey = manifest.MainChatKey
				}
			}
			mainKeys[record.WorkspacePath] = mainKey
		}
		record.IsMainChat = record.ConversationKey == mainKey
		chats = append(chats, record)
	}
	return chats, nil
}

func (r conversationRegistryRows) scheduleOwnChats(ctx context.Context, profile, project string) (map[string]ProductConversationRecord, error) {
	project = strings.TrimSpace(project)
	if project == "" || strings.Contains(project, ":") {
		return nil, errors.New("project id is required")
	}
	main := productConversationRegistryEntryKey(profile, project)
	prefix := main + scheduleOwnChatMarker
	selected, err := r.entries(ctx, `entry_key>=? AND entry_key<?`, prefix, main+":schedule;")
	if err != nil {
		return nil, err
	}
	out := map[string]ProductConversationRecord{}
	for _, row := range selected {
		key := strings.TrimPrefix(row.key, strings.TrimSpace(profile)+"/")
		if strings.TrimSpace(row.record.SessionID) != "" && isScheduleOwnChatKey(key) {
			out[key] = row.record
		}
	}
	return out, nil
}

func (r conversationRegistryRows) workflowStepRun(ctx context.Context, session, crew, workflow string) (string, error) {
	selected, err := r.entries(ctx, `profile_id=? AND session_id=? AND workspace=?`, crewProfileID, session, crew)
	if err != nil {
		return "", err
	}
	if len(selected) == 0 {
		return "", errors.New("this chat is unavailable; reopen the Crew chat before running its steps")
	}
	if len(selected) > 1 {
		return "", errAmbiguousConversationRegistry
	}
	record := selected[0].record
	if record.CurrentWorkflowRun != nil && record.CurrentWorkflowRun.WorkflowID == workflow {
		return record.CurrentWorkflowRun.Folder, nil
	}
	return "", nil
}

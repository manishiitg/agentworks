package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/sqlitefile"
	_ "modernc.org/sqlite"
)

// The AgentBridge mailbox is one SQLite database owned by the agent server
// process (like the cost ledger). No other process opens it: the workspace
// service refuses every document request for it. Each change is one
// transaction that touches only the rows it changed; the loaded image in this
// process is replaced only after the transaction commits.
const (
	agentMailboxDir    = "_system/agent_messages"
	agentMailboxDBName = "mailbox.sqlite"
)

const agentMailboxSchema = `
CREATE TABLE IF NOT EXISTS mailbox_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS conversations (
    inbox_id TEXT PRIMARY KEY,
    ord INTEGER NOT NULL UNIQUE,
    next_sequence INTEGER NOT NULL,
    a_user_id TEXT NOT NULL,
    a_kind TEXT NOT NULL,
    a_ref_id TEXT NOT NULL,
    a_named_agent_id TEXT NOT NULL,
    a_external INTEGER NOT NULL,
    a_profile TEXT NOT NULL,
    a_session TEXT NOT NULL,
    b_user_id TEXT NOT NULL,
    b_kind TEXT NOT NULL,
    b_ref_id TEXT NOT NULL,
    b_named_agent_id TEXT NOT NULL,
    b_external INTEGER NOT NULL,
    b_profile TEXT NOT NULL,
    b_session TEXT NOT NULL,
    endpoints TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS conversations_a_endpoint ON conversations(a_user_id, a_named_agent_id, a_kind, a_ref_id);
CREATE INDEX IF NOT EXISTS conversations_b_endpoint ON conversations(b_user_id, b_named_agent_id, b_kind, b_ref_id);
CREATE TABLE IF NOT EXISTS messages (
    ord INTEGER PRIMARY KEY,
    inbox_id TEXT NOT NULL REFERENCES conversations(inbox_id) ON DELETE CASCADE,
    message_id TEXT NOT NULL,
    sequence INTEGER NOT NULL,
    agent_sequence INTEGER NOT NULL,
    from_side INTEGER NOT NULL,
    submission_id TEXT NOT NULL,
    delivery TEXT NOT NULL,
    delivered INTEGER NOT NULL,
    inbox_removed INTEGER NOT NULL,
    file_id TEXT NOT NULL,
    data TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_inbox ON messages(inbox_id, ord);
CREATE INDEX IF NOT EXISTS messages_inbox_agent_sequence ON messages(inbox_id, agent_sequence);
CREATE UNIQUE INDEX IF NOT EXISTS messages_inbox_sequence ON messages(inbox_id, sequence) WHERE sequence > 0;
CREATE UNIQUE INDEX IF NOT EXISTS messages_inbox_message ON messages(inbox_id, message_id) WHERE message_id <> '';
CREATE UNIQUE INDEX IF NOT EXISTS messages_submission ON messages(inbox_id, from_side, submission_id) WHERE submission_id <> '';
CREATE INDEX IF NOT EXISTS messages_undelivered ON messages(inbox_id, from_side) WHERE delivery IN ('pending', 'dispatching', 'stored');
CREATE TABLE IF NOT EXISTS agents (
    agent_id TEXT PRIMARY KEY,
    ord INTEGER NOT NULL UNIQUE,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    attachment_id TEXT NOT NULL,
    data TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS agents_user_name ON agents(user_id, name);
CREATE TABLE IF NOT EXISTS agent_presence (
    agent_id TEXT PRIMARY KEY,
    last_online_ns INTEGER NOT NULL,
    last_online_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS expired_conversations (
    ord INTEGER PRIMARY KEY,
    inbox_id TEXT NOT NULL,
    data TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS mailbox_records (
    kind TEXT NOT NULL,
    ord INTEGER NOT NULL,
    data TEXT NOT NULL,
    PRIMARY KEY (kind, ord)
);`

type agentMailbox struct {
	db      *sql.DB
	path    string
	mu      sync.RWMutex // guards image
	writeMu sync.Mutex   // one diff + commit at a time (callers also hold agentMessageMu)
	image   *agentMailboxImage
}

// agentMailboxImage is the committed state. It is never edited in place:
// readers share it, and a commit publishes a new one.
type agentMailboxImage struct {
	store       agentMessageStore
	messageOrds map[string][]int64 // per inbox, parallel to its Messages
	convOrds    map[string]int64
	agentOrds   map[string]int64
	nextOrd     int64
}

var agentMailboxes = struct {
	sync.Mutex
	opened *agentMailbox
	byKey  map[string]*agentMailbox
}{byKey: map[string]*agentMailbox{}}

// agentMessageVersion changes after every commit, so a waiting reader
// queries again only when something was written.
var agentMessageVersion atomic.Int64

func agentMailboxPath(docsRoot string) string {
	return filepath.Join(docsRoot, filepath.FromSlash(agentMailboxDir), agentMailboxDBName)
}

// openAgentMailbox opens the server's mailbox at startup. A server that still
// has the old JSON files and no database imports them first (one time).
func openAgentMailbox(docsRoot string) (*agentMailbox, error) {
	dir := filepath.Join(docsRoot, filepath.FromSlash(agentMailboxDir))
	path := agentMailboxPath(docsRoot)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if _, jsonErr := os.Stat(filepath.Join(dir, agentMailboxLegacyFile)); jsonErr == nil {
			report, importErr := importAgentMailboxJSON(dir, false)
			if importErr != nil {
				return nil, fmt.Errorf("mailbox import: %w", importErr)
			}
			log.Printf("[AGENT-MAILBOX-IMPORT] imported=%t conversations=%d messages=%d delivered=%d agents=%d bytes=%d renamed=%d",
				report.Imported, report.Conversations, report.Messages, report.Delivered, report.Agents, report.Bytes, len(report.Renamed))
		}
	}
	mb, err := openAgentMailboxFile(path, true)
	if err != nil {
		return nil, err
	}
	agentMailboxes.Lock()
	agentMailboxes.opened = mb
	agentMailboxes.Unlock()
	return mb, nil
}

func openAgentMailboxFile(path string, wal bool) (*agentMailbox, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	journal := "DELETE"
	if wal {
		journal = "WAL"
	}
	dsn := sqlitefile.URI(path) + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(" + journal + ")&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	mb := &agentMailbox{db: db, path: path}
	if err := mb.init(); err != nil {
		db.Close()
		return nil, err
	}
	// Message bodies live here: the server account only (WAL/SHM follow the mode).
	_ = os.Chmod(path, 0o600)
	return mb, nil
}

// Unit tests have no server start: each test workspace gets its own private
// in-memory mailbox, keyed by the environment that names that workspace.
func currentAgentMailbox() (*agentMailbox, error) {
	agentMailboxes.Lock()
	defer agentMailboxes.Unlock()
	if agentMailboxes.opened != nil {
		return agentMailboxes.opened, nil
	}
	key := getWorkspaceAPIURL() + "\x00" + os.Getenv("AGENTWORKS_STATE_ROOT") + "\x00" + fsutil.WorkspaceDocsRoot()
	if mb := agentMailboxes.byKey[key]; mb != nil {
		return mb, nil
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:agent-mailbox-%d?mode=memory&cache=shared&_pragma=foreign_keys(1)&_txlock=immediate", len(agentMailboxes.byKey)+1))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	mb := &agentMailbox{db: db, path: ":memory:"}
	if err := mb.init(); err != nil {
		db.Close()
		return nil, err
	}
	agentMailboxes.byKey[key] = mb
	return mb, nil
}

func (mb *agentMailbox) init() error {
	if _, err := mb.db.Exec(agentMailboxSchema); err != nil {
		return fmt.Errorf("mailbox schema: %w", err)
	}
	image, err := loadAgentMailboxImage(context.Background(), mb.db)
	if err != nil {
		return fmt.Errorf("mailbox load: %w", err)
	}
	mb.image = image
	return nil
}

func (mb *agentMailbox) current() *agentMailboxImage {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	return mb.image
}

func loadAgentMailboxImage(ctx context.Context, db *sql.DB) (*agentMailboxImage, error) {
	image := &agentMailboxImage{messageOrds: map[string][]int64{}, convOrds: map[string]int64{}, agentOrds: map[string]int64{}, nextOrd: 1}
	s := &image.store
	bump := func(ord int64) {
		if ord >= image.nextOrd {
			image.nextOrd = ord + 1
		}
	}
	var next sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT value FROM mailbox_meta WHERE key='next_agent_sequence'`).Scan(&next); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if next.Valid {
		_, _ = fmt.Sscan(next.String, &s.NextAgentSequence)
	}
	presence := map[string]time.Time{}
	rows, err := db.QueryContext(ctx, `SELECT agent_id, last_online_at FROM agent_presence`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, at string
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			return nil, err
		}
		t, _ := time.Parse(time.RFC3339Nano, at)
		presence[id] = t
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `SELECT agent_id, ord, data FROM agents ORDER BY ord`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, data string
		var ord int64
		if err := rows.Scan(&id, &ord, &data); err != nil {
			rows.Close()
			return nil, err
		}
		var a namedMessageAgent
		if err := json.Unmarshal([]byte(data), &a); err != nil {
			rows.Close()
			return nil, err
		}
		a.LastOnlineAt = presence[id]
		s.Agents = append(s.Agents, a)
		image.agentOrds[id] = ord
		bump(ord)
	}
	rows.Close()
	index := map[string]int{}
	rows, err = db.QueryContext(ctx, `SELECT inbox_id, ord, next_sequence, endpoints FROM conversations ORDER BY ord`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c agentConversation
		var ord int64
		var endpoints string
		if err := rows.Scan(&c.ID, &ord, &c.Next, &endpoints); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(endpoints), &c.Endpoints); err != nil {
			rows.Close()
			return nil, err
		}
		index[c.ID] = len(s.Conversations)
		s.Conversations = append(s.Conversations, c)
		image.convOrds[c.ID] = ord
		bump(ord)
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `SELECT inbox_id, ord, data FROM messages ORDER BY ord`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var inbox, data string
		var ord int64
		if err := rows.Scan(&inbox, &ord, &data); err != nil {
			rows.Close()
			return nil, err
		}
		i, ok := index[inbox]
		if !ok {
			continue
		}
		var m agentMessage
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			rows.Close()
			return nil, err
		}
		s.Conversations[i].Messages = append(s.Conversations[i].Messages, m)
		image.messageOrds[inbox] = append(image.messageOrds[inbox], ord)
		bump(ord)
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `SELECT ord, data FROM expired_conversations ORDER BY ord`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ord int64
		var data string
		var e expiredAgentConversation
		if err := rows.Scan(&ord, &data); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			rows.Close()
			return nil, err
		}
		s.Expired = append(s.Expired, e)
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `SELECT kind, data FROM mailbox_records ORDER BY kind, ord`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind, data string
		if err := rows.Scan(&kind, &data); err != nil {
			rows.Close()
			return nil, err
		}
		if err := s.appendRecord(kind, []byte(data)); err != nil {
			rows.Close()
			return nil, err
		}
	}
	rows.Close()
	return image, rows.Err()
}

// The small bounded lists (shares, blocks, audits, notices) are rows of one
// table, kind + order + the record itself.
func (s *agentMessageStore) appendRecord(kind string, raw []byte) error {
	var err error
	switch kind {
	case "shares":
		var v agentShare
		if err = json.Unmarshal(raw, &v); err == nil {
			s.Shares = append(s.Shares, v)
		}
	case "blocks":
		var v agentShareBlock
		if err = json.Unmarshal(raw, &v); err == nil {
			s.Blocks = append(s.Blocks, v)
		}
	case "share_audit":
		var v agentShareAudit
		if err = json.Unmarshal(raw, &v); err == nil {
			s.ShareAudit = append(s.ShareAudit, v)
		}
	case "admin_audit":
		var v bullpenAdminAudit
		if err = json.Unmarshal(raw, &v); err == nil {
			s.AdminAudit = append(s.AdminAudit, v)
		}
	case "owner_notices":
		var v agentOwnerNotice
		if err = json.Unmarshal(raw, &v); err == nil {
			s.Notices = append(s.Notices, v)
		}
	default:
		err = fmt.Errorf("unknown mailbox record kind %q", kind)
	}
	return err
}

func (s agentMessageStore) records() map[string][]any {
	out := map[string][]any{}
	add := func(kind string, n int, at func(int) any) {
		for i := 0; i < n; i++ {
			out[kind] = append(out[kind], at(i))
		}
	}
	add("shares", len(s.Shares), func(i int) any { return s.Shares[i] })
	add("blocks", len(s.Blocks), func(i int) any { return s.Blocks[i] })
	add("share_audit", len(s.ShareAudit), func(i int) any { return s.ShareAudit[i] })
	add("admin_audit", len(s.AdminAudit), func(i int) any { return s.AdminAudit[i] })
	add("owner_notices", len(s.Notices), func(i int) any { return s.Notices[i] })
	return out
}

func cloneAgentMessages(in []agentMessage) []agentMessage {
	if in == nil {
		return nil
	}
	out := slices.Clone(in)
	for i := range out {
		if out[i].File != nil {
			f := *out[i].File
			out[i].File = &f
		}
	}
	return out
}

func cloneNamedAgent(a namedMessageAgent) namedMessageAgent {
	a.Connections = append([]string(nil), a.Connections...)
	if a.Attachment != nil {
		copy := *a.Attachment
		a.Attachment = &copy
	}
	return a
}

// A private copy a caller may edit before saving.
func cloneAgentMessageStore(s agentMessageStore) agentMessageStore {
	out := s
	if s.Agents != nil {
		out.Agents = make([]namedMessageAgent, len(s.Agents))
		for i, a := range s.Agents {
			out.Agents[i] = cloneNamedAgent(a)
		}
	}
	if s.Conversations != nil {
		out.Conversations = make([]agentConversation, len(s.Conversations))
		for i, c := range s.Conversations {
			c.Messages = cloneAgentMessages(c.Messages)
			out.Conversations[i] = c
		}
	}
	out.Expired = slices.Clone(s.Expired)
	out.Shares = slices.Clone(s.Shares)
	out.Blocks = slices.Clone(s.Blocks)
	out.ShareAudit = slices.Clone(s.ShareAudit)
	out.AdminAudit = slices.Clone(s.AdminAudit)
	out.Notices = slices.Clone(s.Notices)
	return out
}

// readAgentMessageStore returns a private copy for a mutation. Callers that
// save it hold agentMessageMu from this read until the save.
func readAgentMessageStore(ctx context.Context) (agentMessageStore, error) {
	start := time.Now()
	caller := agentMailboxReadCaller()
	mb, err := currentAgentMailbox()
	if err != nil {
		return agentMessageStore{}, err
	}
	s := cloneAgentMessageStore(mb.current().store)
	agentMailboxTiming("read", start, agentMailboxMessageCount(s), 0, caller)
	return s, ctx.Err()
}

// readAgentMessageSnapshot returns the committed state shared by all readers.
// It must be treated as immutable; mutations use readAgentMessageStore.
func readAgentMessageSnapshot(ctx context.Context) (agentMessageStore, error) {
	agentMailboxReadCaller()
	mb, err := currentAgentMailbox()
	if err != nil {
		return agentMessageStore{}, err
	}
	return mb.current().store, ctx.Err()
}

func agentMailboxMessageCount(s agentMessageStore) int {
	n := 0
	for _, c := range s.Conversations {
		n += len(c.Messages)
	}
	return n
}

// Slow-path metadata only: never message bodies, account names or credentials.
func agentMailboxTiming(operation string, start time.Time, rows int, ioTime time.Duration, caller ...string) {
	if elapsed := time.Since(start); elapsed >= 100*time.Millisecond {
		source := "snapshot"
		if len(caller) > 0 {
			source = caller[0]
		}
		log.Printf("[AGENT-MAILBOX-TIMING] operation=%s rows=%d io_ms=%d total_ms=%d caller=%s", operation, rows, ioTime.Milliseconds(), elapsed.Milliseconds(), source)
	}
}

// saveAgentMessageStore commits the rows that differ from the committed
// image in one transaction, then publishes s as the new image and wakes
// waiting readers. Callers hold agentMessageMu since their read.
func saveAgentMessageStore(ctx context.Context, s agentMessageStore) error {
	start := time.Now()
	mb, err := currentAgentMailbox()
	if err != nil {
		return err
	}
	mb.writeMu.Lock()
	defer mb.writeMu.Unlock()
	base := mb.current()
	// BEGIN IMMEDIATE (_txlock): the sequence check below reads the rows the
	// insert depends on, so the write lock is held from the start.
	tx, err := mb.db.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return err
	}
	next, w, err := writeAgentMailboxDiff(tx, base, &s)
	if err != nil {
		tx.Rollback()
		agentMailboxTiming("write", start, w.rows, time.Since(start), "failed")
		return err
	}
	if w.rows == 0 {
		tx.Rollback()
		return nil
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	ioTime := time.Since(start)
	mb.mu.Lock()
	// Keep presence that was recorded outside this transaction meanwhile.
	if mb.image != base {
		last := map[string]time.Time{}
		for _, a := range mb.image.store.Agents {
			last[a.ID] = a.LastOnlineAt
		}
		for i := range next.store.Agents {
			if t := last[next.store.Agents[i].ID]; t.After(next.store.Agents[i].LastOnlineAt) {
				next.store.Agents[i].LastOnlineAt = t
			}
		}
	}
	mb.image = next
	mb.mu.Unlock()
	agentMessageVersion.Add(1)
	notifyAgentLiveChanged()
	agentMailboxTiming("write", start, w.rows, ioTime)
	for _, id := range w.deletedFiles {
		go func(id string) {
			_ = deleteWorkspaceFile(context.Background(), agentMessageFilesDir+id+".json")
			log.Printf("[AGENT_FILE] removed file=%s (its message was removed by retention or cleanup)", id)
		}(id)
	}
	return nil
}

type agentMailboxWrite struct {
	rows         int
	deletedFiles []string
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func endpointColumns(e agentMessageEndpoint) []any {
	return []any{e.UserID, e.Kind, e.ID, e.NamedAgentID, boolInt(e.External), e.Profile, e.Session}
}

func sameAgentMessage(a, b agentMessage) bool {
	if (a.File == nil) != (b.File == nil) || a.File != nil && *a.File != *b.File {
		return false
	}
	a.File, b.File = nil, nil
	return a == b
}

func sameNamedAgentRow(a, b namedMessageAgent) bool {
	if (a.Attachment == nil) != (b.Attachment == nil) || a.Attachment != nil && *a.Attachment != *b.Attachment {
		return false
	}
	return a.ID == b.ID && a.UserID == b.UserID && a.Name == b.Name && a.Label == b.Label && slices.Equal(a.Connections, b.Connections) &&
		a.CreatedAt.Equal(b.CreatedAt) && a.Purpose == b.Purpose && a.PurposeUpdatedAt.Equal(b.PurposeUpdatedAt) && a.EverAttached == b.EverAttached
}

// A receipt only moves forward: once a message is delivered, a later write
// never takes that back or clears its delivery time.
func keepAgentMessageReceipt(old agentMessage, m *agentMessage) {
	if old.Delivery == "delivered" && m.Delivery != "delivered" {
		m.Delivery, m.Error = old.Delivery, old.Error
	}
	if !old.DeliveredAt.IsZero() && !m.DeliveredAt.Equal(old.DeliveredAt) {
		m.DeliveredAt = old.DeliveredAt
	}
}

func messageRowValues(inbox string, m agentMessage) ([]any, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	fileID := ""
	if m.File != nil {
		fileID = m.File.ID
	}
	return []any{inbox, m.ID, m.Sequence, m.AgentSequence, m.From, m.Submission, m.Delivery, boolInt(!m.DeliveredAt.IsZero()), boolInt(!m.InboxRemovedAt.IsZero()), fileID, string(raw)}, nil
}

// writeAgentMailboxDiff writes what s changed relative to base and returns the
// image s becomes once committed. It edits s only to keep receipts forward.
func writeAgentMailboxDiff(tx *sql.Tx, base *agentMailboxImage, s *agentMessageStore) (*agentMailboxImage, agentMailboxWrite, error) {
	var w agentMailboxWrite
	old := base.store
	next := &agentMailboxImage{messageOrds: map[string][]int64{}, convOrds: map[string]int64{}, agentOrds: map[string]int64{}, nextOrd: base.nextOrd}
	exec := func(query string, args ...any) error {
		w.rows++
		_, err := tx.Exec(query, args...)
		return err
	}
	if s.NextAgentSequence != old.NextAgentSequence {
		if err := exec(`INSERT INTO mailbox_meta(key, value) VALUES ('next_agent_sequence', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprint(s.NextAgentSequence)); err != nil {
			return nil, w, err
		}
	}

	// Conversations and their messages. Removed conversations go first so a
	// reused key never collides.
	oldConv := map[string]int{}
	for i, c := range old.Conversations {
		oldConv[c.ID] = i
	}
	keep := map[string]bool{}
	for _, c := range s.Conversations {
		keep[c.ID] = true
	}
	for _, c := range old.Conversations {
		if keep[c.ID] {
			continue
		}
		for _, m := range c.Messages {
			if m.File != nil {
				w.deletedFiles = append(w.deletedFiles, m.File.ID)
			}
		}
		if err := exec(`DELETE FROM messages WHERE inbox_id=?`, c.ID); err != nil {
			return nil, w, err
		}
		if err := exec(`DELETE FROM conversations WHERE inbox_id=?`, c.ID); err != nil {
			return nil, w, err
		}
	}
	lastOrd := int64(0)
	for ci := range s.Conversations {
		c := &s.Conversations[ci]
		if c.ID == "" {
			return nil, w, fmt.Errorf("conversation without an inbox id")
		}
		if _, dup := next.convOrds[c.ID]; dup {
			return nil, w, fmt.Errorf("duplicate conversation %s", c.ID)
		}
		endpoints, err := json.Marshal(c.Endpoints)
		if err != nil {
			return nil, w, err
		}
		oi, existed := oldConv[c.ID]
		ord, hadOrd := base.convOrds[c.ID]
		if !existed || !hadOrd || ord <= lastOrd {
			ord = next.nextOrd
			next.nextOrd++
		}
		lastOrd = ord
		next.convOrds[c.ID] = ord
		cols := append([]any{c.ID, ord, c.Next}, endpointColumns(c.Endpoints[0])...)
		cols = append(cols, endpointColumns(c.Endpoints[1])...)
		cols = append(cols, string(endpoints))
		if !existed || old.Conversations[oi].Next != c.Next || old.Conversations[oi].Endpoints != c.Endpoints || base.convOrds[c.ID] != ord {
			if err := exec(`INSERT INTO conversations(inbox_id, ord, next_sequence, a_user_id, a_kind, a_ref_id, a_named_agent_id, a_external, a_profile, a_session, b_user_id, b_kind, b_ref_id, b_named_agent_id, b_external, b_profile, b_session, endpoints)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(inbox_id) DO UPDATE SET ord=excluded.ord, next_sequence=excluded.next_sequence,
 a_user_id=excluded.a_user_id, a_kind=excluded.a_kind, a_ref_id=excluded.a_ref_id, a_named_agent_id=excluded.a_named_agent_id, a_external=excluded.a_external, a_profile=excluded.a_profile, a_session=excluded.a_session,
 b_user_id=excluded.b_user_id, b_kind=excluded.b_kind, b_ref_id=excluded.b_ref_id, b_named_agent_id=excluded.b_named_agent_id, b_external=excluded.b_external, b_profile=excluded.b_profile, b_session=excluded.b_session,
 endpoints=excluded.endpoints`, cols...); err != nil {
				return nil, w, err
			}
		}
		// Match messages by (sequence, id) in order: edits update their row,
		// trimmed ones are deleted, new ones are appended.
		var oldMessages []agentMessage
		var oldOrds []int64
		if existed {
			oldMessages, oldOrds = old.Conversations[oi].Messages, base.messageOrds[c.ID]
		}
		type messageKey struct {
			seq int
			id  string
		}
		pool := map[messageKey][]int{}
		for i, m := range oldMessages {
			k := messageKey{m.Sequence, m.ID}
			pool[k] = append(pool[k], i)
		}
		matched := make([]int, len(c.Messages))
		used := make([]bool, len(oldMessages))
		for i, m := range c.Messages {
			k := messageKey{m.Sequence, m.ID}
			matched[i] = -1
			if list := pool[k]; len(list) > 0 {
				matched[i], pool[k] = list[0], list[1:]
				used[matched[i]] = true
			}
		}
		for i, m := range oldMessages {
			if used[i] {
				continue
			}
			if m.File != nil {
				w.deletedFiles = append(w.deletedFiles, m.File.ID)
			}
			if err := exec(`DELETE FROM messages WHERE ord=?`, oldOrds[i]); err != nil {
				return nil, w, err
			}
		}
		ords := make([]int64, len(c.Messages))
		lastMessageOrd := int64(0)
		for i := range c.Messages {
			m := &c.Messages[i]
			if j := matched[i]; j >= 0 && j < len(oldOrds) && oldOrds[j] > lastMessageOrd {
				keepAgentMessageReceipt(oldMessages[j], m)
				ords[i], lastMessageOrd = oldOrds[j], oldOrds[j]
				if sameAgentMessage(oldMessages[j], *m) {
					continue
				}
				values, err := messageRowValues(c.ID, *m)
				if err != nil {
					return nil, w, err
				}
				w.rows++
				res, err := tx.Exec(`UPDATE messages SET inbox_id=?, message_id=?, sequence=?, agent_sequence=?, from_side=?, submission_id=?, delivery=?, delivered=?, inbox_removed=?, file_id=?, data=?
WHERE ord=? AND NOT (delivery='delivered' AND ?<>'delivered')`, append(values, ords[i], m.Delivery)...)
				if err != nil {
					return nil, w, err
				}
				if n, _ := res.RowsAffected(); n != 1 {
					return nil, w, fmt.Errorf("message %s receipt would move backwards", m.ID)
				}
				continue
			}
			if j := matched[i]; j >= 0 {
				// Out of order relative to its old row: rewrite it at the end.
				if err := exec(`DELETE FROM messages WHERE ord=?`, oldOrds[j]); err != nil {
					return nil, w, err
				}
			}
			ord := next.nextOrd
			next.nextOrd++
			ords[i], lastMessageOrd = ord, ord
			values, err := messageRowValues(c.ID, *m)
			if err != nil {
				return nil, w, err
			}
			// The per-conversation order is checked inside the transaction:
			// a new message's sequence must follow every stored one.
			w.rows++
			res, err := tx.Exec(`INSERT INTO messages(ord, inbox_id, message_id, sequence, agent_sequence, from_side, submission_id, delivery, delivered, inbox_removed, file_id, data)
SELECT ?,?,?,?,?,?,?,?,?,?,?,? WHERE ? = 0 OR ? > (SELECT COALESCE(MAX(sequence), 0) FROM messages WHERE inbox_id = ?)`,
				append(append([]any{ord}, values...), m.Sequence, m.Sequence, c.ID)...)
			if err != nil {
				return nil, w, err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return nil, w, fmt.Errorf("message sequence %d is not after the stored messages of %s", m.Sequence, c.ID)
			}
		}
		next.messageOrds[c.ID] = ords
	}

	// Named agents; presence is its own row and only moves forward.
	oldAgents := map[string]namedMessageAgent{}
	for _, a := range old.Agents {
		oldAgents[a.ID] = a
	}
	keepAgent := map[string]bool{}
	for _, a := range s.Agents {
		keepAgent[a.ID] = true
	}
	for _, a := range old.Agents {
		if !keepAgent[a.ID] {
			if err := exec(`DELETE FROM agents WHERE agent_id=?`, a.ID); err != nil {
				return nil, w, err
			}
			if err := exec(`DELETE FROM agent_presence WHERE agent_id=?`, a.ID); err != nil {
				return nil, w, err
			}
		}
	}
	lastOrd = 0
	for i := range s.Agents {
		a := s.Agents[i]
		if _, dup := next.agentOrds[a.ID]; dup {
			return nil, w, fmt.Errorf("duplicate agent %s", a.ID)
		}
		prior, existed := oldAgents[a.ID]
		ord, hadOrd := base.agentOrds[a.ID]
		if !existed || !hadOrd || ord <= lastOrd {
			ord = next.nextOrd
			next.nextOrd++
		}
		lastOrd = ord
		next.agentOrds[a.ID] = ord
		if !existed || !sameNamedAgentRow(prior, a) || base.agentOrds[a.ID] != ord {
			row := a
			row.LastOnlineAt = time.Time{}
			raw, err := json.Marshal(row)
			if err != nil {
				return nil, w, err
			}
			attachment := ""
			if a.Attachment != nil {
				attachment = a.Attachment.ID
			}
			if err := exec(`INSERT INTO agents(agent_id, ord, user_id, name, attachment_id, data) VALUES (?,?,?,?,?,?)
ON CONFLICT(agent_id) DO UPDATE SET ord=excluded.ord, user_id=excluded.user_id, name=excluded.name, attachment_id=excluded.attachment_id, data=excluded.data`,
				a.ID, ord, a.UserID, a.Name, attachment, string(raw)); err != nil {
				return nil, w, err
			}
		}
		if !a.LastOnlineAt.IsZero() && a.LastOnlineAt.After(prior.LastOnlineAt) {
			if err := upsertAgentPresence(tx, a.ID, a.LastOnlineAt, &w); err != nil {
				return nil, w, err
			}
		}
	}

	// Expired-conversation metadata (no text) and the small record lists are
	// rewritten only when they changed.
	if !slices.EqualFunc(old.Expired, s.Expired, func(a, b expiredAgentConversation) bool {
		return a.ID == b.ID && a.Endpoints == b.Endpoints && a.ExpiredAt.Equal(b.ExpiredAt)
	}) {
		if err := exec(`DELETE FROM expired_conversations`); err != nil {
			return nil, w, err
		}
		for i, e := range s.Expired {
			raw, err := json.Marshal(e)
			if err != nil {
				return nil, w, err
			}
			if err := exec(`INSERT INTO expired_conversations(ord, inbox_id, data) VALUES (?,?,?)`, i+1, e.ID, string(raw)); err != nil {
				return nil, w, err
			}
		}
	}
	oldRecords, newRecords := old.records(), s.records()
	for _, kind := range []string{"shares", "blocks", "share_audit", "admin_audit", "owner_notices"} {
		if recordsEqual(oldRecords[kind], newRecords[kind]) {
			continue
		}
		if err := exec(`DELETE FROM mailbox_records WHERE kind=?`, kind); err != nil {
			return nil, w, err
		}
		for i, r := range newRecords[kind] {
			raw, err := json.Marshal(r)
			if err != nil {
				return nil, w, err
			}
			if err := exec(`INSERT INTO mailbox_records(kind, ord, data) VALUES (?,?,?)`, kind, i+1, string(raw)); err != nil {
				return nil, w, err
			}
		}
	}
	next.store = cloneAgentMessageStore(*s)
	return next, w, nil
}

func recordsEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, _ := json.Marshal(a[i])
		y, _ := json.Marshal(b[i])
		if string(x) != string(y) {
			return false
		}
	}
	return true
}

func upsertAgentPresence(tx interface {
	Exec(string, ...any) (sql.Result, error)
}, agentID string, at time.Time, w *agentMailboxWrite) error {
	if w != nil {
		w.rows++
	}
	_, err := tx.Exec(`INSERT INTO agent_presence(agent_id, last_online_ns, last_online_at) VALUES (?,?,?)
ON CONFLICT(agent_id) DO UPDATE SET last_online_ns=excluded.last_online_ns, last_online_at=excluded.last_online_at
WHERE excluded.last_online_ns > agent_presence.last_online_ns`, agentID, at.UnixNano(), at.UTC().Format(time.RFC3339Nano))
	return err
}

// recordAgentPresence is one row write. It never takes agentMessageMu, so it
// never waits for message work; SQLite orders it with any running commit.
func recordAgentPresence(ctx context.Context, agentID string, at time.Time) error {
	mb, err := currentAgentMailbox()
	if err != nil {
		return err
	}
	start := time.Now()
	if err := upsertAgentPresence(mb.db, agentID, at, nil); err != nil {
		return err
	}
	mb.mu.Lock()
	image := *mb.image
	image.store.Agents = slices.Clone(image.store.Agents)
	for i := range image.store.Agents {
		if image.store.Agents[i].ID == agentID && at.After(image.store.Agents[i].LastOnlineAt) {
			image.store.Agents[i].LastOnlineAt = at
		}
	}
	mb.image = &image
	mb.mu.Unlock()
	agentMessageVersion.Add(1)
	notifyAgentLiveChanged()
	agentMailboxTiming("presence", start, 1, time.Since(start))
	return ctx.Err()
}

// agentMailboxEndpointSQL is messageEndpointMatches as a WHERE clause for one
// side ("a" or "b"), so a query only ever returns the caller's conversations.
func agentMailboxEndpointSQL(side, userID string, c triggerLinkCaller) (string, []any) {
	session := "\x00" // matches no stored session
	if c.Chat != nil {
		session = c.Chat.SessionID
	}
	clause := fmt.Sprintf("(%[1]s_named_agent_id = ? AND %[1]s_user_id = ? AND %[1]s_kind = ? AND %[1]s_ref_id = ? AND (%[1]s_external = 1 OR (%[1]s_profile = ? AND %[1]s_session <> '' AND %[1]s_session = ?)))", side)
	return clause, []any{c.NamedAgentID, userID, c.Stamp.Type, c.Stamp.ID, normalizeInternalProfileID(c.Stamp.ProfileID), session}
}

// queryAgentMailboxConversations loads the conversations matching where, with
// their messages, straight from the database.
func queryAgentMailboxConversations(ctx context.Context, where string, args ...any) ([]agentConversation, error) {
	start := time.Now()
	mb, err := currentAgentMailbox()
	if err != nil {
		return nil, err
	}
	rows, err := mb.db.QueryContext(ctx, `SELECT inbox_id, next_sequence, endpoints FROM conversations WHERE `+where+` ORDER BY ord`, args...)
	if err != nil {
		return nil, err
	}
	var out []agentConversation
	for rows.Next() {
		var c agentConversation
		var endpoints string
		if err := rows.Scan(&c.ID, &c.Next, &endpoints); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(endpoints), &c.Endpoints); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	count := 0
	for i := range out {
		messages, err := mb.db.QueryContext(ctx, `SELECT data FROM messages WHERE inbox_id = ? ORDER BY ord`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for messages.Next() {
			var data string
			var m agentMessage
			if err := messages.Scan(&data); err != nil {
				messages.Close()
				return nil, err
			}
			if err := json.Unmarshal([]byte(data), &m); err != nil {
				messages.Close()
				return nil, err
			}
			out[i].Messages = append(out[i].Messages, m)
			count++
		}
		messages.Close()
	}
	agentMailboxTiming("query", start, count, time.Since(start))
	return out, nil
}

// callerAgentMailboxConversation returns one inbox only when the caller is one
// of its two endpoints, with the side the caller is on.
func callerAgentMailboxConversation(ctx context.Context, inboxID, userID string, caller triggerLinkCaller) (agentConversation, int, bool, error) {
	a, aArgs := agentMailboxEndpointSQL("a", userID, caller)
	b, bArgs := agentMailboxEndpointSQL("b", userID, caller)
	args := append(append([]any{inboxID}, aArgs...), bArgs...)
	found, err := queryAgentMailboxConversations(ctx, "inbox_id = ? AND ("+a+" OR "+b+")", args...)
	if err != nil || len(found) == 0 {
		return agentConversation{}, -1, false, err
	}
	c := found[0]
	side := 1
	if messageEndpointMatches(c.Endpoints[0], userID, caller) {
		side = 0
	}
	return c, side, true, nil
}

// namedAgentMailboxConversations returns the conversations where this named
// agent of this account is one of the endpoints.
func namedAgentMailboxConversations(ctx context.Context, userID, agentID string) ([]agentConversation, error) {
	if agentID == "" {
		return nil, nil
	}
	return queryAgentMailboxConversations(ctx, "(a_named_agent_id = ? AND a_user_id = ?) OR (b_named_agent_id = ? AND b_user_id = ?)", agentID, userID, agentID, userID)
}

// backupAgentMailbox writes a consistent copy of a live (WAL) mailbox.
func backupAgentMailbox(ctx context.Context, sourcePath, destination string) error {
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("%s already exists", destination)
	}
	db, err := sql.Open("sqlite", sqlitefile.URI(sourcePath)+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		return err
	}
	return os.Chmod(destination, 0o600)
}

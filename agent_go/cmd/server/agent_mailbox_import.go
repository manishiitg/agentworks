package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/spf13/cobra"
)

// One-off move of the old JSON mailbox (conversations.json, and since the
// 30 Oct split conversations.history.json) into mailbox.sqlite. It runs at
// server start when the database is absent, or as `server mailbox-import`.
// The JSON files are renamed, never deleted, and only after the database has
// been read back and matched message by message. Delete this file once every
// server has imported (PLAT-856-522).
const (
	agentMailboxLegacyFile    = "conversations.json"
	agentMailboxLegacyHistory = "conversations.history.json"
)

type agentMailboxImportReport struct {
	Source        string         `json:"source"`
	DryRun        bool           `json:"dry_run"`
	Imported      bool           `json:"imported"`
	Reason        string         `json:"reason,omitempty"`
	Bytes         int64          `json:"bytes"`
	Conversations int            `json:"conversations"`
	Messages      int            `json:"messages"`
	Delivered     int            `json:"delivered"`
	Receipts      map[string]int `json:"receipts_by_state"`
	Agents        int            `json:"agents"`
	Expired       int            `json:"expired_conversations"`
	Records       map[string]int `json:"records"`
	Renamed       []string       `json:"renamed,omitempty"`
}

// readLegacyAgentMailbox assembles the JSON store exactly as the workspace
// service served it: the split metadata file plus its history file, or one
// unsplit file. Unknown fields stop the import instead of being dropped.
func readLegacyAgentMailbox(dir string) (agentMessageStore, int64, string, error) {
	var s agentMessageStore
	meta, err := os.ReadFile(filepath.Join(dir, agentMailboxLegacyFile))
	if err != nil {
		return s, 0, "", err
	}
	size := int64(len(meta))
	digest := sha256.New()
	digest.Write(meta)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(meta, &fields); err != nil {
		return s, 0, "", err
	}
	var format string
	_ = json.Unmarshal(fields["mailbox_storage"], &format)
	delete(fields, "mailbox_storage")
	switch format {
	case "":
	case "history-v1":
		history, err := os.ReadFile(filepath.Join(dir, agentMailboxLegacyHistory))
		if err != nil {
			return s, 0, "", err
		}
		size += int64(len(history))
		digest.Write(history)
		fields["conversations"] = history
	default:
		return s, 0, "", fmt.Errorf("unknown mailbox storage %q", format)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return s, 0, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		return s, 0, "", err
	}
	// Durable messages are the lower bound of the agent cursor (the history
	// part could commit before its metadata); never reuse a position.
	for _, c := range s.Conversations {
		for _, m := range c.Messages {
			s.NextAgentSequence = max(s.NextAgentSequence, m.AgentSequence)
		}
	}
	return s, size, hex.EncodeToString(digest.Sum(nil)), nil
}

// validateLegacyAgentMailbox refuses data the database would reorder or
// merge: every inbox keeps its exact sequence numbers and cursors.
func validateLegacyAgentMailbox(s agentMessageStore) error {
	inboxes := map[string]bool{}
	for _, c := range s.Conversations {
		if c.ID == "" || inboxes[c.ID] {
			return fmt.Errorf("conversation id missing or repeated (%q)", c.ID)
		}
		inboxes[c.ID] = true
		ids, submissions := map[string]bool{}, map[string]bool{}
		last := 0
		for _, m := range c.Messages {
			if m.Sequence > 0 {
				if m.Sequence <= last {
					return fmt.Errorf("inbox %s: sequence %d does not follow %d", c.ID, m.Sequence, last)
				}
				last = m.Sequence
			}
			if m.ID != "" {
				if ids[m.ID] {
					return fmt.Errorf("inbox %s: message %s repeated", c.ID, m.ID)
				}
				ids[m.ID] = true
			}
			if m.Submission != "" {
				key := fmt.Sprint(m.From, "\x00", m.Submission)
				if submissions[key] {
					return fmt.Errorf("inbox %s: submission repeated on one side", c.ID)
				}
				submissions[key] = true
			}
		}
		if c.Next < last {
			return fmt.Errorf("inbox %s: next sequence %d is behind stored sequence %d", c.ID, c.Next, last)
		}
	}
	agents := map[string]bool{}
	for _, a := range s.Agents {
		if agents[a.ID] {
			return fmt.Errorf("agent %s repeated", a.ID)
		}
		agents[a.ID] = true
	}
	return nil
}

func agentMailboxReportCounts(r *agentMailboxImportReport, s agentMessageStore) {
	r.Conversations, r.Agents, r.Expired = len(s.Conversations), len(s.Agents), len(s.Expired)
	r.Receipts, r.Records = map[string]int{}, map[string]int{}
	for _, c := range s.Conversations {
		for _, m := range c.Messages {
			r.Messages++
			r.Receipts[m.Delivery]++
			if !m.DeliveredAt.IsZero() {
				r.Delivered++
			}
		}
	}
	for kind, list := range s.records() {
		r.Records[kind] = len(list)
	}
}

// sameImportedAgentMailbox compares the read-back database with the JSON,
// conversation by conversation and message by message.
func sameImportedAgentMailbox(want, got agentMessageStore) error {
	same := func(a, b any) bool {
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		return bytes.Equal(x, y)
	}
	if want.NextAgentSequence != got.NextAgentSequence {
		return fmt.Errorf("agent cursor %d read back as %d", want.NextAgentSequence, got.NextAgentSequence)
	}
	if len(want.Conversations) != len(got.Conversations) {
		return fmt.Errorf("%d conversations read back as %d", len(want.Conversations), len(got.Conversations))
	}
	for i, c := range want.Conversations {
		g := got.Conversations[i]
		if c.ID != g.ID || c.Next != g.Next || !same(c.Endpoints, g.Endpoints) || len(c.Messages) != len(g.Messages) {
			return fmt.Errorf("inbox %s differs after import (messages %d vs %d, next %d vs %d)", c.ID, len(c.Messages), len(g.Messages), c.Next, g.Next)
		}
		for j := range c.Messages {
			if !same(c.Messages[j], g.Messages[j]) {
				return fmt.Errorf("inbox %s message %d differs after import", c.ID, j)
			}
		}
	}
	if len(want.Agents) != len(got.Agents) {
		return fmt.Errorf("%d agents read back as %d", len(want.Agents), len(got.Agents))
	}
	for i := range want.Agents {
		if !same(want.Agents[i], got.Agents[i]) {
			return fmt.Errorf("agent %s differs after import", want.Agents[i].ID)
		}
	}
	if len(want.Expired) != len(got.Expired) || !same(want.Expired, got.Expired) && len(want.Expired) > 0 {
		return fmt.Errorf("expired conversation metadata differs after import")
	}
	wr, gr := want.records(), got.records()
	for _, kind := range []string{"shares", "blocks", "share_audit", "admin_audit", "owner_notices"} {
		if !recordsEqual(wr[kind], gr[kind]) {
			return fmt.Errorf("%s differ after import", kind)
		}
	}
	return nil
}

// importAgentMailboxJSON imports dir's JSON mailbox into dir/mailbox.sqlite in
// one transaction. A rerun after a completed import (same JSON digest) only
// finishes renaming; a different existing database is never overwritten.
func importAgentMailboxJSON(dir string, dryRun bool) (agentMailboxImportReport, error) {
	report := agentMailboxImportReport{Source: dir, DryRun: dryRun}
	dbPath := filepath.Join(dir, agentMailboxDBName)
	_, dbErr := os.Stat(dbPath)
	if _, err := os.Stat(filepath.Join(dir, agentMailboxLegacyFile)); errors.Is(err, os.ErrNotExist) {
		report.Reason = "no JSON mailbox"
		if dbErr == nil {
			report.Reason = "already imported"
		}
		return report, nil
	}
	s, size, digest, err := readLegacyAgentMailbox(dir)
	if err != nil {
		return report, err
	}
	report.Bytes = size
	agentMailboxReportCounts(&report, s)
	if err := validateLegacyAgentMailbox(s); err != nil {
		return report, err
	}
	if dbErr == nil {
		mb, err := openAgentMailboxFile(dbPath, true)
		if err != nil {
			return report, err
		}
		var stored string
		_ = mb.db.QueryRow(`SELECT value FROM mailbox_meta WHERE key='imported_digest'`).Scan(&stored)
		mb.db.Close()
		if stored != digest && dryRun {
			// The server ignores leftover JSON once the database exists.
			report.Reason = "already imported (the database exists; leftover JSON files are not used)"
			return report, nil
		}
		if stored != digest {
			return report, fmt.Errorf("%s already exists and was not imported from these JSON files; leaving both untouched", agentMailboxDBName)
		}
		report.Reason = "already imported"
		if !dryRun {
			report.Renamed, err = renameImportedAgentMailbox(dir)
		}
		return report, err
	}
	if dryRun {
		report.Reason = "dry run: nothing written"
		return report, nil
	}

	tmp := dbPath + ".importing"
	for _, p := range []string{tmp, tmp + "-journal"} {
		_ = os.Remove(p)
	}
	mb, err := openAgentMailboxFile(tmp, false)
	if err != nil {
		return report, err
	}
	fail := func(err error) (agentMailboxImportReport, error) {
		mb.db.Close()
		_ = os.Remove(tmp)
		return report, err
	}
	tx, err := mb.db.Begin()
	if err != nil {
		return fail(err)
	}
	if _, _, err = writeAgentMailboxDiff(tx, mb.image, &s); err != nil {
		tx.Rollback()
		return fail(err)
	}
	if _, err = tx.Exec(`INSERT INTO mailbox_meta(key, value) VALUES ('imported_digest', ?), ('imported_at', ?)`, digest, time.Now().UTC().Format(time.RFC3339)); err != nil {
		tx.Rollback()
		return fail(err)
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	got, err := loadAgentMailboxImage(context.Background(), mb.db)
	if err != nil {
		return fail(err)
	}
	if err := sameImportedAgentMailbox(s, got.store); err != nil {
		return fail(fmt.Errorf("verification failed, nothing imported: %w", err))
	}
	if err := mb.db.Close(); err != nil {
		return fail(err)
	}
	if err := os.Rename(tmp, dbPath); err != nil {
		_ = os.Remove(tmp)
		return report, err
	}
	report.Imported = true
	report.Renamed, err = renameImportedAgentMailbox(dir)
	return report, err
}

func renameImportedAgentMailbox(dir string) ([]string, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	var renamed []string
	for _, name := range []string{agentMailboxLegacyFile, agentMailboxLegacyHistory} {
		from := filepath.Join(dir, name)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		to := from + ".imported-" + stamp
		if err := os.Rename(from, to); err != nil {
			return renamed, err
		}
		renamed = append(renamed, filepath.Base(to))
	}
	return renamed, nil
}

var agentMailboxImportCmd = &cobra.Command{
	Use:   "mailbox-import",
	Short: "Import the old JSON AgentBridge mailbox into mailbox.sqlite (runs at start too)",
	Long: `Reads _system/agent_messages/conversations.json (and conversations.history.json)
into _system/agent_messages/mailbox.sqlite in one transaction, reads it back and
compares every message, then renames the JSON files to *.imported-<time>.
Run with the agent stopped. --dry-run only reads and reports.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		docs, _ := cmd.Flags().GetString("docs-dir")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		report, err := importAgentMailboxJSON(filepath.Join(docs, filepath.FromSlash(agentMailboxDir)), dryRun)
		out, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(out))
		return err
	},
}

var agentMailboxBackupCmd = &cobra.Command{
	Use:   "mailbox-backup",
	Short: "Write a consistent copy of the live mailbox.sqlite (VACUUM INTO)",
	RunE: func(cmd *cobra.Command, args []string) error {
		docs, _ := cmd.Flags().GetString("docs-dir")
		out, _ := cmd.Flags().GetString("out")
		if out == "" {
			return fmt.Errorf("--out is required")
		}
		return backupAgentMailbox(cmd.Context(), agentMailboxPath(docs), out)
	},
}

func init() {
	for _, c := range []*cobra.Command{agentMailboxImportCmd, agentMailboxBackupCmd} {
		c.Flags().String("docs-dir", fsutil.WorkspaceDocsRoot(), "Workspace docs root (defaults to WORKSPACE_DOCS_PATH)")
		ServerCmd.AddCommand(c)
	}
	agentMailboxImportCmd.Flags().Bool("dry-run", false, "Only read and report")
	agentMailboxBackupCmd.Flags().String("out", "", "Destination file (must not exist)")
}

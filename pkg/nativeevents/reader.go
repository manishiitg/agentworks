// Package nativeevents reads committed records for an explicitly selected
// provider runtime. It never discovers other people's sessions or sends input.
package nativeevents

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const DecoderVersion = "1"
const MaxBatchBytes = 8 << 20
const MaxRecordBytes = 4 << 20

type ShellCommand struct {
	Label string `json:"label,omitempty"`
	Mode  string `json:"mode,omitempty"`
}

type HelperTool struct {
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Mode        string `json:"mode,omitempty"`
}

type Cursor struct {
	PlanOffset int64 `json:"plan_offset,omitempty"`
	// Checklist content lives only in memory and in the admitted chat events.
	PlanTurn   string                  `json:"-"`
	Plan       llmtypes.ClaudeTaskPlan `json:"-"`
	PlanInputs map[string]string       `json:"-"`
	// planRestored: this process already rebuilt the checklist from the
	// record (once after a restart); a chat with no checklist is not re-read
	// from its start on every poll.
	planRestored  bool
	HelperTools   map[string]HelperTool   `json:"helper_tools,omitempty"`
	ShellCommands map[string]ShellCommand `json:"shell_commands,omitempty"`
	Offset        int64                   `json:"offset"`
	TurnID        string                  `json:"turn_id,omitempty"`
	// EndedTurn is the last turn that ended with no turn started since: the
	// parent is idle (Codex helper finishes, #3132).
	EndedTurn   string             `json:"ended_turn,omitempty"`
	Origin      string             `json:"origin,omitempty"`
	TaskID      string             `json:"task_id,omitempty"`
	LastID      string             `json:"last_id,omitempty"`
	PrefixHash  string             `json:"prefix_hash,omitempty"`
	PrefixBytes int                `json:"prefix_bytes,omitempty"`
	Tools       map[string]string  `json:"tools,omitempty"`
	Tasks       map[string]string  `json:"tasks,omitempty"`
	Rejected    bool               `json:"rejected,omitempty"`
	FileID      string             `json:"file_id,omitempty"`
	TailOffset  int64              `json:"tail_offset,omitempty"`
	TailHash    string             `json:"tail_hash,omitempty"`
	Lineage     map[string]Lineage `json:"lineage,omitempty"`
}

type Lineage struct {
	TurnID string `json:"turn_id,omitempty"`
	Origin string `json:"origin,omitempty"`
	TaskID string `json:"task_id,omitempty"`
}

type Batch struct {
	Records  []llmtypes.NativeRecord `json:"records"`
	Next     Cursor                  `json:"cursor"`
	Unknown  map[string]int          `json:"unknown_kinds,omitempty"`
	Invalid  int                     `json:"invalid_records,omitempty"`
	Gaps     int                     `json:"identity_gaps,omitempty"`
	CaughtUp bool                    `json:"caught_up"`
}

// Capabilities describes this decoder, not an installed CLI or usable account.
// Providers without a committed native end signal never claim turn completion.
func Capabilities(provider string) []string {
	switch provider {
	case "claude-code":
		return []string{"reply", "turn", "helper", "background_command", "question", "tool", "compaction", "usage", "file", "plan"}
	case "codex-cli":
		return []string{"reply", "turn", "question", "tool", "compaction", "usage", "file", "error"}
	case "muse-cli":
		return []string{"reply", "turn", "helper", "question", "tool", "compaction", "usage", "retry"}
	case "pi-cli":
		return []string{"reply", "tool", "compaction", "usage"}
	case "agy-cli":
		return []string{"reply", "tool", "helper", "error", "notice"}
	case "cursor-cli":
		return []string{"reply", "tool", "helper"}
	}
	return nil
}

// Read consumes newline-terminated records only. The caller persists Next only
// after accepting every emitted event. Cursor state contains identities and redacted shell labels, never
// prompts, results, reasoning, account limits or secret values.
func Read(ctx context.Context, provider, sessionID, path string, cursor Cursor) (Batch, error) {
	// Next shares the caller's maps until there is something to decode: an
	// idle chat is polled often, and copying every tool and task identity on
	// each poll was most of a busy laptop's allocations.
	b := Batch{Next: cursor, Unknown: map[string]int{}}
	if sessionID == "" || path == "" || len(Capabilities(provider)) == 0 {
		return b, fmt.Errorf("provider, exact native session and path are required")
	}
	if strings.HasSuffix(path, ".db") {
		return readDatabase(ctx, provider, sessionID, path, cursor)
	}
	f, err := os.Open(path)
	if err != nil {
		return b, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return b, err
	}
	if info.Size() < cursor.Offset {
		return b, fmt.Errorf("native record source was truncated; explicit rebind required")
	}
	// Unix device/inode detects rotation even when a replacement shares the
	// same prefix. Other platforms retain the prefix and committed-tail checks.
	if v := reflect.Indirect(reflect.ValueOf(info.Sys())); v.IsValid() && v.Kind() == reflect.Struct {
		ino, dev := v.FieldByName("Ino"), v.FieldByName("Dev")
		if ino.IsValid() && dev.IsValid() {
			b.Next.FileID = fmt.Sprintf("%v:%v", dev, ino)
			if cursor.FileID != "" && cursor.FileID != b.Next.FileID {
				return b, fmt.Errorf("native record inode changed; explicit rebind required")
			}
		}
	}
	if cursor.TailHash != "" && cursor.Offset > cursor.TailOffset {
		tail := make([]byte, cursor.Offset-cursor.TailOffset)
		if _, err := f.ReadAt(tail, cursor.TailOffset); err != nil {
			return b, err
		}
		if fmt.Sprintf("%x", sha256.Sum256(tail)) != cursor.TailHash {
			return b, fmt.Errorf("native committed boundary changed; explicit rebind required")
		}
	}
	if _, err := f.Seek(cursor.Offset, io.SeekStart); err != nil {
		return b, err
	}
	if info.Size() > 0 {
		n := cursor.PrefixBytes
		if n == 0 {
			n = int(min(info.Size(), 256))
		}
		prefix := make([]byte, n)
		if _, err := f.ReadAt(prefix, 0); err != nil {
			return b, err
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(prefix))
		if cursor.PrefixHash != "" && cursor.PrefixHash != hash {
			return b, fmt.Errorf("native record source changed; explicit rebind required")
		}
		b.Next.PrefixHash, b.Next.PrefixBytes = hash, n
	}
	restorePlan := provider == "claude-code" && cursor.Offset > 0 && b.Next.PlanTurn == "" && !cursor.planRestored
	if info.Size() == cursor.Offset && !restorePlan {
		b.CaughtUp = true
		return b, nil
	}
	next := cloneCursor(cursor)
	next.FileID, next.PrefixHash, next.PrefixBytes = b.Next.FileID, b.Next.PrefixHash, b.Next.PrefixBytes
	b.Next = next
	if restorePlan {
		if err := restoreClaudePlan(ctx, sessionID, f, &b); err != nil {
			return b, err
		}
	}
	r := bufio.NewReaderSize(io.LimitReader(f, MaxBatchBytes), 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return b, err
		}
		line, err := r.ReadBytes('\n')
		if len(line) > MaxRecordBytes {
			return b, fmt.Errorf("native record exceeds supported size")
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				b.CaughtUp = b.Next.Offset+int64(len(line)) >= info.Size()
				break
			}
			return b, err
		}
		if len(line) > MaxRecordBytes {
			return b, fmt.Errorf("native record exceeds supported size")
		}
		b.Next.TailOffset = b.Next.Offset
		b.Next.Offset += int64(len(line))
		b.Next.TailHash = fmt.Sprintf("%x", sha256.Sum256(line))
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			b.Invalid++
			continue
		}
		decode(&b, provider, sessionID, row)
	}
	return b, nil
}

func cloneCursor(c Cursor) Cursor {
	n := c
	n.Plan.Replace(c.Plan.Snapshot())
	n.PlanInputs = map[string]string{}
	for k, v := range c.PlanInputs {
		n.PlanInputs[k] = v
	}
	n.HelperTools = map[string]HelperTool{}
	for k, v := range c.HelperTools {
		n.HelperTools[k] = v
	}
	n.ShellCommands = map[string]ShellCommand{}
	for k, v := range c.ShellCommands {
		n.ShellCommands[k] = v
	}
	n.Tools, n.Tasks, n.Lineage = map[string]string{}, map[string]string{}, map[string]Lineage{}
	for k, v := range c.Tools {
		n.Tools[k] = v
	}
	for k, v := range c.Tasks {
		n.Tasks[k] = v
	}
	for k, v := range c.Lineage {
		n.Lineage[k] = v
	}
	return n
}

func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func text(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
func number(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if n, ok := m[k].(float64); ok {
			return int64(n)
		}
	}
	return 0
}
func at(m map[string]any) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, text(m, "timestamp", "created_at", "completed_at"))
	return t
}
func contentText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var parts []string
	if xs, ok := v.([]any); ok {
		for _, x := range xs {
			m := object(x)
			if t := text(m, "type"); t == "text" || t == "output_text" {
				if s := text(m, "text"); s != "" {
					parts = append(parts, s)
				}
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func emit(b *Batch, r llmtypes.NativeRecord) {
	if r.ID == "" {
		b.Gaps++
		return
	}
	if r.TurnID == "" && r.Provider != "claude-code" {
		r.TurnID = b.Next.TurnID
	}
	if r.Origin == "" && r.Provider != "claude-code" {
		r.Origin = b.Next.Origin
	}
	if r.TaskID == "" && r.Origin == "helper_finished" {
		r.TaskID = b.Next.TaskID
	}
	b.Records = append(b.Records, r)
}

func decode(b *Batch, provider, session string, row map[string]any) {
	if b.Next.Rejected {
		b.Gaps++
		return
	}
	switch provider {
	case "claude-code":
		decodeClaude(b, session, row)
	case "codex-cli":
		decodeCodex(b, session, row)
	case "pi-cli":
		decodePi(b, session, row)
	case "muse-cli":
		decodeMuse(b, session, row)
	}
}

// A checkpoint deliberately omits task text and inputs. Rebuild just this
// turn's ephemeral reducer from the already-admitted, identity-checked journal
// before consuming new rows. The persisted start offset contains no payload.
func restoreClaudePlan(ctx context.Context, sessionID string, f *os.File, b *Batch) error {
	start, end := b.Next.PlanOffset, b.Next.Offset
	if start < 0 || start > end {
		return fmt.Errorf("invalid native plan boundary")
	}
	reader := bufio.NewReaderSize(io.NewSectionReader(f, start, end-start), 64<<10)
	replay := Batch{Next: cloneCursor(Cursor{}), Unknown: map[string]int{}}
	for offset := start; offset < end; {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("native plan replay boundary: %w", err)
		}
		if len(line) > MaxRecordBytes {
			return fmt.Errorf("native plan record exceeds supported size")
		}
		replay.Next.TailOffset = offset
		offset += int64(len(line))
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		decodeClaude(&replay, sessionID, row)
		replay.Records = nil
	}
	b.Next.PlanTurn, b.Next.Plan, b.Next.PlanInputs = replay.Next.PlanTurn, replay.Next.Plan, replay.Next.PlanInputs
	b.Next.planRestored = true
	return nil
}

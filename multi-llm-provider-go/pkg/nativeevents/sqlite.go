package nativeevents

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	_ "modernc.org/sqlite"
)

func readDatabase(ctx context.Context, provider, session, path string, c Cursor) (Batch, error) {
	b := Batch{Next: cloneCursor(c), Unknown: map[string]int{}}
	if provider == "cursor-cli" {
		records, last, err := cursorcli.ReadNativeRecordRows(ctx, path, session, c.LastID)
		if err != nil {
			return b, err
		}
		for _, r := range records {
			if r.Kind == "turn" {
				b.Next.TurnID, b.Next.Origin = r.ID, "human"
			}
			if r.Kind == "helper" {
				key := "helper:" + r.TaskID
				if r.State == "started" {
					b.Next.Lineage[key] = Lineage{TurnID: b.Next.TurnID, Origin: b.Next.Origin}
				}
				launch := b.Next.Lineage[key]
				r.TurnID, r.Origin = launch.TurnID, launch.Origin
				if r.State != "started" {
					delete(b.Next.Lineage, key)
				}
			}
			emit(&b, r)
		}
		b.Next.LastID = last
		b.CaughtUp = true
		return b, nil
	}
	if provider != "agy-cli" {
		return b, fmt.Errorf("provider has no native SQLite reader")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return b, err
	}
	defer db.Close()
	// Revisit only outstanding rows separately. Rewinding the forward scan to
	// an old pending step can pin every LIMIT 1024 batch to the same window.
	consume := func(rows *sql.Rows, pending bool) (int, error) {
		defer rows.Close()
		count := 0
		for rows.Next() {
			count++
			var idx int64
			var kind, status int
			var payload []byte
			var parent sql.NullInt64
			if err := rows.Scan(&idx, &kind, &payload, &status, &parent); err != nil {
				return count, err
			}
			if !pending && idx >= b.Next.Offset {
				b.Next.Offset = idx + 1
			}
			key := strconv.FormatInt(idx, 10)
			state := strconv.Itoa(status)
			if pending && c.Tools[key] == state {
				continue // An unchanged running row is not a new activity event.
			}
			r := llmtypes.NativeRecord{Provider: provider, SessionID: session, ID: fmt.Sprintf("step:%d:%d", idx, status)}
			if pending && parent.Valid {
				r.TurnID, r.Origin = "step:"+strconv.FormatInt(parent.Int64, 10), "human"
			}
			switch kind {
			case 14:
				b.Next.TurnID, b.Next.Origin = "step:"+key, "human"
				r.Kind, r.State = "turn", "started"
			case 15:
				if status != 3 {
					if status == 6 {
						delete(b.Next.Tools, key) // Interrupted text is not a committed reply.
					} else {
						b.Next.Tools[key] = state
					}
					continue
				}
				r.Text, _, _ = agycli.DecodeNativeStepText(kind, payload)
				r.Kind, r.State = "reply", "committed"
			case 132:
				r.Kind, r.State = "tool", "started"
				switch status {
				case 3:
					r.State = "finished"
					// invoke_subagent launches children; each one is a row until its own conversation ends.
					for _, id := range agycli.NativeSubagentIDs(payload) {
						if _, known := b.Next.Tasks[id]; !known {
							b.Next.Tasks[id] = "launched"
							// Both of a child's records belong to the turn that launched it, not the latest one scanned.
							turn, origin := r.TurnID, r.Origin
							if turn == "" {
								turn, origin = b.Next.TurnID, b.Next.Origin
							}
							b.Next.Lineage[id] = Lineage{TurnID: turn, Origin: origin}
							emit(&b, llmtypes.NativeRecord{Provider: provider, SessionID: session, ID: "subagent:" + id + ":started", Kind: "helper", State: "started", TaskID: id, ToolID: id, Name: "Subagent", HelperType: "invoke_subagent", TurnID: turn, Origin: origin})
						}
					}
				case 6:
					r.State = "stopped"
				case 0, 1, 2:
					b.Next.Tools[key] = state
				default:
					b.Next.Tools[key] = state
					b.Gaps++
					continue
				}
			case 17:
				r.Kind, r.State = "error", "failed"
			case 101:
				if id := agycli.NativeSubagentReceiptID(payload); id != "" && b.Next.Tasks[id] == "launched" {
					b.Next.Tasks[id] = "notified"
				}
				r.Kind, r.State = "notice", "observed"
			default:
				b.Unknown["step:"+strconv.Itoa(kind)]++
				continue
			}
			if status == 3 || status == 6 {
				delete(b.Next.Tools, key)
			}
			emit(&b, r)
		}
		return count, rows.Err()
	}
	if len(c.Tools) > 0 {
		keys := make([]string, 0, len(c.Tools))
		for key := range c.Tools {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		args := make([]any, 0, len(keys)+1)
		for _, key := range keys {
			idx, err := strconv.ParseInt(key, 10, 64)
			if err != nil {
				return b, fmt.Errorf("invalid pending native step index")
			}
			args = append(args, idx)
		}
		args = append(args, c.Offset)
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
		// A mutable old tool belongs to its original turn, not the latest one.
		rows, err := db.QueryContext(ctx, `SELECT s.idx,s.step_type,s.step_payload,s.status,
			(SELECT MAX(t.idx) FROM steps t WHERE t.step_type=14 AND t.idx<=s.idx)
			FROM steps s WHERE s.idx IN (`+placeholders+`) AND s.idx<? ORDER BY s.idx`, args...)
		if err != nil {
			return b, err
		}
		if _, err := consume(rows, true); err != nil {
			return b, err
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT idx,step_type,step_payload,status,NULL FROM steps WHERE idx>=? ORDER BY idx LIMIT 1024`, c.Offset)
	if err != nil {
		return b, err
	}
	count, err := consume(rows, false)
	b.CaughtUp = count < 1024
	if err == nil {
		// A launched child whose own conversation has ended closes its row.
		ids := make([]string, 0, len(b.Next.Tasks))
		for id, state := range b.Next.Tasks {
			if state == "notified" {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			if agycli.NativeSubagentSettled(filepath.Join(filepath.Dir(path), id+".db")) {
				delete(b.Next.Tasks, id)
				launch := b.Next.Lineage[id]
				delete(b.Next.Lineage, id)
				emit(&b, llmtypes.NativeRecord{Provider: provider, SessionID: session, ID: "subagent:" + id + ":finished", Kind: "helper", State: "finished", TaskID: id, ToolID: id, Name: "Subagent", HelperType: "invoke_subagent", TurnID: launch.TurnID, Origin: launch.Origin})
			}
		}
	}
	return b, err
}

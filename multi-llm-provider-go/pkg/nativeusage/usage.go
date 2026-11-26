// Package nativeusage reads accounting observations from an explicitly owned
// native runtime. It never starts a model or decides who may spend tokens.
package nativeusage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Record identifies one provider measurement, independently of the foreground
// or runtime reader. Cached input remains separate and its inclusion explicit.
type Record struct {
	ID                 string    `json:"id"`
	NativeSessionID    string    `json:"native_session_id"`
	TurnID             string    `json:"native_turn_id,omitempty"`
	ParentTurnID       string    `json:"parent_native_turn_id,omitempty"`
	TaskID             string    `json:"task_id,omitempty"`
	OwnerType          string    `json:"owner_type,omitempty"`
	Model              string    `json:"model,omitempty"`
	ParentModel        string    `json:"parent_model,omitempty"`
	Timestamp          time.Time `json:"timestamp"`
	Input              int64     `json:"input_tokens"`
	Output             int64     `json:"output_tokens"`
	CacheRead          int64     `json:"cache_read_tokens"`
	CacheWrite         int64     `json:"cache_write_tokens"`
	Reasoning          int64     `json:"reasoning_tokens"`
	InputIncludesCache bool      `json:"input_includes_cache"`
	EstimatedUSD       *float64  `json:"estimated_usd,omitempty"`
	Path               string    `json:"source_path"`
	Offset             int64     `json:"source_offset"`
}

// Key is shared by every accounting path. A notification is not another model
// call: a helper message keeps its message id when mirrored in the parent.
func (r Record) Key(provider, account string) string {
	return strings.Join([]string{"native", provider, account, r.NativeSessionID, r.ID}, "\x1f")
}

type Totals struct{ Input, Output, CacheRead, CacheWrite int64 }
type Limit struct {
	Name                    string
	UsedPercent             float64
	WindowMinutes, ResetsAt int64
}

// PrivateUsage must not be put in a shared chat event or project response.
type PrivateUsage struct {
	ContextUsed, ContextWindow int64
	Limits                     []Limit
}
type Snapshot struct {
	Records     []Record
	Positions   map[string]int64
	Cumulative  map[string]Totals
	Private     PrivateUsage `json:"-"`
	Supported   bool
	CaughtUp    bool
	Gaps        int
	SourceStamp string
}

func Supported(provider string) bool {
	return provider == "claude-code" || provider == "codex-cli" || provider == "muse-cli" || provider == "pi-cli"
}

func sourcePaths(provider, path string) ([]string, error) {
	paths := []string{path}
	if provider == "claude-code" {
		children, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "agent-*.jsonl"))
		paths = append(paths, children...)
	}
	if provider == "muse-cli" {
		root := filepath.Join(filepath.Dir(path), "subagent")
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() && d.Name() == "session.jsonl" {
				paths = append(paths, p)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return paths, nil
}

func stampPaths(paths []string) (string, error) {
	var stamp strings.Builder
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&stamp, "%s\x00%d\x00%d\n", path, info.Size(), info.ModTime().UnixNano())
	}
	return stamp.String(), nil
}

// SourceStamp checks only file metadata, including new owned helper files.
func SourceStamp(provider, path string) (string, error) {
	paths, err := sourcePaths(provider, path)
	if err != nil {
		return "", err
	}
	return stampPaths(paths)
}

// Read includes owned helper files under this runtime only. It returns no
// historical billing decision: the host applies its durable coverage boundary.
func Read(ctx context.Context, provider, session, path string) (Snapshot, error) {
	s := Snapshot{Supported: Supported(provider), CaughtUp: true, Positions: map[string]int64{}, Cumulative: map[string]Totals{}}
	if !s.Supported {
		return s, nil
	}
	if session == "" || path == "" {
		return s, fmt.Errorf("exact native session and source required")
	}
	paths, err := sourcePaths(provider, path)
	if err != nil {
		return s, err
	}
	// Capture before reading. A concurrent append then guarantees a changed
	// stamp on the next poll, even if it raced this reader's end of file.
	s.SourceStamp, err = stampPaths(paths)
	if err != nil {
		return s, err
	}
	byID := map[string]Record{}
	models := map[string]string{}
	ambiguousModels := map[string]bool{}
	setModel := func(turn, model string) {
		if model == "" {
			return
		}
		if old := models[turn]; old != "" && old != model {
			ambiguousModels[turn] = true
		}
		models[turn] = model
	}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return s, err
		}
		f, err := os.Open(p)
		if err != nil {
			return s, err
		}
		reader := bufio.NewReader(f)
		var offset int64
		turn, model := "", ""
		for {
			if err := ctx.Err(); err != nil {
				_ = f.Close()
				return s, err
			}
			line, e := reader.ReadBytes('\n')
			if e == io.EOF {
				if len(line) > 0 {
					s.CaughtUp = false
				}
				break
			}
			if e != nil {
				_ = f.Close()
				return s, e
			}
			offset += int64(len(line))
			if len(line) > 8<<20 {
				s.Gaps++
				continue
			}
			var row map[string]any
			if json.Unmarshal(line, &row) != nil {
				s.Gaps++
				continue
			}
			r := Record{NativeSessionID: session, Path: p, Offset: offset, Timestamp: timestamp(row)}
			switch provider {
			case "claude-code":
				if p == path && str(row, "sessionId") != "" && str(row, "sessionId") != session {
					_ = f.Close()
					return s, fmt.Errorf("native session mismatch")
				}
				if str(row, "type") == "user" {
					turn = str(row, "uuid")
				}
				if str(row, "type") == "cost-state" {
					for name, v := range obj(row["modelUsage"]) {
						u := obj(v)
						s.Cumulative[name] = Totals{num(u, "inputTokens"), num(u, "outputTokens"), num(u, "cacheReadInputTokens"), num(u, "cacheCreationInputTokens")}
					}
					continue
				}
				if str(row, "type") != "assistant" {
					continue
				}
				m := obj(row["message"])
				u := obj(m["usage"])
				r.ID = str(m, "id")
				r.Model = str(m, "model")
				r.TurnID = turn
				sidechain, _ := row["isSidechain"].(bool)
				if p != path || sidechain {
					r.TaskID = strings.TrimSuffix(filepath.Base(p), ".jsonl")
					r.OwnerType = "helper"
				}
				r.Input = num(u, "input_tokens")
				r.Output = num(u, "output_tokens")
				r.CacheRead = num(u, "cache_read_input_tokens")
				r.CacheWrite = num(u, "cache_creation_input_tokens")
				// The main thread's latest prompt is what fills the window; Claude
				// does not write the window size, so callers use the model's.
				if r.OwnerType == "" {
					if used := r.Input + r.CacheRead + r.CacheWrite; used > 0 {
						s.Private.ContextUsed = used
					}
				}
			case "codex-cli":
				p := obj(row["payload"])
				if str(row, "type") == "session_meta" && str(p, "id") != "" && str(p, "id") != session {
					_ = f.Close()
					return s, fmt.Errorf("native session mismatch")
				}
				if str(row, "type") == "turn_context" {
					turn = str(p, "turn_id")
					model = str(p, "model")
					models[turn] = model
				}
				if str(row, "type") == "event_msg" && str(p, "type") == "token_count" {
					info := obj(p["info"])
					u := obj(info["total_token_usage"])
					s.Cumulative[session] = Totals{num(u, "input_tokens"), num(u, "output_tokens"), num(u, "cached_input_tokens"), num(u, "cache_write_input_tokens")}
					last := obj(info["last_token_usage"])
					s.Private.ContextUsed = num(last, "total_tokens")
					s.Private.ContextWindow = num(info, "model_context_window")
					limits := obj(p["rate_limits"])
					s.Private.Limits = nil
					for _, name := range []string{"primary", "secondary"} {
						if w := obj(limits[name]); w != nil {
							percent, ok := w["used_percent"].(float64)
							if ok {
								s.Private.Limits = append(s.Private.Limits, Limit{name, percent, num(w, "window_minutes"), num(w, "resets_at")})
							}
						}
					}
					continue
				}
				if str(row, "type") != "token_usage_record" {
					continue
				}
				u := obj(p["usage"])
				r.ID = str(p, "response_id")
				r.TurnID = str(p, "turn_id")
				r.Model = models[r.TurnID]
				if r.Model == "" {
					r.Model = model
				}
				r.InputIncludesCache = true
				r.Input = num(u, "input_tokens")
				r.Output = num(u, "output_tokens")
				r.CacheRead = num(u, "cached_input_tokens")
				r.CacheWrite = num(u, "cache_write_input_tokens")
				r.Reasoning = num(u, "reasoning_output_tokens")
			case "muse-cli":
				w := obj(row["payload"])
				e := obj(w["event"])
				if e == nil {
					continue
				}
				r.TurnID = str(w, "run_id")
				if str(e, "kind") == "model_completed" {
					if name := str(e, "model"); name != "" {
						setModel(r.TurnID, name)
					}
					continue
				}
				if str(e, "kind") == "run_model" {
					rec := obj(e["record"])
					setModel(str(obj(rec["run_stream"]), "id"), str(rec, "model_id"))
					continue
				}
				if str(e, "kind") != "goal_usage_attribution" {
					continue
				}
				rec := obj(e["record"])
				u := obj(rec["quantity"])
				reported, _ := u["reported"].(bool)
				if str(rec, "usage_family") != "provider" || str(u, "unit") != "tokens" || !reported {
					continue
				}
				o := obj(rec["owner"])
				r.ID = str(rec, "usage_id")
				r.OwnerType = str(o, "owner_type")
				r.TaskID = str(o, "owner_id")
				r.InputIncludesCache = true
				if ownerRun := str(o, "run_id"); ownerRun != "" && ownerRun != r.TurnID {
					r.ParentTurnID = r.TurnID
					r.TurnID = ownerRun
				}
				r.Input = num(u, "input_tokens")
				r.Output = num(u, "output_tokens")
				r.CacheRead = num(u, "cached_tokens")
				r.CacheWrite = num(u, "cache_write_tokens")
				r.Reasoning = num(u, "reasoning_tokens")
			case "pi-cli":
				if str(row, "type") == "session" && str(row, "id") != session {
					_ = f.Close()
					return s, fmt.Errorf("native session mismatch")
				}
				m := obj(row["message"])
				if str(row, "type") != "message" || str(m, "role") != "assistant" {
					continue
				}
				u := obj(m["usage"])
				r.ID = str(row, "id")
				r.Model = str(m, "model")
				r.Input = num(u, "input")
				r.Output = num(u, "output")
				r.CacheRead = num(u, "cacheRead")
				r.CacheWrite = num(u, "cacheWrite")
			}
			if r.Input+r.Output+r.CacheRead+r.CacheWrite == 0 {
				continue
			}
			if r.ID == "" {
				s.Gaps++
				continue
			}
			if old, ok := byID[r.ID]; ok {
				if r.OwnerType == "" && old.OwnerType != "" {
					r.OwnerType, r.TaskID = old.OwnerType, old.TaskID
				}
				r.Input = max(r.Input, old.Input)
				r.Output = max(r.Output, old.Output)
				r.CacheRead = max(r.CacheRead, old.CacheRead)
				r.CacheWrite = max(r.CacheWrite, old.CacheWrite)
				r.Reasoning = max(r.Reasoning, old.Reasoning)
				if r.Model == "" {
					r.Model = old.Model
				}
				// Growing observations keep the call's first timestamp. Moving
				// it into a later generation could charge the same call twice.
				if !old.Timestamp.IsZero() && (r.Timestamp.IsZero() || old.Timestamp.Before(r.Timestamp)) {
					r.Timestamp = old.Timestamp
				}
			}
			byID[r.ID] = r
		}
		_ = f.Close()
		s.Positions[p] = offset
	}
	for _, r := range byID {
		if provider == "muse-cli" {
			if !ambiguousModels[r.TurnID] {
				r.Model = models[r.TurnID]
			}
			if r.Model == "" && !ambiguousModels[r.ParentTurnID] {
				r.ParentModel = models[r.ParentTurnID]
			}
		}
		s.Records = append(s.Records, r)
	}
	sort.Slice(s.Records, func(i, j int) bool {
		a, b := s.Records[i], s.Records[j]
		if a.Timestamp.Equal(b.Timestamp) {
			return a.ID < b.ID
		}
		return a.Timestamp.Before(b.Timestamp)
	})
	return s, nil
}

func obj(v any) map[string]any              { m, _ := v.(map[string]any); return m }
func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func num(m map[string]any, k string) int64  { n, _ := m[k].(float64); return int64(n) }
func timestamp(m map[string]any) time.Time {
	if n := num(m, "recorded_at"); n > 0 {
		return time.UnixMicro(n)
	}
	t, _ := time.Parse(time.RFC3339Nano, str(m, "timestamp"))
	return t
}

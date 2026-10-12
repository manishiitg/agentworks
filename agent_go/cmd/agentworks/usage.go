package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentusage"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

func usageCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "usage", Short: "Optional native token usage for attached chats (off by default)"}
	for _, action := range []string{"on", "off", "status"} {
		cmd.AddCommand(&cobra.Command{Use: action, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			root, err := agentworksclient.DefaultLocalBridgeRoot()
			if err != nil {
				return err
			}
			if c.Name() != "status" {
				if err = personAgentTerminal(); err != nil {
					return errors.New("usage on/off are person commands; run them in your own terminal outside the coding agent")
				}
				if err = ensureLocalBridge(c.Context(), root, o); err != nil {
					return err
				}
				if err = agentworksclient.SetLocalUsage(root, c.Name() == "on"); err != nil {
					return err
				}
				if _, err = agentworksclient.CallLocalBridge(c.Context(), root, agentworksclient.LocalBridgeRequest{Action: "usage_refresh"}); err != nil {
					return err
				}
			}
			s, err := agentworksclient.LocalUsageStatus(root)
			if err != nil {
				return err
			}
			if o.jsonOutput {
				return json.NewEncoder(o.stdout).Encode(s)
			}
			state := "off"
			if s.Enabled {
				state = "on"
			}
			fmt.Fprintf(o.stdout, "Native usage: %s. %d tracked sessions; %d pending remote clears.\n", state, s.Sessions, s.PendingClear)
			if len(s.Caps) > 0 {
				fmt.Fprintf(o.stdout, "Caps reached: %s\n", strings.Join(s.Caps, ", "))
			}
			fmt.Fprintln(o.stdout, "Hourly readings use the CLI's recorded numbers. Turning off clears current reporting; history remains for 90 days or until you delete it in Bullpen.")
			return nil
		}})
	}
	return cmd
}

type usageReader struct {
	ctx context.Context
	r   io.Reader
	n   int64
}

func (r *usageReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, e := r.r.Read(p)
	r.n += int64(n)
	return n, e
}

// Exact native IDs only; no latest-file or cwd fallback. Index queries select
// path/model metadata only, never title, prompts, payloads or credentials.
func usageIndex(ctx context.Context, p, query, session string) (string, string) {
	info, e := os.Lstat(p)
	if e != nil || !info.Mode().IsRegular() {
		return "", ""
	}
	u := url.URL{Scheme: "file", Path: p}
	db, e := sql.Open("sqlite", u.String()+"?mode=ro&_pragma=busy_timeout(1000)")
	if e != nil {
		return "", ""
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var path, model string
	if db.QueryRowContext(ctx, query, session).Scan(&path, &model) != nil {
		return "", ""
	}
	return path, agentusage.ModelID(model)
}

func readAttachedUsage(root string) func(context.Context, agentworksclient.AgentAttachment, string, int64) (agentusage.Native, error) {
	return func(ctx context.Context, a agentworksclient.AgentAttachment, host string, budget int64) (out agentusage.Native, err error) {
		if _, e := uuid.Parse(a.SessionID); e != nil {
			return out, agentusage.ErrIdentity
		}
		if a.Provider == "pi" {
			return readPiUsage(ctx, root, a, host)
		}
		if a.Provider == "cursor" || a.Provider == "agy" {
			return out, agentusage.ErrUnavailable
		}
		home, e := os.UserHomeDir()
		if e != nil {
			return out, agentusage.ErrUnavailable
		}
		var path, model string
		switch a.Provider {
		case "codex":
			base := os.Getenv("CODEX_HOME")
			if base == "" {
				base = filepath.Join(home, ".codex")
			}
			path, model = usageIndex(ctx, filepath.Join(base, "state_5.sqlite"), "SELECT rollout_path, COALESCE(model,'') FROM threads WHERE id=?", a.SessionID)
			if path == "" {
				paths, _ := filepath.Glob(filepath.Join(base, "sessions", "*", "*", "*", "rollout-*-"+a.SessionID+".jsonl"))
				if len(paths) == 1 {
					path = paths[0]
				}
			}
			// An index cannot direct collection outside this provider's log root.
			rel, e := filepath.Rel(filepath.Join(base, "sessions"), path)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return out, agentusage.ErrIdentity
			}
		case "claude":
			base := os.Getenv("CLAUDE_CONFIG_DIR")
			if base == "" {
				base = filepath.Join(home, ".claude")
			}
			paths, _ := filepath.Glob(filepath.Join(base, "projects", "*", a.SessionID+".jsonl"))
			if len(paths) == 1 {
				path = paths[0]
			}
		case "muse":
			base := os.Getenv("XDG_DATA_HOME")
			if base == "" {
				base = filepath.Join(home, ".local", "share")
			}
			base = filepath.Join(base, "muse")
			path, model = usageIndex(ctx, filepath.Join(base, "session-index.db"), "SELECT session_log_path, COALESCE(model_id,'') FROM sessions WHERE session_id=?", a.SessionID)
			if path == "" {
				paths, _ := filepath.Glob(filepath.Join(base, "sessions", "*", "*", "*", a.SessionID, "session.jsonl"))
				if len(paths) == 1 {
					path = paths[0]
				}
			}
			rel, e := filepath.Rel(filepath.Join(base, "sessions"), path)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Base(filepath.Dir(path)) != a.SessionID {
				return out, agentusage.ErrIdentity
			}
		default:
			return out, agentusage.ErrUnavailable
		}
		st, e := os.Lstat(path)
		if e != nil || !st.Mode().IsRegular() {
			return out, agentusage.ErrUnavailable
		}
		f, e := os.Open(path)
		if e != nil {
			return out, agentusage.ErrUnavailable
		}
		defer f.Close()
		opened, e := f.Stat()
		if e != nil || !os.SameFile(st, opened) {
			return out, agentusage.ErrIdentity
		}
		if budget > agentusage.MaxReadBytes {
			budget = agentusage.MaxReadBytes
		}
		var input io.Reader = f
		if a.Provider == "claude" || a.Provider == "muse" {
			cfg, e := agentworksclient.LocalUsageSettings(root)
			if e != nil || !cfg.Enabled {
				return out, agentusage.ErrUnavailable
			}
			digest := sha256.Sum256([]byte(a.Provider + ":" + a.SessionID))
			key := hex.EncodeToString(digest[:])
			cache, e := agentworksclient.LoadUsageLedger(root, key)
			if e != nil {
				return out, agentusage.ErrUnavailable
			}
			identity := usageFileIdentity(st)
			if cache.Generation != cfg.Generation || cache.Identity != identity || cache.Ledger.Offset > st.Size() {
				cache = agentworksclient.UsageLedger{Generation: cfg.Generation, Identity: identity}
			}
			if _, e = f.Seek(cache.Ledger.Offset, io.SeekStart); e != nil {
				return out, agentusage.ErrUnavailable
			}
			count := &usageReader{ctx: ctx, r: io.LimitReader(f, budget)}
			if a.Provider == "claude" {
				out, err = agentusage.ClaudeChunk(count, a.SessionID, &cache.Ledger)
			} else {
				out, err = agentusage.MuseChunk(count, a.SessionID, &cache.Ledger)
			}
			out.BytesRead = count.n
			if err == nil || errors.Is(err, agentusage.ErrUnavailable) || errors.Is(err, agentusage.ErrLimit) {
				if e = agentworksclient.SaveUsageLedger(root, key, cache); e != nil {
					return out, agentusage.ErrUnavailable
				}
			}
			if cache.Ledger.Offset < st.Size() {
				return out, agentusage.ErrLimit
			}
			if out.Model == "" {
				out.Model = model
			}
			return out, err
		}
		if st.Size() > budget {
			// The latest cumulative Codex total remains complete even in a
			// bounded tail. Prepend its exact identity header and discard the
			// first partial tail line before decoding numeric records.
			header, e := bufio.NewReader(io.LimitReader(f, 65536)).ReadBytes('\n')
			if e != nil {
				return out, agentusage.ErrIdentity
			}
			if budget < int64(len(header))+agentusage.MaxLineBytes {
				return out, agentusage.ErrLimit
			}
			start := st.Size() - (budget - int64(len(header)))
			if _, e = f.Seek(start, io.SeekStart); e != nil {
				return out, agentusage.ErrUnavailable
			}
			tail := bufio.NewReader(f)
			if _, e = tail.ReadBytes('\n'); e != nil {
				return out, agentusage.ErrUnavailable
			}
			input = io.MultiReader(bytes.NewReader(header), tail)
		}
		count := &usageReader{ctx: ctx, r: input}
		defer func() { out.BytesRead = count.n }()
		switch a.Provider {
		case "codex":
			out, err = agentusage.Codex(count, a.SessionID)
		case "claude":
			out, err = agentusage.Claude(count, a.SessionID)
		case "muse":
			out, err = agentusage.Muse(count, a.SessionID)
		}
		if out.Model == "" {
			out.Model = model
		}
		return out, err
	}
}

func readPiUsage(ctx context.Context, root string, a agentworksclient.AgentAttachment, host string) (agentusage.Native, error) {
	record, err := piSessionForHost(root, host, a.SessionID)
	if err != nil {
		return agentusage.Native{}, agentusage.ErrUnavailable
	}
	body, _ := json.Marshal(map[string]string{"host_key": host, "session_id": a.SessionID, "attachment_id": a.ID})
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dialPiSDK(ctx, record) }}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/usage-stats", bytes.NewReader(body))
	if err != nil {
		return agentusage.Native{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return agentusage.Native{}, agentusage.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return agentusage.Native{}, agentusage.ErrUnavailable
	}
	count := &usageReader{ctx: ctx, r: resp.Body}
	n, e := agentusage.PiStats(count, a.SessionID)
	n.BytesRead = count.n
	return n, e
}

func usageFileIdentity(info os.FileInfo) string {
	v := reflect.ValueOf(info.Sys())
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.IsValid() && v.Kind() == reflect.Struct {
		dev, ino := v.FieldByName("Dev"), v.FieldByName("Ino")
		if dev.IsValid() && ino.IsValid() {
			return fmt.Sprint(dev.Interface(), ":", ino.Interface())
		}
		birth := v.FieldByName("CreationTime")
		if birth.IsValid() {
			return fmt.Sprint(birth.Interface())
		}
	}
	return info.Name()
}

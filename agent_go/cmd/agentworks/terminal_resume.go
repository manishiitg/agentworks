package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chatterminal"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
	"github.com/spf13/cobra"
)

type terminalReceipt struct {
	Reference chatterminal.Reference `json:"chat"`
	Receipt   string                 `json:"receipt"`
	Session   string                 `json:"session"`
	Native    string                 `json:"native"`
	Folder    string                 `json:"folder"`
}

func resumeCommand(o *options) *cobra.Command {
	return &cobra.Command{Use: "resume <chat>", Short: "Continue your own AgentWorks Claude chat in this terminal", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return runTerminalResume(cmd.Context(), o, args[0]) }}
}

func terminalClient(o *options, server string) (*agentworksclient.Client, error) {
	if _, e := agentworksclient.ValidateServer(server); e != nil {
		return nil, e
	}
	copy := *o
	if cfg, _, e := o.connection(false); e == nil && cfg.Server == server {
		return o.client()
	}
	logins, _ := agentworksclient.ListLogins()
	for _, login := range logins {
		if strings.TrimRight(login.Server, "/") == server {
			p, e := agentworksclient.LoginPath(login.Name)
			if e != nil {
				return nil, e
			}
			copy.configPath = p
			copy.serverURL = server
			copy.getenv = func(k string) string {
				if k == "AGENTWORKS_TOKEN" || k == "AGENTWORKS_SERVER" {
					return ""
				}
				return o.getenv(k)
			}
			return copy.client()
		}
	}
	return nil, fmt.Errorf("sign in to this chat's AgentWorks connection first with agentworks login --server %s", server)
}

func terminalReceiptPath(o *options, native string) (string, error) {
	if !sessiontransfer.UUID.MatchString(native) {
		return "", fmt.Errorf("invalid native session")
	}
	p, e := o.path()
	if e != nil {
		return "", e
	}
	dir := filepath.Join(filepath.Dir(p), "terminal-resume")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	return filepath.Join(dir, native+".json"), nil
}

func terminalLocalFolder(ref chatterminal.Reference, hint string) (string, error) {
	uri, e := url.Parse(ref.Server)
	if e != nil {
		return "", e
	}
	loopback := uri.Hostname() == "localhost"
	if ip := net.ParseIP(uri.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if !loopback || hint == "" {
		state, e := crewroot.StateRoot()
		if e != nil {
			return "", e
		}
		var doc struct {
			Projects map[string]crewroot.Record `json:"projects"`
		}
		raw, e := os.ReadFile(filepath.Join(state, "ownership", "projects.json"))
		if e == nil {
			e = json.Unmarshal(raw, &doc)
		}
		if e != nil {
			return "", fmt.Errorf("this server chat needs its project files on this laptop. Open its Crew or synced Team folder in the Mac app first")
		}
		hint = ""
		for _, record := range doc.Projects {
			match := loopback && record.ID == ref.Project
			if record.Product != "work" || record.Folder == "" || filepath.Base(record.Folder) != record.Folder || record.Folder == "." || record.Folder == ".." {
				continue
			}
			if binding, err := crewroot.ReadSync(record.ID); err == nil && binding != nil {
				match = binding.Side == "local" && binding.PeerProjectID == ref.Project && strings.TrimRight(binding.PeerServer, "/") == ref.Server
			}
			if !match {
				continue
			}
			candidate := record.LocalRoot
			if candidate == "" {
				var settings struct {
					Docs string `json:"docsDir"`
				}
				settingsRaw, err := os.ReadFile(filepath.Join(filepath.Dir(state), "config.json"))
				if err == nil {
					_ = json.Unmarshal(settingsRaw, &settings)
				}
				docs := settings.Docs
				if docs == "" {
					docs = filepath.Join(filepath.Dir(state), "workspace-docs")
				}
				candidate = filepath.Join(docs, "Crew", record.Folder)
			}
			if hint != "" && hint != candidate {
				return "", fmt.Errorf("more than one local folder matches this chat; open its Crew in the Mac app")
			}
			hint = candidate
		}
	}
	if !filepath.IsAbs(hint) {
		return "", fmt.Errorf("this chat needs its Crew or synced Team folder on this laptop first")
	}
	root, e := filepath.EvalSymlinks(hint)
	if e != nil {
		return "", fmt.Errorf("this chat's local project folder is unavailable")
	}
	info, e := os.Stat(root)
	if e != nil || !info.IsDir() {
		return "", fmt.Errorf("this chat's local project folder is unavailable")
	}
	return root, nil
}

func installTerminalJournal(store, folder string, b *sessiontransfer.Bundle) (string, error) {
	if e := b.ValidateFiles(); e != nil {
		return "", e
	}
	if e := b.ValidateJournals(); e != nil {
		return "", e
	}
	if b.Provider != "claude-code" || len(b.Files) != 1 {
		return "", fmt.Errorf("only one Claude journal can move")
	}
	dest, e := sessiontransfer.Path(store, "projects/"+sessiontransfer.ClaudeSlug(folder)+"/"+b.SessionID+".jsonl", true)
	if e != nil {
		return "", e
	}
	if raw, e := os.ReadFile(dest); e == nil {
		h := sha256.Sum256(raw)
		if int64(len(raw)) == b.Files["session.jsonl"].Size && hex.EncodeToString(h[:]) == b.Files["session.jsonl"].SHA256 {
			return dest, nil
		}
		return "", fmt.Errorf("a different local Claude journal already exists. Nothing was overwritten; choose Continue here instead in AgentWorks")
	} else if !os.IsNotExist(e) {
		return "", e
	}
	f, e := os.CreateTemp(filepath.Dir(dest), ".terminal-session-")
	if e != nil {
		return "", e
	}
	defer os.Remove(f.Name())
	in, e := os.Open(filepath.Join(b.Root, "session.jsonl"))
	if e != nil {
		f.Close()
		return "", e
	}
	defer in.Close()
	_, e = io.Copy(f, in)
	ce := f.Close()
	if e != nil {
		return "", e
	}
	if ce != nil {
		return "", ce
	}
	// Link publishes complete bytes without replacing a file another process
	// created after our absence check. No differing journal is ever replaced.
	if e = os.Link(f.Name(), dest); e != nil {
		return "", e
	}
	return dest, nil
}

func runTerminalResume(ctx context.Context, o *options, reference string) (result error) {
	chatID, nativeID, reason := "", "", "invalid_command"
	defer func() {
		if result != nil {
			fmt.Fprintf(o.stderr, "[CHAT_TERMINAL_RESUME] chat=%s session=%s action=refused reason=%s\n", chatID, nativeID, reason)
		}
	}()

	ref, e := chatterminal.ParseReference(reference)
	if e != nil {
		return e
	}
	chatID = ref.Key
	reason = "connection_unavailable"
	client, e := terminalClient(o, ref.Server)
	if e != nil {
		return e
	}
	reason = "claude_unavailable"
	if _, e = exec.LookPath("claude"); e != nil {
		return fmt.Errorf("install and sign in to Claude Code on this laptop first; the chat has not moved")
	}
	reason = "folder_unavailable"
	status, e := client.TerminalStatus(ctx, ref.Key)
	if e != nil {
		return e
	}
	if status["out"] == true {
		return fmt.Errorf("this chat is already in a terminal. Attach it back, or choose Continue here instead in AgentWorks")
	}
	hint, _ := status["local_folder"].(string)
	folder, e := terminalLocalFolder(ref, hint)
	if e != nil {
		return e
	}
	path, e := o.path()
	if e != nil {
		return e
	}
	dir, e := os.MkdirTemp(filepath.Dir(path), "terminal-stage-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	reason = "fetch_failed"
	body, e := client.TerminalSession(ctx, ref.Key, "fetch", "", nil)
	if e != nil {
		return e
	}
	t, e := chatterminal.Read(body, dir)
	body.Close()
	if e == nil && (t.Reference.Key != ref.Key || t.Reference.Project != ref.Project) {
		e = fmt.Errorf("chat identity mismatch")
	}
	if e != nil {
		return fmt.Errorf("the session transfer could not be verified; use Continue here instead in AgentWorks to recover the chat")
	}
	abort := func() {
		b, err := client.TerminalSession(ctx, ref.Key, "abort", t.Receipt, nil)
		if err == nil {
			b.Close()
		}
	}
	nativeID = t.Bundle.SessionID
	reason = "local_folder_unavailable"
	currentFolder, e := terminalLocalFolder(ref, t.LocalFolder)
	if e == nil && currentFolder != folder {
		e = fmt.Errorf("the project folder changed during this handoff; try again from the chat menu")
	}
	if e != nil {
		abort()
		return e
	}
	home, e := os.UserHomeDir()
	if e != nil {
		abort()
		return e
	}
	store := filepath.Join(home, ".claude")
	if configured := o.getenv("CLAUDE_CONFIG_DIR"); configured != "" {
		store = configured
	}
	if !filepath.IsAbs(store) {
		abort()
		return fmt.Errorf("Claude's configuration folder must be absolute")
	}
	reason = "journal_not_written"
	if _, e = installTerminalJournal(store, folder, t.Bundle); e != nil {
		abort()
		return e
	}
	receiptPath, e := terminalReceiptPath(o, t.Bundle.SessionID)
	if e != nil {
		abort()
		return e
	}
	receipt := terminalReceipt{Reference: ref, Receipt: t.Receipt, Session: t.Session, Native: t.Bundle.SessionID, Folder: folder}
	if e = fsutil.WriteJSONAtomic(receiptPath, receipt, 0600); e != nil {
		abort()
		return e
	}
	fmt.Fprintf(o.stderr, "[CHAT_TERMINAL_RESUME] chat=%s session=%s action=written reason=verified_session\n", ref.Key, t.Bundle.SessionID)
	cmd := exec.Command("claude", "--resume", t.Bundle.SessionID)
	cmd.Dir = folder
	cmd.Stdin = o.stdin
	cmd.Stdout = o.stdout
	cmd.Stderr = o.stderr
	reason = "launch_failed"
	if e = cmd.Start(); e != nil {
		abort()
		return e
	}
	fmt.Fprintf(o.stderr, "[CHAT_TERMINAL_RESUME] chat=%s session=%s action=resumed reason=terminal_started\n", ref.Key, t.Bundle.SessionID)
	reason = "terminal_exit_nonzero"
	return cmd.Wait()
}

// Called only after the existing attach path proves its exact live session.
// A recovered/superseded terminal cannot overwrite the original chat.
func returnTerminalChat(ctx context.Context, o *options, chosen liveAgentSession) (bool, error) {
	if chosen.provider != "claude" {
		return false, nil
	}
	path, e := terminalReceiptPath(o, chosen.session)
	if e != nil {
		return false, e
	}
	raw, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return true, e
	}
	var receipt terminalReceipt
	if json.Unmarshal(raw, &receipt) != nil || receipt.Native != chosen.session {
		return true, fmt.Errorf("invalid terminal return receipt")
	}
	if chosen.cwd != "" {
		root, e := filepath.EvalSymlinks(chosen.cwd)
		if e != nil || root != receipt.Folder {
			return true, fmt.Errorf("attach from the exact project folder where this chat was resumed")
		}
	}
	client, e := terminalClient(o, receipt.Reference.Server)
	if e != nil {
		return true, e
	}
	status, e := client.TerminalStatus(ctx, receipt.Reference.Key)
	if e != nil {
		return true, e
	}
	if status["out"] != true {
		return true, fmt.Errorf("this old terminal copy is no longer the live chat. Continue in AgentWorks, or import it as a new chat")
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return true, e
	}
	store := filepath.Join(home, ".claude")
	if configured := o.getenv("CLAUDE_CONFIG_DIR"); configured != "" {
		store = configured
	}
	dir, e := os.MkdirTemp(filepath.Dir(path), "return-stage-")
	if e != nil {
		return true, e
	}
	defer os.RemoveAll(dir)
	b, e := sessiontransfer.ExportJSONL("claude-code", "structured", store, receipt.Folder, receipt.Native, dir)
	if e != nil {
		return true, e
	}
	b.Files = map[string]sessiontransfer.File{"session.jsonl": b.Files["session.jsonl"]}
	archive, e := os.CreateTemp(dir, "return-*.tar")
	if e != nil {
		return true, e
	}
	defer archive.Close()
	e = chatterminal.Write(archive, chatterminal.Transfer{Reference: receipt.Reference, Receipt: receipt.Receipt, Session: receipt.Session, Bundle: b})
	if e != nil {
		return true, e
	}
	if _, e = archive.Seek(0, io.SeekStart); e != nil {
		return true, e
	}
	response, e := client.TerminalSession(ctx, receipt.Reference.Key, "return", "", archive)
	if e != nil {
		return true, e
	}
	response.Close()
	fmt.Fprintf(o.stdout, "The chat is back in AgentWorks. This terminal copy is no longer the live one.\n")
	return true, nil
}

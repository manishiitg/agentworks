package main

import (
	"context"
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
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
	"github.com/spf13/cobra"
)

func resumeCommand(o *options) *cobra.Command {
	return &cobra.Command{Use: "resume <chat>", Short: "Open a copy of your own AgentWorks Claude chat in this terminal", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return runTerminalResume(cmd.Context(), o, args[0]) }}
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
		folders, e := localCrewFolders()
		if e != nil {
			return "", e
		}
		hint = ""
		for _, f := range folders {
			match := loopback && f.ID == ref.Project
			if f.Binding != nil {
				match = f.Binding.Side == "local" && f.Binding.PeerProjectID == ref.Project && strings.TrimRight(f.Binding.PeerServer, "/") == ref.Server
			}
			if !match {
				continue
			}
			if hint != "" && hint != f.Root {
				return "", fmt.Errorf("more than one local folder matches this chat; open its project in the Mac app")
			}
			hint = f.Root
		}
	}
	if !filepath.IsAbs(hint) {
		return "", fmt.Errorf("this chat needs its project or synced Team folder on this laptop first")
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

// localCrewFolder is one Crew on this Mac (product "work"), its local folder and, for a synced Team copy, the binding
// to its server project.
type localCrewFolder struct {
	ID      string
	Root    string
	Binding *crewroot.SyncBinding
}

func localCrewFolders() ([]localCrewFolder, error) {
	state, e := crewroot.StateRoot()
	if e != nil {
		return nil, e
	}
	var doc struct {
		Projects map[string]crewroot.Record `json:"projects"`
	}
	raw, e := os.ReadFile(filepath.Join(state, "ownership", "projects.json"))
	if e == nil {
		e = json.Unmarshal(raw, &doc)
	}
	if e != nil {
		return nil, fmt.Errorf("this server chat needs its project files on this laptop. Open its project or synced Team folder in the Mac app first")
	}
	var out []localCrewFolder
	for _, record := range doc.Projects {
		if record.Product != "work" || record.Folder == "" || filepath.Base(record.Folder) != record.Folder || record.Folder == "." || record.Folder == ".." {
			continue
		}
		f := localCrewFolder{ID: record.ID, Root: record.LocalRoot}
		if binding, err := crewroot.ReadSync(record.ID); err == nil && binding != nil {
			f.Binding = binding
		}
		if f.Root == "" {
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
			f.Root = filepath.Join(docs, "Crew", record.Folder)
		}
		out = append(out, f)
	}
	return out, nil
}

// The copy has its own new session ID, so an existing file is never replaced.
func installTerminalJournal(store, folder string, b *sessiontransfer.Bundle) (string, error) {
	if e := b.ValidateFiles(); e != nil {
		return "", e
	}
	if e := b.ValidateJournals(); e != nil {
		return "", e
	}
	if b.Provider != "claude-code" || len(b.Files) != 1 {
		return "", fmt.Errorf("only one Claude journal can be copied")
	}
	dest, e := sessiontransfer.Path(store, "projects/"+sessiontransfer.ClaudeSlug(folder)+"/"+b.SessionID+".jsonl", true)
	if e != nil {
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
	if e = os.Link(f.Name(), dest); e != nil {
		return "", fmt.Errorf("a local Claude session with this ID already exists; nothing was overwritten")
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
		return fmt.Errorf("install and sign in to Claude Code on this laptop first")
	}
	reason = "server_refused"
	command, e := client.TerminalCommand(ctx, ref.Key)
	if e != nil {
		return e
	}
	reason = "folder_unavailable"
	hint, _ := command["local_folder"].(string)
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
	body, e := client.TerminalSession(ctx, ref.Key, "fetch")
	if e != nil {
		return e
	}
	t, e := chatterminal.Read(body, dir)
	body.Close()
	if e == nil && (t.Reference.Key != ref.Key || t.Reference.Project != ref.Project) {
		e = fmt.Errorf("chat identity mismatch")
	}
	if e != nil {
		return fmt.Errorf("the chat's session could not be verified; try again from the chat menu")
	}
	nativeID = t.Bundle.SessionID
	reason = "local_folder_unavailable"
	if current, err := terminalLocalFolder(ref, t.LocalFolder); err != nil || current != folder {
		return fmt.Errorf("the project folder changed; try again from the chat menu")
	}
	// A copy with its own session ID: the AgentWorks chat keeps working.
	reason = "copy_failed"
	copied, e := sessiontransfer.ForkJSONL(t.Bundle, filepath.Join(dir, "copy"), folder)
	if e != nil {
		return e
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	store := filepath.Join(home, ".claude")
	if configured := o.getenv("CLAUDE_CONFIG_DIR"); configured != "" {
		store = configured
	}
	if !filepath.IsAbs(store) {
		return fmt.Errorf("Claude's configuration folder must be absolute")
	}
	reason = "journal_not_written"
	if _, e = installTerminalJournal(store, folder, copied); e != nil {
		return e
	}
	fmt.Fprintf(o.stderr, "[CHAT_TERMINAL_RESUME] chat=%s session=%s action=written reason=terminal_copy\n", ref.Key, copied.SessionID)
	fmt.Fprintf(o.stderr, "This is a copy of the chat; the chat in AgentWorks keeps working on its own.\n")
	cmd := exec.Command("claude", "--resume", copied.SessionID)
	cmd.Dir = folder
	cmd.Stdin = o.stdin
	cmd.Stdout = o.stdout
	cmd.Stderr = o.stderr
	reason = "launch_failed"
	if e = cmd.Start(); e != nil {
		return e
	}
	reason = "terminal_exit_nonzero"
	return cmd.Wait()
}

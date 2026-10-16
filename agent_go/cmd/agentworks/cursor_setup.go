package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// cursorHookEvents are the Cursor agent hooks AgentWorks uses: attach and the
// other /agentworks commands (beforeSubmitPrompt), delivery when a turn ends
// (stop), reconnect and close (sessionStart, sessionEnd), and the per-call chat
// record for the messaging tools in the shared desktop app (beforeMCPExecution).
var cursorHookEvents = []string{"sessionStart", "beforeSubmitPrompt", "stop", "sessionEnd", "beforeMCPExecution"}

func setupCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "setup", Short: "Set up a coding app to work with AgentWorks: claude, codex, cursor, muse or pi"}
	cmd.AddCommand(&cobra.Command{Use: "cursor", Args: cobra.NoArgs, Short: "Set up the Cursor app for AgentWorks (every project)", Long: "Adds the AgentWorks hooks and the AgentWorks message tools to your Cursor user settings (~/.cursor/hooks.json and ~/.cursor/mcp.json), so every Cursor chat in every project can be connected with /agentworks attach. Other entries in those files are kept. Run it again any time; it only changes what is missing or outdated.", RunE: func(cmd *cobra.Command, _ []string) error {
		if runtime.GOOS == "windows" {
			return errors.New("Cursor on Windows cannot connect to AgentWorks yet. Use Claude or Codex on Windows for now")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		var backups []string
		changed, err := setupCursor(filepath.Join(home, ".cursor"), exe, &backups)
		for _, backup := range backups {
			fmt.Fprintln(o.stdout, "Saved a copy of your previous settings: "+backup)
		}
		if err != nil {
			return fmt.Errorf("Cursor setup was not completed: %w", err)
		}
		if changed {
			fmt.Fprintln(o.stdout, "Cursor is set up for AgentWorks: added the AgentWorks hooks to ~/.cursor/hooks.json and the AgentWorks message tools (agentworks-messages) to ~/.cursor/mcp.json. This applies to every project you open in Cursor.")
		} else {
			fmt.Fprintln(o.stdout, "Cursor was already set up for AgentWorks (every project).")
		}
		if line := addToolsServer(cmd.Context(), o, "cursor"); line != "" {
			fmt.Fprintln(o.stdout, line)
		}
		fmt.Fprintln(o.stdout, "Next: restart Cursor, open a chat and type /agentworks attach (or /agentworks attach NAME) as its own message.")
		fmt.Fprintln(o.stdout, "To undo: remove the entries that run agentworks from those two files (or put back a saved copy listed above), then restart Cursor.")
		fmt.Fprintln(o.stdout, "Messages from other agents reach a connected chat when it finishes a turn, and can start a turn you did not type. A chat that is idle gets them after its next turn.")
		if cwd, e := os.Getwd(); e == nil && cursorProjectHasAgentWorksHooks(filepath.Join(cwd, ".cursor", "hooks.json")) {
			fmt.Fprintln(o.stdout, "This project also has its own AgentWorks hooks from the older command-line setup. Remove the AgentWorks entries from .cursor/hooks.json here so they do not run twice.")
		}
		return nil
	}})
	var remove, noPreview, preview bool
	notifications := &cobra.Command{Use: "notifications", Args: cobra.NoArgs, Short: "Notification settings: the AgentWorks notifier app (macOS) and message previews", RunE: func(cmd *cobra.Command, _ []string) error {
		if noPreview || preview {
			line, err := setNotificationPreview(preview)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(o.stdout, line)
			return err
		}
		pick := restoreNotifierApp
		if remove {
			pick = removeNotifierApp
		}
		line, err := pick()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(o.stdout, line)
		return err
	}}
	notifications.Flags().BoolVar(&remove, "remove", false, "Delete the AgentWorks notifier app")
	notifications.Flags().BoolVar(&noPreview, "no-preview", false, "Leave message text out of notifications (they can show on a lock screen)")
	notifications.Flags().BoolVar(&preview, "preview", false, "Show the first line of a waiting message in notifications again")
	cmd.AddCommand(notifications)
	for _, app := range []struct{ provider, name, what, next, undo string }{
		{"claude", "Claude Code", "the AgentWorks plugin (agentworks@agentworks) for your user in Claude Code (~/.claude)", "Restart Claude Code, then type /agentworks attach (or /agentworks attach NAME) in a chat.", "claude plugin uninstall agentworks@agentworks --scope user"},
		{"codex", "Codex", "the AgentWorks plugin (agentworks-codex@agentworks) in your Codex profile (~/.codex), used by the Codex app and the Codex CLI", "In the Codex app, open Settings > Hooks and trust the AgentWorks hooks, then reload. In any Codex chat type /agentworks attach (or /agentworks attach NAME).", "codex plugin remove agentworks-codex@agentworks"},
		{"muse", "Muse", "the AgentWorks plugin (agentworks-muse) for your user in Muse", "Muse asks you to trust AgentWorks' hooks: approve once with muse plugins approve agentworks-muse (they tell AgentWorks which Muse chat is which; without them messaging does not work). Then start a new Muse chat and say \"connect this chat to AgentWorks\".", "muse plugins remove agentworks-muse"},
		{"pi", "Pi", "the AgentWorks Pi package for your user in Pi", "Reload Pi, then type /agentworks attach in the chat.", "pi list (find the AgentWorks entry), then pi remove <that source>"},
	} {
		app := app
		var noHooks bool
		sub := &cobra.Command{Use: app.provider, Args: cobra.NoArgs, Short: "Set up " + app.name + " for AgentWorks (every project)", Long: "Installs the AgentWorks plugin for " + app.name + " with its own plugin commands, for every project. Running it again is safe. It does not trust hooks, sign in or connect a chat; you do those next.", RunE: func(cmd *cobra.Command, _ []string) error {
			o.setupAll = true
			if noHooks && (app.provider == "claude" || app.provider == "codex") {
				installed, undo, err := installToolsPlugin(cmd.Context(), o, app.provider)
				if err != nil {
					return fmt.Errorf("%s setup was not completed: %w", app.name, err)
				}
				fmt.Fprintln(o.stdout, app.name+" is set up for AgentWorks without hooks: installed "+installed+". This applies to every project.")
				if line := addToolsServer(cmd.Context(), o, app.provider); line != "" {
					fmt.Fprintln(o.stdout, line)
				}
				fmt.Fprintln(o.stdout, "Next: restart "+app.name+", then say \"connect this chat to AgentWorks\" in a chat. Nothing is typed and nothing shows as blocked.")
				fmt.Fprintln(o.stdout, noHooksGivesUp)
				fmt.Fprintln(o.stdout, "To undo: "+undo)
				return nil
			}
			if noHooks {
				return fmt.Errorf("--no-hooks is available for Claude Code and the Codex app; %s needs its hooks", app.name)
			}
			if err := installAgentPlugin(cmd.Context(), o, app.provider); err != nil {
				return fmt.Errorf("%s setup was not completed: %w", app.name, err)
			}
			fmt.Fprintln(o.stdout, app.name+" is set up for AgentWorks: installed "+app.what+". This applies to every project.")
			if line := addToolsServer(cmd.Context(), o, app.provider); line != "" {
				fmt.Fprintln(o.stdout, line)
			}
			if app.provider == "muse" {
				offerMuseApproval(cmd.Context(), o)
			}
			fmt.Fprintln(o.stdout, "Next: "+app.next)
			fmt.Fprintln(o.stdout, "To undo: "+app.undo)
			if app.provider == "claude" {
				for _, project := range claudeProjectInstalls() {
					fmt.Fprintf(o.stdout, "AgentWorks is also installed just for the project %s, so in that project its hooks run twice. Remove that copy: cd %s && claude plugin uninstall agentworks@agentworks --scope project\n", project, shellWord(project))
				}
			}
			return nil
		}}
		if app.provider == "claude" || app.provider == "codex" {
			sub.Flags().BoolVar(&noHooks, "no-hooks", false, "Install the tools only, with no hooks (nothing shows as blocked); you ask the agent to connect")
		}
		cmd.AddCommand(sub)
	}
	return cmd
}

// setupCursor merges the AgentWorks entries into the user-level Cursor files.
func setupCursor(dir, exe string, backups *[]string) (bool, error) {
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("~/.cursor is a symlink; check it and retry")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	command := shellWord(exe) + " plugin hook cursor"
	hooksChanged, err := updateCursorJSON(filepath.Join(dir, "hooks.json"), backups, func(data map[string]any) error {
		if v, ok := data["version"]; ok && v != float64(1) {
			return errors.New("hooks.json has a version AgentWorks does not know")
		}
		data["version"] = 1
		hooks, _ := data["hooks"].(map[string]any)
		if hooks == nil {
			if _, exists := data["hooks"]; exists {
				return errors.New("the hooks field in hooks.json is not an object")
			}
			hooks = map[string]any{}
			data["hooks"] = hooks
		}
		for _, event := range cursorHookEvents {
			list, _ := hooks[event].([]any)
			if hooks[event] != nil && list == nil {
				return fmt.Errorf("the %s hooks in hooks.json are not a list", event)
			}
			kept := []any{}
			for _, item := range list {
				entry, _ := item.(map[string]any)
				line, _ := entry["command"].(string)
				if entry != nil && cursorAgentWorksHook(line) {
					continue
				}
				kept = append(kept, item)
			}
			hooks[event] = append(kept, map[string]any{"command": command, "timeout": 20})
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	mcpChanged, err := updateCursorJSON(filepath.Join(dir, "mcp.json"), backups, func(data map[string]any) error {
		servers, _ := data["mcpServers"].(map[string]any)
		if servers == nil {
			if _, exists := data["mcpServers"]; exists {
				return errors.New("the mcpServers field in mcp.json is not an object")
			}
			servers = map[string]any{}
			data["mcpServers"] = servers
		}
		servers[cursorMessagesServer] = map[string]any{"command": exe, "args": []any{"plugin", "mcp", "cursor"}}
		return nil
	})
	return hooksChanged || mcpChanged, err
}

func cursorAgentWorksHook(command string) bool {
	return strings.HasSuffix(strings.TrimSpace(command), " plugin hook cursor") || strings.Contains(command, "cursor_hook.py")
}

func cursorProjectHasAgentWorksHooks(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var data struct {
		Hooks map[string][]struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return false
	}
	for _, list := range data.Hooks {
		for _, entry := range list {
			if cursorAgentWorksHook(entry.Command) {
				return true
			}
		}
	}
	return false
}

// updateCursorJSON applies edit to one JSON object file and writes it back
// atomically (owner-only) when it changed; a file edited meanwhile is refused.
func updateCursorJSON(path string, backups *[]string, edit func(map[string]any) error) (bool, error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	prior, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	data := map[string]any{}
	if len(bytes.TrimSpace(prior)) > 0 {
		if err = json.Unmarshal(prior, &data); err != nil {
			return false, fmt.Errorf("%s is not valid JSON; fix it and retry", filepath.Base(path))
		}
	}
	if err = edit(data); err != nil {
		return false, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err = enc.Encode(data); err != nil {
		return false, err
	}
	encoded := buf.Bytes()
	if bytes.Equal(encoded, prior) {
		return false, nil
	}
	// The person's other entries are their own data: keep the previous file.
	if len(prior) > 0 {
		backup := path + ".agentworks-backup-" + time.Now().Format("20060102-150405")
		if err = os.WriteFile(backup, prior, 0o600); err != nil {
			return false, fmt.Errorf("could not keep a copy of %s: %w", filepath.Base(path), err)
		}
		*backups = append(*backups, backup)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".agentworks-*.json")
	if err != nil {
		return false, err
	}
	defer os.Remove(temp.Name())
	if err = temp.Chmod(0o600); err == nil {
		_, err = temp.Write(encoded)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return false, err
	}
	if now, e := os.ReadFile(path); (e == nil || !errors.Is(e, os.ErrNotExist)) && !bytes.Equal(now, prior) {
		return false, fmt.Errorf("%s changed while setting up; retry", filepath.Base(path))
	}
	return true, os.Rename(temp.Name(), path)
}

// shellWord quotes a path for the shell string Cursor runs as a hook command.
func shellWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// claudeProjectInstalls lists projects where the AgentWorks plugin is also
// installed with project scope (next to the every-project install).
func claudeProjectInstalls() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var data struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			ProjectPath string `json:"projectPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return nil
	}
	var out []string
	for _, entry := range data.Plugins["agentworks@agentworks"] {
		if entry.Scope == "project" && entry.ProjectPath != "" {
			out = append(out, entry.ProjectPath)
		}
	}
	return out
}

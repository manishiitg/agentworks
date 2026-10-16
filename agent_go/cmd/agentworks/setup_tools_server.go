package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

// agentworks setup <app> also gives the app the AgentWorks TOOLS server (workflows, Crews, files and
// the rest, the same server as the login), next to the messaging plugin, so one command gives a
// person both. The app asks for the sign-in itself, in the browser, the first time it uses the tools.

// toolsServerName is "agentworks" for the default login and "agentworks-<login>" for a named one, so a
// person with several logins gets one tools server per network in each app.
func toolsServerName(configPath string) string {
	if filepath.Base(filepath.Dir(configPath)) == "logins" {
		if name := strings.TrimSuffix(filepath.Base(configPath), ".json"); name != "" {
			return "agentworks-" + name
		}
	}
	return "agentworks"
}

// toolsServerURL is the tools endpoint of the saved login, and the name to register it under.
func toolsServerURL(o *options) (url, name string, err error) {
	cfg, path, err := o.connection(false)
	if err != nil {
		return "", "", err
	}
	return strings.TrimRight(cfg.Server, "/") + "/api/external/v1/mcp", toolsServerName(path), nil
}

// addToolsServer adds the tools server to one app and returns a plain line for the person.
// It never fails the setup: a missing login or an app command that errors becomes a note.
func addToolsServer(ctx context.Context, o *options, provider string) string {
	url, serverName, err := toolsServerURL(o)
	if err != nil {
		return "The AgentWorks tools were not added because there is no saved sign-in yet. Run agentworks login, then agentworks setup " + provider + " again."
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "The AgentWorks tools were not added: " + err.Error()
	}
	signIn := ""
	switch provider {
	case "claude":
		exe := providerExecutable(provider)
		if exec.CommandContext(ctx, exe, "mcp", "get", serverName).Run() == nil {
			return "The AgentWorks tools are already added to Claude Code (" + serverName + "); nothing changed."
		}
		if out, e := exec.CommandContext(ctx, exe, "mcp", "add", "--scope", "user", "--transport", "http", serverName, url).CombinedOutput(); e != nil {
			return "The AgentWorks tools were not added to Claude Code: " + strings.TrimSpace(string(out))
		}
		signIn = "In Claude Code type /mcp, pick " + serverName + " and approve it in the browser."
	case "codex":
		exe := providerExecutable(provider)
		if exec.CommandContext(ctx, exe, "mcp", "get", serverName).Run() == nil {
			return "The AgentWorks tools are already added to Codex (" + serverName + "); nothing changed."
		}
		if out, e := exec.CommandContext(ctx, exe, "mcp", "add", serverName, "--url", url).CombinedOutput(); e != nil {
			return "The AgentWorks tools were not added to Codex: " + strings.TrimSpace(string(out))
		}
		signIn = "Approve it in the browser with: codex mcp login " + serverName
	case "pi":
		if out, e := exec.CommandContext(ctx, "pi", "mcp", "list", "--json").Output(); e == nil && strings.Contains(string(out), url) {
			return "The AgentWorks tools are already added to Pi; nothing changed."
		}
		if out, e := exec.CommandContext(ctx, "pi", "mcp", "add", serverName, "--url", url).CombinedOutput(); e != nil {
			return "The AgentWorks tools were not added to Pi: " + strings.TrimSpace(string(out))
		}
		signIn = "Approve it in the browser with: pi mcp login " + serverName
	case "cursor", "muse":
		path := filepath.Join(home, ".cursor", "mcp.json")
		signIn = "Cursor asks you to approve it in the browser the first time it uses the tools."
		if provider == "muse" {
			path = filepath.Join(home, ".config", "muse", "settings.json")
			signIn = "Approve it in the browser with: muse mcp login " + serverName
		}
		changed, backup, e := mergeToolsServerURL(path, serverName, url)
		if e != nil {
			return "The AgentWorks tools were not added to " + provider + ": " + e.Error()
		}
		if !changed {
			return "The AgentWorks tools are already added to " + provider + " (" + serverName + "); nothing changed."
		}
		if backup != "" {
			fmt.Fprintln(o.stdout, "Saved a copy of your previous settings: "+backup)
		}
	default:
		return ""
	}
	return "Added the AgentWorks tools (" + serverName + " at " + url + "). " + signIn
}

// mergeToolsServerURL adds mcpServers.<name> = {"url": url} to a JSON settings file, keeping every
// other entry. It reports whether the file changed and where it saved a copy of the old one.
func mergeToolsServerURL(path, name, url string) (changed bool, backup string, err error) {
	doc := map[string]any{}
	raw, readErr := os.ReadFile(path)
	if readErr == nil {
		if err = json.Unmarshal(raw, &doc); err != nil {
			return false, "", fmt.Errorf("%s is not plain JSON, so it was left alone", path)
		}
	} else if !os.IsNotExist(readErr) {
		return false, "", readErr
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	for _, v := range servers {
		if entry, ok := v.(map[string]any); ok && entry["url"] == url {
			return false, "", nil
		}
	}
	if _, taken := servers[name]; taken {
		return false, "", fmt.Errorf("%s already has a different server named %s; remove it or rename it, then run setup again", path, name)
	}
	if readErr == nil {
		backup = path + ".agentworks-backup-" + time.Now().Format("20060102-150405")
		if err = os.WriteFile(backup, raw, 0o600); err != nil {
			return false, "", err
		}
	}
	servers[name] = map[string]any{"url": url}
	doc["mcpServers"] = servers
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, "", err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, "", err
	}
	// Write beside the file and rename, so an interrupted write never leaves half a settings file; an
	// existing file keeps its permissions.
	mode := os.FileMode(0o600)
	if info, e := os.Stat(path); e == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agentworks-*.tmp")
	if err != nil {
		return false, "", err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(append(out, '\n')); err == nil {
		err = tmp.Chmod(mode)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, "", err
	}
	return true, backup, os.Rename(tmp.Name(), path)
}

// offerMuseApproval asks whether to approve the AgentWorks plugin in Muse now (it must be trusted
// before Muse chats can report their progress). It only asks at a terminal and only runs the
// approval on a yes; otherwise it prints the one command.
func offerMuseApproval(ctx context.Context, o *options) {
	command := "muse plugins approve agentworks-muse"
	f, isFile := o.stdin.(*os.File)
	if !isFile || !term.IsTerminal(int(f.Fd())) {
		fmt.Fprintln(o.stdout, "Next: approve AgentWorks' hooks in Muse once with: "+command)
		return
	}
	fmt.Fprint(o.stdout, "Approve the AgentWorks plugin in Muse now, so Muse chats can report their progress? (y/N): ")
	var answer string
	_, _ = fmt.Fscanln(o.stdin, &answer)
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		fmt.Fprintln(o.stdout, "Not approved. Do it any time with: "+command)
		return
	}
	if out, err := exec.CommandContext(ctx, "muse", "plugins", "approve", "agentworks-muse").CombinedOutput(); err != nil {
		fmt.Fprintln(o.stdout, "Muse did not approve it: "+strings.TrimSpace(string(out))+". Run it yourself: "+command)
		return
	}
	fmt.Fprintln(o.stdout, "Approved. To undo: muse plugins remove agentworks-muse")
}

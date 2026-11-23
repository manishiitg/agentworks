package agycli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// agyIsolatedHome gives one run its own MCP catalogue and permission list.
// The catalogue contains bridge credentials, so every file is private and
// credentials never appear in agy mcp add argv or another user's settings.
// Conversations are shared with the normal AGY home for durable transcript
// reads and native --conversation resume.
func agyIsolatedHome(servers []agyMCPServer, workingDirs ...string) (string, func(), error) {
	return agyIsolatedHomeWithConversations(servers, "", workingDirs...)
}

// Confined runs persist only in the chat's durable private home. Linking to
// the server conversation directory would leave the kernel refusing SQLite
// writes; granting that shared directory would expose other chats.
func agyIsolatedHomeForCall(servers []agyMCPServer, workingDir string, opts *llmtypes.CallOptions) (string, func(), error) {
	return agyIsolatedHomeWithConversations(servers, agyTranscriptHome(opts), workingDir)
}

func agyTranscriptHome(opts *llmtypes.CallOptions) string {
	if opts != nil && opts.CLISecurity.LandlockEnforced() {
		return opts.CLISecurity.PrivateHome
	}
	home, _ := os.UserHomeDir()
	return home
}

func agyHome(accountHome ...string) (string, error) {
	if len(accountHome) > 0 && accountHome[0] != "" {
		return accountHome[0], nil
	}
	return os.UserHomeDir()
}

func agyIsolatedHomeWithConversations(servers []agyMCPServer, conversationHome string, workingDirs ...string) (string, func(), error) {
	base, err := os.UserHomeDir()
	if err != nil {
		return "", nil, err
	}
	agyPrivateHomesSweepOnce.Do(agySweepStalePrivateHomes)
	// A run as a person's slot keeps its per-run home inside that slot's own private home: one in the app's temp
	// folder never reached the slot's agy, which then fell back to a Google login ("Authentication required",
	// PLAT-872-86).
	parent, mode := "", os.FileMode(0o700)
	if conversationHome != "" && slotfs.IsSlotLaunch(conversationHome) {
		parent = filepath.Join(conversationHome, ".agy-runs")
		mode = slotfs.Mode(conversationHome, 0o700)
		if err := os.MkdirAll(parent, mode); err != nil {
			return "", nil, err
		}
	}
	home, err := os.MkdirTemp(parent, fmt.Sprintf("agentworks-agy-%d-", os.Getpid()))
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(home) }
	if err := os.Chmod(home, mode); err != nil {
		cleanup()
		return "", nil, err
	}
	baseGemini := filepath.Join(base, ".gemini")
	privateGemini := filepath.Join(home, ".gemini")
	// Copy account metadata and settings, but never the global MCP catalogue.
	// AGY may keep login metadata in either config or antigravity-cli.
	for _, subdir := range []string{"config", "antigravity-cli"} {
		if err := agyCopyHomeTree(filepath.Join(baseGemini, subdir), filepath.Join(privateGemini, subdir), map[string]bool{
			"mcp_config.json": true, "mcp": true, "conversations": true,
			// Each private home creates its own conversation index. Copying
			// an active SQLite index races with CLI checkpoint/sidecar cleanup.
			"conversation_summaries.db": true, "conversation_summaries.db-wal": true, "conversation_summaries.db-shm": true,
			"crashes": true, "updater": true, "bin": true,
			"hooks.json": true, "hooks": true,
		}); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	privateRuntime := filepath.Join(privateGemini, "antigravity-cli")
	if err := os.MkdirAll(privateRuntime, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	if conversationHome == "" {
		conversationHome = base
	}
	baseConversations := filepath.Join(conversationHome, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(baseConversations, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := os.Symlink(baseConversations, filepath.Join(privateRuntime, "conversations")); err != nil {
		cleanup()
		return "", nil, err
	}
	settingsPath := filepath.Join(privateRuntime, "settings.json")
	settings := map[string]interface{}{}
	if raw, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("parse agy settings: %w", err)
		}
	}
	if settings == nil {
		settings = map[string]interface{}{}
	}
	// Only the managed workspace hook may execute during this run. Never copy
	// user-level hooks into the private home.
	delete(settings, "hooks")
	// Same for the person's status line: it is a command from their own config,
	// and a stale one (a temp script that is gone) made every start show a
	// "Statusline Error" and never look ready (owner's Mac, 2026-10-04).
	delete(settings, "statusLine")
	// A supplied key opts this private run into Gemini API mode. This changes
	// only the private copy; the user's global login settings stay untouched.
	if os.Getenv("GEMINI_API_KEY") != "" {
		settings["modelProvider"] = "gemini"
	}
	if len(workingDirs) > 0 && workingDirs[0] != "" {
		trusted, _ := settings["trustedWorkspaces"].([]interface{})
		alreadyTrusted := false
		for _, entry := range trusted {
			if entry == workingDirs[0] {
				alreadyTrusted = true
				break
			}
		}
		if !alreadyTrusted {
			settings["trustedWorkspaces"] = append(trusted, workingDirs[0])
		}
	}
	perms, _ := settings["permissions"].(map[string]interface{})
	if perms == nil {
		perms = map[string]interface{}{}
	}
	allows, _ := perms["allow"].([]interface{})
	entries := map[string]interface{}{}
	for _, srv := range servers {
		name := fmt.Sprintf("agentworks-%s-%d-%s", agySanitizeServerName(srv.name), os.Getpid(), agyMountRandomHex())
		allows = append(allows, "mcp("+name+"/*)")
		entries[name] = map[string]interface{}{"command": srv.command, "args": srv.args, "env": srv.env}
	}
	perms["allow"] = allows
	settings["permissions"] = perms
	if err := agyWritePrivateJSON(settingsPath, settings); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := agyWritePrivateJSON(filepath.Join(privateGemini, "config", "mcp_config.json"), map[string]interface{}{"mcpServers": entries}); err != nil {
		cleanup()
		return "", nil, err
	}
	// The copied account files keep their private modes; a slot's agy could not read its own settings.json and
	// asked for a Google login. Give the slot's group the whole per-run tree.
	if parent != "" {
		if err := slotfs.ShareTree(conversationHome, home); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("give the user's account its agy home: %w", err)
		}
	}
	return home, cleanup, nil
}

func agyCopyHomeTree(src, dst string, skip map[string]bool) error {
	entries, err := os.ReadDir(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, entry := range entries {
		if skip[entry.Name()] {
			continue
		}
		from, to := filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := agyCopyHomeTree(from, to, skip); err != nil {
				return err
			}
			continue
		}
		if !entry.Type().IsRegular() {
			continue
		}
		in, err := os.Open(from)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func agyWritePrivateJSON(path string, value interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

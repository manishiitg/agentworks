package claudecode

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

// claudeLandlockArgs starts Claude Code confined to its folder when the
// policy asks for it. Beyond the policy's folders, a launch needs the files
// the adapter prepared outside them: every file named in argv (MCP config,
// settings, system prompt, status-line helper), the permission hooks, and the
// status-line file Claude writes for this session.
func claudeLandlockArgs(opts *llmtypes.CallOptions, args []string, workingDir, sessionName string) ([]string, func(), error) {
	if opts == nil || !opts.CLISecurity.Confined() {
		return args, func() {}, nil
	}
	read := claudeLandlockReads(args, workingDir)
	// Seatbelt keeps Claude's real home (its Keychain login is tied to it), so
	// there is no private home to adopt into or mirror reads for.
	if opts.CLISecurity.LandlockEnforced() {
		claudeAdoptResumedConversation(opts.CLISecurity, args, workingDir)
		if err := claudeMirrorLandlockReads(opts.CLISecurity, workingDir); err != nil {
			return nil, func() {}, err
		}
	}
	statusline := claudeStatuslinePath(sessionName)
	if f, err := os.OpenFile(statusline, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_ = f.Close()
	}
	read, write := claudeModSandboxPaths(args, sessionName, read, []string{statusline})
	if err := claudeModInboxOutsideWrites(opts.CLISecurity, workingDir, write, sessionName); err != nil {
		return nil, func() {}, err
	}
	return clisandbox.LandlockArgs(opts.CLISecurity, args, workingDir, read, write)
}

// claudeAdoptResumedConversation copies the conversation a confined launch resumes into the
// private home when only the unconfined home has it. A chat started before its CLI was confined
// kept its conversation in the server's (or account's) ~/.claude; the confined launch reads the
// private home, so --resume failed with "No conversation found" and the pane exited at once
// (RTS 2026-09-30, a Crew chat after the lock was turned on). Only this folder's own
// conversation is copied, never the rest of that home; an existing private copy is kept.
func claudeAdoptResumedConversation(policy *llmtypes.CLISecurityPolicy, args []string, workingDir string) {
	resumeID := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--resume" {
			resumeID = strings.TrimSpace(args[i+1])
		}
	}
	if resumeID == "" || strings.ContainsAny(resumeID, `/\`) || policy == nil {
		return
	}
	source := strings.TrimSpace(policy.CredentialHome)
	if source == "" {
		source, _ = os.UserHomeDir()
	}
	if source == "" || filepath.Clean(source) == filepath.Clean(policy.PrivateHome) {
		return
	}
	for _, dir := range pathidentity.Candidates(workingDir) {
		slug := claudeTranscriptProjectSlug(dir)
		if slug == "" {
			continue
		}
		target := filepath.Join(policy.PrivateHome, ".claude", "projects", slug, resumeID+".jsonl")
		if _, err := os.Stat(target); err == nil {
			return
		}
		from := filepath.Join(source, ".claude", "projects", slug, resumeID+".jsonl")
		data, err := os.ReadFile(from)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return
		}
		_ = os.WriteFile(target, data, 0o600)
		// Subagent transcripts of this conversation, when Claude kept them beside it.
		_ = copyDirIfMissing(filepath.Join(source, ".claude", "projects", slug, resumeID), filepath.Join(filepath.Dir(target), resumeID))
		return
	}
}

func copyDirIfMissing(from, to string) error {
	if _, err := os.Stat(to); err == nil {
		return nil
	}
	info, err := os.Stat(from)
	if err != nil || !info.IsDir() {
		return err
	}
	return filepath.Walk(from, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		dest := filepath.Join(to, rel)
		if fi.IsDir() {
			return os.MkdirAll(dest, 0o700)
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o600)
	})
}

// claudeMirrorLandlockReads lists the Landlock grants as Claude's additional
// directories, in the private home's user settings (Claude reads this from user
// settings only), so granted folders read normally. Reads outside them are left
// to the kernel, which refuses them.
func claudeMirrorLandlockReads(policy *llmtypes.CLISecurityPolicy, workingDir string) error {
	path := filepath.Join(policy.PrivateHome, ".claude", "settings.json")
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	permissions, _ := settings["permissions"].(map[string]any)
	if permissions == nil {
		permissions = map[string]any{}
	}
	dirs := []any{} // Claude rejects the whole settings file when this is null
	seen := map[string]bool{workingDir: true}
	for _, list := range [][]string{policy.WorkspaceReadPaths, policy.WorkspaceWritePaths, policy.HostReadPaths, policy.HostWritePaths} {
		for _, dir := range list {
			if info, err := os.Stat(dir); err == nil && info.IsDir() && !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
	}
	// Not blockReadsOutsideWorkingDirectories: with it Claude in dontAsk mode refused every Bash command it could not
	// prove stays inside the folders (an inline script, a loop, a variable path), although the built-in shell is on
	// (owner, 2026-10-15) and Landlock already confines it. Without it Claude neither asks nor refuses; the kernel
	// enforces the grants and refuses an outside read with an ordinary error.
	delete(permissions, "blockReadsOutsideWorkingDirectories")
	permissions["additionalDirectories"] = dirs
	settings["permissions"] = permissions
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// claudeLandlockReads are the files a confined Claude launch needs outside
// its granted folders: files named in argv, the MCP servers and the
// status-line helper and hook scripts those configs name, and the hooks.
func claudeLandlockReads(args []string, workingDir string) []string {
	read := clisandbox.ArgFilePaths(args)
	configs := append(append([]string(nil), read...), filepath.Join(workingDir, ".mcp.json"))
	read = append(read, clisandbox.MCPCommandPaths(configs)...)
	read = append(read, clisandbox.JSONFilePaths(configs)...)
	return append(read, filepath.Join(os.TempDir(), "claude-code-hooks"))
}

// claudeSDKLandlock confines the Claude Code process the SDK bridge starts,
// not the bridge: under Landlock a confined node cannot give its child its own
// /proc entry and Claude aborts at startup (PLAT-872-51). Seatbelt still wraps
// the whole command; its children inherit the profile and there is no /proc.
// cmd is node <bridge> <sdk.mjs> <claude> <args...>.
func claudeSDKLandlock(opts *llmtypes.CallOptions, cmd *exec.Cmd, workingDir string) (func(), error) {
	if opts == nil || !opts.CLISecurity.LandlockEnforced() {
		return claudeLandlockCmd(opts, cmd, workingDir)
	}
	if err := claudeMirrorLandlockReads(opts.CLISecurity, workingDir); err != nil {
		return func() {}, err
	}
	cli := cmd.Args[3:]
	return clisandbox.LandlockChild(opts.CLISecurity, cmd, cli, workingDir, claudeLandlockReads(cli, workingDir), nil,
		[]string{filepath.Dir(cmd.Args[2])}, []string{filepath.Dir(cmd.Args[1])})
}

// claudeLandlockCmd confines a structured (stream-json) Claude launch.
func claudeLandlockCmd(opts *llmtypes.CallOptions, cmd *exec.Cmd, workingDir string) (func(), error) {
	if opts == nil || !opts.CLISecurity.Confined() {
		return func() {}, nil
	}
	if opts.CLISecurity.LandlockEnforced() {
		if err := claudeMirrorLandlockReads(opts.CLISecurity, workingDir); err != nil {
			return func() {}, err
		}
	}
	return clisandbox.LandlockCmd(opts.CLISecurity, cmd, workingDir, claudeLandlockReads(cmd.Args, workingDir), nil)
}

// claudeSeatbeltAddDirs are the chat's granted folders as --add-dir flags, for
// a launch under Seatbelt (see clisandbox.SeatbeltGrantedDirs). Without them
// Claude, in dontAsk mode, refuses every edit through the workflow linked as
// project/ before the sandbox decides (owner test 2026-10-04).
func claudeSeatbeltAddDirs(opts *llmtypes.CallOptions, workingDir string) []string {
	if opts == nil {
		return nil
	}
	var args []string
	for _, dir := range clisandbox.SeatbeltGrantedDirs(opts.CLISecurity, workingDir) {
		args = append(args, "--add-dir", dir)
	}
	return args
}

// Policy staging and process lifetime are different: the 30-second backstop
// deletes only the policy, while unregister/kill owns the observer's final flush.
func retainClaudeSessionSandboxCleanup(sessionName, policyFile string, cleanup func(), delay time.Duration) {
	claudeInteractiveSessionRegistry.Lock()
	claudeInteractiveSessionRegistry.sandboxCleanup[sessionName] = cleanup
	claudeInteractiveSessionRegistry.Unlock()
	if policyFile != "" {
		time.AfterFunc(delay, func() { _ = os.Remove(policyFile) })
	} else {
		time.AfterFunc(delay, cleanup)
	}
}

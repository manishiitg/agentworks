package clisandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// seatbeltExec is macOS's sandbox launcher (a variable for tests).
var seatbeltExec = "/usr/bin/sandbox-exec"

// SeatbeltGrants are what one CLI needs beyond the policy's folders: files the
// adapter prepared for this launch, and the CLI's own config under the real
// home (its login and settings live there on a Mac). WritePatterns are
// regular expressions for files the CLI replaces by write-and-rename next to
// its config (e.g. ~/.claude.json.tmp.123), which a folder grant cannot name.
type SeatbeltGrants struct {
	ReadPaths     []string
	WritePaths    []string
	WritePatterns []string
}

// SeatbeltArgs wraps a coding CLI's argv so it starts under macOS sandbox-exec.
// It returns args unchanged when the policy does not ask for Seatbelt on this
// host. The person's own Mac stays open to the CLI (their home, settings,
// logins, terminal config); Seatbelt closes AgentWorks' workspace data except
// the folders this chat is granted, refuses the workflow's blocked paths, and
// blocks the ways out of the sandbox: starting apps (open, LaunchServices) and
// scripting them (osascript, Apple Events).
func SeatbeltArgs(policy *llmtypes.CLISecurityPolicy, args []string, workingDir string, grants SeatbeltGrants) ([]string, func(), error) {
	noop := func() {}
	if !policy.SeatbeltEnforced() {
		return args, noop, nil
	}
	if len(args) == 0 {
		return nil, noop, errors.New("coding CLI launch command is empty")
	}
	if _, err := os.Stat(seatbeltExec); err != nil {
		return nil, noop, fmt.Errorf("%w: sandbox-exec unavailable: %w", ErrUnsupported, err)
	}
	home := canonical(policy.PrivateHome)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, noop, fmt.Errorf("create CLI sandbox folder: %w", err)
	}
	if strings.TrimSpace(policy.Provider) == "codex-cli" {
		if err := prepareSeatbeltCodexHome(policy, args, home); err != nil {
			return nil, noop, err
		}
	}
	profile := seatbeltProfile(policy, args, workingDir, grants)
	path := filepath.Join(home, "agentworks-cli-seatbelt.sb")
	// A stable file, replaced atomically: sandbox-exec reads it as the CLI
	// starts, which may be after a tmux launch returns, so it is never deleted.
	tmp, err := os.CreateTemp(home, ".agentworks-cli-seatbelt-*")
	if err != nil {
		return nil, noop, fmt.Errorf("create CLI Seatbelt profile: %w", err)
	}
	_, err = tmp.WriteString(profile)
	if err == nil {
		err = tmp.Chmod(0o600)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return nil, noop, fmt.Errorf("write CLI Seatbelt profile: %w", err)
	}
	wrapped := append([]string{seatbeltExec, "-f", path, "/usr/bin/env"}, args...)
	return wrapped, noop, nil
}

// SeatbeltCmd confines a structured-transport launch (exec.Cmd) the same way.
func SeatbeltCmd(policy *llmtypes.CLISecurityPolicy, cmd *exec.Cmd, workingDir string, grants SeatbeltGrants) (func(), error) {
	if !policy.SeatbeltEnforced() {
		return func() {}, nil
	}
	if cmd == nil || len(cmd.Args) == 0 {
		return func() {}, errors.New("coding CLI command is empty")
	}
	if workingDir == "" {
		workingDir = cmd.Dir
	}
	args := append([]string{cmd.Path}, cmd.Args[1:]...)
	grants.ReadPaths = append(grants.ReadPaths, ArgFilePaths(args)...)
	wrapped, cleanup, err := SeatbeltArgs(policy, args, workingDir, grants)
	if err != nil {
		return func() {}, err
	}
	cmd.Path = wrapped[0]
	cmd.Args = wrapped
	return cleanup, nil
}

// seatbeltEscapes are programs that hand work to processes outside the
// sandbox (apps started by LaunchServices, scripted apps), so nothing the CLI
// does through them would be confined.
var seatbeltEscapes = []string{"/usr/bin/open", "/usr/bin/osascript", "/usr/bin/osacompile", "/usr/bin/automator", "/usr/bin/shortcuts"}

func seatbeltProfile(policy *llmtypes.CLISecurityPolicy, args []string, workingDir string, grants SeatbeltGrants) string {
	write := []string{workingDir, policy.PrivateHome}
	write = append(write, policy.WorkspaceWritePaths...)
	write = append(write, policy.HostWritePaths...)
	write = append(write, grants.WritePaths...)
	read := append([]string{}, write...)
	read = append(read, policy.WorkspaceReadPaths...)
	read = append(read, policy.HostReadPaths...)
	read = append(read, grants.ReadPaths...)
	read = append(read, executableDirs(args)...)
	read, write = canonicalUnique(read), canonicalUnique(write)

	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	// AgentWorks' workspace data is closed; the grants below reopen exactly
	// what this launch may use. Later rules win, so the order matters.
	protected := canonicalUnique(policy.ProtectedRoots)
	for _, root := range protected {
		b.WriteString(`(deny file-read* file-write* (subpath "` + sandboxQuote(root) + "\"))\n")
	}
	if len(protected) > 0 {
		ancestors := ancestorPaths(append(append([]string(nil), read...), write...))
		b.WriteString("(allow file-read-metadata\n")
		for _, path := range ancestors {
			b.WriteString(`  (literal "` + sandboxQuote(path) + "\")\n")
		}
		b.WriteString(")\n")
		// Muse walks up from its working folder looking for project sources
		// (.mcp.json, rules) and must be able to read every folder on the way.
		// Refused, the whole MCP startup fails ("source_reservation") and the
		// chat has no bridge. A literal grant lists names only; nothing inside
		// the closed folders becomes readable.
		if strings.TrimSpace(policy.Provider) == "muse-cli" && len(ancestors) > 0 {
			b.WriteString("(allow file-read-data\n")
			for _, path := range ancestors {
				b.WriteString(`  (literal "` + sandboxQuote(path) + "\")\n")
			}
			b.WriteString(")\n")
		}
		if len(read) > 0 {
			b.WriteString("(allow file-read*\n")
			for _, path := range read {
				b.WriteString(`  (subpath "` + sandboxQuote(path) + "\")\n")
			}
			b.WriteString(")\n")
		}
		if len(write) > 0 || len(grants.WritePatterns) > 0 {
			b.WriteString("(allow file-read* file-write*\n")
			for _, path := range write {
				b.WriteString(`  (subpath "` + sandboxQuote(path) + "\")\n")
			}
			for _, pattern := range grants.WritePatterns {
				b.WriteString(`  (regex #"` + strings.ReplaceAll(pattern, `"`, `\"`) + "\")\n")
			}
			b.WriteString(")\n")
		}
	}
	// No way out of the sandbox through another process.
	b.WriteString("(deny process-exec\n")
	for _, path := range seatbeltEscapes {
		b.WriteString(`  (literal "` + path + "\")\n")
	}
	b.WriteString(")\n(deny appleevent-send)\n")
	b.WriteString(`(deny mach-lookup (global-name "com.apple.coreservices.launchservicesd"))` + "\n")
	// Blocked paths last, so they win over the folder grants above.
	for _, path := range canonicalUnique(policy.BlockedWritePaths) {
		b.WriteString(`(deny file-write* (subpath "` + sandboxQuote(path) + "\"))\n")
	}
	for _, path := range canonicalUnique(policy.BlockedPaths) {
		b.WriteString(`(deny file-read* file-write* (subpath "` + sandboxQuote(path) + "\"))\n")
	}
	return b.String()
}

// prepareSeatbeltCodexHome builds the CODEX_HOME a Seatbelt-confined Codex runs
// with (see llmtypes.SandboxHomeEnvironment): the folder itself, the person's
// login linked in (never copied: logins rotate refresh tokens, so every session
// shares the one file), and the native session a resumed chat continues, which
// an earlier launch kept in the person's own ~/.codex.
func prepareSeatbeltCodexHome(policy *llmtypes.CLISecurityPolicy, args []string, home string) error {
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		return fmt.Errorf("create Codex home: %w", err)
	}
	adoptResumedSession(policy, args)
	if _, err := linkCredentialFiles(policy, home); err != nil {
		return err
	}
	return nil
}

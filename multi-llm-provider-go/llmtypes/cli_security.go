package llmtypes

import (
	"path/filepath"
	"runtime"
	"strings"
)

// CLISecurityMode describes how a coding CLI may access host filesystem state.
// Compatibility is the backward-compatible default for callers that do not
// explicitly select a mode.
type CLISecurityMode string

const (
	CLISecurityModeCompatibility CLISecurityMode = "compatibility"
	// CLISecurityModeIsolated is a MAJOR BLOCKER for shipping this as a real
	// product feature (both AgentWorks and SparkQuill/family-server let the
	// user freely switch the coding CLI provider — Claude Code, Codex CLI,
	// Cursor CLI, Pi CLI — at any time via settings), for two separate reasons:
	//
	//  1. Only codex-cli has real enforcement (internal/clisandbox.PrepareCodexCommand,
	//     macOS sandbox-exec). Claude Code, Cursor CLI, and Pi CLI all call
	//     ValidateCLISecurityLaunch with zero enforced modes, so requesting
	//     Isolated for them fails closed (safe) but breaks the product outright —
	//     there is no fallback/degrade path, just a raw error.
	//  2. Credentials held in the macOS login keychain cannot be granted
	//     selectively. Denying the home directory also denies
	//     ~/Library/Keychains/login.keychain-db, so keychain lookups fail
	//     outright — measured, not assumed: `security find-generic-password -s
	//     "Claude Code-credentials"` succeeds unsandboxed and returns "could not
	//     be found" under a profile that denies home. Claude Code stores its
	//     session ONLY there (no token file anywhere under ~/.claude/), so it
	//     simply cannot authenticate under either strict mode. Granting it back
	//     is all-or-nothing: that one file also holds every other secret the
	//     user owns (Wi-Fi, Safari passwords, ...), and filesystem ACLs cannot
	//     expose a single keychain item. This is unlike ~/.codex/auth.json,
	//     which is a scoped, single-purpose file safe to grant via
	//     HostReadPaths. Cursor CLI is a hybrid — tokens in the keychain
	//     ("cursor-access-token"/"cursor-refresh-token") but file-based account
	//     identity in ~/.cursor/cli-config.json — and is untested.
	//
	// NOTE ON MODE CHOICE: this reason does NOT argue for Isolated over
	// Verified. Isolated (private $HOME + a fresh login inside it) buys ACCOUNT
	// separation, which is rarely the goal — a user normally WANTS the CLI on
	// their own subscription. The usual goal is FILESYSTEM isolation, and
	// Verified serves it directly: keep the real $HOME, deny it wholesale, then
	// grant back only the CLI's own credential directory. Reach for Verified
	// first; Isolated only when a genuinely separate account is required.
	//
	// A product cannot honestly offer either strict mode until per-provider
	// sandbox enforcement exists for all four CLIs (reason 1), and Claude Code
	// additionally needs a credential path that is not the shared login
	// keychain — otherwise a separate OS user account, whose keychain is its
	// own. Tracked at a product level in GitHub issue coding-agent-loop#142
	// ("certify Claude Code, Cursor CLI, and Pi independently" is listed there
	// as separate, not-yet-done follow-up work).
	CLISecurityModeIsolated CLISecurityMode = "isolated"
	CLISecurityModeVerified CLISecurityMode = "verified"
)

// NormalizeCLISecurityMode returns the canonical mode. Empty and unknown values
// resolve to compatibility so adding this field cannot break existing callers.
func NormalizeCLISecurityMode(mode CLISecurityMode) CLISecurityMode {
	switch CLISecurityMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case CLISecurityModeIsolated:
		return CLISecurityModeIsolated
	case CLISecurityModeVerified:
		return CLISecurityModeVerified
	default:
		return CLISecurityModeCompatibility
	}
}

// CLISecurityPolicy is the immutable, resolved launch policy passed to a coding
// provider. It is produced by trusted application code; model/tool arguments
// must never be decoded directly into this type.
type CLISecurityPolicy struct {
	Mode     CLISecurityMode `json:"mode"`
	Provider string          `json:"provider"`
	// OwnLoginEnv, set by a launch whose login variables depend on the turn (Pi: the chosen model's provider), is
	// the only login variables the CLI keeps (PLAT-872-91). Empty: the per-provider list in clisandbox applies.
	OwnLoginEnv          []string `json:"own_login_env,omitempty"`
	ProfileVersion       string   `json:"profile_version,omitempty"`
	WorkspaceReadPaths   []string `json:"workspace_read_paths,omitempty"`
	WorkspaceWritePaths  []string `json:"workspace_write_paths,omitempty"`
	HostReadPaths        []string `json:"host_read_paths,omitempty"`
	HostWritePaths       []string `json:"host_write_paths,omitempty"`
	EnvironmentVariables []string `json:"environment_variables,omitempty"`
	PrivateHome          string   `json:"private_home,omitempty"`
	ApprovedCapabilities []string `json:"approved_capabilities,omitempty"`
	// LandlockRunner is the host's Landlock launcher (absolute path). With a
	// strict mode on Linux, the CLI starts under it: it can write only the
	// workspace write paths and PrivateHome, read only the granted paths and
	// the launcher's system baseline, and everything else is refused by the
	// kernel for the CLI and every process it starts.
	LandlockRunner string `json:"landlock_runner,omitempty"`
	// CredentialHome is where the CLI's login lives: the provider account's
	// home, or the server's HOME for the server account. Only the CLI's own
	// credential files are linked from it into PrivateHome; the rest of that
	// home (other people's sessions and history) stays outside the sandbox.
	CredentialHome string `json:"credential_home,omitempty"`
	// CredentialEnv is the account's path environment (XDG_CONFIG_HOME,
	// CODEX_HOME, CLAUDE_CONFIG_DIR) when it keeps logins somewhere other
	// than under CredentialHome; empty for the server account, whose own
	// process environment says where its logins are.
	CredentialEnv map[string]string `json:"credential_env,omitempty"`
	// Seatbelt asks for macOS sandbox-exec confinement (a person's own Mac,
	// which has no Landlock). The CLI keeps its real home, so its login (the
	// macOS Keychain entry is tied to its config folder) still works; the
	// profile limits reads and writes under /Users to the granted folders and
	// the CLI's own config.
	Seatbelt bool `json:"seatbelt,omitempty"`
	// BlockedPaths may be neither read nor written, and BlockedWritePaths not
	// written, even inside a granted folder (a workflow's planning/, its raw
	// database). Seatbelt enforces them; Landlock cannot carve them out, so on
	// Linux the bridge tools still do.
	BlockedPaths      []string `json:"blocked_paths,omitempty"`
	BlockedWritePaths []string `json:"blocked_write_paths,omitempty"`
	// ProtectedRoots are folders the CLI may use only where a grant reopens
	// them (AgentWorks' workspace data: other workflows, users, config). On
	// Linux the lock denies everything outside the grants anyway; Seatbelt on a
	// Mac leaves the rest of the person's home open and closes these.
	ProtectedRoots []string `json:"protected_roots,omitempty"`
	// RunAs is the application's explicit decision of the account this launch runs as (see RunAs).
	RunAs RunAs `json:"run_as,omitempty"`
}

// SeatbeltEnforced reports whether this policy confines the CLI with macOS
// sandbox-exec on this host.
func (p *CLISecurityPolicy) SeatbeltEnforced() bool {
	return p != nil && runtime.GOOS == "darwin" && p.Seatbelt &&
		strings.TrimSpace(p.PrivateHome) != "" && NormalizeCLISecurityMode(p.Mode) != CLISecurityModeCompatibility
}

// Confined reports whether the CLI starts under a kernel-enforced sandbox on
// this host (Landlock on Linux, Seatbelt on macOS).
func (p *CLISecurityPolicy) Confined() bool {
	return p.LandlockEnforced() || p.SeatbeltEnforced()
}

// LandlockEnforced reports whether this policy confines the CLI with the
// Landlock launcher on this host.
func (p *CLISecurityPolicy) LandlockEnforced() bool {
	return p != nil && runtime.GOOS == "linux" && strings.TrimSpace(p.LandlockRunner) != "" &&
		strings.TrimSpace(p.PrivateHome) != "" && NormalizeCLISecurityMode(p.Mode) != CLISecurityModeCompatibility
}

// Clone returns a deep copy so a running session cannot observe later mutations
// to configuration slices owned by the caller.
func (p CLISecurityPolicy) Clone() CLISecurityPolicy {
	copyPolicy := p
	copyPolicy.Mode = NormalizeCLISecurityMode(p.Mode)
	copyPolicy.Provider = strings.ToLower(strings.TrimSpace(p.Provider))
	copyPolicy.ProfileVersion = strings.TrimSpace(p.ProfileVersion)
	copyPolicy.PrivateHome = strings.TrimSpace(p.PrivateHome)
	copyPolicy.WorkspaceReadPaths = append([]string(nil), p.WorkspaceReadPaths...)
	copyPolicy.WorkspaceWritePaths = append([]string(nil), p.WorkspaceWritePaths...)
	copyPolicy.HostReadPaths = append([]string(nil), p.HostReadPaths...)
	copyPolicy.HostWritePaths = append([]string(nil), p.HostWritePaths...)
	copyPolicy.EnvironmentVariables = append([]string(nil), p.EnvironmentVariables...)
	copyPolicy.ApprovedCapabilities = append([]string(nil), p.ApprovedCapabilities...)
	copyPolicy.LandlockRunner = strings.TrimSpace(p.LandlockRunner)
	copyPolicy.CredentialHome = strings.TrimSpace(p.CredentialHome)
	copyPolicy.BlockedPaths = append([]string(nil), p.BlockedPaths...)
	copyPolicy.BlockedWritePaths = append([]string(nil), p.BlockedWritePaths...)
	copyPolicy.ProtectedRoots = append([]string(nil), p.ProtectedRoots...)
	if p.CredentialEnv != nil {
		copyPolicy.CredentialEnv = make(map[string]string, len(p.CredentialEnv))
		for key, value := range p.CredentialEnv {
			copyPolicy.CredentialEnv[key] = value
		}
	}
	return copyPolicy
}

// SandboxHomeEnvironment is the CLI's private home layout when the Landlock
// launcher confines it, else nil. It replaces the account's (or server's)
// home for the CLI and for every server-side reader of the CLI's files.
func SandboxHomeEnvironment(opts *CallOptions) map[string]string {
	if opts != nil && opts.CLISecurity.SeatbeltEnforced() && strings.EqualFold(strings.TrimSpace(opts.CLISecurity.Provider), "codex-cli") {
		// A Mac keeps the person's own home open to every CLI, but Codex reads its
		// config, MCP servers, plugins and profile files from CODEX_HOME: left at
		// the person's own ~/.codex it loaded their personal MCP servers next to the
		// platform's, and could not see the session profile that carries the
		// platform's tools. Codex gets a CODEX_HOME of its own; the login is linked in.
		return map[string]string{"CODEX_HOME": filepath.Join(filepath.Clean(opts.CLISecurity.PrivateHome), ".codex")}
	}
	if opts == nil || !opts.CLISecurity.LandlockEnforced() {
		return nil
	}
	home := filepath.Clean(opts.CLISecurity.PrivateHome)
	return map[string]string{
		"HOME":              home,
		"XDG_CONFIG_HOME":   filepath.Join(home, ".config"),
		"XDG_DATA_HOME":     filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":    filepath.Join(home, ".local", "state"),
		"CODEX_HOME":        filepath.Join(home, ".codex"),
		"CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"),
		// The shared /tmp is outside the sandbox; the CLI's temp files live
		// in its private home.
		"TMPDIR": filepath.Join(home, "tmp"),
	}
}

// ConfinedModes lists the policy's mode when the CLI is confined on this
// host (the Landlock launcher on Linux, Seatbelt on a Mac), for
// ValidateCLISecurityLaunch: every adapter wraps its launch through
// clisandbox.LandlockArgs/LandlockCmd, which apply whichever applies, and a
// strict mode that cannot be confined is still refused.
func ConfinedModes(opts *CallOptions) []CLISecurityMode {
	if opts == nil || !opts.CLISecurity.Confined() {
		return nil
	}
	return []CLISecurityMode{NormalizeCLISecurityMode(opts.CLISecurity.Mode)}
}

// CLIHomeEnvironment is where the CLI keeps its files for this call: the
// sandbox's private home when confined, else the provider account's paths
// (empty for the server account, meaning the process HOME).
func CLIHomeEnvironment(opts *CallOptions) map[string]string {
	if env := SandboxHomeEnvironment(opts); env != nil {
		return env
	}
	return ProviderAccountEnvironment(opts)
}

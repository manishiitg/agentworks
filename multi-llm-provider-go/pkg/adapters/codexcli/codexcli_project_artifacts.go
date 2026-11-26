package codexcli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// writeCodexProjectArtifacts is the unified opt-in writer invoked by
// the adapter when MetadataKeyWriteProjectInstructionFile is set. It
// projects these artifacts into <workingDir>, removed on teardown:
//
//   - <workingDir>/AGENTS.md           — per-session system prompt
//   - <workingDir>/.codex/config.toml  — AgentWorks session defaults plus
//     optional [mcp_servers.NAME] tables
//   - <workingDir>/.codex/hooks.json   — PreToolUse deny entry
//     pointing at the deny script
//   - <workingDir>/.codex/hooks/deny-builtin.sh — POSIX deny script
//     that exits 2 on built-in
//     tool invocations
//
// The returned cleanup is a composite: it walks each captured cleanup in
// REVERSE order (LIFO) so nested files are removed before their parent
// directories are considered for removal of empty ancestors we created.
//
// Best-effort by design: a write failure for any single artifact does
// not block the session — the primary injection paths
// (-c model_instructions_file, -c overrides for MCP) already carry the
// configuration. The workspace projection is additive belt-and-
// suspenders, useful when downstream tooling reads codex's
// project-scoped files directly.
func writeCodexProjectArtifacts(workingDir, systemPrompt, mcpServersJSON string, denyBuiltins bool) (func(), error) {
	noop := func() {}
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return noop, nil
	}

	var cleanups []func()
	rollback := func() {
		// LIFO: restore most-recently-written artifact first.
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	if strings.TrimSpace(systemPrompt) != "" {
		cu, err := writeCodexProjectAgentsFile(workingDir, systemPrompt)
		if err != nil {
			rollback()
			return noop, fmt.Errorf("codex AGENTS.md: %w", err)
		}
		if cu != nil {
			cleanups = append(cleanups, cu)
		}
	}

	cu, err := writeCodexProjectConfigTOML(workingDir, mcpServersJSON)
	if err != nil {
		rollback()
		return noop, fmt.Errorf("codex .codex/config.toml: %w", err)
	}
	if cu != nil {
		cleanups = append(cleanups, cu)
	}

	// denyBuiltins is intentionally unused for codex. The hooks-file
	// projection (.codex/hooks.json + deny script) was REMOVED because
	// codex already has first-class --disable <feature> CLI flags for
	// every built-in tool we'd want to block. The canonical deny-builtin
	// path on codex is appendCodexDisabledFeatureArgs with
	// codexBridgeOnlyDisabledFeatures (in options.go) — that list covers
	// shell_tool, apply_patch via patch tool, unified_exec, tool_search,
	// multi_agent, apps, browser_use, computer_use,
	// workspace_dependencies, hooks, plugins, unavailable_dummy_tools,
	// image_generation.
	// Passing those as flags is strictly cleaner than dropping a hook
	// script: no SHA-keyed trust prompt to dismiss, no
	// MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS gating, no per-session
	// auto-dismiss flakiness. The denyBuiltins parameter is retained on
	// the function signature for API symmetry with the other adapters
	// (gemini, cursor) but is a no-op here. Operators who want
	// deny-builtin behavior on codex should call WithDisableShellTool /
	// WithDisableFeatures via the adapter options instead.
	_ = denyBuiltins

	// Final teardown: nuke the whole .codex/ tree. Registered LAST so it
	// fires FIRST in LIFO order, making the earlier per-file restore
	// callbacks no-ops on already-gone files. The intent is a clean wipe
	// between sessions — orphaned config.toml from a prior session whose
	// cleanup callback didn't fire would otherwise leak. Trade-off: an
	// operator's own pre-existing content under .codex/ is destroyed.
	// AGENTS.md (workingDir root, outside .codex/) is a per-session block,
	// so an operator AGENTS.md survives.
	cleanups = append(cleanups, func() {
		_ = os.RemoveAll(filepath.Join(workingDir, ".codex"))
	})

	return rollback, nil
}

// writeCodexProjectConfigTOML writes the AgentWorks-scoped Codex defaults and
// optionally projects operator-supplied MCP server JSON into
// <workingDir>/.codex/config.toml, removed on cleanup.
//
// JSON shape expected (matches WithMCPServers): a map of server name
// → {command: string, args?: []string, env?: map[string]string,
//
//	env_vars?: []string, startup_timeout_sec?: int,
//	tool_timeout_sec?: int, enabled?: bool}.
//
// The TOML emitter is intentionally minimal — codex MCP server
// definitions have a known small shape (per
// developers.openai.com/codex/mcp), so we avoid pulling in a TOML
// dependency.
func writeCodexProjectConfigTOML(workingDir, mcpServersJSON string) (func(), error) {
	noop := func() {}
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return noop, nil
	}

	var servers map[string]codexMCPServerSpec
	if strings.TrimSpace(mcpServersJSON) != "" {
		if err := json.Unmarshal([]byte(mcpServersJSON), &servers); err != nil {
			return noop, fmt.Errorf("parse MCP servers JSON: %w", err)
		}
	}

	codexDir := filepath.Join(workingDir, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		return noop, fmt.Errorf("create .codex dir: %w", err)
	}
	codexDirCreatedByUs := dirIsEmptyOrJustCreated(codexDir)

	path := filepath.Join(codexDir, "config.toml")

	toml := renderCodexProjectConfigTOML(servers)
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		return noop, fmt.Errorf("write .codex/config.toml: %w", err)
	}

	return func() {
		_ = os.Remove(path)
		if codexDirCreatedByUs {
			_ = os.Remove(codexDir) // best-effort, no-op if other files landed
		}
	}, nil
}

// writeCodexProjectDenyBuiltinHooks was REMOVED. It used to write
// <workingDir>/.codex/hooks.json + .codex/hooks/deny-builtin.sh to
// implement deny-builtin-tools via codex's PreToolUse hook contract.
// Dropping those files triggered codex's interactive hook trust review
// screen on first invocation per hook-content SHA, which the tmux
// adapter couldn't reliably auto-dismiss across codex's two-form
// prompt sequence (see commit 367291d for the partial auto-dismiss).
//
// The cleaner path is codex's first-class --disable <feature> CLI
// flags: appendCodexDisabledFeatureArgs in options.go applies the
// codexBridgeOnlyDisabledFeatures list (shell_tool, unified_exec,
// tool_search, multi_agent, apps, browser_use, computer_use,
// workspace_dependencies, hooks, plugins,
// unavailable_dummy_tools, etc.) when the caller asks for MCP-only
// routing. Flags don't trigger any trust prompts, don't need
// SHA-keyed caching, and work on the first invocation in a fresh
// tempdir. There's no reason to use a hook script when CLI flags
// already cover the use case.
//
// Operators who want deny-builtin behavior on codex should call
// WithDisableShellTool / WithDisableFeatures via the adapter options
// instead of relying on a workspace-dropped hook file.

// codexMCPServerSpec mirrors codex's MCP server schema from
// developers.openai.com/codex/mcp. Fields are optional except command.
type codexMCPServerSpec struct {
	Command                  string            `json:"command"`
	Args                     []string          `json:"args,omitempty"`
	Env                      map[string]string `json:"env,omitempty"`
	EnvVars                  []string          `json:"env_vars,omitempty"`
	StartupTimeoutSec        *int              `json:"startup_timeout_sec,omitempty"`
	ToolTimeoutSec           *int              `json:"tool_timeout_sec,omitempty"`
	Enabled                  *bool             `json:"enabled,omitempty"`
	DefaultToolsApprovalMode string            `json:"default_tools_approval_mode,omitempty"`
}

// strictCodexMCPRuntimePaths returns the orchestrator-provided bridge
// executable that must remain readable inside the macOS sandbox. Strict modes
// intentionally accept only the single AgentWorks api-bridge server; allowing
// arbitrary MCP commands here would turn MCP configuration into a path-grant
// escape hatch.
func strictCodexMCPRuntimePaths(opts *llmtypes.CallOptions) ([]string, error) {
	if opts == nil || opts.CLISecurity == nil ||
		llmtypes.NormalizeCLISecurityMode(opts.CLISecurity.Mode) == llmtypes.CLISecurityModeCompatibility {
		return nil, nil
	}
	if opts.Metadata == nil || opts.Metadata.Custom == nil {
		return nil, fmt.Errorf("strict Codex CLI security requires the AgentWorks api-bridge")
	}
	raw, _ := opts.Metadata.Custom[MetadataKeyMCPServers].(string)
	if strings.TrimSpace(raw) == "" {
		// No MCP servers at all: nothing to grant, and nothing to escape through.
		return nil, nil
	}
	var servers map[string]codexMCPServerSpec
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		return nil, fmt.Errorf("parse strict Codex MCP servers: %w", err)
	}
	if len(servers) != 1 {
		return nil, fmt.Errorf("strict Codex CLI security permits only the AgentWorks api-bridge")
	}
	bridge, ok := servers["api-bridge"]
	if !ok {
		return nil, fmt.Errorf("strict Codex CLI security requires the AgentWorks api-bridge")
	}
	command := strings.TrimSpace(bridge.Command)
	if !filepath.IsAbs(command) {
		return nil, fmt.Errorf("strict Codex api-bridge command must be an absolute path")
	}
	resolved, err := filepath.EvalSymlinks(command)
	if err != nil {
		return nil, fmt.Errorf("resolve strict Codex api-bridge command: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("inspect strict Codex api-bridge command: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("strict Codex api-bridge command is not executable")
	}
	return []string{resolved}, nil
}

// writeCodexSessionMCPProfile materializes per-invocation MCP configuration in
// a unique profile under CODEX_HOME. Codex has no --config-file flag, while
// -c mcp_servers.* overrides put every environment value (including schemas
// and secrets) on the tmux command line. A profile keeps argv bounded, remains
// isolated across concurrent agents, and preserves the user's normal
// CODEX_HOME authentication/session state. autoApproveTools also writes the
// MCP-specific approval mode because Codex's global approval policy alone does
// not suppress its per-tool MCP confirmation dialog.
func writeCodexSessionMCPProfile(mcpServersJSON string, autoApproveTools bool, policy *llmtypes.CLISecurityPolicy, accountRoot ...string) (string, func(), error) {
	noop := func() {}
	if strings.TrimSpace(mcpServersJSON) == "" {
		return "", noop, nil
	}

	var servers map[string]codexMCPServerSpec
	if err := json.Unmarshal([]byte(mcpServersJSON), &servers); err != nil {
		return "", noop, fmt.Errorf("parse session MCP servers JSON: %w", err)
	}
	if len(servers) == 0 {
		return "", noop, fmt.Errorf("session MCP servers JSON is empty")
	}
	if autoApproveTools {
		for name, spec := range servers {
			if strings.TrimSpace(spec.DefaultToolsApprovalMode) == "" {
				spec.DefaultToolsApprovalMode = "approve"
				servers[name] = spec
			}
		}
	}

	codexHome := ""
	if policy != nil && llmtypes.NormalizeCLISecurityMode(policy.Mode) != llmtypes.CLISecurityModeCompatibility {
		if llmtypes.NormalizeCLISecurityMode(policy.Mode) == llmtypes.CLISecurityModeIsolated {
			if strings.TrimSpace(policy.PrivateHome) == "" {
				return "", noop, fmt.Errorf("isolated CLI security requires a private home")
			}
			codexHome = filepath.Join(policy.PrivateHome, ".codex")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", noop, fmt.Errorf("resolve Codex home: %w", err)
			}
			codexHome = filepath.Join(home, ".codex")
		}
	} else {
		codexHome = strings.TrimSpace(os.Getenv("CODEX_HOME"))
	}
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", noop, fmt.Errorf("resolve Codex home: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	if len(accountRoot) > 0 && accountRoot[0] != "" {
		codexHome = accountRoot[0]
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return "", noop, fmt.Errorf("create Codex home: %w", err)
	}
	sweepStaleCodexProfiles(codexHome)

	profileFile, err := os.CreateTemp(codexHome, "agentworks-*.config.toml")
	if err != nil {
		return "", noop, fmt.Errorf("create Codex session MCP profile: %w", err)
	}
	path := profileFile.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := profileFile.WriteString(renderCodexConfigTOML(servers, personalCodexMCPServers(codexHome))); err != nil {
		_ = profileFile.Close()
		cleanup()
		return "", noop, fmt.Errorf("write Codex session MCP profile: %w", err)
	}
	if err := profileFile.Close(); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("close Codex session MCP profile: %w", err)
	}

	base := filepath.Base(path)
	profileName := strings.TrimSuffix(base, ".config.toml")
	return profileName, cleanup, nil
}

// renderCodexProjectConfigTOML emits AgentWorks' project-scoped Codex defaults
// followed by optional [mcp_servers.NAME] blocks. These defaults keep personal
// CLI UI, connector, plugin-discovery, and /fast choices out of automated
// AgentWorks sessions while still allowing explicit per-invocation -c
// overrides to win.
func renderCodexProjectConfigTOML(servers map[string]codexMCPServerSpec) string {
	return renderCodexConfigTOML(servers, nil)
}

// renderCodexConfigTOML is renderCodexProjectConfigTOML plus "enabled = false"
// for each name in personal: the person's own MCP servers, which Codex loads
// from their config next to the bridge. A bare enabled=false is valid only
// layered over a config that defines the server, so the names must come from
// the Codex home this session actually uses.
func renderCodexConfigTOML(servers map[string]codexMCPServerSpec, personal []string) string {
	var b strings.Builder
	b.WriteString("# mlp-session: orchestrator-generated Codex project config.\n")
	b.WriteString("# Auto-removed at session cleanup. Pre-existing content is byte-restored.\n\n")
	b.WriteString("# Do not inherit a user-level /fast choice into AgentWorks workflows.\n")
	b.WriteString("service_tier = \"default\"\n")
	b.WriteString("# Avoid update checks and make automated tmux input deterministic.\n")
	b.WriteString("check_for_update_on_startup = false\n")
	b.WriteString("disable_paste_burst = true\n\n")
	b.WriteString("[tui]\n")
	b.WriteString("notifications = false\n")
	b.WriteString("animations = false\n")
	b.WriteString("show_tooltips = false\n\n")
	b.WriteString("# Personal ChatGPT connectors are outside the AgentWorks tool boundary.\n")
	b.WriteString("[apps._default]\n")
	b.WriteString("enabled = false\n\n")
	b.WriteString("# Do not discover remote plugins or auto-install skill MCP dependencies.\n")
	b.WriteString("[features]\n")
	b.WriteString("remote_plugin = false\n")
	b.WriteString("skill_mcp_dependency_install = false\n\n")

	// The person's own MCP servers (their ~/.codex/config.toml: other
	// AgentWorks servers, computer use, REPLs) are outside the AgentWorks tool
	// boundary. Under Seatbelt Codex keeps that home for its login, so they
	// loaded next to the bridge: a chat called another AgentWorks server and
	// reported that its workflow did not exist (owner test 2026-10-04).
	var off []string
	for _, name := range personal {
		if _, ours := servers[name]; !ours {
			off = append(off, name)
		}
	}
	if len(off) > 0 {
		b.WriteString("# The person's own MCP servers stay off in AgentWorks sessions.\n")
		for _, name := range off {
			fmt.Fprintf(&b, "[mcp_servers.%s]\nenabled = false\n\n", tomlQuoteKey(name))
		}
	}

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		spec := servers[name]
		fmt.Fprintf(&b, "[mcp_servers.%s]\n", tomlQuoteKey(name))
		fmt.Fprintf(&b, "command = %s\n", tomlQuoteString(spec.Command))
		if len(spec.Args) > 0 {
			fmt.Fprintf(&b, "args = %s\n", tomlStringArray(spec.Args))
		}
		if len(spec.EnvVars) > 0 {
			fmt.Fprintf(&b, "env_vars = %s\n", tomlStringArray(spec.EnvVars))
		}
		if spec.StartupTimeoutSec != nil {
			fmt.Fprintf(&b, "startup_timeout_sec = %d\n", *spec.StartupTimeoutSec)
		}
		if spec.ToolTimeoutSec != nil {
			fmt.Fprintf(&b, "tool_timeout_sec = %d\n", *spec.ToolTimeoutSec)
		}
		if spec.Enabled != nil {
			fmt.Fprintf(&b, "enabled = %t\n", *spec.Enabled)
		}
		if spec.DefaultToolsApprovalMode != "" {
			fmt.Fprintf(&b, "default_tools_approval_mode = %s\n", tomlQuoteString(spec.DefaultToolsApprovalMode))
		}
		if len(spec.Env) > 0 {
			fmt.Fprintf(&b, "\n[mcp_servers.%s.env]\n", tomlQuoteKey(name))
			envKeys := make([]string, 0, len(spec.Env))
			for k := range spec.Env {
				envKeys = append(envKeys, k)
			}
			sort.Strings(envKeys)
			for _, k := range envKeys {
				fmt.Fprintf(&b, "%s = %s\n", tomlQuoteKey(k), tomlQuoteString(spec.Env[k]))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// tomlQuoteKey returns a bare key when it matches TOML's bare-key
// grammar (A-Za-z0-9_-), otherwise emits a quoted key. Server names
// like "api-bridge" stay bare; names with dots or spaces get quoted.
func tomlQuoteKey(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		isAlpha := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		if !isAlpha && !isDigit && r != '_' && r != '-' {
			return tomlQuoteString(s)
		}
	}
	return s
}

// tomlQuoteString emits a basic-string TOML literal with the minimum
// set of escape sequences for the kinds of strings we expect in MCP
// configs (paths, URLs, command names).
func tomlQuoteString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func tomlStringArray(items []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(tomlQuoteString(item))
	}
	b.WriteByte(']')
	return b.String()
}

// dirIsEmptyOrJustCreated returns true iff the directory exists and has
// no children — a heuristic for "we just created this and nothing has
// been written into it yet, so cleanup can attempt os.Remove (which
// will fail harmlessly if other artifacts land later)."
func dirIsEmptyOrJustCreated(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(entries) == 0
}

// personalCodexMCPServers lists the MCP servers in the config.toml of the
// Codex home a session uses, sorted (a variable for tests).
var personalCodexMCPServers = func(codexHome string) []string {
	if strings.TrimSpace(codexHome) == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(codexHome, "config.toml")) // #nosec G304 -- the session's own Codex config
	if err != nil {
		return nil
	}
	return codexMCPServerNamesInConfig(string(data))
}

// codexMCPServerNamesInConfig returns the server names of [mcp_servers.NAME]
// tables (sub-tables such as [mcp_servers.NAME.env] count once).
func codexMCPServerNamesInConfig(config string) []string {
	seen := map[string]bool{}
	var names []string
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[mcp_servers.") || !strings.HasSuffix(line, "]") || strings.HasPrefix(line, "[[") {
			continue
		}
		rest := strings.TrimSuffix(strings.TrimPrefix(line, "[mcp_servers."), "]")
		name := rest
		if strings.HasPrefix(rest, `"`) {
			if end := strings.Index(rest[1:], `"`); end >= 0 {
				name = rest[1 : end+1]
			}
		} else if dot := strings.Index(rest, "."); dot >= 0 {
			name = rest[:dot]
		}
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

var (
	codexProfileName   = regexp.MustCompile(`^agentworks-[0-9]+\.config\.toml$`)
	codexProfileSweeps sync.Map // codex home -> time of the last sweep
)

// sweepStaleCodexProfiles removes the session profile files (agentworks-<n>.config.toml)
// that earlier sessions left in a Codex home: a launch whose cleanup never ran
// (a crash, a restart) leaves its file behind, and a developer's own ~/.codex
// held 229 of them. A profile is a few hundred bytes to several KB of MCP tool
// descriptions; one younger than a day may belong to a live session and stays.
// At most once a minute per folder.
func sweepStaleCodexProfiles(codexHome string) {
	now := time.Now()
	if last, ok := codexProfileSweeps.Load(codexHome); ok && now.Sub(last.(time.Time)) < time.Minute {
		return
	}
	codexProfileSweeps.Store(codexHome, now)
	entries, err := os.ReadDir(codexHome)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !codexProfileName.MatchString(entry.Name()) {
			continue
		}
		if info, err := entry.Info(); err == nil && now.Sub(info.ModTime()) > 24*time.Hour {
			_ = os.Remove(filepath.Join(codexHome, entry.Name()))
		}
	}
}

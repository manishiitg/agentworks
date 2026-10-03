package security

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SandboxPersistentDirName is the reserved folder a workflow grants every one
// of its steps for writing: <workflow>/.sandbox-cache. Package managers are
// pointed at it (below), so "pip install --user X" on run 1 is simply
// "already satisfied" -- with no network -- on every later run of that
// workflow. Steps run in runs/iteration-N/, which rotates, so anything
// installed there is gone next run (PLAT-284; PLAT-283 made installs possible
// at all). The orchestrator mirrors this name when it builds the grant
// (agent_go step_based_workflow); the two modules only meet over HTTP, so the
// name itself is the contract: the isolator recognises the folder by it.
const SandboxPersistentDirName = ".sandbox-cache"

// SandboxPersistentDirEnv tells the sandboxed command where the persistent
// folder is, so an agent can put a venv or a downloaded binary there itself.
const SandboxPersistentDirEnv = "SANDBOX_PERSISTENT_DIR"

// sandboxToolEnv returns env with package-manager and toolchain state routed
// to disk the sandboxed command can actually write. writePaths and workDir
// must already be canonical (what the backend enforces).
//
// Persistent (a write path named SandboxPersistentDirName is present): pip's
// user site and cache, npm's cache and global prefix, Go, Cargo and pipx all
// live under it, its bin/ folders lead PATH, and SANDBOX_PERSISTENT_DIR names
// it. Only TMPDIR stays per run -- scratch should not persist.
//
// Fallback (no such grant, e.g. an older orchestrator or a non-workflow
// caller): today's behaviour from PLAT-283 -- caches routed into the run
// folder so installs at least work, even if only for that run.
//
// Neither (no write grant at all): env is returned untouched. Pointing pip at
// a folder Landlock will also deny would only make the failure more
// confusing than the one it already gets.
func sandboxToolEnv(env []string, workDir string, writePaths []string) []string {
	persistent := ""
	scratchCandidates := make([]string, 0, len(writePaths))
	for _, wp := range writePaths {
		if persistent == "" && filepath.Base(wp) == SandboxPersistentDirName {
			persistent = wp
			continue
		}
		scratchCandidates = append(scratchCandidates, wp)
	}
	// The persistent folder is never scratch: per-run temp files must not
	// accumulate in the one place that survives runs.
	scratch := scratchBase(workDir, scratchCandidates)
	if persistent == "" && scratch == "" {
		return env
	}

	if persistent == "" {
		env = privateSandboxHome(env, filepath.Join(scratch, SandboxPersistentDirName, "home"))
		cacheDir := filepath.Join(scratch, ".cache")
		pipCacheDir := filepath.Join(cacheDir, "pip")
		npmCacheDir := filepath.Join(cacheDir, "npm")
		userBase := filepath.Join(scratch, ".local")
		tmpDir := filepath.Join(scratch, ".tmp")
		for _, dir := range []string{pipCacheDir, npmCacheDir, userBase, tmpDir} {
			ensureScratchDir(dir)
		}
		return append(env,
			"PIP_CACHE_DIR="+pipCacheDir,
			"XDG_CACHE_HOME="+cacheDir,
			"PYTHONUSERBASE="+userBase,
			"npm_config_cache="+npmCacheDir,
			"TMPDIR="+tmpDir,
		)
	}

	binDir := filepath.Join(persistent, "bin")
	pipCacheDir := filepath.Join(persistent, "pip")
	pythonBase := filepath.Join(persistent, "python")
	xdgCacheDir := filepath.Join(persistent, "xdg")
	npmCacheDir := filepath.Join(persistent, "npm")
	npmPrefix := filepath.Join(persistent, "npm-global")
	goPath := filepath.Join(persistent, "go")
	goCache := filepath.Join(persistent, "go-build")
	cargoHome := filepath.Join(persistent, "cargo")
	pipxHome := filepath.Join(persistent, "pipx")
	tmpDir := filepath.Join(persistent, "tmp")
	if scratch != "" {
		tmpDir = filepath.Join(scratch, ".tmp")
	}
	for _, dir := range []string{binDir, pipCacheDir, pythonBase, xdgCacheDir, npmCacheDir, npmPrefix, goPath, goCache, cargoHome, pipxHome, tmpDir} {
		ensureScratchDir(dir)
	}

	toolBins := strings.Join([]string{
		binDir,
		filepath.Join(pythonBase, "bin"),
		filepath.Join(npmPrefix, "bin"),
		filepath.Join(goPath, "bin"),
		filepath.Join(cargoHome, "bin"),
	}, string(os.PathListSeparator))

	env = privateSandboxHome(env, filepath.Join(persistent, "home"))
	out := make([]string, 0, len(env)+16)
	pathSeen := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathSeen = true
			existing := strings.TrimPrefix(kv, "PATH=")
			if existing == "" {
				kv = "PATH=" + toolBins
			} else {
				kv = "PATH=" + toolBins + string(os.PathListSeparator) + existing
			}
		}
		out = append(out, kv)
	}
	if !pathSeen {
		out = append(out, "PATH="+toolBins)
	}
	return append(out,
		SandboxPersistentDirEnv+"="+persistent,
		"PIP_CACHE_DIR="+pipCacheDir,
		"PYTHONUSERBASE="+pythonBase,
		"XDG_CACHE_HOME="+xdgCacheDir,
		"npm_config_cache="+npmCacheDir,
		"npm_config_prefix="+npmPrefix,
		"GOPATH="+goPath,
		"GOCACHE="+goCache,
		"CARGO_HOME="+cargoHome,
		"PIPX_HOME="+pipxHome,
		"PIPX_BIN_DIR="+binDir,
		"TMPDIR="+tmpDir,
	)
}

// scratchBase picks the per-run folder the command can write: WorkDir when it
// falls inside a granted write path (the common case -- a step's folder is
// both), otherwise the first granted write path, otherwise "".
func scratchBase(workDir string, writePaths []string) string {
	if workDir != "" {
		for _, writable := range writePaths {
			if pathWithin(workDir, writable) {
				return workDir
			}
		}
	}
	if len(writePaths) > 0 {
		return writePaths[0]
	}
	return ""
}

// toolEnv is the backend-agnostic entry point for the mount-namespace and
// macOS backends, which enforce iso.WritePaths as given; the Landlock backend
// passes its already-canonical policy paths to sandboxToolEnv directly.
func (iso *Isolator) toolEnv(env []string) []string {
	writes := make([]string, 0, len(iso.WritePaths))
	for _, path := range iso.WritePaths {
		if resolved, ok := iso.sandboxAllowedPath(path); ok {
			writes = append(writes, resolved)
		}
	}
	return sandboxToolEnv(env, canonicalPath(iso.WorkDir), writes)
}

// privateSandboxHome replaces the shared HOME=/tmp of the Docker-mode
// environment with home, a folder only this workflow or Crew can write: git,
// ssh, CLI logins and tool config written under HOME used to land in a /tmp
// every sandboxed command shared. A real host HOME (native mode) is kept,
// since host-installed CLIs read their config from it.
func privateSandboxHome(env []string, home string) []string {
	shared := false
	for _, kv := range env {
		if kv == "HOME="+sandboxSharedHome {
			shared = true
			break
		}
	}
	if !shared {
		return env
	}
	config := filepath.Join(home, ".config")
	if err := os.MkdirAll(config, 0o700); err != nil {
		return env
	}
	// The service creates these folders, but a command that runs as a user's slot account (another user, same group as the project) must
	// be able to use them: owner-only left the slot unable to enter its own HOME, so every installer that writes under it (nvm, rustup, ...)
	// failed with "Permission denied". The project folder around them already grants that group the same access. Best effort, like
	// ensureScratchDir: a folder the slot made itself is not ours to change.
	for _, dir := range []string{config, home, filepath.Dir(home)} {
		if dir == config || dir == home || filepath.Base(dir) == SandboxPersistentDirName {
			_ = os.Chmod(dir, 0o770|os.ModeSetgid)
		}
	}
	out := make([]string, 0, len(env)+2)
	socketDirSet := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			continue
		}
		socketDirSet = socketDirSet || strings.HasPrefix(kv, "AGENT_BROWSER_SOCKET_DIR=")
		out = append(out, kv)
	}
	out = append(out, "HOME="+home, "XDG_CONFIG_HOME="+config)
	// agent-browser keeps its sockets under $HOME by default; a workflow
	// home is far past the 103-byte Unix socket path limit.
	if !socketDirSet {
		out = append(out, "AGENT_BROWSER_SOCKET_DIR="+browserSocketDir)
	}
	return out
}

// ensureScratchDir creates a scratch or cache folder the sandboxed command writes to. The platform creates it, so for a
// command that runs as a slot account (a different user in the same group) it must be group-writable, or the slot
// cannot create a temp file in its own TMPDIR (the private terminal launcher failed this way on Confida).
func ensureScratchDir(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	// Best effort: a folder the slot created itself is not ours to change, and it is already writable by it.
	_ = os.Chmod(dir, 0o770|os.ModeSetgid)
}

// SlotHomeEnv gives a command that runs as a user's slot account the project's private home, whatever the service's own HOME is.
// In native mode (NATIVE_WORKSPACE=true, Excellence) privateSandboxHome keeps the real host HOME for host CLIs, so an agent's shell
// as the slot got the service account's home (/srv/agents/home), which the slot cannot even read, while the Code terminal used the
// project's home: nvm installed in the terminal was invisible to the agent, which kept the system Node (2026-10-03). The home is the
// same one the terminal uses: <the granted folder holding the working folder>/.sandbox-cache/home (a workflow's persistent
// .sandbox-cache/home when it has one). If that home has nvm with a default version, its bin folder leads PATH, so a non-interactive
// `sh -c` (which never reads ~/.bashrc) runs the same node as the terminal.
func SlotHomeEnv(env []string, workDir string, writePaths []string, userHome string) []string {
	if userHome != "" {
		// The slot's own home (Code): it belongs to the slot, which creates what it needs there; the service does not touch it.
		return withHome(env, userHome)
	}
	home := ""
	for _, wp := range writePaths {
		if filepath.Base(wp) == SandboxPersistentDirName {
			home = filepath.Join(wp, "home")
			break
		}
	}
	if home == "" {
		for _, wp := range writePaths {
			if workDir != "" && pathWithin(workDir, wp) {
				home = filepath.Join(wp, SandboxPersistentDirName, "home")
				break
			}
		}
	}
	if home == "" && len(writePaths) > 0 {
		home = filepath.Join(writePaths[0], SandboxPersistentDirName, "home")
	}
	if home == "" {
		return env
	}
	config := filepath.Join(home, ".config")
	if err := os.MkdirAll(config, 0o700); err != nil {
		return env
	}
	for _, dir := range []string{filepath.Dir(home), home, config} {
		_ = os.Chmod(dir, 0o770|os.ModeSetgid)
	}
	return withHome(env, home)
}

// withHome sets HOME and XDG_CONFIG_HOME to home and puts nvm's default Node (if home has one) first on PATH.
func withHome(env []string, home string) []string {
	config := filepath.Join(home, ".config")
	out := make([]string, 0, len(env)+2)
	path := ""
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "HOME="), strings.HasPrefix(kv, "XDG_CONFIG_HOME="):
			continue
		case strings.HasPrefix(kv, "PATH="):
			path = strings.TrimPrefix(kv, "PATH=")
			continue
		}
		out = append(out, kv)
	}
	if bin := nvmDefaultNodeBin(home); bin != "" {
		path = bin + string(os.PathListSeparator) + path
	}
	out = append(out, "HOME="+home, "XDG_CONFIG_HOME="+config)
	if path != "" {
		out = append(out, "PATH="+path)
	}
	return out
}

// nvmDefaultNodeBin is the bin folder of nvm's default Node in home (nvm installs under $XDG_CONFIG_HOME/nvm when that is set,
// else ~/.nvm), or "" when there is none. The default alias is a version or a prefix of one ("24", "v24.21", "node" = newest).
func nvmDefaultNodeBin(home string) string {
	for _, dir := range []string{filepath.Join(home, ".config", "nvm"), filepath.Join(home, ".nvm")} {
		raw, err := os.ReadFile(filepath.Join(dir, "alias", "default"))
		if err != nil {
			continue
		}
		want := strings.TrimPrefix(strings.TrimSpace(string(raw)), "v")
		entries, err := os.ReadDir(filepath.Join(dir, "versions", "node"))
		if err != nil {
			continue
		}
		best := ""
		for _, entry := range entries {
			name := entry.Name()
			version := strings.TrimPrefix(name, "v")
			if !entry.IsDir() || !strings.HasPrefix(name, "v") {
				continue
			}
			if want != "node" && want != "" && version != want && !strings.HasPrefix(version, want+".") {
				continue
			}
			if best == "" || nodeVersionLess(strings.TrimPrefix(best, "v"), version) {
				best = name
			}
		}
		if best != "" {
			bin := filepath.Join(dir, "versions", "node", best, "bin")
			if info, err := os.Stat(filepath.Join(bin, "node")); err == nil && !info.IsDir() {
				return bin
			}
		}
	}
	return ""
}

// nodeVersionLess compares dotted numeric versions ("24.9.0" < "24.21.0").
func nodeVersionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

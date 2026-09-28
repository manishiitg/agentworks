package security

import (
	"os"
	"path/filepath"
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
			_ = os.MkdirAll(dir, 0755)
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
		_ = os.MkdirAll(dir, 0755)
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

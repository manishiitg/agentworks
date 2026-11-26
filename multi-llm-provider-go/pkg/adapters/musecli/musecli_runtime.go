package musecli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// museShortSocketLimit is the longest runtime folder that still leaves room for
// Muse's `muse/ms-<id>.sock.lease` inside the 104-byte unix socket path limit.
const museShortSocketLimit = 60

// museRuntimeDirFor is the XDG_RUNTIME_DIR a Landlock-confined Muse gets: a
// "run" folder in its private home (the one place it may write). Without it
// Muse falls back to /tmp/tbh-<uid>-rt, which a confined CLI cannot use, and
// every start printed "local session messaging unavailable: registry_io ...
// Permission denied" (Excellence, 2026-10-03). A private home deep in a
// runtime folder is far past the socket path limit, so Muse is handed a short
// symlink to it instead, one per private home, reused across turns.
func museRuntimeDirFor(privateHome string) (string, error) {
	privateHome = strings.TrimSpace(privateHome)
	if privateHome == "" {
		return "", nil
	}
	run := filepath.Join(privateHome, "run")
	if err := os.MkdirAll(run, 0o700); err != nil {
		return "", err
	}
	// The user's own Linux account runs Muse on a slot: it must own what it writes.
	if err := slotfs.ShareTree(privateHome, run); err != nil {
		return "", err
	}
	if len(run) <= museShortSocketLimit {
		return run, nil
	}
	sum := sha256.Sum256([]byte(run))
	link := filepath.Join(slotfs.TempDir(privateHome), "mrt-"+hex.EncodeToString(sum[:])[:10])
	if target, err := os.Readlink(link); err == nil && target == run {
		return link, nil
	}
	_ = os.Remove(link)
	if err := os.Symlink(run, link); err != nil {
		// Never fall back to a path Muse cannot use; no env is better than a wrong one.
		return "", err
	}
	return link, nil
}

// museRuntimeEnv is the environment of a Landlock-confined launch ("" elsewhere:
// on a Mac or an unconfined run Muse's own defaults work): XDG_RUNTIME_DIR, and
// MUSE_NO_AUTO_UPDATE, because the wrapper's update-check stamp lives beside the
// shared binary, which the slot cannot write. The platform updates Muse itself;
// without this every start prints "Permission denied" from the wrapper.
func museRuntimeEnv(opts *llmtypes.CallOptions) []string {
	if opts == nil || !opts.CLISecurity.LandlockEnforced() {
		return nil
	}
	dir, err := museRuntimeDirFor(opts.CLISecurity.PrivateHome)
	if err != nil || dir == "" {
		return nil
	}
	return []string{"XDG_RUNTIME_DIR=" + dir, "MUSE_NO_AUTO_UPDATE=1"}
}

// museProbePrefix is the folder Muse creates in its temp folder at every start
// to test its workspace (and never removes): 572 of them piled up on Excellence
// and 1,286 in a developer's Mac temp folder.
const museProbePrefix = "muse-workspace-probe-"

// museSweepScript removes Muse's old probe folders, then becomes the real
// command. It runs inside the launch, as the user who runs Muse: on a slot that
// is the slot user, who owns those folders (mode 700), so the server process
// could not remove them itself. A probe younger than two minutes may belong to a
// start in progress and stays.
const museSweepScript = `d="$1"; shift; if [ -n "$d" ]; then find "$d" -maxdepth 1 -type d -name '` + museProbePrefix + `*' -mmin +2 -exec rm -rf {} + 2>/dev/null; fi; exec "$@"`

// musePreludeArgv puts the sweep in front of a Muse launch argv. The temp
// folder is the one the CLI will use: its private home's tmp when confined,
// else the system one.
func musePreludeArgv(opts *llmtypes.CallOptions, argv []string) []string {
	dir := llmtypes.CLIHomeEnvironment(opts)["TMPDIR"]
	if strings.TrimSpace(dir) == "" {
		dir = os.TempDir()
	}
	out := []string{"sh", "-c", museSweepScript, "muse-sweep", dir}
	return append(out, museArgvWithAbsoluteBinary(argv)...)
}

// museArgvWithAbsoluteBinary replaces the bare command name "muse" in argv with the path this process resolves it to. The sweep's `exec "$@"` runs inside the
// slot, where sudo resets PATH to the system folders: a bare `muse` was not found there, so every confined Muse launch ended at once and the chat said
// "muse tmux session died while waiting for muse TUI to settle" (Excellence, 2026-10-04). This process's PATH has the install folder. Without a match
// argv is returned unchanged.
func museArgvWithAbsoluteBinary(argv []string) []string {
	out := append([]string(nil), argv...)
	for i, arg := range out {
		if arg != "muse" {
			continue
		}
		if resolved, err := exec.LookPath("muse"); err == nil && filepath.IsAbs(resolved) {
			out[i] = resolved
		}
		break
	}
	return out
}

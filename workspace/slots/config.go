package slots

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"
)

// DefaultSlotctlConfig is the root-owned allow-list slotctl reads. It sits beside the launcher in a
// world-readable folder because slotctl runs as the slot, which cannot enter /etc/agentworks.
const DefaultSlotctlConfig = "/usr/local/libexec/agentworks/slotctl.json"

// ConfigPath is the allow-list config this product's processes read: AGENTWORKS_SLOTCTL_CONFIG, else the
// default. A product with its own slots on a shared host sets it (and its own launcher) in its service
// environment.
func ConfigPath() string {
	if override := strings.TrimSpace(os.Getenv(EnvConfig)); override != "" {
		return override
	}
	return DefaultSlotctlConfig
}

// ConfigBesideExecutable is the config slotctl reads: the one next to the launcher (each product's launcher
// has its own folder, and slotctl runs as the slot with no service environment), else the default.
func ConfigBesideExecutable() string {
	if exe, err := os.Executable(); err == nil {
		beside := filepath.Join(filepath.Dir(exe), "slotctl.json")
		if _, statErr := os.Stat(beside); statErr == nil {
			return beside
		}
	}
	return DefaultSlotctlConfig
}

// maxRequestBytes bounds a request (an environment and a sandbox policy are a few kilobytes).
const maxRequestBytes = 8 << 20

// ExecConfig is what slotctl will agree to run. It is root-owned, so the platform account cannot
// widen it.
type ExecConfig struct {
	// SlotPrefix names this product's slot accounts (slot_prefix); empty means "slot".
	SlotPrefix string `json:"slot_prefix,omitempty"`
	// SlotDocker says every slot has its own rootless Docker (provision-slots.sh docker): a command run as a
	// slot then gets DOCKER_HOST pointing at it, replacing the platform account's socket, which a slot cannot
	// reach.
	SlotDocker bool `json:"slot_docker,omitempty"`
	// AllowedExec lists the absolute programs a request may start; * matches one path segment.
	AllowedExec []string `json:"allowed_exec"`
	// AllowedCwd lists the folders a request may start in (or below).
	AllowedCwd []string `json:"allowed_cwd"`
	// SlotRunRoot is the folder holding each slot's run folder (<root>/<slot>/). A tmux request may
	// only name that slot's own socket, <root>/<slot>/tmux.sock.
	SlotRunRoot string `json:"slot_run_root,omitempty"`
	// SlotStateRoot holds each slot's runtime folders (<root>/<slot>/...). The tmux front-end sends a
	// session started inside one of them to that slot's tmux server.
	SlotStateRoot string `json:"slot_state_root,omitempty"`
	// DocsRoot is the workspace folder whose _users/<user id>/ trees belong to slots; SlotTable is the
	// root-owned file mapping user ids to slots. Together they let a launch in a user's own folder be
	// recognised as that user's slot launch.
	DocsRoot  string `json:"docs_root,omitempty"`
	SlotTable string `json:"slot_table,omitempty"`
}

// AllowsProgram reports whether allowed_exec lists program (an absolute, clean path); a pattern may use * for one
// path segment (release folders change at every deploy). slotctl and the deploy self-test use this one rule.
func (cfg ExecConfig) AllowsProgram(program string) bool {
	for _, allowed := range cfg.AllowedExec {
		if allowed == program {
			return true
		}
		if ok, _ := filepath.Match(allowed, program); ok && strings.Contains(allowed, "*") {
			return true
		}
	}
	return false
}

// AllowsCwd reports whether dir (resolved through symlinks) lies in one of allowed_cwd, the rule slotctl applies to a
// request's working folder.
func (cfg ExecConfig) AllowsCwd(dir string) bool {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	for _, root := range cfg.AllowedCwd {
		if rootResolved, rerr := filepath.EvalSymlinks(root); rerr == nil && withinDir(rootResolved, resolved) {
			return true
		}
	}
	return false
}

func withinDir(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	return candidate == root || strings.HasPrefix(candidate, root+string(filepath.Separator))
}

// TmuxPath is the tmux the launcher will run for a slot.
const TmuxPath = "/usr/bin/tmux"

// ChmodPath is the chmod slotctl may run, only to open a slot's own tmux socket to its group.
const ChmodPath = "/usr/bin/chmod"

// SlotSocket is the tmux socket of a slot under a run root.
func SlotSocket(runRoot, slot string) string { return filepath.Join(runRoot, slot, "tmux.sock") }

// LoadExecConfig reads the allow-list.
func LoadExecConfig(path string) (ExecConfig, error) {
	var cfg ExecConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	SetPrefix(cfg.SlotPrefix)
	return cfg, nil
}

// SlotStateDir is a slot's runtime folder: the CLI runtime folders of that user are created inside it.
func (cfg ExecConfig) SlotStateDir(slot string) string {
	return filepath.Join(cfg.SlotStateRoot, slot)
}

// StateDirFor returns the folder a slot's CLI runtimes live in, from the root-owned config file.
func StateDirFor(slot string) (string, error) {
	cfg, err := LoadExecConfig(ConfigPath())
	if err != nil {
		return "", err
	}
	if cfg.SlotStateRoot == "" || !ValidSlot(slot) {
		return "", fmt.Errorf("no state folder for slot %q", slot)
	}
	return cfg.SlotStateDir(slot), nil
}

// RunDirFor returns a slot's run folder (its tmux socket and short-lived files).
func RunDirFor(slot string) (string, error) {
	cfg, err := LoadExecConfig(ConfigPath())
	if err != nil {
		return "", err
	}
	if cfg.SlotRunRoot == "" || !ValidSlot(slot) {
		return "", fmt.Errorf("no run folder for slot %q", slot)
	}
	return filepath.Join(cfg.SlotRunRoot, slot), nil
}

// SlotForDir returns the slot a folder belongs to by where it lies: a folder in a slot's state or run area (the slot
// is named in the path), or in a user's own tree <docs root>/_users/<user id>/ when that user holds a slot. The
// second rule is the legacy folder rule (PLAT-442): the tmux front-end no longer decides from it, see
// SlotForLaunch; it is kept to detect a launch whose folder and script disagree.
func (cfg ExecConfig) SlotForDir(dir string) string {
	if slot := SlotOfDir(cfg.SlotStateRoot, dir); slot != "" {
		return slot
	}
	if slot := SlotOfDir(cfg.SlotRunRoot, dir); slot != "" {
		return slot
	}
	if cfg.DocsRoot == "" || cfg.SlotTable == "" {
		return ""
	}
	rel, err := filepath.Rel(filepath.Clean(cfg.DocsRoot), filepath.Clean(dir))
	if err != nil {
		return ""
	}
	ref, ok := workspaceref.Parse(filepath.ToSlash(rel))
	if !ok || !ref.HasOwner() {
		return ""
	}
	table, err := LoadTable(cfg.SlotTable)
	if err != nil {
		return ""
	}
	slot, _ := table.SlotFor(ref.Owner())
	return slot
}

// ErrSlotMismatch is the error a launch whose script names one slot and whose folder names another carries
// (PLAT-451). It is a refusal, never "no slot requested": a caller that gets it must not run the launch on any
// account.
var ErrSlotMismatch = errors.New("slot mismatch")

// SlotMismatchError says which slot the script named and which the working folder belongs to.
type SlotMismatchError struct{ ScriptSlot, Dir, DirSlot string }

func (e *SlotMismatchError) Error() string {
	return fmt.Sprintf("[SLOT_EXPLICIT_MISMATCH] launch refused: the launch script is in %s's run folder but the folder %s belongs to %s", e.ScriptSlot, e.Dir, e.DirSlot)
}

// Is makes errors.Is(err, ErrSlotMismatch) true for a *SlotMismatchError.
func (e *SlotMismatchError) Is(target error) bool { return target == ErrSlotMismatch }

// SlotForLaunch is the slot a tmux new-session runs as: the one the platform named by placing the launch script in
// that slot's run folder (<run root>/<slot>/..., created by the provider only for a launch it decided runs as the
// slot). command is the pane's command line. A script that is not in any slot's run folder is not a slot launch:
// ("", nil). The folder the session starts in is not consulted to choose the slot (PLAT-442); if it names a
// different slot than the script does, the launch is refused: ("", *SlotMismatchError), which a caller must treat
// as a refusal, never as a launch without a slot (PLAT-451).
func (cfg ExecConfig) SlotForLaunch(dir, command string) (string, error) {
	slot := cfg.SlotNamedByScript(command)
	if slot == "" {
		return "", nil
	}
	if byDir := cfg.SlotForDir(dir); byDir != "" && byDir != slot {
		return "", &SlotMismatchError{ScriptSlot: slot, Dir: dir, DirSlot: byDir}
	}
	return slot, nil
}

// SlotNamedByScript returns the slot whose run folder the command refers to ("" when none).
func (cfg ExecConfig) SlotNamedByScript(command string) string {
	if cfg.SlotRunRoot == "" {
		return ""
	}
	prefix := filepath.Clean(cfg.SlotRunRoot) + string(filepath.Separator)
	for from := 0; from < len(command); {
		i := strings.Index(command[from:], prefix)
		if i < 0 {
			return ""
		}
		i += from
		rest := command[i+len(prefix):]
		name, _, _ := strings.Cut(rest, string(filepath.Separator))
		if ValidSlot(name) && strings.Contains(rest, string(filepath.Separator)) {
			return name
		}
		from = i + len(prefix)
	}
	return ""
}

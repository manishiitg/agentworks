package slots

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// SlotForDir returns the slot a folder belongs to: a folder in a slot's state or run area, or in a
// user's own tree <docs root>/_users/<user id>/ when that user holds a slot.
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
	prefix := filepath.Join(filepath.Clean(cfg.DocsRoot), "_users") + string(filepath.Separator)
	clean := filepath.Clean(dir)
	if !strings.HasPrefix(clean, prefix) {
		return ""
	}
	userID := strings.SplitN(strings.TrimPrefix(clean, prefix), string(filepath.Separator), 2)[0]
	table, err := LoadTable(cfg.SlotTable)
	if err != nil {
		return ""
	}
	slot, _ := table.SlotFor(userID)
	return slot
}

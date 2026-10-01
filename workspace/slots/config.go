package slots

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultSlotctlConfig is the root-owned allow-list slotctl reads. It sits beside the launcher in a
// world-readable folder because slotctl runs as the slot, which cannot enter /etc/agentworks.
const DefaultSlotctlConfig = "/usr/local/libexec/agentworks/slotctl.json"

// maxRequestBytes bounds a request (an environment and a sandbox policy are a few kilobytes).
const maxRequestBytes = 8 << 20

// ExecConfig is what slotctl will agree to run. It is root-owned, so the platform account cannot
// widen it.
type ExecConfig struct {
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
}

// TmuxPath is the tmux the launcher will run for a slot.
const TmuxPath = "/usr/bin/tmux"

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
	return cfg, nil
}

// SlotStateDir is a slot's runtime folder: the CLI runtime folders of that user are created inside it.
func (cfg ExecConfig) SlotStateDir(slot string) string {
	return filepath.Join(cfg.SlotStateRoot, slot)
}

// StateDirFor returns the folder a slot's CLI runtimes live in, from the root-owned config file.
func StateDirFor(slot string) (string, error) {
	cfg, err := LoadExecConfig(DefaultSlotctlConfig)
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
	cfg, err := LoadExecConfig(DefaultSlotctlConfig)
	if err != nil {
		return "", err
	}
	if cfg.SlotRunRoot == "" || !ValidSlot(slot) {
		return "", fmt.Errorf("no run folder for slot %q", slot)
	}
	return filepath.Join(cfg.SlotRunRoot, slot), nil
}

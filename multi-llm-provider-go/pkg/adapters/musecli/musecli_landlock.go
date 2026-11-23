package musecli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// museLandlockGrants are what a confined Muse launch needs outside its
// granted folders. Muse runs on a per-launch config folder
// (XDG_CONFIG_HOME=/tmp/agentworks-muse-config-*: its login, trust and
// settings with the MCP servers and hooks); it may write there, and it
// reads the programs and hook scripts that settings name.
func museLandlockGrants(args []string, env []string) (read, write []string) {
	configHome, dataHome := "", ""
	for _, entry := range append(append([]string(nil), args...), env...) {
		if value, ok := strings.CutPrefix(entry, "XDG_CONFIG_HOME="); ok {
			configHome = value
		}
		if value, ok := strings.CutPrefix(entry, "XDG_DATA_HOME="); ok {
			dataHome = value
		}
	}
	// Muse keeps its local-messaging endpoint lease under <data>/muse/runtime.
	// Without write access every launch warned "local session messaging unavailable:
	// ... direct endpoint lease: Permission denied" (excellence 2026-09-30).
	if dataHome != "" {
		runtimeDir := filepath.Join(dataHome, "muse", "runtime")
		if os.MkdirAll(runtimeDir, 0o700) == nil {
			write = append(write, runtimeDir)
		}
	}
	read = clisandbox.ArgFilePaths(args)
	if configHome != "" {
		settings := []string{filepath.Join(configHome, "muse", "settings.json")}
		read = append(read, clisandbox.MCPCommandPaths(settings)...)
		read = append(read, clisandbox.JSONFilePaths(settings)...)
		read = append(read, museManagedHooksDirs(settings)...)
		write = append(write, configHome)
	}
	if dir, err := museHookDir(); err == nil {
		read = append(read, dir)
	}
	return read, write
}

func museLandlockArgs(opts *llmtypes.CallOptions, args []string, workdir string) ([]string, func(), error) {
	if opts == nil || !opts.CLISecurity.Confined() {
		return args, func() {}, nil
	}
	read, write := museLandlockGrants(args, nil)
	wrapped, cleanup, err := clisandbox.LandlockArgs(opts.CLISecurity, args, workdir, read, write)
	if err != nil {
		return nil, func() {}, fmt.Errorf("confine Muse: %w", err)
	}
	return wrapped, cleanup, nil
}

func museLandlockCmd(opts *llmtypes.CallOptions, cmd *exec.Cmd, workdir string) (func(), error) {
	return museLandlockCmdWithCredentials(opts, cmd, workdir, nil)
}

// museLandlockCmdWithCredentials also names login files made for this launch:
// Muse reads them, its shells cannot.
func museLandlockCmdWithCredentials(opts *llmtypes.CallOptions, cmd *exec.Cmd, workdir string, credentials []string) (func(), error) {
	if opts == nil || !opts.CLISecurity.Confined() {
		return func() {}, nil
	}
	read, write := museLandlockGrants(cmd.Args, cmd.Env)
	cleanup, err := clisandbox.LandlockCmdWithCredentials(opts.CLISecurity, cmd, workdir, read, write, credentials)
	if err != nil {
		return func() {}, fmt.Errorf("confine Muse: %w", err)
	}
	return cleanup, nil
}

// museManagedHooksDirs are the folders of the hook files the settings name in managed_hooks_path.
// Muse runs those hook scripts for every prompt; without read access to their folder the lock made
// each prompt fail with "Prompt blocked by hook ... Permission denied" (excellence 2026-09-30).
func museManagedHooksDirs(settingsFiles []string) []string {
	var dirs []string
	for _, file := range settingsFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var settings struct {
			ManagedHooksPath string `json:"managed_hooks_path"`
		}
		if json.Unmarshal(data, &settings) != nil || !filepath.IsAbs(settings.ManagedHooksPath) {
			continue
		}
		dirs = append(dirs, filepath.Dir(settings.ManagedHooksPath))
	}
	return dirs
}

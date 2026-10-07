//go:build linux

package security

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// ReapBrowserHelpersArg is the Landlock launcher's subcommand that cleans up the calling account's own leftover
// agent-browser helpers (workspace/browserreap.ReapOwnLeftovers).
const ReapBrowserHelpersArg = "reap-browser-helpers"

// ReapSlotBrowserHelpers asks every assigned slot account to clean up its own leftover agent-browser helpers
// (PLAT-685): orphaned daemons no .pid file names and day-old files with no live daemon. The platform cannot see
// or signal another account's processes and is given no way to: each pass runs as the slot, through slotctl and
// the slot's Landlock launcher (already on slotctl's allow-list), and only touches what that account owns. A helper
// a .pid file still names is never closed, so a slot's live job keeps its browser. A no-op without slots.
func ReapSlotBrowserHelpers(ctx context.Context) {
	assigned, err := slots.Assigned()
	if err != nil || len(assigned) == 0 {
		if err != nil {
			log.Printf("[BROWSER_HELPER_REAPER] slot table unavailable, slot helpers not reaped: %v", err)
		}
		return
	}
	cfg, err := slots.LoadExecConfig(slots.ConfigPath())
	if err != nil || cfg.SlotRunRoot == "" {
		return
	}
	runner, err := landlockRunnerPath()
	if err != nil {
		return
	}
	for _, slot := range assigned {
		if ctx.Err() != nil {
			return
		}
		runDir := filepath.Join(cfg.SlotRunRoot, slot)
		if info, statErr := os.Stat(runDir); statErr != nil || !info.IsDir() || !cfg.AllowsCwd(runDir) {
			continue
		}
		home := slots.HomeOf(slot)
		if home == "" {
			home = runDir
		}
		passCtx, cancel := context.WithTimeout(ctx, time.Minute)
		cmd := exec.CommandContext(passCtx, runner, ReapBrowserHelpersArg)
		cmd.Dir = runDir
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home}
		wrapped, wrapErr := slots.WrapCommand(passCtx, cmd, slot)
		if wrapErr != nil {
			cancel()
			continue
		}
		wrapped.Dir = "/"
		out, runErr := wrapped.CombinedOutput()
		cancel()
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				log.Printf("[BROWSER_HELPER_REAPER] %s: %s", slot, line)
			}
		}
		if runErr != nil {
			log.Printf("[BROWSER_HELPER_REAPER] %s: slot pass failed: %v", slot, runErr)
		}
	}
}

package claudecode

import (
	"sync"
	"time"
)

// NativeProjectInstructionFiles declares project files this CLI is configured
// to discover from its working directory. This is capability metadata, not a
// receipt that any particular file was read. Returned slices are caller-owned.
// AGENTS.md is not listed: only Claude Code 2.1.284 and later read it, so
// callers ask ReadsAgentsMD for the installed binary.
func NativeProjectInstructionFiles() []string {
	return []string{"CLAUDE.md"}
}

var readsAgentsMDCache struct {
	sync.Mutex
	at time.Time
	ok bool
}

// ReadsAgentsMD reports whether the claude on PATH reads the project's
// AGENTS.md itself (2.1.284 and later). The answer is kept for ten minutes so
// a turn does not run claude --version each time; a missing or unreadable
// binary reports false, which keeps AGENTS.md injected (a duplicate at worst,
// never a gap).
func ReadsAgentsMD() bool {
	readsAgentsMDCache.Lock()
	defer readsAgentsMDCache.Unlock()
	if !readsAgentsMDCache.at.IsZero() && time.Since(readsAgentsMDCache.at) < 10*time.Minute {
		return readsAgentsMDCache.ok
	}
	supported, err := claudeReadsAgentsMDChecked()
	if err != nil && !readsAgentsMDCache.at.IsZero() {
		// Transient probe failure: keep the last verified answer so the system
		// prompt does not suddenly re-inject AGENTS.md and bust the cache (#4560).
		return readsAgentsMDCache.ok
	}
	if err == nil {
		readsAgentsMDCache.ok = supported
		readsAgentsMDCache.at = time.Now()
		return readsAgentsMDCache.ok
	}
	return false
}

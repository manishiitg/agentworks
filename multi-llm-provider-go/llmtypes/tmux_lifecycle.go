package llmtypes

import "time"

// TmuxKillDelay is the grace window between a bounded coding-agent turn
// completing and its tmux session being torn down. It is intentionally short:
// the session only needs to stay alive long enough for the periodic pane
// scraper to capture trailing CLI output into the final snapshot. Holding the
// live shell + tmux pane (and the MCP node subprocesses the CLI spawned) open
// any longer just leaks process/memory resources for no benefit — the captured
// snapshot, not a live process, is what backs scrollback in the UI.
//
// Do NOT conflate this with rail-display retention. The read-only snapshot
// persists in the terminals store for the per-adapter, env-configurable
// XxxInteractiveRetention() window (default 30 min); that retention applies to
// the captured record, not to this kill delay. See commit 889e99d ("Split tmux
// kill delay from rail-display retention") — net behavior: tmux dies fast,
// snapshot lingers, no zombie shells.
//
// Shared across all interactive adapters (claudecode_interactive,
// codex_interactive, gemini_interactive, cursor_interactive) so the behavior is
// uniform.
const TmuxKillDelay = 30 * time.Second

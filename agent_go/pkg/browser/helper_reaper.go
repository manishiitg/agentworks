package browser

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
	"github.com/manishiitg/coding-agent-loop/workspace/browserreap"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

// Leftover agent-browser helper reaper (PLAT-662). The rules live in
// workspace/browserreap: orphans (no .pid file names them), per-chat helpers
// whose chat or extension connection has ended, and day-old bookkeeping files.
//
// Only this user's daemons are touched. The shared shared-cdp-<port> session is
// never closed, only its orphans. On a host with slot accounts each slot also
// cleans up its own orphans and stale files, as itself (PLAT-685): the server
// only asks, through the slot's own launcher.

const (
	helperReapInterval = 10 * time.Minute
	helperCloseTimeout = 8 * time.Second
	slotReapTimeout    = 5 * time.Minute
)

var (
	helperReapMu        sync.Mutex
	helperLiveSessionFn func() []string
)

// SetHelperLiveSessions lets the server report the browser sessions of chats
// and runs that are still active. Until it is set, per-chat helpers are never
// closed (only orphans and stale files are reaped).
func SetHelperLiveSessions(fn func() []string) {
	helperReapMu.Lock()
	helperLiveSessionFn = fn
	helperReapMu.Unlock()
}

// StartHelperReaper reaps leftover agent-browser helpers now and every ~10 minutes,
// this account's and (on a host with slot accounts) each assigned slot's own.
func StartHelperReaper() {
	go func() {
		reapAll := func() {
			ReapLeftoverHelpers()
			if ShouldRunGlobalStartupCleanup() {
				ctx, cancel := context.WithTimeout(context.Background(), slotReapTimeout)
				security.ReapSlotBrowserHelpers(ctx)
				cancel()
			}
		}
		reapAll()
		ticker := time.NewTicker(helperReapInterval)
		defer ticker.Stop()
		for range ticker.C {
			reapAll()
		}
	}()
}

// ReapLeftoverHelpersAsync runs one reap in the background (used when a chat ends).
func ReapLeftoverHelpersAsync() { go ReapLeftoverHelpers() }

// ReapLeftoverHelpers runs one pass. It is a no-op when agent-browser is not
// installed, and overlapping calls skip instead of queueing.
func ReapLeftoverHelpers() {
	if _, err := exec.LookPath("agent-browser"); err != nil {
		return
	}
	if !helperReapMu.TryLock() {
		return
	}
	liveFn := helperLiveSessionFn
	defer helperReapMu.Unlock()

	daemons, err := browserreap.ListDaemons()
	if err != nil {
		log.Printf("[BROWSER_HELPER_REAPER] could not list agent-browser daemons: %v", err)
		return
	}
	var live func(string) bool
	if liveFn != nil {
		names := map[string]bool{}
		for _, s := range liveFn() {
			names[s] = true
		}
		for _, s := range browserrelay.Default.LiveSessions() {
			names[s] = true
		}
		tracker := GetSessionTracker()
		tracker.mu.Lock()
		for s := range tracker.sessions {
			names[s] = true
		}
		tracker.mu.Unlock()
		live = func(s string) bool { return names[s] }
	}
	// A named instance shares the host with the main app: it may only end its
	// own (prefixed) chat helpers, never orphans or files that may be the app's.
	global := ShouldRunGlobalStartupCleanup()
	prefix := strings.TrimSpace(os.Getenv("AGENTWORKS_BROWSER_SESSION_PREFIX"))
	plan := browserreap.PlanReap(daemons, sessionDirs(), live, prefix, time.Now(), browserreap.ProcessAlive)
	if !global {
		if prefix == "" {
			plan.Ended = nil
		}
		plan.Orphans, plan.StaleFiles = nil, nil
	}

	for _, d := range plan.Orphans {
		log.Printf("[BROWSER_HELPER_REAPER] orphan agent-browser daemon pid=%d session=%q age=%s (not the PID in any .pid file): terminating", d.PID, d.Session, d.Age.Round(time.Second))
		browserreap.Terminate(d)
	}
	for _, d := range plan.Ended {
		log.Printf("[BROWSER_HELPER_REAPER] chat helper %q pid=%d cdp=%v has no live chat or connection: closing", d.Session, d.PID, d.CDP != "")
		closeEndedHelper(d)
	}
	for _, f := range plan.StaleFiles {
		if err := os.Remove(f); err == nil {
			log.Printf("[BROWSER_HELPER_REAPER] removed stale agent-browser file %s", f)
		}
	}
}

// closeEndedHelper ends a per-chat helper. A CDP-attached daemon is closed with
// `agent-browser --session X --cdp <endpoint> close`, which only disconnects
// (verified 2026-10-07: the Chrome and its pages stay open). A headless one
// owns its Chrome, so the full teardown runs.
func closeEndedHelper(d browserreap.Daemon) {
	if d.CDP == "" {
		killSessionRuntimeFully(d.Session)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), helperCloseTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "agent-browser", "--session", d.Session, "--cdp", d.CDP, "close", "--json")
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "AGENT_BROWSER_SOCKET_DIR=") && !strings.HasPrefix(kv, "AGENT_BROWSER_NAMESPACE=") {
			env = append(env, kv)
		}
	}
	if d.SocketDir != "" {
		env = append(env, "AGENT_BROWSER_SOCKET_DIR="+d.SocketDir)
	}
	if d.Namespace != "" {
		env = append(env, "AGENT_BROWSER_NAMESPACE="+d.Namespace)
	}
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		log.Printf("[BROWSER_HELPER_REAPER] close %q failed (%v): terminating its daemon", d.Session, err)
	}
	time.Sleep(500 * time.Millisecond)
	if isProcessAlive(d.PID) {
		browserreap.Terminate(d) // CDP-attached: no Chrome of its own to leak
	}
	clearSessionTabScope(d.Session)
	removeSessionFiles(d.Session)
	browserconfig.RemoveEmptySessionSocketDirs(d.Session)
}

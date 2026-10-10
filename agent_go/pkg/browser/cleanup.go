package browser

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

// ShouldRunGlobalStartupCleanup reports whether singleton startup may clean
// browser processes it did not create. Named local instances disable this so
// they cannot disrupt the normal AgentWorks application.
func ShouldRunGlobalStartupCleanup() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTWORKS_SKIP_GLOBAL_BROWSER_CLEANUP")), "true")
}

// KillAllTrackedSessions kills every daemon + Chrome process for all sessions
// currently tracked by the global SessionTracker. Call this on SIGTERM/SIGINT so
// Chrome and daemon processes don't linger after the server exits.
func KillAllTrackedSessions() {
	tracker := GetSessionTracker()
	tracker.mu.Lock()
	sessions := make([]string, 0, len(tracker.sessions))
	for name := range tracker.sessions {
		sessions = append(sessions, name)
	}
	tracker.mu.Unlock()

	if len(sessions) == 0 {
		log.Printf("[BROWSER_CLEANUP] SIGTERM: no tracked browser sessions to kill")
		return
	}
	log.Printf("[BROWSER_CLEANUP] SIGTERM: killing %d tracked browser session(s): %v", len(sessions), sessions)
	for i, session := range sessions {
		log.Printf("[BROWSER_CLEANUP] Killing browser session %d/%d: %s", i+1, len(sessions), session)
		killSessionRuntimeFully(session)
		log.Printf("[BROWSER_CLEANUP] Finished browser session %d/%d: %s", i+1, len(sessions), session)
	}
	tracker.Clear()
	log.Printf("[BROWSER_CLEANUP] All browser sessions killed")
}

// CleanupStaleRuntimeState removes stale agent-browser runtime files (PID, socket)
// that point to dead processes. This prevents "CDP response channel closed" errors
// caused by agent-browser trying to connect to a defunct runtime server.
//
// Call this at server startup before any browser commands are executed.
func CleanupStaleRuntimeState() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("[BROWSER_CLEANUP] Could not determine home directory: %v", err)
		return
	}

	abDir := filepath.Join(homeDir, ".agent-browser")

	// Also check /tmp/.agent-browser (used when HOME=/tmp, e.g. Docker)
	dirs := []string{abDir}
	tmpABDir := "/tmp/.agent-browser"
	if tmpABDir != abDir {
		dirs = append(dirs, tmpABDir)
	}

	dirs = append(dirs, browserconfig.ManagedSocketDirs()...)
	// A daemon started from a coding CLI's sandbox keeps them under the shared tmp folder (see
	// browserconfig.SandboxSocketDir).
	dirs = append(dirs, browserconfig.SandboxSocketDirs()...)
	for _, dir := range dirs {
		cleanupDir(dir)
	}
	closeManagedBrowsersInSharedFolder()
}

// closeManagedBrowsersInSharedFolder stops managed browsers (one per
// workflow, project or session) still running from the shared socket folder,
// left by a release before each got its own folder. Left running, they keep
// their Chrome profile locked, so the same browser could not start again in
// its own folder. The next command starts it there with the same profile and
// logins.
func closeManagedBrowsersInSharedFolder() {
	entries, err := os.ReadDir(browserconfig.SocketRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".pid") {
			continue
		}
		session := strings.TrimSuffix(name, ".pid")
		if !browserconfig.IsUserSession(session) || browserconfig.SocketDirForSession(session) == browserconfig.SocketRoot {
			continue
		}
		log.Printf("[BROWSER_CLEANUP] Closing %s, still running from the shared socket folder", session)
		killSessionRuntimeFully(session)
	}
}

func cleanupDir(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("[BROWSER_CLEANUP] Could not read %s: %v", dir, err)
		return
	}

	// Pass 1: iterate .pid files (daemon PIDs). Remove each session's state if
	// the daemon PID is dead or the PID file is corrupt.
	seenBase := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".pid") || strings.HasSuffix(name, ".chrome-pid") {
			continue
		}

		pidFile := filepath.Join(dir, name)
		baseName := strings.TrimSuffix(name, ".pid")
		seenBase[baseName] = true
		sockFile := filepath.Join(dir, baseName+".sock")

		pidBytes, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}

		pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
		if err != nil {
			log.Printf("[BROWSER_CLEANUP] Removing corrupt PID file: %s", pidFile)
			os.Remove(pidFile)
			os.Remove(sockFile)
			continue
		}

		if !isProcessAlive(pid) {
			removeStalePair(pidFile, sockFile, pid, "process not found or dead")
			continue
		}

		log.Printf("[BROWSER_CLEANUP] Runtime %s (PID %d) is alive — keeping", baseName, pid)
	}

	// Pass 2: sweep orphan .chrome-pid files — sessions that crashed without
	// leaving a .pid behind. Without this sweep, .chrome-pid files accumulate
	// indefinitely from crashed sessions.
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".chrome-pid") {
			continue
		}
		baseName := strings.TrimSuffix(name, ".chrome-pid")
		if seenBase[baseName] {
			continue // pass 1 handled this session's files
		}

		chromePIDFile := filepath.Join(dir, name)
		pidBytes, err := os.ReadFile(chromePIDFile)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
		if err != nil || !isProcessAlive(pid) {
			log.Printf("[BROWSER_CLEANUP] Removing orphan chrome-pid file: %s", chromePIDFile)
			os.Remove(chromePIDFile)
			// Best-effort sweep of any other extras left by this session.
			for _, ext := range []string{".stream", ".engine", ".version", ".sock"} {
				os.Remove(filepath.Join(dir, baseName+ext))
			}
		}
	}
}

// isProcessAlive returns true if the PID refers to a live process.
// On Unix, FindProcess always succeeds, so we probe with signal 0.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func removeStalePair(pidFile, sockFile string, pid int, reason string) {
	log.Printf("[BROWSER_CLEANUP] Removing stale runtime state: PID %d (%s)", pid, reason)

	if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
		log.Printf("[BROWSER_CLEANUP] Warning: could not remove %s: %v", pidFile, err)
	} else {
		fmt.Printf("🧹 Cleaned stale agent-browser PID file: %s\n", pidFile)
	}

	if err := os.Remove(sockFile); err != nil && !os.IsNotExist(err) {
		log.Printf("[BROWSER_CLEANUP] Warning: could not remove %s: %v", sockFile, err)
	} else if _, statErr := os.Stat(sockFile); statErr == nil {
		// Only log if file existed
		fmt.Printf("🧹 Cleaned stale agent-browser socket: %s\n", sockFile)
	}

	// Also clean related files (stream, engine, version)
	baseDir := filepath.Dir(pidFile)
	baseName := strings.TrimSuffix(filepath.Base(pidFile), ".pid")
	for _, ext := range []string{".stream", ".engine", ".version"} {
		extraFile := filepath.Join(baseDir, baseName+ext)
		os.Remove(extraFile) // Best-effort, ignore errors
	}
}

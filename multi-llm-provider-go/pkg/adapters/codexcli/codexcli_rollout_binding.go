package codexcli

import (
	"strings"
	"time"
)

// Rollout binding pins an interactive Codex session to the exact rollout file
// that records ITS conversation.
//
// Why this exists (PLAT-106): retained-answer lookup previously resolved a
// transcript by working directory + newest modification time. A workflow's
// interactive Chat and its scheduled run execute in the SAME directory, so the
// newest rollout in that directory frequently belongs to the other session. The
// wrong final answer was then returned and stamped with the requesting
// session's IDs, which made it undetectable downstream — no frontend guard can
// recognise a foreign event that already carries the local session's identity.
//
// Binding resolves in order:
//  1. a thread ID already pinned to this session — exact, and the steady state;
//  2. otherwise a directory/recency match that EXCLUDES rollouts already bound
//     to other live sessions, which is what stops two sessions in one directory
//     from claiming the same transcript.
//
// Once a rollout is resolved its thread ID is recorded, so every later lookup
// takes branch 1.

// boundCodexRolloutPaths returns rollout paths already claimed by sessions
// other than the caller, so a directory match cannot steal another
// conversation's transcript.
func boundCodexRolloutPaths(exclude *codexInteractiveSession) map[string]bool {
	claimed := make(map[string]bool)
	for _, session := range codexPersistentRegistry.Snapshot() {
		if session == nil || session == exclude {
			continue
		}
		session.rolloutMu.RLock()
		path, threadID, accountRoot := session.rolloutPath, session.threadID, session.accountRoot
		session.rolloutMu.RUnlock()
		// A resumed session knows its thread before it has resolved a path (after a restart, until its
		// first turn ends); its rollout is still its own.
		if strings.TrimSpace(path) == "" && threadID != "" {
			path = findCodexRolloutForThread(threadID, accountRoot)
		}
		if strings.TrimSpace(path) != "" {
			claimed[path] = true
		}
	}
	return claimed
}

// setCodexRolloutBindPrompt records the prompt a not-yet-bound session is
// about to send, so its rollout can be recognised by content. Only the first
// prompt before binding counts; a bound session ignores it.
func setCodexRolloutBindPrompt(session *codexInteractiveSession, prompt string, since time.Time) {
	if session == nil || strings.TrimSpace(prompt) == "" {
		return
	}
	session.rolloutMu.Lock()
	defer session.rolloutMu.Unlock()
	if session.threadID == "" && session.rolloutPath == "" && session.bindPrompt == "" {
		session.bindPrompt = prompt
		session.bindSince = since
	}
}

func codexRolloutBindPrompt(session *codexInteractiveSession) (string, time.Time) {
	if session == nil {
		return "", time.Time{}
	}
	session.rolloutMu.RLock()
	defer session.rolloutMu.RUnlock()
	return session.bindPrompt, session.bindSince
}

func codexRolloutIdentity(session *codexInteractiveSession) (path, threadID string) {
	if session == nil {
		return "", ""
	}
	session.rolloutMu.RLock()
	defer session.rolloutMu.RUnlock()
	return session.rolloutPath, session.threadID
}

// resolveCodexRolloutPath returns the rollout file for this session, binding it
// on first use. The caller must NOT hold session.mu.
func resolveCodexRolloutPath(session *codexInteractiveSession, turnStart time.Time) string {
	if session == nil {
		return ""
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return resolveCodexRolloutPathLocked(session, turnStart)
}

// resolveCodexRolloutPathLocked is the same resolution with the caller already
// holding session.mu.
//
// This variant exists because the interactive adapter holds session.mu for the
// entire GenerateContent body — it is acquired when the session is claimed and
// released by releaseCodexInteractiveSession in a defer. Go mutexes are not
// reentrant, so calling the locking variant from inside a turn deadlocks the
// live path. Unit tests never reach it (they do not drive a real tmux Codex
// session), so the compiler and the suite are both silent about it.
func resolveCodexRolloutPathLocked(session *codexInteractiveSession, turnStart time.Time) string {
	if session == nil {
		return ""
	}

	session.rolloutMu.Lock()
	// A pinned thread ID is authoritative. Re-resolving from it (rather than
	// trusting a cached path) survives Codex rotating or compacting the file.
	if session.threadID != "" {
		if path := findCodexRolloutForThread(session.threadID, session.accountRoot); path != "" {
			session.rolloutPath = path
			session.rolloutMu.Unlock()
			return path
		}
		// The thread is known but its file is momentarily unreadable. Returning
		// the last known path is still correct for THIS conversation; falling
		// back to a directory scan would risk another session's transcript.
		path := session.rolloutPath
		session.rolloutMu.Unlock()
		return path
	}

	if session.rolloutPath != "" {
		path := session.rolloutPath
		session.rolloutMu.Unlock()
		return path
	}
	workingDir := session.workingDir
	bindPrompt, bindSince := session.bindPrompt, session.bindSince
	session.rolloutMu.Unlock()

	// Never hold this session's rollout lock while reading other sessions. That
	// keeps the claim scan free of cross-session lock ordering requirements.
	path := findCodexRolloutForSessionScan(turnStart, workingDir, boundCodexRolloutPaths(session), bindPrompt, bindSince, session.accountRoot)
	if path == "" {
		return ""
	}

	session.rolloutMu.Lock()
	defer session.rolloutMu.Unlock()
	// A concurrent exact binding wins over this directory-scan candidate.
	if session.threadID != "" || session.rolloutPath != "" {
		return session.rolloutPath
	}
	session.rolloutPath = path
	if threadID := readCodexRolloutThreadID(path); threadID != "" {
		session.threadID = threadID
	}
	return path
}

// codexRolloutResolverForSession snapshots this session's identity and returns
// a lock-free resolver usable from inside the turn loop.
//
// The completion tracker and transcript stream poll while session.mu is held by
// GenerateContent, so they cannot take the lock themselves. Snapshotting the
// thread ID (and the set of rollouts other sessions have claimed) gives them an
// exact, allocation-free lookup with no locking at all. The caller must hold
// session.mu when calling this.
func codexRolloutResolverForSession(session *codexInteractiveSession) func(time.Time) string {
	if session == nil {
		return nil
	}
	knownPath, threadID := codexRolloutIdentity(session)
	workingDir := session.workingDir
	claimed := boundCodexRolloutPaths(session)
	accountRoot := session.accountRoot
	bindPrompt, bindSince := codexRolloutBindPrompt(session)

	return func(turnStart time.Time) string {
		if threadID != "" {
			if path := findCodexRolloutForThread(threadID, accountRoot); path != "" {
				return path
			}
			return knownPath
		}
		if knownPath != "" {
			return knownPath
		}
		return findCodexRolloutForSessionScan(turnStart, workingDir, claimed, bindPrompt, bindSince, accountRoot)
	}
}

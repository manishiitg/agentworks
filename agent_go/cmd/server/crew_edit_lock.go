package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Crew edit lock (owner, 2026-10-16). A Crew's owners are equal, but two of them changing its instructions, skills and
// settings at once overwrite each other, so one owner at a time edits. The lock is taken by an owner's Builder turn or
// their first write (files, skills, settings, instructions, over the website or MCP), and refreshed by each of their
// changes. It is freed when the holder's Builder chat ends or its terminal closes, or after 15 minutes without a
// change. Reading, asking, running functions, schedules, memory and run outputs are never locked. In memory only: a
// restart frees every lock, which is the safe direction.
const crewEditLockDefaultMinutes = 15

type crewEditLockEntry struct {
	userID, sessionID string
	since, last       time.Time
}

var crewEditLocks = struct {
	sync.Mutex
	held map[string]*crewEditLockEntry
}{held: map[string]*crewEditLockEntry{}}

// crewEditLockWindow is how long a lock survives without a change. AGENTWORKS_CREW_LOCK_MINUTES shortens it for tests.
func crewEditLockWindow() time.Duration {
	if minutes, err := strconv.Atoi(strings.TrimSpace(os.Getenv("AGENTWORKS_CREW_LOCK_MINUTES"))); err == nil && minutes > 0 {
		return time.Duration(minutes) * time.Minute
	}
	return crewEditLockDefaultMinutes * time.Minute
}

// crewEditLockExempt says whether a Crew-relative path is outside the lock: memory and what runs write.
func crewEditLockExempt(rel string) bool {
	p := strings.ToLower(strings.Trim(path.Clean("/"+strings.TrimSpace(rel)), "/"))
	if p == "" {
		return false
	}
	return p == "memory.md" || p == "memory" || strings.HasPrefix(p, "memory/") || strings.HasPrefix(p, "runs/") ||
		strings.HasPrefix(p, "outputs/") || strings.HasPrefix(p, "shared/") || crewCallerPrivatePath(p)
}

// crewEditLockTake gives userID the lock of the Crew at root, or refreshes it. It returns "" on success, or the plain
// message for a person who is not the holder. sessionID is the holder's Builder chat when the lock comes from a turn.
func crewEditLockTake(root, userID, sessionID string, now time.Time) string {
	root, person := canonicalCrewWorkspaceRoot(root), sanitizeUserIDForPath(userID)
	if root == "" || person == "" {
		return ""
	}
	window := crewEditLockWindow()
	crewEditLocks.Lock()
	defer crewEditLocks.Unlock()
	entry := crewEditLocks.held[root]
	if entry != nil && entry.userID != person && now.Sub(entry.last) < window {
		return fmt.Sprintf("%s is editing right now; you can change it when they're done or after %d minutes of no changes.", crewEditLockName(entry.userID), int(window/time.Minute))
	}
	if entry == nil || entry.userID != person {
		entry = &crewEditLockEntry{userID: person, since: now}
		crewEditLocks.held[root] = entry
		log.Printf("[CREW_LOCK] crew=%s taken by=%s session=%s", root, person, sessionID)
	}
	entry.last = now
	if sessionID != "" {
		entry.sessionID = sessionID
	}
	return ""
}

func crewEditLockName(userID string) string {
	if rec := directoryUserFor(userID, "", ""); rec != nil && strings.TrimSpace(rec.Username) != "" {
		return strings.TrimSpace(rec.Username)
	}
	return userID
}

// crewEditLockRelease frees userID's lock on the Crew at root (they were removed as an owner).
func crewEditLockRelease(root, userID string) {
	root, person := canonicalCrewWorkspaceRoot(root), sanitizeUserIDForPath(userID)
	crewEditLocks.Lock()
	defer crewEditLocks.Unlock()
	if entry := crewEditLocks.held[root]; entry != nil && entry.userID == person {
		delete(crewEditLocks.held, root)
		log.Printf("[CREW_LOCK] crew=%s freed (owner %s removed)", root, person)
	}
}

// crewEditLockReleaseSession frees every lock a Builder chat took: its chat ended or its terminal closed.
func crewEditLockReleaseSession(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	crewEditLocks.Lock()
	defer crewEditLocks.Unlock()
	for root, entry := range crewEditLocks.held {
		if entry.sessionID == sessionID {
			delete(crewEditLocks.held, root)
			log.Printf("[CREW_LOCK] crew=%s freed (holder %s's chat ended)", root, entry.userID)
		}
	}
}

type crewEditLockState struct {
	Held      bool      `json:"held"`
	Holder    string    `json:"holder,omitempty"`
	Since     time.Time `json:"since,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

func crewEditLockStatus(root string, now time.Time) crewEditLockState {
	crewEditLocks.Lock()
	defer crewEditLocks.Unlock()
	entry := crewEditLocks.held[canonicalCrewWorkspaceRoot(root)]
	window := crewEditLockWindow()
	if entry == nil || now.Sub(entry.last) >= window {
		return crewEditLockState{}
	}
	return crewEditLockState{Held: true, Holder: crewEditLockName(entry.userID), Since: entry.since, ExpiresAt: entry.last.Add(window)}
}

// crewEditLockRefuse takes the lock for an owner's write (rel is the Crew-relative path, "" for settings and
// instructions). It writes the refusal and returns true when another owner holds it.
func crewEditLockRefuse(w http.ResponseWriter, crew crewProjectBinding, claims *UserClaims, rel string) bool {
	if claims == nil || crewEditLockExempt(rel) {
		return false
	}
	if message := crewEditLockTake(crew.Binding.WorkspacePath, claims.UserID, "", time.Now()); message != "" {
		externalError(w, http.StatusConflict, "edit_lock", message)
		return true
	}
	return false
}

// crewBuilderTurn says whether a Crew turn is a person typing in the Builder chat (not a schedule, bot or sub-agent).
func crewBuilderTurn(req QueryRequest) bool {
	trigger := strings.ToLower(strings.TrimSpace(req.TriggeredBy))
	return (trigger == "" || trigger == "manual" || req.UserInteractiveContinuation) && strings.TrimSpace(req.BotPlatform) == "" &&
		strings.TrimSpace(req.ParentSessionID) == "" && strings.TrimSpace(req.SessionKind) == ""
}

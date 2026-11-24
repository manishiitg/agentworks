package server

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A synced chat is kept only while the server would still accept a sync for it (owner, 24 Nov: "yes build it"):
// the agent is still on the Team (an app agent: shared there, Team on) and its person is still a member. Removing or
// deleting the agent, taking it out of the Team, its person leaving, or switching the Team off deletes the copy; a
// joined Claude Code or Codex session keeps syncing with the Team off, so it keeps its copy.
// There is no time limit while it is kept. Reads stop at once through admission; the files go on the next prune.

// crewSyncedChatPruneEvery also repairs anything a missed change notice left behind.
const crewSyncedChatPruneEvery = 10 * time.Minute

var crewSyncedChatPruner sync.Once

// startCrewSyncedChatPruner prunes every Team once at startup (this also removes leftovers from before these
// rules), then on a timer.
func startCrewSyncedChatPruner() {
	crewSyncedChatPruner.Do(func() {
		go func() {
			pruneAllCrewSyncedChats()
			for range time.Tick(crewSyncedChatPruneEvery) {
				pruneAllCrewSyncedChats()
			}
		}()
	})
}

func pruneAllCrewSyncedChats() {
	root, err := crewroot.StateRoot()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(filepath.Join(root, "team-folders"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			pruneCrewSyncedChats(e.Name())
		}
	}
}

// crewSyncedChatKept is the upload rule itself: wanted (an app agent on a Team that is on, or a joined terminal,
// whose joining is its capture choice) and its maker still saved on the Team.
func crewSyncedChatKept(t crewTeam, a crewTeamRegisteredAgent) bool {
	wanted, err := crewOfflineAppWanted(t, a)
	return err == nil && wanted && crewTeamSourceRowAdmits(t, a.OwnerID, a)
}

// pruneCrewSyncedChats deletes one Team's synced chats that are no longer kept, and folders that hold no chat.
// The decision is made under the Team lock against the current record; the files are removed after it.
func pruneCrewSyncedChats(project string) {
	dir, err := crewTeamDir(project)
	if err != nil {
		return
	}
	copies := filepath.Join(dir, "offline-copies")
	entries, err := os.ReadDir(copies)
	if err != nil {
		return
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	t, err := readCrewTeam(project)
	if err != nil && !os.IsNotExist(err) {
		mu.Unlock()
		return
	}
	var trash []string
	removed, empty := 0, 0
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(copies, name)
		if strings.HasPrefix(name, ".pruned-") {
			trash = append(trash, path)
			continue
		}
		if !e.IsDir() {
			continue
		}
		if a, ok := t.Agents[crewTeamAgentIDPrefix+name]; ok && crewSyncedChatKept(t, a) {
			if _, statErr := os.Stat(filepath.Join(path, "history.json")); statErr == nil {
				continue
			}
			if inner, _ := os.ReadDir(path); len(inner) > 0 {
				continue // a first upload is still in progress
			}
			empty++
		} else {
			removed++
			log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s%s event=pruned", project, crewTeamAgentIDPrefix, name)
		}
		// Rename under the lock so no upload or read sees a half-deleted folder.
		moved := filepath.Join(copies, ".pruned-"+name+"-"+time.Now().UTC().Format("20060102T150405.000000000"))
		if os.Rename(path, moved) == nil {
			trash = append(trash, moved)
		}
	}
	mu.Unlock()
	for _, path := range trash {
		if err := os.RemoveAll(path); err != nil {
			log.Printf("[AGENT_OFFLINE_COPY] project=%s event=prune_failed error=%q", project, err.Error())
		}
	}
	if removed > 0 || empty > 0 {
		log.Printf("[AGENT_OFFLINE_COPY] project=%s event=prune removed=%d empty=%d", project, removed, empty)
	}
}

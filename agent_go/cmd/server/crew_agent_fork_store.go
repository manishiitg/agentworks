package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Conversation is deliberately the portable history/native descriptor, not a
// ProductConversationRecord: account IDs, runtime handles and grants stay home.
type crewAgentForkPayload struct {
	Conversation *crewHandoffConversation `json:"conversation"`
	NativeFiles  map[string]chunkedFile   `json:"native_files,omitempty"`
}

func readCrewAgentFork(project, id string) (crewAgentFork, error) {
	var offer crewAgentFork
	dir, err := crewAgentForkDirectory(project, id)
	if err != nil {
		return offer, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "fork.json"))
	if err != nil {
		return offer, err
	}
	if err = json.Unmarshal(raw, &offer); err != nil {
		return offer, err
	}
	if offer.Project != project || offer.ID != id {
		return offer, fmt.Errorf("agent fork record is unavailable")
	}
	return offer, nil
}

// Staging bytes never travel through the Team's shared-folder namespace. Each
// fork has its own chunk store, removable together with the private chat.
func crewAgentForkChunkStore(offer crewAgentFork) (string, error) {
	dir, err := crewAgentForkDirectory(offer.Project, offer.ID)
	if err != nil {
		return "", err
	}
	store := filepath.Join(dir, "chunks")
	return store, os.MkdirAll(store, 0700)
}

func validateCrewAgentForkPayload(payload crewAgentForkPayload) error {
	if err := validateCrewHandoffConversation(payload.Conversation); err != nil {
		return err
	}
	if payload.Conversation.Native == nil {
		if len(payload.NativeFiles) != 0 {
			return fmt.Errorf("native files need their session descriptor")
		}
		return nil
	}
	if err := payload.Conversation.Native.Validate(); err != nil {
		return err
	}
	return crewNativeFilesMatch(payload.Conversation.Native, payload.NativeFiles)
}

// Caller holds the Team record lock across admission and commit. Metadata is
// published as ready only after every chunk and assembled native file checks.
func stageCrewAgentFork(team crewTeam, offer crewAgentFork, person string, payload crewAgentForkPayload, now time.Time) error {
	if person != offer.Sender {
		return fmt.Errorf("only the maker can upload this agent")
	}
	if err := offer.admit(team, person, false, now); err != nil {
		return err
	}
	if err := validateCrewAgentForkPayload(payload); err != nil {
		return err
	}
	dir, err := crewAgentForkDirectory(offer.Project, offer.ID)
	if err != nil {
		return err
	}
	if payload.Conversation.Native != nil {
		store, err := crewAgentForkChunkStore(offer)
		if err != nil {
			return err
		}
		stage := filepath.Join(dir, "native")
		if err = os.MkdirAll(stage, 0700); err != nil {
			return err
		}
		if err = crewNativeAssembleInto(stage, payload.NativeFiles, func(c transferChunk) ([]byte, error) { return readChunk(store, c) }); err != nil {
			return err
		}
		bundle := *payload.Conversation.Native
		bundle.Root = stage
		if err = bundle.ValidateFiles(); err != nil {
			return err
		}
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "conversation.json"), payload, 0600); err != nil {
		return err
	}
	offer.Ready = true
	return saveCrewAgentFork(offer)
}

func downloadCrewAgentFork(team crewTeam, offer crewAgentFork, person string, now time.Time) (crewAgentForkPayload, error) {
	var payload crewAgentForkPayload
	if err := offer.admit(team, person, true, now); err != nil {
		return payload, err
	}
	dir, err := crewAgentForkDirectory(offer.Project, offer.ID)
	if err != nil {
		return payload, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "conversation.json"))
	if err != nil {
		return payload, err
	}
	if len(raw) > crewHandoffHistoryLimit {
		return payload, fmt.Errorf("the staged conversation is too large")
	}
	if err = json.Unmarshal(raw, &payload); err != nil {
		return payload, err
	}
	return payload, validateCrewAgentForkPayload(payload)
}

func cancelCrewAgentFork(team crewTeam, offer crewAgentFork, person string, now time.Time) error {
	if err := offer.admit(team, person, false, now); err != nil {
		return err
	}
	return removeCrewAgentFork(offer)
}

// Used by every pending-fork surface. Expiry cleanup also runs on the
// server maintenance tick, so unattended private uploads do not live forever.
func listCrewAgentForks(project, person string, now time.Time) ([]crewAgentFork, error) {
	out := []crewAgentFork{}
	team, err := readCrewTeam(project)
	if err != nil {
		return out, err
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return out, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "agent-forks"))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		offer, err := readCrewAgentFork(project, entry.Name())
		if err != nil {
			return out, err
		}
		if !now.Before(offer.Expires) {
			if err = removeCrewAgentFork(offer); err != nil {
				return out, err
			}
			continue
		}
		if offer.Ready && offer.admit(team, person, false, now) == nil {
			out = append(out, offer)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// Maintenance has no viewing identity; this scans only fork metadata in
// private Team state. It never reads or logs the staged conversation itself.
func expireCrewAgentForks(ctx context.Context, project string, now time.Time) error {
	dir, err := crewTeamDir(project)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "agent-forks"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() {
			continue
		}
		offer, err := readCrewAgentFork(project, entry.Name())
		if err != nil {
			return err
		}
		if !now.Before(offer.Expires) {
			if err = removeCrewAgentFork(offer); err != nil {
				return err
			}
		}
	}
	return nil
}

// Include Team-off Crews too: turning a Team off must not retain an upload.
func expireAllCrewAgentForks(ctx context.Context, now time.Time) {
	root, err := crewroot.StateRoot()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(filepath.Join(root, "team-folders"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		if !entry.IsDir() {
			continue
		}
		dir, err := crewTeamDir(entry.Name())
		if err != nil {
			continue
		}
		mu := crewTeamRecordMutex(dir)
		mu.Lock()
		_ = expireCrewAgentForks(ctx, entry.Name(), now)
		mu.Unlock()
	}
}

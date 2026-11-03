package server

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
)

type crewMoveWatcherState struct {
	Ledger *conditiontrigger.Ledger `json:"ledger,omitempty"`
	Wake   *conditionWake           `json:"wake,omitempty"`
}

func readCrewMoveWatcherJSON(relative string, out any) error {
	f, err := os.Open(filepath.Join(fsutil.WorkspaceDocsRoot(), relative))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(io.LimitReader(f, crewSyncMetadataBytes+1)).Decode(out)
}

func captureCrewMoveWatchers(h *crewMoveHeader) error {
	h.Watchers = map[string]crewMoveWatcherState{}
	for _, s := range h.Schedules {
		var input scheduleExecutionInput
		if err := json.Unmarshal(s.Input, &input); err != nil {
			return err
		}
		if input.Condition == nil {
			continue
		}
		job := projectScheduleJobID(crewProfileID, h.SourceID, s.ID)
		key := h.SourceOwner + "/" + job
		var state crewMoveWatcherState
		if err := readCrewMoveWatcherJSON(conditionLedgerPath(key), &state.Ledger); err != nil {
			return err
		}
		wakeID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("watcher:"+key)).String()
		if err := readCrewMoveWatcherJSON(conditionWakePath(wakeID), &state.Wake); err != nil {
			return err
		}
		if state.Wake != nil && (state.Wake.Owner != h.SourceOwner || state.Wake.JobID != job) {
			return fmt.Errorf("watcher state does not belong to this Crew")
		}
		h.Watchers[s.ID] = state
	}
	return nil
}

func importCrewMoveWatcher(state crewMoveWatcherState, owner string, source, destination projectactions.Schedule) error {
	var before, after scheduleExecutionInput
	if err := json.Unmarshal(source.Input, &before); err != nil {
		return err
	}
	if err := json.Unmarshal(destination.Input, &after); err != nil {
		return err
	}
	if before.Condition == nil || after.Condition == nil {
		return fmt.Errorf("moved watcher state has no watcher")
	}
	oldRevision, newRevision := before.Condition.Revision(), after.Condition.Revision()
	job := projectScheduleJobID(crewProfileID, destination.Project.ID, destination.ID)
	key := owner + "/" + job
	if state.Ledger != nil {
		for i := range state.Ledger.Rows {
			if state.Ledger.Rows[i].Revision == oldRevision {
				state.Ledger.Rows[i].Revision = newRevision
			}
		}
		if state.Ledger.OnceRevision == oldRevision {
			state.Ledger.OnceRevision = newRevision
		}
		if err := fsutil.WriteJSONAtomic(filepath.Join(fsutil.WorkspaceDocsRoot(), conditionLedgerPath(key)), state.Ledger, 0600); err != nil {
			return err
		}
	}
	if state.Wake != nil {
		state.Wake.Owner, state.Wake.JobID = owner, job
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("watcher:"+key)).String()
		return fsutil.WriteJSONAtomic(filepath.Join(fsutil.WorkspaceDocsRoot(), conditionWakePath(id)), state.Wake, 0600)
	}
	return nil
}

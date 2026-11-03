package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

type crewMoveCaller struct {
	Definition conditionCallerDefinition `json:"definition"`
	Script     string                    `json:"script,omitempty"`
	State      crewMoveWatcherState      `json:"state"`
}

func (api *StreamingAPI) crewMoveDispatcher() *ProjectActionDispatcher {
	if api.projectActions != nil {
		return api.projectActions
	}
	return NewProjectActionDispatcher(api, api.agentProfiles)
}

func (api *StreamingAPI) captureCrewMoveCallers(ctx context.Context, roots crewroot.Roots, h *crewMoveHeader) error {
	s := api.crewMoveDispatcher()
	states, err := s.loadState(ctx, h.SourceOwner)
	if err != nil {
		return err
	}
	for _, job := range conditionCallerJobs(h.SourceOwner, states) {
		if job.Profile.ID != crewProfileID || job.ProjectID != roots.ID {
			continue
		}
		if canonicalCrewWorkspaceRoot(job.WatcherSource.Path) != roots.Virtual {
			return fmt.Errorf("watcher belongs to another Crew")
		}
		item := crewMoveCaller{Definition: conditionCallerDefinition{Source: *job.WatcherSource, Schedule: job.Schedule}}
		cfg := job.Schedule.Condition
		if cfg.GitHubPR == nil {
			if err = cfg.Validate(); err != nil {
				return err
			}
			text, found, e := s.readFile(ctx, path.Join(conditionScriptRoot(job), cfg.Script))
			if e != nil {
				return e
			}
			if !found || len(text) > conditiontrigger.MaxOutput || conditiontrigger.Hash(text) != cfg.SHA256 {
				return fmt.Errorf("watcher script is missing or changed")
			}
			item.Script = text
		}
		if err = readCrewMoveWatcherJSON(conditionLedgerPath(conditionKey(job)), &item.State.Ledger); err != nil {
			return err
		}
		if err = readCrewMoveWatcherJSON(conditionWakePath(conditionWakeID(job)), &item.State.Wake); err != nil {
			return err
		}
		h.Callers = append(h.Callers, item)
	}
	return validateCrewMoveCallers(*h)
}

func validateCrewMoveCallers(h crewMoveHeader) error {
	seen := map[string]bool{}
	for _, item := range h.Callers {
		d, cfg := item.Definition, item.Definition.Schedule.Condition
		id := d.Schedule.ID
		if id == "" || seen[id] || d.Source.ProfileID != crewProfileID || d.Source.ProjectID != h.SourceID || cfg == nil {
			return fmt.Errorf("invalid moved watcher")
		}
		seen[id] = true
		if err := cfg.Validate(); err != nil {
			return err
		}
		for _, key := range []string{cfg.Target.ChatID} {
			if key != "" && key != "main" && key != "new" {
				if _, err := crewMoveKey(key, h, "destination", "main"); err != nil {
					return err
				}
			}
		}
		if cfg.GitHubPR == nil && (len(item.Script) > conditiontrigger.MaxOutput || conditiontrigger.Hash(item.Script) != cfg.SHA256) {
			return fmt.Errorf("invalid moved watcher script")
		}
		if wake := item.State.Wake; wake != nil && (wake.Owner != h.SourceOwner || wake.JobID != projectScheduleJobID(crewProfileID, h.SourceID, id)) {
			return fmt.Errorf("watcher wake belongs to another Crew")
		}
	}
	return nil
}

func (api *StreamingAPI) pauseCrewMoveCallers(ctx context.Context, h crewMoveHeader) error {
	s := api.crewMoveDispatcher()
	for _, item := range h.Callers {
		key := projectScheduleStateKey(crewProfileID, h.SourceID, item.Definition.Schedule.ID)
		if err := s.updateStateByKey(ctx, h.SourceOwner, key, func(st *productScheduleUserState) {
			if st.Watcher != nil {
				st.Watcher.Schedule.Enabled = false
			}
		}); err != nil {
			return err
		}
	}
	return nil
}

func (api *StreamingAPI) importCrewMoveCallers(ctx context.Context, claims *UserClaims, roots crewroot.Roots, h crewMoveHeader, pair, main string) error {
	s := api.crewMoveDispatcher()
	for _, item := range h.Callers {
		d := item.Definition
		oldID := d.Schedule.ID
		// Use the same config rebinding as timer-backed watchers, preserving the
		// caller's own script workspace and Vault authority on the destination.
		raw, err := json.Marshal(scheduleExecutionInput{Condition: d.Schedule.Condition})
		if err != nil {
			return err
		}
		before := projectactions.Schedule{ID: oldID, Input: raw}
		after := projectactions.Schedule{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(pair+"/watcher/"+oldID)).String(), Input: raw, Enabled: d.Schedule.Enabled, Project: projectactions.Project{Kind: "crew", ID: roots.ID}}
		if err = rebindCrewMoveWatcher(&after, h, roots.ID, main, pair); err != nil {
			return err
		}
		var rebound scheduleExecutionInput
		if err = json.Unmarshal(after.Input, &rebound); err != nil {
			return err
		}
		cfg := rebound.Condition
		if cfg.Target.Name == d.Source.Path {
			cfg.Target.Name = roots.Virtual
		}
		if cfg.GitHubPR == nil {
			cfg.Script = path.Join("code/triggers", after.ID+".py")
		}
		d.Schedule.ID, d.Schedule.Condition, d.Schedule.Enabled = after.ID, cfg, after.Enabled
		d.Source.ProjectID, d.Source.Path = roots.ID, roots.Virtual
		job := conditionCallerJob(claims.UserID, d, productScheduleUserState{})
		if cfg.GitHubPR == nil {
			if err = s.writeFile(ctx, path.Join(conditionScriptRoot(job), cfg.Script), item.Script); err != nil {
				return err
			}
		}
		after.Input, _ = json.Marshal(scheduleExecutionInput{Condition: cfg})
		if err = importCrewMoveWatcher(item.State, claims.UserID, before, after); err != nil {
			return err
		}
		if err = s.updateStateByKey(ctx, claims.UserID, scheduleStateKey(job), func(st *productScheduleUserState) {
			*st = productScheduleUserState{Watcher: &d}
		}); err != nil {
			return err
		}
	}
	return nil
}

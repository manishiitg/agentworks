package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// One-off retirement of AgentWorks' Goal watching (owner, 7 Nov: AgentWorks does not keep watching Goals; the
// person sets up their own schedules, and AgentWorks reaches their other agents, nothing more). Run once per
// server, before the scheduler starts: for each person's AgentWorks it deletes the three built-in schedules (and the
// earlier per-Goal tab checks), releases every Goal it had linked (unlinks what AgentWorks linked, clears a wake_tab
// only where it points at this AgentWorks, archives the notes), and records a done marker. A plain copy of the state
// and the schedules is written first. A Goal's wake_tab that points anywhere else, and every other schedule, are
// never touched. Remove this file once every server has started on this version.

const (
	agentWorksStateFile = "agentworks.json"
	// agentWorksMainChatID is how a Goal's wake_tab names the AgentWorks main chat (as workspace action=chats does).
	agentWorksMainChatID = "main"
	// agentWorksOldCheckPrefix: the per-Goal tab checks of an earlier design.
	agentWorksOldCheckPrefix   = "aw-tab-"
	agentWorksRetirementBackup = "notes/archive/before-goal-watch-retirement.json"
)

// agentWorksRetiredScheduleIDs are the schedules the platform created for AgentWorks' Goal watching.
var agentWorksRetiredScheduleIDs = []string{"aw-morning", "aw-evening", "aw-daily"}

type agentWorksGoalEntry struct {
	Path  string `json:"workspace_path"`
	Label string `json:"label,omitempty"`
	Notes string `json:"notes,omitempty"`
	// ChatID is the Goal's tab from the earlier design.
	ChatID string `json:"chat_id,omitempty"`
	// Detached: the person unlinked this Goal from AgentWorks.
	Detached bool `json:"detached,omitempty"`
}

// agentWorksState is the platform's old record of which notes belong to which Goal (keyed by Goal id).
type agentWorksState struct {
	Goals map[string]*agentWorksGoalEntry `json:"goals"`
	// GoalWatchRetired is set (RFC 3339) once the retirement ran for this AgentWorks.
	GoalWatchRetired string `json:"goal_watch_retired,omitempty"`
}

func readAgentWorksState(ctx context.Context, root string) agentWorksState {
	state := agentWorksState{Goals: map[string]*agentWorksGoalEntry{}}
	if raw, found, err := readFileFromWorkspace(ctx, root+"/"+agentWorksStateFile); err == nil && found {
		_ = json.Unmarshal([]byte(raw), &state)
	}
	if state.Goals == nil {
		state.Goals = map[string]*agentWorksGoalEntry{}
	}
	return state
}

func writeAgentWorksState(ctx context.Context, root string, state agentWorksState) error {
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeRawFileToWorkspace(ctx, root+"/"+agentWorksStateFile, string(encoded)+"\n")
}

func updateGoalWakeTab(ctx context.Context, path string, wake *WorkflowWakeTab) error {
	manifest, found, err := ReadWorkflowManifest(ctx, path)
	if err != nil || !found || manifest == nil {
		return firstError(err, fmt.Errorf("goal unavailable"))
	}
	if manifest.Capabilities.Notifications == nil {
		manifest.Capabilities.Notifications = &WorkflowNotificationConfig{}
	}
	manifest.Capabilities.Notifications.WakeTab = wake
	return WriteWorkflowManifest(ctx, path, manifest)
}

// retireAgentWorksGoalWatching runs the one-off pass for every person who has an AgentWorks.
func (api *StreamingAPI) retireAgentWorksGoalWatching(ctx context.Context) error {
	if api == nil || api.projectActions == nil {
		return nil
	}
	users, err := os.ReadDir(filepath.Join(fsutil.WorkspaceDocsRoot(), "_users"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var failures []error
	for _, user := range users {
		if !user.IsDir() {
			continue
		}
		if err := api.retireAgentWorksGoalWatchingFor(ctx, user.Name()); err != nil {
			failures = append(failures, fmt.Errorf("user %s: %w", user.Name(), err))
		}
	}
	return errors.Join(failures...)
}

func (api *StreamingAPI) retireAgentWorksGoalWatchingFor(ctx context.Context, userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil
	}
	ctx = internalBotRequestContext(ctx, userID)
	defer lockAgentWorks(userID)()
	crew := agentWorksCrew{CrewID: agentWorksCrewID(userID)}
	_, binding, manifest, err := api.projectActions.projectManifest(ctx, userID, crewProfileID, crew.CrewID)
	if err != nil {
		if !errors.Is(err, errProjectNotFound) {
			log.Printf("[AGENTWORKS] event=goal_watch_retirement_skipped user=%s crew=%s error=%v", userID, crew.CrewID, err)
		}
		return nil // no AgentWorks yet, or its manifest could not be read (logged)
	}
	if manifest.Role != agentWorksRole {
		return nil
	}
	crew.WorkspacePath = binding.WorkspacePath
	root := crew.WorkspacePath
	state := readAgentWorksState(ctx, root)
	if state.GoalWatchRetired != "" {
		return nil
	}
	schedules, err := readProjectSchedules(ctx, root)
	if err != nil {
		return fmt.Errorf("read schedules: %w", err)
	}
	// A plain copy of what is about to change.
	backup, err := json.MarshalIndent(map[string]any{"state": state, "schedules": schedules}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeCrewSpecFiles(ctx, root, map[string]string{agentWorksRetirementBackup: string(backup) + "\n"}); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	removed := []string{}
	var failures []error
	for _, schedule := range schedules {
		if !slices.Contains(agentWorksRetiredScheduleIDs, schedule.ID) && !strings.HasPrefix(schedule.ID, agentWorksOldCheckPrefix) {
			continue
		}
		if err := api.projectActions.DeleteProjectSchedule(ctx, userID, projectScheduleJobID(crewProfileID, crew.CrewID, schedule.ID)); err != nil {
			failures = append(failures, fmt.Errorf("schedule %s: %w", schedule.ID, err))
			continue
		}
		removed = append(removed, schedule.ID)
	}
	if err := errors.Join(failures...); err != nil {
		log.Printf("[AGENTWORKS] event=goal_watch_retirement_incomplete user=%s crew=%s removed=%v error=%v", userID, crew.CrewID, removed, err)
		return err // no marker: the next start tries again
	}
	released := 0
	for id, entry := range state.Goals {
		api.releaseAgentWorksGoal(ctx, userID, crew, id, entry, !entry.Detached)
		released++
	}
	state.Goals = map[string]*agentWorksGoalEntry{}
	state.GoalWatchRetired = time.Now().UTC().Format(time.RFC3339)
	if err := writeAgentWorksState(ctx, root, state); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	log.Printf("[AGENTWORKS] event=goal_watch_retired user=%s crew=%s schedules_removed=%v goals_released=%d backup=%s", userID, crew.CrewID, removed, released, agentWorksRetirementBackup)
	return nil
}

// releaseAgentWorksGoal stops watching one Goal: its notes move to notes/archive/ and (when the Goal still points
// here) its wake_tab goes. unlink also removes the link AgentWorks made (a detach already did).
func (api *StreamingAPI) releaseAgentWorksGoal(ctx context.Context, userID string, crew agentWorksCrew, goalID string, entry *agentWorksGoalEntry, unlink bool) {
	root := crew.WorkspacePath
	if unlink && entry.Path != "" {
		if _, err := updateWorkWorkflowReferences(ctx, root, func(existing []string) ([]string, error) {
			kept := []string{}
			for _, ref := range existing {
				if ref != entry.Path {
					kept = append(kept, ref)
				}
			}
			return kept, nil
		}); err != nil {
			log.Printf("[AGENTWORKS] user=%s goal=%s not unlinked: %v", userID, goalID, err)
		}
	}
	if entry.Notes != "" {
		if raw, found, err := readFileFromWorkspace(ctx, root+"/"+entry.Notes); err == nil && found {
			archived := "notes/archive/" + strings.TrimPrefix(entry.Notes, "notes/")
			if err := writeCrewSpecFiles(ctx, root, map[string]string{archived: raw}); err == nil {
				_ = deleteWorkspaceFile(ctx, root+"/"+entry.Notes)
			}
		}
	}
	if entry.Path != "" {
		if manifest, found, err := ReadWorkflowManifest(ctx, entry.Path); err == nil && found && manifest != nil {
			if n := manifest.Capabilities.Notifications; n != nil && n.WakeTab != nil && n.WakeTab.CrewID == crew.CrewID {
				if err := updateGoalWakeTab(ctx, entry.Path, nil); err != nil {
					log.Printf("[AGENTWORKS] user=%s goal=%s wake_tab not cleared: %v", userID, goalID, err)
				}
			}
		}
	}
}

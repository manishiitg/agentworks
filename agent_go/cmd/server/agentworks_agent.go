package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// The person's own AgentWorks agent (PLAT-825-99): an ordinary Crew the platform creates once per person and marks
// with role "agentworks". It works in its main chat only (tabs are made only by the person): it looks after the Goals
// the person runs (their run-as owner); the platform keeps each such Goal linked with a notes file, runs one daily check
// of all of them in the main chat, and wakes the main chat when a scheduled run goes wrong. It can be paused (its
// schedules turned off), never deleted.

// agentWorksRole is the product.json role of the person's AgentWorks Crew.
const agentWorksRole = "agentworks"

const (
	agentWorksName      = "AgentWorks"
	agentWorksTimezone  = "Asia/Kolkata"
	agentWorksStateFile = "agentworks.json"
	agentWorksDailyID   = "aw-daily"
	// agentWorksMainChatID is how a Goal's wake_tab names the AgentWorks main chat (as workspace action=chats does).
	agentWorksMainChatID = "main"
	// agentWorksOldCheckPrefix: the per-Goal tab checks of the earlier design, removed by the one-time move.
	agentWorksOldCheckPrefix = "aw-tab-"
)

// agentWorksBuiltinSkills are the platform's AgentWorks skills (internal/workproduct/skills/agentworks-*).
var agentWorksBuiltinSkills = []string{
	"agentworks-add-new-thing", "agentworks-check-measurement", "agentworks-goal-anatomy", "agentworks-improve-goals",
	"agentworks-inspect-crew", "agentworks-inspect-goal", "agentworks-inspect-relay", "agentworks-review-architecture",
	"agentworks-review-qa", "agentworks-review-strategy", "agentworks-schedules-watchers", "agentworks-suggest-and-build",
}

var errAgentWorksDeleteRefused = errors.New("AgentWorks can be paused, not deleted. To pause it, turn its schedules off (in its Schedules, or ask it in its chat); turn them back on to resume")

// agentWorksCrewID is the person's reserved AgentWorks identity, so two first visits can never make two.
func agentWorksCrewID(userID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("agentworks\x00"+strings.TrimSpace(userID))).String()
}

var agentWorksLocks sync.Map

func lockAgentWorks(userID string) func() {
	value, _ := agentWorksLocks.LoadOrStore(strings.TrimSpace(userID), &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

type agentWorksCrew struct {
	CrewID          string
	WorkspacePath   string
	ConversationKey string
	Created         bool
}

func agentWorksMainSchedules() []productschedule.Schedule {
	return []productschedule.Schedule{
		{ID: "aw-morning", Name: "Morning roll-up", Enabled: true, CronExpression: "45 9 * * *", Timezone: agentWorksTimezone, Messages: []string{
			"Morning roll-up. Read notes/overview.md. For each linked Goal, say in a line what changed since yesterday and its measurement health (read_linked_goal). Then list what needs the person and at most three suggestions with evidence. Keep notes/overview.md current.",
		}},
		{ID: "aw-evening", Name: "Evening summary", Enabled: true, CronExpression: "30 20 * * *", Timezone: agentWorksTimezone, Messages: []string{
			"Evening summary. From today's runs of each linked Goal (read_linked_goal) and what the daily check found, write a short summary readable on a phone: what happened, what was fixed, and what the person needs to decide. Email it to the person with integrations google (Gmail) through this Crew's own Google account; if this Crew has no Google account attached, leave the summary here and say once that attaching one lets you email them. If nothing changed today, send one line saying so.",
		}},
	}
}

// agentWorksDailyCheck is the one daily check of every linked Goal, in the main chat.
func agentWorksDailyCheck() productschedule.Schedule {
	return productschedule.Schedule{ID: agentWorksDailyID, Name: "Daily check", Enabled: true, CronExpression: "45 10 * * *", Timezone: agentWorksTimezone, Messages: []string{
		"Daily check of every linked Goal (and every Crew or Relay listed in notes/overview.md). For each Goal, read its notes (notes/goal-<name>.md) and read_linked_goal for its runs and metrics. A Goal with nothing new gets one line. If a run was due and did not happen (missed, paused or failed to start), or a run failed or stopped partway, find the cause with agentworks-inspect-goal, save the lesson in that Goal's notes and report it here in detail. Only Goals with something to say get more than one line. On Mondays also run the improvement pass (agentworks-improve-goals).",
	}}
}

func agentWorksSpec() crewSpec {
	available := []string{}
	for _, name := range agentWorksBuiltinSkills {
		if skills.IsBuiltinSkillIn("Crew", name) {
			available = append(available, name)
		}
	}
	return crewSpec{
		Name: agentWorksName, Icon: "A", Mode: "cowork",
		Purpose:        "Your own agent: looks after your Goals, Crews and Relays, keeps watch, gets things fixed and tells you only what needs you.",
		SelectedSkills: available,
		Files: map[string]string{
			"notes/overview.md":       "# Overview (keep this file current)\n\nRead this at the start of a turn.\n\n## Goals\nEach Goal you look after has its notes in notes/goal-<name>.md.\n\n## State\nNothing recorded yet.\n",
			"notes/archive/README.md": "Older lessons and the notes of Goals no longer looked after.\n",
		},
	}
}

// ensureAgentWorks is the one place a person's AgentWorks is made and kept in shape: create it when missing (reserved
// id), re-add missing built-in skills, and reconcile the Goals the person runs. Every call site (first visit, Goal and
// Crew changes, the MCP action) goes through here.
func (api *StreamingAPI) ensureAgentWorks(ctx context.Context, userID string) (agentWorksCrew, error) {
	userID = strings.TrimSpace(userID)
	if api == nil || api.agentProfiles == nil || api.projectActions == nil || userID == "" {
		return agentWorksCrew{}, fmt.Errorf("AgentWorks is unavailable on this server")
	}
	ctx = internalBotRequestContext(ctx, userID)
	defer lockAgentWorks(userID)()
	crew := agentWorksCrew{CrewID: agentWorksCrewID(userID)}
	_, binding, manifest, err := api.projectActions.projectManifest(ctx, userID, crewProfileID, crew.CrewID)
	if err != nil {
		spec := agentWorksSpec()
		id, setupErr := api.createWorkspace(ctx, GetUserFromContext(ctx), crewProfileID, spec, true, workspaceCreation{ReservedID: crew.CrewID, Role: agentWorksRole})
		if id == "" {
			return crew, setupErr
		}
		if setupErr != nil {
			log.Printf("[AGENTWORKS] user=%s crew=%s created with setup warning: %v", userID, id, setupErr)
		}
		log.Printf("[AGENTWORKS] user=%s crew=%s created", userID, id)
		crew.Created = true
		if _, binding, manifest, err = api.projectActions.projectManifest(ctx, userID, crewProfileID, crew.CrewID); err != nil {
			return crew, fmt.Errorf("AgentWorks was created but could not be opened: %w", err)
		}
	}
	if manifest.Role != agentWorksRole {
		return crew, fmt.Errorf("AgentWorks is unavailable: its reserved project is not marked as AgentWorks")
	}
	crew.WorkspacePath = binding.WorkspacePath
	crew.ConversationKey = binding.ConversationKey
	if !crew.Created {
		missing := []string{}
		for _, name := range agentWorksSpec().SelectedSkills {
			if !slices.Contains(manifest.Capabilities.SelectedSkills, name) {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			if err := applyCrewCreationSelections(ctx, crewProfileID, crew.WorkspacePath, missing, nil, nil, nil); err != nil {
				log.Printf("[AGENTWORKS] user=%s skills not re-added: %v", userID, err)
			}
		}
	}
	if err := api.reconcileAgentWorksGoals(ctx, userID, crew); err != nil {
		log.Printf("[AGENTWORKS] user=%s goals not reconciled: %v", userID, err)
	}
	return crew, nil
}

// agentWorksSeen remembers who already has an AgentWorks in this process, so a visit costs nothing after the first.
var agentWorksSeen sync.Map

// ensureAgentWorksOnVisit gives every signed-in person an AgentWorks on their first visit, in the background.
func (api *StreamingAPI) ensureAgentWorksOnVisit(userID string) {
	userID = strings.TrimSpace(userID)
	if api == nil || api.projectActions == nil || userID == "" {
		return
	}
	if _, seen := agentWorksSeen.LoadOrStore(userID, true); seen {
		return
	}
	go func() {
		if _, err := api.ensureAgentWorks(context.Background(), userID); err != nil {
			log.Printf("[AGENTWORKS] user=%s not ensured on visit: %v", userID, err)
		}
	}()
}

const agentWorksRefreshDelay = 20 * time.Second

var agentWorksRefresh = struct {
	sync.Mutex
	timers map[string]*time.Timer
}{timers: map[string]*time.Timer{}}

// refreshAgentWorksSoon runs ensure for a person shortly after a Goal or Crew change; bursts of saves collapse into one.
func (api *StreamingAPI) refreshAgentWorksSoon(userIDs ...string) {
	if api == nil || api.projectActions == nil {
		return
	}
	agentWorksRefresh.Lock()
	defer agentWorksRefresh.Unlock()
	for _, userID := range userIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		if timer, pending := agentWorksRefresh.timers[userID]; pending {
			timer.Reset(agentWorksRefreshDelay)
			continue
		}
		agentWorksRefresh.timers[userID] = time.AfterFunc(agentWorksRefreshDelay, func() {
			agentWorksRefresh.Lock()
			delete(agentWorksRefresh.timers, userID)
			agentWorksRefresh.Unlock()
			if _, err := api.ensureAgentWorks(context.Background(), userID); err != nil {
				log.Printf("[AGENTWORKS] user=%s not refreshed: %v", userID, err)
			}
		})
	}
}

// agentWorksGoalChanged is set at startup; a saved Goal tells its owners' AgentWorks to catch up.
var agentWorksGoalChanged func(manifest *WorkflowManifest)

func notifyAgentWorksGoalChanged(manifest *WorkflowManifest) {
	if hook := agentWorksGoalChanged; hook != nil && manifest != nil && manifest.Kind != "relay" {
		hook(manifest)
	}
}

// agentWorksGoalOwners are the people whose AgentWorks may need to change after this Goal changed: its run-as owner
// (who watches it) and its other owners (one of whom may have watched it before an ownership change).
func agentWorksGoalOwners(manifest *WorkflowManifest) []string {
	owners := append([]string{workflowExecutionOwnerUserID(manifest)}, manifest.effectiveOwners()...)
	out := []string{}
	for _, owner := range owners {
		if owner = strings.TrimSpace(owner); owner != "" && !slices.Contains(out, owner) {
			out = append(out, owner)
		}
	}
	return out
}

// isAgentWorksCrew reports whether the project at root is someone's AgentWorks.
func isAgentWorksCrew(ctx context.Context, root string) bool {
	manifest, err := readCrewProjectManifests(ctx, crewProfileID, root)
	return err == nil && manifest.Role == agentWorksRole
}

// --- Goals ---

type agentWorksGoalEntry struct {
	Path  string `json:"workspace_path"`
	Label string `json:"label,omitempty"`
	Notes string `json:"notes,omitempty"`
	// ChatID is the Goal's tab from the earlier design; it is only read by the one-time move, which closes that tab.
	ChatID string `json:"chat_id,omitempty"`
	// Detached: the person unlinked this Goal from AgentWorks; it stays unwatched until they link it again.
	Detached bool `json:"detached,omitempty"`
}

// agentWorksState is the platform's record of which notes belong to which Goal (keyed by Goal id).
type agentWorksState struct {
	Goals map[string]*agentWorksGoalEntry `json:"goals"`
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

type agentWorksGoal struct {
	Path     string
	Manifest *WorkflowManifest
	// HasTimers: the Goal has at least one enabled timed schedule.
	HasTimers bool
}

func agentWorksHasTimers(manifest *WorkflowManifest) bool {
	for _, schedule := range manifest.Schedules {
		if schedule.ScheduleType != "webhook" && schedule.Enabled {
			return true
		}
	}
	return false
}

// agentWorksOwnedGoals are the Goals this person runs (their run-as owner).
func agentWorksOwnedGoals(ctx context.Context, userID string) ([]agentWorksGoal, error) {
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return nil, err
	}
	goals := []agentWorksGoal{}
	for _, workflow := range discovered {
		path := strings.TrimSuffix(strings.TrimSpace(workflow.WorkspacePath), "/")
		if workflow.Manifest == nil || workflow.Manifest.Kind == "relay" || !strings.HasPrefix(path, "Workflow/") || strings.TrimSpace(workflow.Manifest.ID) == "" {
			continue
		}
		if !containsID([]string{workflowExecutionOwnerUserID(workflow.Manifest)}, userID) {
			continue
		}
		manifest, found, err := ReadWorkflowManifest(ctx, path)
		if err != nil || !found || manifest == nil {
			continue
		}
		goals = append(goals, agentWorksGoal{Path: path, Manifest: manifest, HasTimers: agentWorksHasTimers(manifest)})
	}
	sort.Slice(goals, func(i, j int) bool { return goals[i].Manifest.ID < goals[j].Manifest.ID })
	return goals, nil
}

func agentWorksGoalNotes(label, path string) string {
	return fmt.Sprintf("These notes look after the Goal %q (%s).\n\nMode: watch\n\n## Focus\nNone yet.\n\n## Targets\nNone yet: ask the person once and save the answer here.\n\n## Rules specific to this Goal\n\n## Latest lessons\nAt most five; move older ones to notes/archive/.\n", label, path)
}

// reconcileAgentWorksGoals keeps every Goal the person runs linked, with its notes and wake_tab, keeps the main-chat
// schedules, and tidies up Goals that are gone or now run by someone else. Each step runs only when its result is
// missing, so a second call changes nothing and anything the person paused stays paused.
func (api *StreamingAPI) reconcileAgentWorksGoals(ctx context.Context, userID string, crew agentWorksCrew) error {
	goals, err := agentWorksOwnedGoals(ctx, userID)
	if err != nil {
		return err
	}
	root := crew.WorkspacePath
	state := readAgentWorksState(ctx, root)
	before, _ := json.Marshal(state)
	refs, err := readWorkWorkflowReferences(ctx, root)
	if err != nil {
		return err
	}
	_, _, manifest, err := api.projectActions.projectManifest(ctx, userID, crewProfileID, crew.CrewID)
	if err != nil {
		return err
	}
	schedules := map[string]productschedule.Schedule{}
	for _, schedule := range manifest.Schedules {
		schedules[schedule.ID] = schedule
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	// One-time move from the earlier per-Goal tabs to main chat only.
	for id, entry := range state.Goals {
		api.moveAgentWorksGoalToMainChat(ctx, request, userID, crew, id, entry, schedules)
	}
	owned := map[string]bool{}
	hasTimers := false
	for _, goal := range goals {
		id := goal.Manifest.ID
		owned[id] = true
		entry := state.Goals[id]
		if entry == nil {
			entry = &agentWorksGoalEntry{}
			state.Goals[id] = entry
		}
		authorized, _, authErr := authorizeWorkflowContextPathsWithReadRoots(ctx, []string{goal.Path})
		if authErr != nil || len(authorized) != 1 {
			log.Printf("[AGENTWORKS] user=%s goal=%s not linkable: %v", userID, id, authErr)
			continue
		}
		entry.Path = authorized[0]
		linked := slices.Contains(refs, entry.Path)
		if entry.Detached && !linked {
			continue
		}
		entry.Detached = false
		label := firstNonEmptyTrimmed(goal.Manifest.Label, id)
		entry.Label = label
		// 1. Link.
		if !linked {
			if refs, err = updateWorkWorkflowReferences(ctx, root, func(existing []string) ([]string, error) {
				return appendUniqueStrings(existing, entry.Path), nil
			}); err != nil {
				log.Printf("[AGENTWORKS] user=%s goal=%s not linked: %v", userID, id, err)
				continue
			}
		}
		// 2. Notes.
		if entry.Notes == "" {
			entry.Notes = "notes/goal-" + slugifyCrewTitle(label) + ".md"
			for otherID, other := range state.Goals {
				if otherID != id && other.Notes == entry.Notes {
					entry.Notes = "notes/goal-" + slugifyCrewTitle(label) + "-" + id[:min(6, len(id))] + ".md"
				}
			}
		}
		if _, found, readErr := readFileFromWorkspace(ctx, root+"/"+entry.Notes); readErr == nil && !found {
			if err := writeCrewSpecFiles(ctx, root, map[string]string{entry.Notes: agentWorksGoalNotes(label, entry.Path)}); err != nil {
				log.Printf("[AGENTWORKS] user=%s goal=%s notes not written: %v", userID, id, err)
			}
		}
		// 3. wake_tab: this Goal's failed scheduled runs wake the main chat.
		api.setAgentWorksWakeTab(ctx, userID, goal, crew.CrewID)
		hasTimers = hasTimers || goal.HasTimers
	}
	// The main-chat schedules exist only once there is a Goal to watch; each is created once, so a pause stays.
	mainSchedules := []productschedule.Schedule{}
	if len(goals) > 0 {
		mainSchedules = agentWorksMainSchedules()
	}
	if hasTimers {
		mainSchedules = append(mainSchedules, agentWorksDailyCheck())
	}
	for _, schedule := range mainSchedules {
		if saved, ok := schedules[schedule.ID]; ok {
			// One-time: a saved platform message that still names the removed email_me tool takes the current
			// text; the person's own edits and the on/off state are kept.
			if strings.Contains(strings.Join(saved.Messages, "\n"), "email_me") {
				messages := schedule.Messages
				if _, err := api.projectActions.UpdateProjectSchedule(ctx, userID, projectScheduleJobID(crewProfileID, crew.CrewID, schedule.ID), func(s *productschedule.Schedule) { s.Messages = messages }); err != nil {
					log.Printf("[AGENTWORKS] user=%s schedule %s not updated: %v", userID, schedule.ID, err)
				}
			}
			continue
		}
		if _, err := api.projectActions.CreateProjectSchedule(ctx, userID, crewProfileID, crew.CrewID, schedule); err != nil {
			log.Printf("[AGENTWORKS] user=%s schedule %s not created: %v", userID, schedule.ID, err)
		}
	}
	for id, entry := range state.Goals {
		if owned[id] {
			continue
		}
		api.releaseAgentWorksGoal(ctx, userID, crew, id, entry, true)
		delete(state.Goals, id)
	}
	if after, _ := json.Marshal(state); string(after) != string(before) {
		return writeAgentWorksState(ctx, root, state)
	}
	return nil
}

// moveAgentWorksGoalToMainChat is the one-time move from the earlier design (a tab and a daily check per Goal): it
// removes the Goal's tab check, closes its tab, and renames notes/tab-<name>.md to notes/goal-<name>.md with its
// content kept. Once done it finds nothing to do.
func (api *StreamingAPI) moveAgentWorksGoalToMainChat(ctx context.Context, request *http.Request, userID string, crew agentWorksCrew, goalID string, entry *agentWorksGoalEntry, schedules map[string]productschedule.Schedule) {
	checkID := agentWorksOldCheckPrefix + goalID
	if _, ok := schedules[checkID]; ok {
		if err := api.projectActions.DeleteProjectSchedule(ctx, userID, projectScheduleJobID(crewProfileID, crew.CrewID, checkID)); err != nil {
			log.Printf("[AGENTWORKS] user=%s goal=%s tab check not removed: %v", userID, goalID, err)
		} else {
			delete(schedules, checkID)
		}
	}
	if entry.ChatID != "" {
		if status, body := scheduleHandler(request, api.handleCloseAgentProfileSideChat, http.MethodPost, "/api/agent-profiles/work/conversation/close", map[string]string{"id": crewProfileID}, nil, AgentProfileConversationRequest{ConversationKey: crew.CrewID + codeChatSideMarker + entry.ChatID}); status >= 400 && status != http.StatusNotFound {
			log.Printf("[AGENTWORKS] user=%s goal=%s tab not closed: %d %s", userID, goalID, status, strings.TrimSpace(string(body)))
		} else {
			entry.ChatID = ""
		}
	}
	if rest, ok := strings.CutPrefix(entry.Notes, "notes/tab-"); ok {
		root := crew.WorkspacePath
		moved := "notes/goal-" + rest
		raw, found, err := readFileFromWorkspace(ctx, root+"/"+entry.Notes)
		if err != nil {
			return
		}
		if found {
			if err := writeCrewSpecFiles(ctx, root, map[string]string{moved: raw}); err != nil {
				log.Printf("[AGENTWORKS] user=%s goal=%s notes not moved: %v", userID, goalID, err)
				return
			}
			_ = deleteWorkspaceFile(ctx, root+"/"+entry.Notes)
		}
		entry.Notes = moved
	}
}

// setAgentWorksWakeTab points the Goal's wake_tab at the AgentWorks main chat when it is empty, names a Crew that no
// longer exists, or names this same AgentWorks (including one of its old tabs). A wake_tab the person set to another
// Crew stays. Only the run-as owner's Goals come here.
func (api *StreamingAPI) setAgentWorksWakeTab(ctx context.Context, userID string, goal agentWorksGoal, crewID string) {
	var current *WorkflowWakeTab
	if notifications := goal.Manifest.Capabilities.Notifications; notifications != nil {
		current = notifications.WakeTab
	}
	switch {
	case current == nil || strings.TrimSpace(current.CrewID) == "":
	case current.CrewID == crewID:
		if current.ChatID == agentWorksMainChatID {
			return
		}
	case api.projectActions.crewProjectExists(ctx, userID, crewProfileID, current.CrewID):
		return
	}
	if err := updateGoalWakeTab(ctx, goal.Path, &WorkflowWakeTab{CrewID: crewID, ChatID: agentWorksMainChatID}); err != nil {
		log.Printf("[AGENTWORKS] user=%s goal=%s wake_tab not set: %v", userID, goal.Manifest.ID, err)
	}
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

// releaseAgentWorksGoal stops watching one Goal: its notes move to notes/archive/ and (when the Goal still points
// here) its wake_tab goes. unlink also removes the link (a detach already did).
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

// agentWorksGoalDetached records that the person unlinked a Goal from their AgentWorks, and stops watching it.
func (api *StreamingAPI) agentWorksGoalDetached(ctx context.Context, userID, root, path string) {
	if api == nil || api.projectActions == nil || !isAgentWorksCrew(ctx, root) {
		return
	}
	defer lockAgentWorks(userID)()
	state := readAgentWorksState(ctx, root)
	crew := agentWorksCrew{CrewID: agentWorksCrewID(userID), WorkspacePath: root}
	for id, entry := range state.Goals {
		if entry.Path != path || entry.Detached {
			continue
		}
		api.releaseAgentWorksGoal(ctx, userID, crew, id, entry, false)
		*entry = agentWorksGoalEntry{Path: entry.Path, Label: entry.Label, Detached: true}
		if err := writeAgentWorksState(ctx, root, state); err != nil {
			log.Printf("[AGENTWORKS] user=%s goal=%s detach not recorded: %v", userID, id, err)
		}
		return
	}
}

// handleOpenAgentWorks is the app's AgentWorks entry: it returns (making it on first use) the signed-in person's own
// AgentWorks, the same ensure as a first visit and workspace action=agentworks. An optional {"message": "..."} is sent
// into its main chat as the person, through the app's own chat send (handleAgentProfileChatQuery), without waiting
// for the answer; the reply streams in the chat as usual.
func (api *StreamingAPI) handleOpenAgentWorks(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil || strings.TrimSpace(user.UserID) == "" {
		http.Error(w, "Sign in to open AgentWorks.", http.StatusUnauthorized)
		return
	}
	var input struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes)).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "Send your message as plain text.", http.StatusBadRequest)
		return
	}
	message := strings.TrimSpace(input.Message)
	if len([]rune(message)) > codeChatMessageMaxRune {
		http.Error(w, "That message is too long. Shorten it, or send the details in the chat in parts.", http.StatusBadRequest)
		return
	}
	crew, err := api.ensureAgentWorks(context.Background(), user.UserID)
	if err != nil {
		http.Error(w, "Your AgentWorks could not be opened: "+err.Error(), http.StatusConflict)
		return
	}
	out := map[string]any{"crew_id": crew.CrewID, "conversation_key": crew.ConversationKey, "name": agentWorksName, "created": crew.Created}
	if message != "" {
		// The owner's main chat is addressed by the project id, as the app's main chat send does.
		status, body := scheduleHandler(r, api.handleAgentProfileChatQuery, http.MethodPost, "/api/agent-profiles/"+crewProfileID+"/query", map[string]string{"id": crewProfileID}, nil, AgentProfileChatRequest{Message: message, ConversationKey: crew.CrewID})
		if status >= 300 {
			log.Printf("[AGENTWORKS] user=%s crew=%s message not sent: %d %s", user.UserID, crew.CrewID, status, strings.TrimSpace(string(body)))
			reason := "Open AgentWorks and try again in its chat."
			var failure struct{ Error, Message string }
			if json.Unmarshal(body, &failure) == nil && strings.TrimSpace(failure.Error+failure.Message) != "" {
				reason = strings.TrimSpace(failure.Error + " " + failure.Message)
			}
			http.Error(w, "Your message could not be sent to AgentWorks: "+reason, status)
			return
		}
		var receipt map[string]any
		if json.Unmarshal(body, &receipt) == nil {
			if queryID, _ := receipt["query_id"].(string); queryID != "" {
				out["query_id"] = queryID
			}
		}
		out["sent"] = true
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

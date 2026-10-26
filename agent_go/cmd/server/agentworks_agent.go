package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// The person's own AgentWorks agent (PLAT-825-99): an ordinary Crew the platform creates once per person and marks
// with role "agentworks". It looks after the Goals the person runs (their run-as owner); the platform keeps each such
// Goal linked, gives it a tab, a notes file and a daily check, and wakes that tab when a scheduled run goes wrong.
// It can be paused (its schedules turned off), never deleted.

// agentWorksRole is the product.json role of the person's AgentWorks Crew.
const agentWorksRole = "agentworks"

const (
	agentWorksName      = "AgentWorks"
	agentWorksTimezone  = "Asia/Kolkata"
	agentWorksStateFile = "agentworks.json"
	// A Goal with timers but no plain daily cron gets its check at 10:45.
	agentWorksDefaultCheckMinute = 10*60 + 45
	agentWorksCheckPrefix        = "aw-tab-"
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
	CrewID        string
	WorkspacePath string
	Created       bool
}

func agentWorksMainSchedules() []productschedule.Schedule {
	return []productschedule.Schedule{
		{ID: "aw-morning", Name: "Morning roll-up", Enabled: true, CronExpression: "45 9 * * *", Timezone: agentWorksTimezone, Messages: []string{
			"Morning roll-up. Read notes/overview.md. For each tab, say in a line what it found since yesterday and the measurement health of its Goal (read_linked_goal). Then list what needs the person and at most three suggestions with evidence. Keep notes/overview.md current.",
		}},
		{ID: "aw-evening", Name: "Evening summary", Enabled: true, CronExpression: "30 20 * * *", Timezone: agentWorksTimezone, Messages: []string{
			"Evening summary. From today's runs of each linked Goal (read_linked_goal) and what the tabs found, write a short summary readable on a phone: what happened, what was fixed, and what the person needs to decide. Send it with email_me. If nothing changed today, send one line saying so.",
		}},
	}
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
			"notes/overview.md":       "# Overview (keep this file current)\n\nRead this at the start of a main-tab turn.\n\n## Tabs\nOne tab per Goal you run; each has its notes in notes/tab-<name>.md.\n\n## State\nNothing recorded yet.\n",
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
	Path   string `json:"workspace_path"`
	Label  string `json:"label,omitempty"`
	ChatID string `json:"chat_id,omitempty"`
	Notes  string `json:"notes,omitempty"`
	// Detached: the person unlinked this Goal from AgentWorks; it stays unwatched until they link it again.
	Detached bool `json:"detached,omitempty"`
}

// agentWorksState is the platform's record of which tab and notes belong to which Goal (keyed by Goal id).
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
	// CheckMinute is the daily check's minute of the day (Asia/Kolkata); -1 when the Goal has no active timers.
	CheckMinute int
}

// agentWorksCheckMinute is an hour after the Goal's first daily run, in Asia/Kolkata; -1 when it has no active timers.
func agentWorksCheckMinute(manifest *WorkflowManifest) int {
	ist, err := time.LoadLocation(agentWorksTimezone)
	if err != nil {
		ist = time.FixedZone("IST", 5*3600+1800)
	}
	now := time.Now().In(ist)
	dayStart := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, ist)
	active, first := false, -1
	for _, schedule := range manifest.Schedules {
		if schedule.ScheduleType == "webhook" || !schedule.Enabled {
			continue
		}
		active = true
		expression := strings.TrimSpace(schedule.CronExpression)
		if expression == "" {
			continue
		}
		if tz := strings.TrimSpace(schedule.Timezone); tz != "" {
			expression = "CRON_TZ=" + tz + " " + expression
		}
		parsed, err := cron.ParseStandard(expression)
		if err != nil {
			continue
		}
		next := parsed.Next(dayStart.Add(-time.Second))
		if next.IsZero() || !next.Before(dayStart.Add(24*time.Hour)) {
			continue
		}
		local := next.In(ist)
		if minute := local.Hour()*60 + local.Minute(); first < 0 || minute < first {
			first = minute
		}
	}
	if !active {
		return -1
	}
	if first < 0 {
		return agentWorksDefaultCheckMinute
	}
	return (first + 60) % (24 * 60)
}

// agentWorksOwnedGoals are the Goals this person runs (their run-as owner), each with its daily check minute,
// staggered so no two checks start in the same minute.
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
		goals = append(goals, agentWorksGoal{Path: path, Manifest: manifest, CheckMinute: agentWorksCheckMinute(manifest)})
	}
	sort.Slice(goals, func(i, j int) bool {
		if goals[i].CheckMinute != goals[j].CheckMinute {
			return goals[i].CheckMinute < goals[j].CheckMinute
		}
		return goals[i].Manifest.ID < goals[j].Manifest.ID
	})
	taken := map[int]bool{}
	for i := range goals {
		if goals[i].CheckMinute < 0 {
			continue
		}
		for taken[goals[i].CheckMinute] {
			goals[i].CheckMinute = (goals[i].CheckMinute + 1) % (24 * 60)
		}
		taken[goals[i].CheckMinute] = true
	}
	return goals, nil
}

func agentWorksCheckMessage(label, path, notes string) string {
	return fmt.Sprintf("Daily check of the Goal %q (%s). Read %s, then read_linked_goal action=runs for %s. If no run was due since the last check, reply in one line. If a run was due and did not happen (missed, paused or failed to start), or a run failed or stopped partway, find the cause with agentworks-inspect-goal, save the lesson in the notes and report it here. On Mondays also run the improvement pass (agentworks-improve-goals).", label, path, notes, path)
}

func agentWorksTabNotes(label, path string) string {
	return fmt.Sprintf("This tab looks after the Goal %q (%s).\n\nMode: watch\n\n## Focus\nNone yet.\n\n## Targets\nNone yet: ask the person once and save the answer here.\n\n## Rules specific to this tab\n\n## Latest lessons\nAt most five; move older ones to notes/archive/.\n", label, path)
}

// reconcileAgentWorksGoals keeps every Goal the person runs linked, with its tab, notes, daily check and wake_tab, and
// tidies up Goals that are gone or now run by someone else. Each step runs only when its result is missing, so a
// second call changes nothing and anything the person paused stays paused.
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
	tabs := map[string]bool{}
	for _, chat := range api.crewChats(request, userID, crew.CrewID, root) {
		if chat.CanWrite && !chat.IsMain {
			tabs[chat.ChatID] = true
		}
	}
	owned := map[string]bool{}
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
		// 2. Tab named after the Goal.
		if entry.ChatID == "" || !tabs[entry.ChatID] {
			chatID, tabErr := api.openAgentWorksTab(request, crew.CrewID, label)
			if tabErr != nil {
				log.Printf("[AGENTWORKS] user=%s goal=%s no tab: %v", userID, id, tabErr)
				entry.ChatID = ""
			} else {
				entry.ChatID = chatID
				tabs[chatID] = true
			}
		}
		// 3. Notes.
		if entry.Notes == "" {
			entry.Notes = "notes/tab-" + slugifyCrewTitle(label) + ".md"
			for otherID, other := range state.Goals {
				if otherID != id && other.Notes == entry.Notes {
					entry.Notes = "notes/tab-" + slugifyCrewTitle(label) + "-" + id[:min(6, len(id))] + ".md"
				}
			}
		}
		if _, found, readErr := readFileFromWorkspace(ctx, root+"/"+entry.Notes); readErr == nil && !found {
			if err := writeCrewSpecFiles(ctx, root, map[string]string{entry.Notes: agentWorksTabNotes(label, entry.Path)}); err != nil {
				log.Printf("[AGENTWORKS] user=%s goal=%s notes not written: %v", userID, id, err)
			}
		}
		// 4. Daily check, only for a Goal with active timers.
		checkID := agentWorksCheckPrefix + id
		existing, hasCheck := schedules[checkID]
		switch {
		case goal.CheckMinute < 0 && hasCheck:
			if err := api.projectActions.DeleteProjectSchedule(ctx, userID, projectScheduleJobID(crewProfileID, crew.CrewID, checkID)); err != nil {
				log.Printf("[AGENTWORKS] user=%s goal=%s check not removed: %v", userID, id, err)
			}
		case goal.CheckMinute >= 0 && entry.ChatID != "":
			cronExpression := fmt.Sprintf("%d %d * * *", goal.CheckMinute%60, goal.CheckMinute/60)
			message := agentWorksCheckMessage(label, entry.Path, entry.Notes)
			chatKey := crew.CrewID + codeChatSideMarker + entry.ChatID
			if !hasCheck {
				check := productschedule.Schedule{ID: checkID, Name: "Daily check: " + label, Enabled: true, CronExpression: cronExpression, Timezone: agentWorksTimezone, Messages: []string{message}, ChatKey: chatKey}
				if _, err := api.projectActions.CreateProjectSchedule(ctx, userID, crewProfileID, crew.CrewID, check); err != nil {
					log.Printf("[AGENTWORKS] user=%s goal=%s check not created: %v", userID, id, err)
				}
			} else if existing.CronExpression != cronExpression || existing.ChatKey != chatKey || len(existing.Messages) != 1 || existing.Messages[0] != message {
				// The Goal's time, name or tab moved: follow it, keeping the check's on/off state.
				if _, err := api.projectActions.UpdateProjectSchedule(ctx, userID, projectScheduleJobID(crewProfileID, crew.CrewID, checkID), func(schedule *productschedule.Schedule) {
					schedule.CronExpression, schedule.Timezone, schedule.ChatKey, schedule.Messages = cronExpression, agentWorksTimezone, chatKey, []string{message}
				}); err != nil {
					log.Printf("[AGENTWORKS] user=%s goal=%s check not updated: %v", userID, id, err)
				}
			}
			// 5. wake_tab: this Goal's failed scheduled runs wake its tab.
			api.setAgentWorksWakeTab(ctx, userID, goal, crew.CrewID, entry.ChatID)
		}
	}
	// The main roll-up and evening summary exist only once there is a Goal to watch.
	if len(goals) > 0 {
		for _, schedule := range agentWorksMainSchedules() {
			if _, ok := schedules[schedule.ID]; ok {
				continue
			}
			if _, err := api.projectActions.CreateProjectSchedule(ctx, userID, crewProfileID, crew.CrewID, schedule); err != nil {
				log.Printf("[AGENTWORKS] user=%s schedule %s not created: %v", userID, schedule.ID, err)
			}
		}
	}
	for id, entry := range state.Goals {
		if owned[id] {
			continue
		}
		api.releaseAgentWorksGoal(ctx, request, userID, crew, id, entry, true)
		delete(state.Goals, id)
	}
	if after, _ := json.Marshal(state); string(after) != string(before) {
		return writeAgentWorksState(ctx, root, state)
	}
	return nil
}

// setAgentWorksWakeTab points the Goal's wake_tab at this tab when it is empty, names a Crew that no longer exists,
// or names an older tab of this same AgentWorks. A wake_tab the person set to another Crew stays.
func (api *StreamingAPI) setAgentWorksWakeTab(ctx context.Context, userID string, goal agentWorksGoal, crewID, chatID string) {
	var current *WorkflowWakeTab
	if notifications := goal.Manifest.Capabilities.Notifications; notifications != nil {
		current = notifications.WakeTab
	}
	switch {
	case current == nil || strings.TrimSpace(current.CrewID) == "":
	case current.CrewID == crewID:
		if current.ChatID == chatID {
			return
		}
	case api.projectActions.crewProjectExists(ctx, userID, crewProfileID, current.CrewID):
		return
	}
	if err := updateGoalWakeTab(ctx, goal.Path, &WorkflowWakeTab{CrewID: crewID, ChatID: chatID}); err != nil {
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

// releaseAgentWorksGoal stops watching one Goal: its daily check, its tab and (when the Goal still points here) its
// wake_tab go, its notes move to notes/archive/. unlink also removes the link (a detach already did).
func (api *StreamingAPI) releaseAgentWorksGoal(ctx context.Context, request *http.Request, userID string, crew agentWorksCrew, goalID string, entry *agentWorksGoalEntry, unlink bool) {
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
	if err := api.projectActions.DeleteProjectSchedule(ctx, userID, projectScheduleJobID(crewProfileID, crew.CrewID, agentWorksCheckPrefix+goalID)); err != nil && !strings.Contains(err.Error(), "not found") {
		log.Printf("[AGENTWORKS] user=%s goal=%s check not removed: %v", userID, goalID, err)
	}
	if entry.ChatID != "" {
		if status, body := scheduleHandler(request, api.handleCloseAgentProfileSideChat, http.MethodPost, "/api/agent-profiles/work/conversation/close", map[string]string{"id": crewProfileID}, nil, AgentProfileConversationRequest{ConversationKey: crew.CrewID + codeChatSideMarker + entry.ChatID}); status >= 400 {
			log.Printf("[AGENTWORKS] user=%s goal=%s tab not closed: %d %s", userID, goalID, status, strings.TrimSpace(string(body)))
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
	request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	for id, entry := range state.Goals {
		if entry.Path != path || entry.Detached {
			continue
		}
		api.releaseAgentWorksGoal(ctx, request, userID, crew, id, entry, false)
		*entry = agentWorksGoalEntry{Path: entry.Path, Label: entry.Label, Detached: true}
		if err := writeAgentWorksState(ctx, root, state); err != nil {
			log.Printf("[AGENTWORKS] user=%s goal=%s detach not recorded: %v", userID, id, err)
		}
		return
	}
}

// openAgentWorksTab opens a named side chat in the person's AgentWorks, the same way manage_crew_chats side_open does.
func (api *StreamingAPI) openAgentWorksTab(request *http.Request, crewID, name string) (string, error) {
	name, err := normalizeChatHistoryTitle(name)
	if err != nil {
		return "", err
	}
	raw := make([]byte, 4)
	_, _ = rand.Read(raw)
	id := hex.EncodeToString(raw)
	status, body := scheduleHandler(request, api.handleResolveAgentProfileConversation, http.MethodPost, "/api/agent-profiles/work/conversation", map[string]string{"id": crewProfileID}, nil, AgentProfileConversationRequest{ConversationKey: crewID + codeChatSideMarker + id, Name: name})
	if status >= 400 {
		return "", fmt.Errorf("%d %s", status, strings.TrimSpace(string(body)))
	}
	return id, nil
}

// handleOpenAgentWorks is the app's "Ask AgentWorks" entry: it returns (making it on first use) the signed-in
// person's own AgentWorks, the same ensure as a first visit and workspace action=agentworks.
func (api *StreamingAPI) handleOpenAgentWorks(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil || strings.TrimSpace(user.UserID) == "" {
		http.Error(w, "Sign in to open AgentWorks.", http.StatusUnauthorized)
		return
	}
	crew, err := api.ensureAgentWorks(context.Background(), user.UserID)
	if err != nil {
		http.Error(w, "Your AgentWorks could not be opened: "+err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"crew_id": crew.CrewID, "name": agentWorksName, "created": crew.Created})
}

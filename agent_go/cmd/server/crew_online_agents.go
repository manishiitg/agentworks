package server

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Online agents are the confirmed server placements, with automations read on
// that server. Disabled Mac entries are not the online agent's current setup.
type crewOnlineAgent struct {
	Tab         string              `json:"tab"`
	Name        string              `json:"name"`
	Program     string              `json:"program,omitempty"`
	Automations []crewTabAutomation `json:"automations"`
}

// The caller has already passed crewSyncRoots' owner admission. This is an
// owner status projection, not a new directory or automation mutation path.
func (api *StreamingAPI) crewOnlineAgents(ctx context.Context, owner string, roots crewroot.Roots, placements map[string]crewAgentPlacement) (rows []crewOnlineAgent, err error) {
	started := time.Now()
	reason := "conversation_registry_unavailable"
	defer func() {
		if err != nil {
			log.Printf("[CREW_ONLINE_AGENTS] owner=%s project=%s result=failed reason=%s elapsed_ms=%d", owner, roots.ID, reason, time.Since(started).Milliseconds())
			return
		}
		automations := 0
		for _, row := range rows {
			automations += len(row.Automations)
		}
		log.Printf("[CREW_ONLINE_AGENTS] owner=%s project=%s result=read agents=%d automations=%d elapsed_ms=%d", owner, roots.ID, len(rows), automations, time.Since(started).Milliseconds())
	}()
	ready := false
	for _, placement := range placements {
		ready = ready || placement.Location == "server"
	}
	if !ready {
		return []crewOnlineAgent{}, nil
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(owner))
	if err != nil {
		return nil, err
	}
	reason = "project_automations_unavailable"
	dispatcher := api.crewMoveDispatcher()
	profile, binding, manifest, _, err := dispatcher.projectManifestAnyOwner(ctx, owner, crewProfileID, roots.ID)
	if err != nil {
		return nil, err
	}
	reason = "automation_state_unavailable"
	states, err := dispatcher.loadState(ctx, owner)
	if err != nil {
		return nil, err
	}
	reason = "webhooks_unavailable"
	hooks, err := readProjectWebhookDefinitions(ctx, roots.Virtual)
	if err != nil {
		return nil, err
	}
	var automations []crewTabAutomation
	runs := agentProfileRuntimeWorkspace(owner, binding.WorkspacePath)
	for _, schedule := range manifest.Schedules {
		if schedule.WorkflowID != "" || schedule.Condition != nil && schedule.Condition.Target.Kind != "agent" {
			continue // workflows run directly, not as one of these agents
		}
		job := productScheduleJob{UserID: owner, Profile: profile, Schedule: schedule, ProjectID: roots.ID, WorkspacePath: binding.WorkspacePath}
		job.State = states[scheduleStateKey(job)]
		automations = append(automations, crewOnlineAutomation(dispatcher, job, runs))
	}
	for _, job := range conditionCallerJobs(owner, states) {
		if job.Profile.ID != crewProfileID || job.ProjectID != roots.ID || job.Schedule.Condition.Target.Kind != "agent" || canonicalCrewWorkspaceRoot(job.WatcherSource.Path) != roots.Virtual {
			continue
		}
		automations = append(automations, crewOnlineAutomation(dispatcher, job, runs))
	}
	for _, hook := range hooks {
		if hook.Action.Type != "message" {
			continue
		}
		line := "when called"
		if !hook.Enabled {
			line += " · off"
		}
		automations = append(automations, crewTabAutomation{Tab: firstNonEmptyTrimmed(hook.Action.Tab, macMainTab), Kind: "Webhook", Name: hook.Name, On: hook.Enabled, Line: line})
	}
	return crewOnlineAgentRows(roots.ID, placements, doc.Entries, automations), nil
}

func crewOnlineAutomation(dispatcher *ProjectActionDispatcher, job productScheduleJob, runs string) crewTabAutomation {
	resp := dispatcher.jobResponse(job, runs)
	item := crewTabAutomation{Tab: firstNonEmptyTrimmed(job.Schedule.ChatKey, macMainTab), Kind: "Schedule", Name: job.Schedule.Name, On: resp.Enabled}
	when := crewTabTimingWords(resp)
	if cfg := job.Schedule.Condition; cfg != nil {
		item.Kind, item.Tab = "Watcher", firstNonEmptyTrimmed(cfg.Target.ChatID, macMainTab)
		switch cfg.CheckMode {
		case "event":
			when = "when an event arrives"
		case "inbox":
			when = "when an email arrives"
		default:
			when = "checks " + when
		}
	}
	switch {
	case !item.On:
		item.Line = when + " · off"
	case resp.LastStatus == "running":
		item.Line = when + " · running now"
	case resp.NextRunAt != nil:
		item.Line = when + " · next " + crewTabUntil(*resp.NextRunAt)
	default:
		item.Line = when
	}
	return item
}

func crewOnlineAgentRows(project string, placements map[string]crewAgentPlacement, records map[string]ProductConversationRecord, automations []crewTabAutomation) []crewOnlineAgent {
	rows := []crewOnlineAgent{}
	for _, place := range placements {
		if place.Location != "server" {
			continue
		}
		row := crewOnlineAgent{Tab: place.Tab, Name: place.Name, Automations: []crewTabAutomation{}}
		key := productConversationRegistryEntryKey(crewProfileID, crewAgentPublishKey(project, place.Tab))
		if record, ok := records[key]; ok && record.Provider != "" {
			row.Program = providerAccountProductName(record.Provider)
		}
		for _, automation := range automations {
			if crewAgentTargetMatches(automation.Tab, crewMoveHeader{SourceID: project, MainKey: project, AgentTab: place.Tab}) {
				row.Automations = append(row.Automations, automation)
			}
		}
		sort.Slice(row.Automations, func(i, j int) bool { return row.Automations[i].Name < row.Automations[j].Name })
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name) })
	return rows
}

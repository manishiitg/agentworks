package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// A Crew agent tab's setup icon and its card rows (owner, 6 Nov, approved mockup "Tab status"): who can reach the agent
// (public on AgentBridge, shared with the team, or only you) and what starts it on its own (schedules, watchers,
// webhooks). The server words every line; the tab bar and the card only show them, the same in the Mac app and on the
// website, for the agent's owner and for teammates.

// crewTabSetup is one agent's setup on its Team tab row.
type crewTabSetup struct {
	// Reach is "agentbridge" (public on AgentBridge, which implies shared), "team" (shared with the team) or "".
	Reach string `json:"reach"`
	// StartsOnOwn: at least one of Automations is on.
	StartsOnOwn     bool                `json:"starts_on_own"`
	AgentBridgeLine string              `json:"agentbridge_line,omitempty"` // "public · online", only while public
	Automations     []crewTabAutomation `json:"automations"`
}

// crewTabAutomation is one schedule, watcher or webhook that starts an agent, worded for its card.
type crewTabAutomation struct {
	// Tab is the agent it starts: "main" or the tab id (the <id> of "<project>:chat:<id>").
	Tab  string `json:"tab"`
	Kind string `json:"kind"` // Schedule, Watcher or Webhook
	Name string `json:"name"`
	Line string `json:"line"` // "every day at 20:00 (UTC) · next in 3 h", "when called · last call 10 min ago", "off"
	On   bool   `json:"on"`
}

// crewAutomationsByTab are this Crew's schedules, watchers and webhooks, each on the agent it starts. A schedule or
// watcher starts the chat it was set from (Main when none); a webhook has no tab of its own, so it is listed on Main,
// where its turns land (or beside it, in its own chat). Internal triggers and typed functions are entry points other
// people and agents call, not something that starts the agent on its own, so they are left out.
func (s *ProjectActionDispatcher) crewAutomationsByTab(ctx context.Context, owner, project string) []crewTabAutomation {
	if s == nil || owner == "" || project == "" {
		return nil
	}
	profile, binding, manifest, _, err := s.projectManifestAnyOwner(ctx, owner, crewProfileID, project)
	if err != nil {
		return nil
	}
	states, _ := s.loadState(ctx, owner)
	runs := agentProfileRuntimeWorkspace(owner, binding.WorkspacePath)
	out := []crewTabAutomation{}
	for _, sched := range manifest.Schedules {
		job := productScheduleJob{UserID: owner, Profile: profile, Schedule: sched, ProjectID: manifest.ID, ProjectTitle: manifest.displayTitle(),
			WorkspacePath: binding.WorkspacePath, ManifestPath: binding.ManifestPath}
		job.State = states[scheduleStateKey(job)]
		resp := s.jobResponse(job, runs)
		tab := firstNonEmptyTrimmed(resp.ChatID, macMainTab)
		item := crewTabAutomation{Tab: tab, Kind: "Schedule", Name: firstNonEmptyTrimmed(sched.Name, "Schedule"), On: resp.Enabled}
		when := crewTabTimingWords(resp)
		if sched.Condition != nil {
			item.Kind = "Watcher"
			switch sched.Condition.CheckMode {
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
		out = append(out, item)
	}
	for _, trigger := range manifest.Triggers {
		if trigger.IsInternal() || trigger.WorkspaceFunction {
			continue
		}
		item := crewTabAutomation{Tab: macMainTab, Kind: "Webhook", Name: firstNonEmptyTrimmed(trigger.Name, "Webhook"), On: trigger.Enabled, Line: "when called"}
		if trigger.ownConversation() {
			item.Line += " · in its own chat"
		}
		if !item.On {
			item.Line += " · off"
		} else if last, _, err := ListScheduleRuns(ctx, runs, projectScheduleJobID(profile.ID, manifest.ID, trigger.ID), 1, 0); err == nil && len(last) > 0 {
			item.Line += " · last call " + crewAccessAgo(last[0].StartedAt)
		} else {
			item.Line += " · not called yet"
		}
		out = append(out, item)
	}
	return out
}

// crewTabTimingWords is a schedule's saved timing in a few words, from the Schedules list's own sentence.
func crewTabTimingWords(resp ScheduledJobResponse) string {
	words := strings.TrimSpace(scheduleTimingSummary(resp))
	words = strings.TrimSuffix(strings.TrimSuffix(words, "; does not repeat."), ".")
	words = strings.TrimPrefix(words, "Repeats ")
	if words == "" {
		return "on its own clock"
	}
	return strings.ToLower(words[:1]) + words[1:]
}

// crewTabUntil is how far off a run is: "in 12 min", "in 3 h", "on 7 Nov".
func crewTabUntil(at time.Time) string {
	d := time.Until(at)
	switch {
	case d < time.Minute:
		return "any moment"
	case d < time.Hour:
		return fmt.Sprintf("in %d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("in %d h", int(d.Hours()))
	default:
		return "on " + at.Local().Format("2 Jan")
	}
}

// crewTabSetupSource is what the Team tab reads once per reply to word every row's setup.
type crewTabSetupSource struct {
	automations []crewTabAutomation
	macOn       bool // the owner's Mac answers now (a Mac Crew); a Crew on the server is always online
}

func crewTabSetupSourceFor(ctx context.Context, t crewTeam, twin bool) crewTabSetupSource {
	src := crewTabSetupSource{}
	if twin {
		// A Mac Crew's schedules, watchers and webhooks live on its owner's Mac: read there over Mac online.
		src.automations, src.macOn = crewMacAutomations(ctx, t)
		return src
	}
	if runningServerAPI != nil {
		src.automations = runningServerAPI.projectActions.crewAutomationsByTab(ctx, t.OwnerID, t.ProjectID)
	}
	return src
}

// setup is one Team tab row's setup. Automations belong to the Crew's owner (they run as the owner), so only the
// owner's own agents list them; bridge is the owner's agent's AgentBridge setting (nil for anyone else's agent).
func (src crewTabSetupSource) setup(t crewTeam, a crewTeamAgent, person string, inTeam bool, bridge *crewTeamTabBridge) *crewTabSetup {
	out := &crewTabSetup{Automations: []crewTabAutomation{}}
	switch {
	case bridge != nil && bridge.Public:
		out.Reach = "agentbridge"
		online := !crewRelayTwin(t.ProjectID) || a.Place == "server" || a.PlaceServer || src.macOn
		out.AgentBridgeLine = map[bool]string{true: "public · online", false: "public · offline"}[online]
	case t.On && inTeam:
		out.Reach = "team"
	}
	if person == t.OwnerID {
		main := a.Tab == macMainTab || crewTeamMainAgent(t, t.OwnerID, a.ChatKey)
		for _, item := range src.automations {
			if item.Tab == a.Tab || (item.Tab == macMainTab && main) {
				out.Automations = append(out.Automations, item)
				out.StartsOnOwn = out.StartsOnOwn || item.On
			}
		}
	}
	return out
}

type crewMacAutomationsRead struct {
	at    time.Time
	items []crewTabAutomation
}

// crewMacAutomationsCache keeps the last list read from each Mac Crew's Mac, so the icons stay while it is off.
var crewMacAutomationsCache sync.Map

// crewMacAutomations is the owner's Mac's list (at most 15 s old) and whether that Mac is on now.
func crewMacAutomations(ctx context.Context, t crewTeam) ([]crewTabAutomation, bool) {
	pair, ok := crewMacPairFor(t.OwnerID, t.ProjectID)
	if !ok {
		return nil, false
	}
	cached, _ := crewMacAutomationsCache.Load(t.ProjectID)
	last, _ := cached.(crewMacAutomationsRead)
	on := crewMacSocketFor(t.OwnerID, t.ProjectID, pair) != nil
	if !on || time.Since(last.at) < 15*time.Second {
		return last.items, on
	}
	// The Mac answers its own relay op (runDesktopCrewChat); a Mac from before this keeps the last list.
	result := callCrewMacPair(ctx, t.OwnerID, t.ProjectID, pair, crewChatCall{Operation: "automations"}, 5*time.Second)
	var items []crewTabAutomation
	if result.Status == 200 && json.Unmarshal(result.Body, &items) == nil {
		last.items = items
	}
	last.at = time.Now()
	crewMacAutomationsCache.Store(t.ProjectID, last)
	return last.items, on
}

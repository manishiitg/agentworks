package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

const teamOnlineAutomationDescriptor = "automations.json"

type teamOnlineAutomationPackage struct {
	Schedules []projectactions.Schedule `json:"schedules"`
	Functions []crewFunctionSpec        `json:"functions,omitempty"`
	Scripts   map[string]string         `json:"scripts,omitempty"`
}

func teamOnlineScheduleRevision(s projectactions.Schedule) string {
	// Enabled changes after the move, but do not change the editable definition.
	s.Enabled = false
	s.CreatedBy = nil
	s.UpdatedBy = nil
	raw, _ := json.Marshal(s)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (api *StreamingAPI) exportTeamOnlineAutomations(ctx context.Context, source triggerTarget, ids, functions []string, p *teamOnlinePackage) error {
	claims := GetUserFromContext(ctx)
	if claims == nil || source.CrewOwner != claims.UserID {
		return fmt.Errorf("choose one of your own automations")
	}
	item := teamOnlineAutomationPackage{Scripts: map[string]string{}}
	schedules, err := readProjectSchedules(ctx, source.Path)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return fmt.Errorf("choose each automation once")
		}
		seen[id] = true
		i := slices.IndexFunc(schedules, func(s projectactions.Schedule) bool { return s.ID == id })
		if i < 0 {
			return fmt.Errorf("a selected automation is unavailable")
		}
		s := schedules[i]
		if s.Project.Kind != "crew" || s.Project.ID != source.CrewID || s.RunAs != source.CrewOwner {
			return fmt.Errorf("choose an automation owned by this Crew")
		}
		if p.request.Kind == "automation" && p.request.Revision != "" && teamOnlineScheduleRevision(s) != p.request.Revision {
			return fmt.Errorf("the automation changed; preview it again")
		}
		if s.Action.WorkflowID != "" || s.Action.Type != "message" {
			return fmt.Errorf("ask your agent to prepare this automation for the Team")
		}
		if s.When.After != nil {
			for _, id := range s.When.After.Schedules {
				if !slices.Contains(ids, id) {
					return fmt.Errorf("include this automation's prerequisite schedules in the hand-off")
				}
			}
		}
		var input scheduleExecutionInput
		if err = json.Unmarshal(s.Input, &input); err != nil {
			return err
		}
		if cfg := input.Condition; cfg != nil && cfg.GitHubPR == nil {
			if err := cfg.Validate(); err != nil {
				return err
			}
			raw, found, err := readFileFromWorkspace(ctx, path.Join(source.Path, cfg.Script))
			if err != nil || !found {
				return fmt.Errorf("this Watcher's script is unavailable")
			}
			sum := sha256.Sum256([]byte(raw))
			if hex.EncodeToString(sum[:]) != cfg.SHA256 {
				return fmt.Errorf("this Watcher's script changed; save it before adding it to the Team")
			}
			item.Scripts[cfg.Script] = raw
			if cfg.Target.Kind == "function" {
				functions = append(functions, cfg.Target.Function)
			}
		}
		item.Schedules = append(item.Schedules, s)
	}
	all, err := readCrewFunctions(ctx, source)
	if err != nil {
		return err
	}
	for _, name := range functions {
		if slices.ContainsFunc(item.Functions, func(f crewFunctionSpec) bool { return f.Name == name }) {
			continue
		}
		i := slices.IndexFunc(all, func(f crewFunction) bool { return f.Name == name })
		if i < 0 {
			return fmt.Errorf("a declared function is unavailable")
		}
		f := all[i]
		if f.TriggerID != "" || f.publication != nil {
			return fmt.Errorf("prepare the declared function in this Crew before adding it to the Team")
		}
		spec := crewFunctionSpec{Name: f.Name, Description: f.Description, Instructions: f.Instructions, InputSchema: f.InputSchema, ResultSchema: f.ResultSchema, Execution: f.Execution, Agent: f.Agent}
		if err := spec.validate(); err != nil {
			return err
		}
		item.Functions = append(item.Functions, spec)
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	if int64(len(raw)) > publication.MaxFileBytes {
		return fmt.Errorf("the automation package exceeds the normal publication file limit")
	}
	file, err := p.root.OpenFile(teamOnlineAutomationDescriptor, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	ce := file.Close()
	if err != nil {
		return err
	}
	return ce
}

func readTeamOnlineAutomations(p *teamOnlinePackage) (teamOnlineAutomationPackage, error) {
	var item teamOnlineAutomationPackage
	file, err := p.root.Open(teamOnlineAutomationDescriptor)
	if os.IsNotExist(err) {
		return item, nil
	}
	if err != nil {
		return item, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&item)
	return item, err
}

func (api *StreamingAPI) installTeamOnlineAutomations(ctx context.Context, source, destination triggerTarget, p *teamOnlinePackage, enable bool) ([]teamOnlineItemMapping, []string, error) {
	item, err := readTeamOnlineAutomations(p)
	if err != nil {
		return nil, nil, err
	}
	if len(item.Schedules) == 0 && len(item.Functions) == 0 {
		return nil, nil, nil
	}
	mappings := []teamOnlineItemMapping{}
	notes := []string{}
	mainKey := crewOwnerMainChatKey(destination.CrewID)
	if mainKey == "" && len(item.Schedules) > 0 {
		return nil, nil, fmt.Errorf("choose the Team's Main agent before moving automations")
	}

	schedules, err := readProjectSchedules(ctx, destination.Path)
	if err != nil {
		return nil, nil, err
	}
	oldFunctions, err := readCrewFunctions(ctx, destination)
	if err != nil {
		return nil, nil, err
	}
	functions := slices.Clone(oldFunctions)
	for _, spec := range item.Functions {
		// Names are item-scoped; a page cannot replace somebody else's function.
		if err := spec.validate(); err != nil {
			return nil, nil, err
		}
		spec.Name = teamOnlineFunctionName(source.CrewID, spec.Name)
		functions, _ = upsertCrewFunction(functions, spec, GetUserIDFromContext(ctx), time.Now().UTC())
	}
	for _, original := range item.Schedules {
		copied, _ := json.Marshal(original)
		var s projectactions.Schedule
		if err = json.Unmarshal(copied, &s); err != nil {
			return nil, nil, err
		}
		s.ID = teamOnlineDashboardItemID(source.CrewID, "automation", original.ID)
		s.Project = projectactions.Project{Kind: "crew", ID: destination.CrewID}
		s.RunAs = destination.CrewOwner
		s.Action.Tab = mainKey
		s.Action.Conversation = ""
		if p.desiredEnabled == nil {
			p.desiredEnabled = map[string]bool{}
		}
		wanted, seen := p.desiredEnabled[s.ID]
		if !seen {
			wanted = original.Enabled
			if i := slices.IndexFunc(schedules, func(v projectactions.Schedule) bool { return v.ID == s.ID }); i >= 0 {
				wanted = schedules[i].Enabled
			}
			p.desiredEnabled[s.ID] = wanted
		}
		s.Enabled = enable && wanted
		s.CreatedBy = automationActor(ctx)
		s.UpdatedBy = s.CreatedBy
		if s.When.After != nil {
			for i, id := range s.When.After.Schedules {
				s.When.After.Schedules[i] = teamOnlineDashboardItemID(source.CrewID, "automation", id)
			}
		}
		var input scheduleExecutionInput
		if err = json.Unmarshal(s.Input, &input); err != nil {
			return nil, nil, err
		}
		input.TeamAuthorizedBy = ""
		input.TeamPolicyRevision = 0
		if cfg := input.Condition; cfg != nil {
			if cfg.PersonalGmail() || cfg.GitHubPR != nil {
				s.Enabled = false
				cfg.EmailAccountID = ""
				if cfg.GitHubPR != nil {
					cfg.GitHubPR.Login = ""
					cfg.GitHubPR.ConnectionID = ""
					cfg.GitHubPR.Worktree = ""
					cfg.GitHubPR.SessionID = ""
				}
				notes = append(notes, s.Name+": choose an account in its Team settings before turning this Watcher on.")
			}
			if cfg.Target.Kind == "trigger" {
				return nil, nil, fmt.Errorf("prepare this Watcher's destination for the Team first")
			}
			cfg.Target.ChatID = strings.TrimPrefix(mainKey, destination.CrewID+codeChatSideMarker)
			cfg.Target.Name = ""
			if cfg.Target.Kind == "function" {
				cfg.Target.Name = "workspace:work:" + destination.CrewID
				cfg.Target.Function = teamOnlineFunctionName(source.CrewID, cfg.Target.Function)
			} else if cfg.Target.Kind != "agent" {
				return nil, nil, fmt.Errorf("prepare this Watcher's destination for the Team first")
			}
			if cfg.Target.Kind == "agent" && !conditionChatIDPattern.MatchString(cfg.Target.ChatID) {
				return nil, nil, fmt.Errorf("choose a named Team Main agent for this Watcher")
			}

			if err := cfg.Validate(); err != nil {
				return nil, nil, err
			}
			if cfg.GitHubPR == nil {
				script, ok := item.Scripts[cfg.Script]
				if !ok {
					return nil, nil, fmt.Errorf("the Watcher's script is missing")
				}
				sum := sha256.Sum256([]byte(script))
				if hex.EncodeToString(sum[:]) != cfg.SHA256 {
					return nil, nil, fmt.Errorf("the Watcher's script changed")
				}
				cfg.Script = path.Join("code/triggers", s.ID+".py")
				if err = writeFileToWorkspace(ctx, path.Join(destination.Path, cfg.Script), script); err != nil {
					return nil, nil, err
				}
			}
			for _, name := range cfg.Secrets {
				if _, ok := api.projectSecretValueFor(ctx, GetUserIDFromContext(ctx), destination.Path, name); !ok {
					s.Enabled = false
					notes = append(notes, s.Name+": select its secret names in the Team Crew before turning it on.")
					break
				}
			}
		}
		s.Input, err = json.Marshal(input)
		if err != nil {
			return nil, nil, err
		}
		if err = s.Validate(); err != nil {
			return nil, nil, err
		}
		index := slices.IndexFunc(schedules, func(v projectactions.Schedule) bool { return v.ID == s.ID })
		if index < 0 {
			schedules = append(schedules, s)
		} else {
			s.CreatedBy = schedules[index].CreatedBy
			schedules[index] = s
		}
		when := s.When.Cron
		if when == "" && len(s.When.Dates) > 0 {
			when = s.When.Dates[0].Date + " " + s.When.Dates[0].Time
		}
		if s.When.Timezone != "" {
			when += " " + s.When.Timezone
		}
		mappings = append(mappings, teamOnlineItemMapping{SourceProjectID: source.CrewID, Kind: "automation", ItemID: original.ID, DestinationProjectID: destination.CrewID, DestinationItemID: s.ID, Revision: teamOnlineScheduleRevision(original), Name: s.Name, WhenRuns: when})
	}
	if err = writeCrewFunctions(ctx, destination, functions); err != nil {
		return nil, nil, err
	}
	if err = writeProjectSchedules(ctx, destination.Path, schedules); err != nil {
		return nil, nil, err
	}
	way, _ := readTeamWay(p.request.TeamProjectID)
	for _, need := range way.Needs {
		for _, s := range item.Schedules {
			if strings.Contains(strings.ToLower(string(s.Input)), strings.ToLower(need.Label)) {
				notes = append(notes, need.Label+": each person signs in themselves; this online routine cannot use a personal sign-in.")
				break
			}
		}
	}
	return mappings, notes, nil
}
func teamOnlineFunctionName(source, name string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + name))
	prefix := "online_" + hex.EncodeToString(sum[:8]) + "_"
	if len(name) > 48-len(prefix) {
		name = name[:48-len(prefix)]
	}
	return prefix + name
}

// Stop only the exact definitions that were exported. A concurrently edited
// source routine is refused, never accidentally stopped by an old package.
func stopTeamOnlineSource(ctx context.Context, source triggerTarget, p *teamOnlinePackage, restart bool) error {
	item, err := readTeamOnlineAutomations(p)
	if err != nil {
		return err
	}
	if len(item.Schedules) == 0 {
		return nil
	}
	current, err := readProjectSchedules(ctx, source.Path)
	if err != nil {
		return err
	}
	for _, s := range item.Schedules {
		i := slices.IndexFunc(current, func(v projectactions.Schedule) bool { return v.ID == s.ID })
		if i < 0 || current[i].RunAs != source.CrewOwner || teamOnlineScheduleRevision(current[i]) != teamOnlineScheduleRevision(s) {
			return fmt.Errorf("the source automation changed; preview it again")
		}
		current[i].Enabled = restart && s.Enabled
	}
	return writeProjectSchedules(ctx, source.Path, current)
}

func confirmTeamOnlineSchedulesEnabled(ctx context.Context, destination triggerTarget, p *teamOnlinePackage) error {
	current, err := readProjectSchedules(ctx, destination.Path)
	if err != nil {
		return err
	}
	for id, wanted := range p.desiredEnabled {
		if !wanted {
			continue
		}
		i := slices.IndexFunc(current, func(s projectactions.Schedule) bool { return s.ID == id })
		if i < 0 || !current[i].Enabled {
			return fmt.Errorf("the Team routine needs account or secret setup; its source routine is kept running")
		}
	}
	return nil
}

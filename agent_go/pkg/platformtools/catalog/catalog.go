// Package catalog names the platform's public action tools. Member names
// are private registration identities, never alternate callable entry points.
package catalog

import (
	_ "embed"
	"encoding/json"
	"strings"
)

type Action struct {
	Family          string   `json:"family"`
	Name            string   `json:"name"`
	Member          string   `json:"member"`
	Description     string   `json:"description"`
	Aliases         []string `json:"aliases,omitempty"`
	Source          string   `json:"source,omitempty"`
	SourceAction    string   `json:"source_action,omitempty"`
	SourceKey       string   `json:"source_key,omitempty"`
	SourceMember    string   `json:"source_member,omitempty"`
	Scope           string   `json:"scope,omitempty"`
	Fields          []string `json:"fields,omitempty"`
	ExcludeFields   []string `json:"exclude_fields,omitempty"`
	ExternalMembers string   `json:"external_members,omitempty"`
	Required        []string `json:"required,omitempty"`
}

// SourceParameter names the existing selector used by a captured source tool.
func (a Action) SourceParameter() string {
	if a.SourceKey != "" {
		return a.SourceKey
	}
	return "action"
}

// DashboardActions is shared by native chat projection and external MCP.
//
//go:embed dashboard-actions.json
var dashboardSource []byte

var DashboardActions = func() []Action {
	var actions []Action
	if err := json.Unmarshal(dashboardSource, &actions); err != nil {
		panic(err)
	}
	return actions
}()

//go:embed configuration-actions.json
var configurationSource []byte

var ConfigurationActions = func() []Action {
	var actions []Action
	if err := json.Unmarshal(configurationSource, &actions); err != nil {
		panic(err)
	}
	return actions
}()

//go:embed data-actions.json
var dataSource []byte

var DataActions = func() []Action {
	var actions []Action
	if err := json.Unmarshal(dataSource, &actions); err != nil {
		panic(err)
	}
	return actions
}()

//go:embed project-actions.json
var projectSource []byte

var ProjectActions = func() []Action {
	var actions []Action
	if err := json.Unmarshal(projectSource, &actions); err != nil {
		panic(err)
	}
	return actions
}()

//go:embed agent-actions.json
var agentSource []byte

//go:embed triggers-actions.json
var triggersSource []byte
var AgentActions = func() []Action {
	var actions []Action
	if err := json.Unmarshal(agentSource, &actions); err != nil {
		panic(err)
	}
	var triggers []Action
	if err := json.Unmarshal(triggersSource, &triggers); err != nil {
		panic(err)
	}
	return append(actions, triggers...)
}()

//go:embed experience-actions.json
var experienceSource []byte
var ExperienceActions = func() []Action {
	var actions []Action
	if err := json.Unmarshal(experienceSource, &actions); err != nil {
		panic(err)
	}
	return actions
}()

var Actions = func() []Action {
	var all []Action
	for _, group := range [][]Action{DashboardActions, ConfigurationActions, DataActions, ProjectActions, AgentActions, ExperienceActions} {
		all = append(all, group...)
	}
	return all
}()

// Identity is used only while assembling trusted native definitions/policies.
// It never resolves a public call or accepts a retired tool name over MCP.
func Identity(member string) string {
	for _, action := range Actions {
		if member == action.Member {
			return action.Family + "." + action.Name
		}
		for _, alias := range action.Aliases {
			if member == alias {
				return action.Family + "." + action.Name
			}
		}
	}
	return member
}

// Invocation translates a trusted source instruction to its public action.
// It does not admit calls: the current catalog and captured executor do that.
// Ambiguous external members stay unresolved; the typed caller must choose the
// operation explicitly (for example a Crew update also backs function edits).
func Invocation(source string, args map[string]any) (string, map[string]any) {
	return invocation(Actions, source, args)
}

func invocation(actions []Action, source string, args map[string]any) (string, map[string]any) {
	var match *Action
	for i := range actions {
		action := &actions[i]
		matches := source == action.Member
		for _, alias := range action.Aliases {
			matches = matches || source == alias
		}
		for _, member := range strings.Split(action.ExternalMembers, "|") {
			matches = matches || source == member
		}
		if action.Source == source {
			matches = args[action.SourceParameter()] == action.SourceAction
		}
		if !matches {
			continue
		}
		if match != nil && (match.Family != action.Family || match.Name != action.Name || match.Scope != action.Scope) {
			return source, args
		}
		match = action
	}
	if match == nil {
		return source, args
	}
	input := map[string]any{}
	for key, value := range args {
		if match.Source != source || key != match.SourceParameter() {
			input[key] = value
		}
	}
	input["action"] = match.Name
	if match.Scope != "" {
		input["scope"] = match.Scope
	}
	return match.Family, input
}

func PublicName(identity string) string {
	for _, action := range Actions {
		if action.Source != "" && action.Source == identity {
			return action.Family
		}
	}
	identity = Identity(identity)
	for _, action := range Actions {
		if identity == action.Family+"."+action.Name {
			return action.Family
		}
	}
	return identity
}

func IsDashboardMember(name string) bool {
	id := Identity(name)
	return id != name && (strings.HasPrefix(id, "dashboard.") || strings.HasPrefix(id, "share."))
}

func IsMember(name string) bool {
	for _, action := range Actions {
		if action.Source != "" && action.Source == name && action.Source != action.Family {
			return true
		}
	}
	id := Identity(name)
	for _, action := range Actions {
		if id == action.Family+"."+action.Name {
			return true
		}
	}
	return false
}

func IsFamily(name string) bool {
	for _, action := range Actions {
		if action.Family == name {
			return true
		}
	}
	return false
}

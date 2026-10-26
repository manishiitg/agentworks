package server

import (
	"fmt"
	"slices"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
)

// These are existing Crew execution members, with their original service
// checks. The shared catalog owns their public job and private source names.
var crewDatabaseMembers = func() map[string]string {
	result := map[string]string{}
	for _, action := range platformtools.DataActions {
		if action.Family == "database" && action.ExternalMembers != "" && slices.Contains(action.Aliases, action.ExternalMembers) {
			result[action.ExternalMembers] = action.Member
		}
	}
	return result
}()

func groupedCrewDatabaseNative(name string) bool {
	for _, native := range crewDatabaseMembers {
		if name == native {
			return true
		}
	}
	return false
}

func dataSourceAction(name string) (source, operation string) {
	for _, action := range platformtools.DataActions {
		if action.Member == name && (action.Source == "query_database" || action.Source == "crew_files" || action.Source == "crew_folders") {
			return action.Source, action.SourceAction
		}
	}
	return "", ""
}

func dataConsentMember(name string) string {
	if source, _ := dataSourceAction(name); source != "" {
		return source
	}
	if crewDatabaseMembers[name] != "" {
		return "crew_execution"
	}
	return name
}

// Derive only capabilities already present in the external catalog. Sources
// remain private so the dispatcher can reuse their current services.
func dataExternalMembers(catalog []externalTool) ([]externalTool, error) {
	result := []externalTool{}
	for _, tool := range catalog {
		for _, action := range platformtools.DataActions {
			if action.Source != tool.Name || (tool.Name != "query_database" && tool.Name != "crew_files" && tool.Name != "crew_folders") || !platformtools.OffersAction(tool.InputSchema, action.SourceAction) {
				continue
			}
			schema, err := platformtools.MemberSchema(tool.InputSchema, action)
			if err != nil {
				return nil, err
			}
			member := tool
			member.Name, member.Description, member.InputSchema = action.Member, action.Description, schema
			if tool.Name == "crew_folders" {
				member.mutates = !crewCapabilityRead(action.SourceAction)
			}
			result = append(result, member)
		}
		if tool.Name == "crew_execution" {
			defs, err := crewExecutionDefinitions()
			if err != nil {
				return nil, err
			}
			public, err := configurationToolCopy(tool)
			if err != nil {
				return nil, err
			}
			props, err := externalSchemaProperties(public.Name, public.InputSchema)
			if err != nil {
				return nil, err
			}
			names, schemas := []any{"list", "history"}, []any{}
			for _, def := range defs {
				if def.Function == nil || !platformtools.OffersAction(tool.InputSchema, def.Function.Name) {
					continue
				}
				schema, err := crewExecutionSchema(def)
				if err != nil {
					return nil, err
				}
				if !groupedCrewDatabaseNative(def.Function.Name) {
					names = append(names, def.Function.Name)
					schemas = append(schemas, schema)
					continue
				}
				member := tool
				for name, native := range crewDatabaseMembers {
					if native == def.Function.Name {
						member.Name = name
						break
					}
				}
				if member.Name == tool.Name {
					return nil, fmt.Errorf("database job missing for %s", def.Function.Name)
				}
				member.Description = def.Function.Description
				member.mutates = true
				member.InputSchema = schema
				fields, err := externalSchemaProperties(def.Function.Name, schema)
				if err != nil {
					return nil, err
				}
				fields["crew_id"] = props["crew_id"]
				required, _ := schema["required"].([]any)
				schema["required"] = append([]any{"crew_id"}, required...)
				result = append(result, member)
			}
			props["action"] = map[string]any{"type": "string", "enum": names}
			props["arguments"] = map[string]any{"anyOf": schemas}
			tool = public
		}
		result = append(result, tool)
	}
	return result, nil
}

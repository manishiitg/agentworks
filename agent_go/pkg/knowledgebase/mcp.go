package knowledgebase

import (
	"context"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type mcpAction struct {
	name, operation string
}
type mcpTool struct {
	name, description string
	actions           []mcpAction
}

var mcpSurface = []mcpTool{
	{ToolBrowse, "Browse accessible folders or entries. Choose action=folders or entries; supports folder scope and pagination.", []mcpAction{{"folders", "list_knowledgebase_folders"}, {"entries", "list_knowledgebase"}}},
	{ToolRead, "Read an entry (whole, lines, or heading section) with action=read, or literal-search accessible content with action=search. Reads return a current version.", []mcpAction{{"read", "read_knowledgebase"}, {"search", "search_knowledgebase"}}},
	{ToolUpdate, "Save live knowledge: create, update, delete, or create_folder. Migration actions preview/import/cutover/rollback explicitly migrate an owned workflow or Crew after preview. Use expected_version and stable request IDs; saves are immediately shared.", []mcpAction{{"create", "create_knowledgebase"}, {"update", "update_knowledgebase"}, {"delete", "delete_knowledgebase"}, {"create_folder", "create_knowledgebase_folder"}, {"migration_preview", "kb_migration_preview"}, {"migration_import", "kb_migration_import"}, {"migration_cutover", "kb_migration_cutover"}, {"migration_rollback", "kb_migration_rollback"}}},
	{ToolSkills, "Company skills in Brain. action=list finds skills you can read; action=get returns one skill's files to install into your own skills folder (write each file under <skills>/<name>/); action=publish uploads a skill package (SKILL.md plus references/, scripts/, assets) to Skills/<name> (or a folder you name), replacing its previous files; publish only when the person asks to share a skill company-wide. Publishing needs Editor on the folder; a skill with scripts needs Owner.", []mcpAction{{"list", "list_knowledgebase_skills"}, {"get", "get_knowledgebase_skill"}, {"publish", "publish_knowledgebase_skill"}}},
	{ToolAccess, "Inspect folder access. Owners can manage folder grants; administrators can manage service accounts and configure Git backup. Authorized builders and external authoring connections can inspect and set Off/Read/Read & write Brain access on owned workflow/Crew projects (always limited by the owner's folder roles; steps say in their descriptions which folders they read and write).", []mcpAction{{"inspect", "get_knowledgebase_access"}, {"list", "manage_knowledgebase_access"}, {"grant", "manage_knowledgebase_access"}, {"revoke", "manage_knowledgebase_access"}, {"create_service_account", "manage_knowledgebase_access"}, {"disable_service_account", "manage_knowledgebase_access"}, {"configure_backup", "manage_knowledgebase_access"}, {"inspect_project", "kb_inspect_project"}, {"set_project_access", "kb_set_project_access"}}},
}

// ToolDefinitions is the complete five-tool surface. The dedicated access
// builder receives only manage_knowledgebase_access from this definition.
func ToolDefinitions() []ToolDefinition { return mcpDefinitions(true, true, true, false) }

// IsProjectAction is an action that configures a workflow/Crew project (not a folder): inspect and the Off/Read/Read &
// write access setting. Folder bindings were removed (PLAT-628).
func IsProjectAction(action string) bool {
	return strings.HasSuffix(action, "_project") || action == "set_project_access"
}

func IsMCPTool(name string) bool {
	name = CanonicalToolName(name)
	for _, tool := range mcpSurface {
		if tool.name == name {
			return true
		}
	}
	return false
}

// ConnectionToolDefinitions limits discovery to the actions a content
// connection can call. Access mutations remain exclusive to the access builder.
func ConnectionToolDefinitions(canWrite bool) []ToolDefinition {
	return mcpDefinitions(canWrite, false, false, false)
}

func MigrationConnectionToolDefinitions(canWrite bool) []ToolDefinition {
	return mcpDefinitions(canWrite, false, true, false)
}

// ExternalConnectionToolDefinitions exposes direct access management to writable,
// unrestricted external connections. Folder Owner/admin checks remain authoritative.
// Managed workflow/Crew tools retain their content-only definitions.
func ExternalConnectionToolDefinitions(canWrite, canManage, migration bool) []ToolDefinition {
	return mcpDefinitions(canWrite, false, migration, canManage)
}

// ProjectToolDefinitions reuses the global schemas for the workflow Builder.
// It can select folders, but cannot grant permissions or manage credentials.
func ProjectToolDefinitions() []ToolDefinition {
	out := []ToolDefinition{}
	for _, def := range ToolDefinitions() {
		if def.Name == ToolBrowse {
			out = append(out, def)
			continue
		}
		if def.Name != ToolAccess {
			continue
		}
		schema := asMap(def.InputSchema)
		variants := []any{}
		actions := []any{}
		for _, raw := range schema["oneOf"].([]any) {
			variant := asMap(raw)
			action := variant["properties"].(map[string]any)["action"].(map[string]any)["const"].(string)
			if action == "inspect" || IsProjectAction(action) {
				variants = append(variants, variant)
				actions = append(actions, action)
			}
		}
		schema["oneOf"] = variants
		schema["properties"].(map[string]any)["action"] = map[string]any{"type": "string", "enum": actions}
		def.InputSchema = schema
		def.Description = "Inspect or set the current owned workflow's Brain access: off, read, or write (read and write wherever the owner's folder roles allow). Inspect its manifest_version first, then set_project_access with mode, expected_manifest_version and request_id. Which folders each step reads or writes is written in that step's description (Inputs, Output, Rules), not configured here."
		out = append(out, def)
	}
	return out
}

func mcpDefinitions(canWrite, accessBuilder, migration, externalAccess bool) []ToolDefinition {
	operations := map[string]ToolDefinition{}
	for _, op := range operationDefinitions() {
		operations[op.Name] = op
	}
	for _, op := range integrationDefinitions() {
		operations[op.Name] = op
	}
	defs := []ToolDefinition{}
	for _, tool := range mcpSurface {
		if !migration && tool.name == ToolUpdate {
			tool.description = "Save live knowledge: create, update, delete, or create_folder. Use expected_version and stable request IDs; saves are immediately shared."
		}
		props := map[string]any{}
		variants := []any{}
		actions := []any{}
		mutates := false
		for _, action := range tool.actions {
			if action.name == "git" && !accessBuilder && !externalAccess {
				continue
			}
			if !migration && strings.HasPrefix(action.name, "migration_") {
				continue
			}
			if tool.name == ToolAccess && !accessBuilder && action.name != "inspect" && (!externalAccess || !canWrite || (IsProjectAction(action.name) && !migration)) || !canWrite && ToolActionMutates(tool.name, action.name) {
				continue
			}
			schemaOperation := action.operation
			if action.name == "configure_backup" {
				schemaOperation = "kb_configure_backup"
			}
			variant := asMap(operations[schemaOperation].InputSchema)
			fields := variant["properties"].(map[string]any)
			fields["action"] = map[string]any{"type": "string", "const": action.name}
			required, _ := variant["required"].([]any)
			if !containsRequired(required, "action") {
				required = append(required, "action")
			}
			if tool.name == ToolAccess {
				switch action.name {
				case "grant":
					required = append(required, "identity_id", "role")
				case "revoke", "disable_service_account":
					required = append(required, "identity_id")
				case "create_service_account":
					required = append(required, "name")
				}
			}
			variant["required"] = required
			for key, value := range fields {
				props[key] = value
			}
			actions = append(actions, action.name)
			variants = append(variants, variant)
			mutates = mutates || ToolActionMutates(tool.name, action.name)
		}
		if len(actions) == 0 {
			continue
		}
		props["action"] = map[string]any{"type": "string", "enum": actions, "description": "Choose one of the actions available to this connection."}
		description := tool.description
		if tool.name == ToolAccess && !accessBuilder && !externalAccess {
			description = "Inspect effective folder access with action=inspect. Permission changes and service-account management belong to the app's access builder."
		}
		defs = append(defs, ToolDefinition{Name: tool.name, Description: description, InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []any{"action"}, "properties": props, "oneOf": variants}, Mutates: mutates})
	}
	return defs
}

func containsRequired(xs []any, key string) bool {
	for _, x := range xs {
		if x == key {
			return true
		}
	}
	return false
}

func ToolActionMutates(tool, action string) bool {
	switch CanonicalToolName(tool) {
	case ToolUpdate:
		return true
	case ToolBackup:
		return action == "commit" || action == "push" || action == "git"
	case ToolAccess:
		return action != "inspect" && action != "list" && action != "inspect_project"
	case ToolSkills:
		return action == "publish"
	}
	return false
}

var mcpValidators struct {
	sync.Once
	byName map[string]*jsonschema.Schema
	err    error
}

// CallTool is the public MCP boundary. Internal viewer/domain operations remain
// behind Call; their old names are not registered in MCP discovery or dispatch.
func ValidateToolArguments(tool string, args map[string]any) error {
	tool = CanonicalToolName(tool)
	mcpValidators.Do(func() {
		mcpValidators.byName = map[string]*jsonschema.Schema{}
		compiler := jsonschema.NewCompiler()
		for _, def := range ToolDefinitions() {
			uri := "https://knowledgebase.invalid/schema/" + def.Name
			if err := compiler.AddResource(uri, asMap(def.InputSchema)); err != nil {
				mcpValidators.err = err
				return
			}
			validator, err := compiler.Compile(uri)
			if err != nil {
				mcpValidators.err = err
				return
			}
			mcpValidators.byName[def.Name] = validator
		}
	})
	if mcpValidators.err != nil {
		return kbErr("STORAGE_UNAVAILABLE", "Brain tool schemas are unavailable.")
	}
	validator, ok := mcpValidators.byName[tool]
	if !ok {
		return badArg("Unknown Brain MCP tool.")
	}
	if err := validator.Validate(asMap(args)); err != nil {
		return badArg("Arguments must match the selected Brain action.")
	}
	return nil
}

func (s *Service) CallTool(ctx context.Context, p Principal, tool string, args map[string]any) (any, error) {
	tool = CanonicalToolName(tool)
	if err := ValidateToolArguments(tool, args); err != nil {
		return nil, err
	}
	action := stringArg(args, "action")
	if p.AccessOnly && tool != ToolAccess || !p.AccessOnly && tool == ToolAccess && action != "inspect" {
		return nil, kbErr("FORBIDDEN", "Permission changes are restricted to the Brain access builder.")
	}
	if tool == ToolSkills {
		return s.skillsTool(ctx, p, args)
	}
	for _, definition := range mcpSurface {
		if definition.name != tool {
			continue
		}
		for _, operation := range definition.actions {
			if operation.name != action {
				continue
			}
			translated := merge(args, nil)
			delete(translated, "binding_alias")
			if operation.operation != "manage_knowledgebase_access" {
				delete(translated, "action")
			}
			p.requestTool, p.requestArguments = tool, args
			return s.Call(ctx, p, operation.operation, translated)
		}
	}
	return nil, badArg("Unknown Brain action %s.", strings.TrimSpace(action))
}

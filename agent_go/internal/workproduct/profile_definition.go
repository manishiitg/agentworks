package workproduct

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/commands"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/uiuxpromax"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// BuiltinAgentProfile returns the work profile. Brand new, so only one
// version is ever registered -- same reasoning as every other product's
// own BuiltinAgentProfile.
func BuiltinAgentProfile() agentprofiles.Profile {
	manifest := mustWorkManifest()
	profile := manifest.Profile
	profile.SystemPromptTemplate = renderProductPrompt()
	return profile
}

func BuiltinAgentProfiles() []agentprofiles.Profile {
	return []agentprofiles.Profile{BuiltinAgentProfile()}
}

var registerProductSkillsOnce sync.Once
var registerProductSkillsErr error

var productSkills = []agentprofiles.SkillFileBinding{
	{Name: "work-mcp", Description: "Connect and manage MCP servers for Crew projects.", Path: "skills/work-mcp/SKILL.md"},
	{Name: "work-integrations", Description: "Manage Crew secrets, browser access, models, and administrator-authorized server folders.", Path: "skills/work-integrations/SKILL.md"},
	{Name: "work-workflow-files", Description: "Read and interpret attached folders and read-only AgentWorks workflow references in Crew.", Path: "skills/work-workflow-files/SKILL.md"},
	{Name: "work-skills", Description: "Discover, install, import, create, select, and remove reusable skills in Crew.", Path: "skills/work-skills/SKILL.md"},
	{Name: "work-schedules-and-bots", Description: "Manage Crew's message-only schedules, authenticated webhook triggers, and Slack or WhatsApp project-chat bots.", Path: "skills/work-schedules-and-bots/SKILL.md"},
	{Name: "work-dashboard", Description: "Create and maintain a general-purpose visual dashboard for a Crew project, including live data from code/reports scripts via window.report.run.", Path: "skills/work-dashboard/SKILL.md"},
	{Name: "work-ui-control", Description: "Open and refresh Crew project panels through acknowledged UI control without using Workflow view semantics.", Path: "skills/work-ui-control/SKILL.md"},
	{Name: "background-work", Description: "Run a bounded task asynchronously and rely on Crew's automatic completion notification instead of polling.", Path: "skills/background-work/SKILL.md"},
}

var customCommandSlugPattern = regexp.MustCompile(`[^a-z0-9_-]+`)
var crewProjectSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func customCommandSlug(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = customCommandSlugPattern.ReplaceAllString(name, "-")
	return strings.Trim(name, "-")
}

func crewProjectSlug(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = crewProjectSlugPattern.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		return "crew"
	}
	return name
}

func defaultCrewIcon(name string) string {
	for _, char := range strings.TrimSpace(name) {
		return strings.ToUpper(string(char))
	}
	return "C"
}

func customCommandMarkdown(name, description, icon, prompt string, modes []string) string {
	var out strings.Builder
	out.WriteString("---\nname: ")
	out.WriteString(customCommandSlug(name))
	out.WriteString("\ndescription: ")
	out.WriteString(strconv.Quote(strings.TrimSpace(description)))
	out.WriteString("\nicon: ")
	out.WriteString(strconv.Quote(strings.TrimSpace(icon)))
	out.WriteString("\nmodes:")
	for _, mode := range modes {
		out.WriteString("\n  - ")
		out.WriteString(mode)
	}
	out.WriteString("\n---\n\n")
	out.WriteString(strings.TrimSpace(prompt))
	out.WriteString("\n")
	return out.String()
}

func workCustomCommandsFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		commandsPath := path.Join(runtime.WorkspacePath, commands.CustomCommandsSubPath)
		return agentprofiles.ToolSpec{
			Name: "manage_custom_commands", Category: "custom_commands",
			Description: "List, create, update, or delete reusable slash commands for this Crew project when the user asks. Commands appear immediately in this project's slash menu. Product commands are immutable and cannot be changed with this tool.",
			Parameters: map[string]interface{}{
				"type": "object", "additionalProperties": false,
				"properties": map[string]interface{}{
					"action":      map[string]interface{}{"type": "string", "enum": []string{"list", "create", "update", "delete"}},
					"name":        map[string]interface{}{"type": "string", "description": "Slash command name, without the slash."},
					"description": map[string]interface{}{"type": "string", "description": "Short text shown in the slash menu."},
					"prompt":      map[string]interface{}{"type": "string", "description": "Prompt submitted by the command. {{context}} inserts the text the user types with the command; put it on its own line (optionally after a \"Label:\" line) so it disappears when nothing is typed. It is appended automatically when missing."},
					"icon":        map[string]interface{}{"type": "string", "enum": []string{"terminal", "zap", "eye", "code", "file-text", "message-circle", "search", "bookmark", "star"}},
				},
				"required": []string{"action"},
			},
			Execute: func(_ context.Context, args map[string]interface{}) (string, error) {
				action, _ := args["action"].(string)
				name, _ := args["name"].(string)
				name = customCommandSlug(name)
				if action == "list" {
					items, err := commands.DiscoverCommandsAt(workspaceAPIURL, commandsPath)
					if err != nil {
						return "", err
					}
					data, _ := json.Marshal(map[string]interface{}{"commands": items, "total": len(items)})
					return string(data), nil
				}
				if name == "" {
					return "", fmt.Errorf("name is required")
				}
				switch action {
				case "delete":
					if err := commands.DeleteCommandAt(workspaceAPIURL, commandsPath, name); err != nil {
						return "", err
					}
					return fmt.Sprintf("Deleted /%s. The slash menu will refresh the next time it opens.", name), nil
				case "create", "update":
					description, _ := args["description"].(string)
					prompt, _ := args["prompt"].(string)
					icon, _ := args["icon"].(string)
					if icon == "" {
						icon = "terminal"
					}
					if strings.TrimSpace(description) == "" || strings.TrimSpace(prompt) == "" {
						return "", fmt.Errorf("description and prompt are required")
					}
					content := customCommandMarkdown(name, description, icon, prompt, []string{"multi-agent"})
					var err error
					if action == "create" {
						_, err = commands.CreateCommandAt(workspaceAPIURL, commandsPath, name, content)
					} else {
						_, err = commands.UpdateCommandAt(workspaceAPIURL, commandsPath, name, content)
					}
					if err != nil {
						return "", err
					}
					return fmt.Sprintf("Saved /%s. It is available the next time the slash menu opens.", name), nil
				default:
					return "", fmt.Errorf("action must be list, create, update, or delete")
				}
			},
		}, nil
	}
}

// RegisterProductSkills adds Crew's platform-operation contracts. General
// browser, coding, review, and skill-authoring skills still come from the
// existing AgentWorks registry.
func RegisterProductSkills() error {
	registerProductSkillsOnce.Do(func() {
		if err := uiuxpromax.Register(); err != nil {
			registerProductSkillsErr = err
			return
		}
		registerProductSkillsErr = agentprofiles.RegisterEmbeddedSkills(productConfigFiles, productSkills)
	})
	return registerProductSkillsErr
}

type workIdentity struct {
	Icon string `json:"icon,omitempty"`
	Name string `json:"name,omitempty"`
	Role string `json:"role,omitempty"`
}

type workProjectManifest struct {
	Description string        `json:"description,omitempty"`
	Identity    *workIdentity `json:"identity,omitempty"`
	UpdatedAt   string        `json:"updated_at,omitempty"`
}

const (
	workIdentityIconLimit    = 8
	workIdentityNameLimit    = 60
	workIdentityRoleLimit    = 120
	workIdentityPurposeLimit = 1000
)

func normalizeWorkIdentity(identity workIdentity) workIdentity {
	identity.Icon = strings.TrimSpace(identity.Icon)
	identity.Name = strings.TrimSpace(identity.Name)
	identity.Role = strings.TrimSpace(identity.Role)
	return identity
}

func renderWorkIdentity(identity workIdentity) string {
	identity = normalizeWorkIdentity(identity)
	if identity.Icon == "" && identity.Name == "" && identity.Role == "" {
		return ""
	}
	var lines []string
	if identity.Icon != "" {
		// An uploaded image is kilobytes of base64: it feeds the WORK_IDENTITY
		// prompt variable and tool results, so never inline it.
		if workflowtypes.IsImageIcon(identity.Icon) {
			lines = append(lines, "Icon: (custom uploaded image)")
		} else {
			lines = append(lines, "Icon: "+identity.Icon)
		}
	}
	if identity.Name != "" {
		lines = append(lines, "Name: "+identity.Name)
	}
	if identity.Role != "" {
		lines = append(lines, "Role: "+identity.Role)
	}
	return strings.Join(lines, "\n")
}

func validateWorkIdentity(identity workIdentity) string {
	// A preserved uploaded image must not block unrelated identity edits.
	if reason := workflowtypes.ValidateIcon(identity.Icon); reason != "" {
		return fmt.Sprintf("The identity icon is invalid: %s.", reason)
	}
	limits := []struct {
		label string
		value string
		max   int
	}{
		{label: "name", value: identity.Name, max: workIdentityNameLimit},
		{label: "role", value: identity.Role, max: workIdentityRoleLimit},
	}
	for _, field := range limits {
		if utf8.RuneCountInString(field.value) > field.max {
			return fmt.Sprintf("The identity %s must be at most %d characters.", field.label, field.max)
		}
	}
	return ""
}

func workIdentityFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		client := workspace.NewClient(
			workspaceAPIURL,
			workspace.WithUserID(runtime.UserID),
			workspace.WithExtraEnv(map[string]string{"MCP_SESSION_ID": runtime.SessionID}),
		)
		manifestPath := path.Join(runtime.WorkspacePath, "product.json")
		emitIdentityUpdated := func(operation string) {
			if runtime.Emit == nil {
				return
			}
			kind := "identity_updated"
			if runtime.Interaction != nil && strings.TrimSpace(runtime.Interaction.Kind) != "" {
				kind = strings.TrimSpace(runtime.Interaction.Kind)
			}
			product := strings.TrimSpace(runtime.Product)
			if product == "" {
				product = "work"
			}
			runtime.Emit(&orchestratorevents.ProductInteractionEvent{
				Product: product,
				Kind:    kind,
				Payload: map[string]interface{}{"operation": operation},
			})
		}
		return agentprofiles.ToolSpec{
			Name:     "set_work_identity",
			Category: "work_identity",
			Description: "Set, update, or clear this Crew project's agent identity when the user asks. " +
				"A short name, icon, role, and purpose keep the agent consistent across project chats, schedules, bots, and background work; they change presentation and behavior, never permissions. " +
				"Role and purpose are required: ask the user for both before the first save, and refuse to clear them. " +
				"Store it in product.json, preserve omitted fields, and never invent an identity.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"operation": map[string]interface{}{
						"type": "string", "enum": []string{"set", "clear"},
						"description": "Use set to create or update the identity, or clear to remove its icon and name. Role and purpose are required and clear never removes them.",
					},
					"icon":    map[string]interface{}{"type": "string", "maxLength": workIdentityIconLimit, "description": "One emoji or short glyph. Omit to preserve; pass empty to remove."},
					"name":    map[string]interface{}{"type": "string", "maxLength": workIdentityNameLimit, "description": "Short display name. Omit to preserve; pass empty to remove."},
					"role":    map[string]interface{}{"type": "string", "maxLength": workIdentityRoleLimit, "description": "Required short role. Omit to preserve."},
					"purpose": map[string]interface{}{"type": "string", "maxLength": workIdentityPurposeLimit, "description": "Required short purpose for this Crew. Saved as the project description. Omit to preserve."},
				},
				"required": []string{"operation"},
			},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				read, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: manifestPath})
				if err != nil {
					return "", fmt.Errorf("read Crew project configuration: %w", err)
				}
				var manifest map[string]interface{}
				if err := json.Unmarshal([]byte(read.Content), &manifest); err != nil {
					return "", fmt.Errorf("decode Crew project configuration: %w", err)
				}
				operation, _ := args["operation"].(string)
				operation = strings.ToLower(strings.TrimSpace(operation))
				if operation == "clear" {
					// Role is required, so clear removes presentation only.
					// A legacy identity without it is dropped entirely
					// instead of lingering half-empty. Purpose lives in the
					// top-level description and is never touched by clear.
					cleared := workIdentity{}
					if current, ok := manifest["identity"]; ok {
						encoded, _ := json.Marshal(current)
						_ = json.Unmarshal(encoded, &cleared)
						cleared = normalizeWorkIdentity(cleared)
					}
					if cleared.Role == "" {
						delete(manifest, "identity")
					} else {
						manifest["identity"] = workIdentity{Role: cleared.Role}
					}
				} else if operation == "set" {
					identity := workIdentity{}
					if current, ok := manifest["identity"]; ok {
						encoded, _ := json.Marshal(current)
						_ = json.Unmarshal(encoded, &identity)
					}
					if value, ok := args["name"].(string); ok {
						identity.Name = value
					}
					if value, ok := args["icon"].(string); ok {
						identity.Icon = value
					}
					if value, ok := args["role"].(string); ok {
						identity.Role = value
					}
					identity = normalizeWorkIdentity(identity)
					if validationError := validateWorkIdentity(identity); validationError != "" {
						return validationError, nil
					}
					purpose, _ := manifest["description"].(string)
					if value, ok := args["purpose"].(string); ok {
						purpose = strings.TrimSpace(value)
					} else {
						purpose = strings.TrimSpace(purpose)
					}
					if utf8.RuneCountInString(purpose) > workIdentityPurposeLimit {
						return fmt.Sprintf("The identity purpose must be at most %d characters.", workIdentityPurposeLimit), nil
					}
					if identity.Role == "" || purpose == "" {
						return "Role and purpose are both required for the Crew identity. Ask the user for whichever is missing before saving.", nil
					}
					manifest["description"] = purpose
					manifest["identity"] = identity
				} else {
					return "operation must be set or clear.", nil
				}
				manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				encoded, err := json.MarshalIndent(manifest, "", "  ")
				if err != nil {
					return "", fmt.Errorf("encode Crew project configuration: %w", err)
				}
				if _, err := client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: manifestPath, Content: string(encoded) + "\n"}); err != nil {
					return "", fmt.Errorf("save Crew identity: %w", err)
				}
				if operation == "clear" {
					emitIdentityUpdated(operation)
					return "The project icon and name were removed. The required role and purpose are preserved and still apply.", nil
				}
				var saved workIdentity
				encodedIdentity, _ := json.Marshal(manifest["identity"])
				_ = json.Unmarshal(encodedIdentity, &saved)
				emitIdentityUpdated(operation)
				return "The project bot identity is saved. Adopt it immediately in this chat. It will be included automatically in generated provider project instructions on future provider sessions.\n\n" + renderWorkIdentity(saved), nil
			},
		}, nil
	}
}

func createCrewProjectFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		// This trusted tool deliberately omits the current session id: it creates a
		// sibling project outside the active Crew's folder guard. X-User-ID still
		// scopes every Chats/ path to the authenticated user's private workspace.
		client := workspace.NewClient(workspaceAPIURL, workspace.WithUserID(runtime.UserID))
		return agentprofiles.ToolSpec{
			Name:     "create_crew",
			Category: "work_projects",
			Description: "Create another basic Crew project for the signed-in user when they ask. " +
				"The new Crew has its own durable conversation, files, runtime configuration, display name, and icon. " +
				"This does not replace or rename the active Crew. After creation, tell the user to select the new Crew from the top project menu; it asks for its required role and purpose on first chat.",
			Parameters: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"name":        map[string]interface{}{"type": "string", "maxLength": workIdentityNameLimit, "description": "Crew display name and project title."},
					"icon":        map[string]interface{}{"type": "string", "maxLength": workIdentityIconLimit, "description": "One emoji or short glyph. Omit to use the name's initial."},
					"description": map[string]interface{}{"type": "string", "maxLength": 1000, "description": "Optional short purpose for this Crew."},
				},
				"required": []string{"name"},
			},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				name := strings.TrimSpace(fmt.Sprint(args["name"]))
				if name == "" || name == "<nil>" {
					return "", fmt.Errorf("name is required")
				}
				icon, _ := args["icon"].(string)
				description, _ := args["description"].(string)
				identity := normalizeWorkIdentity(workIdentity{Name: name, Icon: icon})
				if identity.Icon == "" {
					identity.Icon = defaultCrewIcon(name)
				}
				if validationError := validateWorkIdentity(identity); validationError != "" {
					return validationError, nil
				}
				description = strings.TrimSpace(description)
				if utf8.RuneCountInString(description) > 1000 {
					return "The Crew description must be at most 1000 characters.", nil
				}

				id := uuid.NewString()
				workspacePath := path.Join("Chats", "Work", "projects", crewProjectSlug(name)+"-"+id[:8])
				now := time.Now().UTC().Format(time.RFC3339)
				workflowManifest := map[string]interface{}{
					"schema_version": 1,
					"id":             id,
					"label":          name,
					"capabilities": map[string]interface{}{
						"selected_servers":             []string{},
						"selected_tools":               []string{},
						"selected_skills":              []string{},
						"selected_secrets":             []string{},
						"selected_global_secret_names": []string{},
						"browser_mode":                 "auto",
						"use_code_execution_mode":      false,
					},
					"workflow_context_paths": []string{},
					"schedules":              []interface{}{},
					"triggers":               []interface{}{},
					"created_at":             now,
					"updated_at":             now,
				}
				productManifest := map[string]interface{}{
					"schema_version": 1,
					"product":        "work",
					"id":             id,
					"title":          name,
					"description":    description,
					"identity":       identity,
					"session_id":     "work:project:" + id,
					"created_at":     now,
					"updated_at":     now,
				}
				writeJSON := func(filePath string, value interface{}) error {
					encoded, err := json.MarshalIndent(value, "", "  ")
					if err != nil {
						return err
					}
					_, err = client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: filePath, Content: string(encoded) + "\n"})
					return err
				}
				if err := writeJSON(path.Join(workspacePath, "workflow.json"), workflowManifest); err != nil {
					return "", fmt.Errorf("create Crew runtime configuration: %w", err)
				}
				if _, err := client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: path.Join(workspacePath, "code", ".gitkeep"), Content: ""}); err != nil {
					return "", fmt.Errorf("create Crew code folder: %w", err)
				}
				if err := writeJSON(path.Join(workspacePath, "product.json"), productManifest); err != nil {
					return "", fmt.Errorf("create Crew project configuration: %w", err)
				}

				if runtime.Emit != nil {
					kind := "project_created"
					if runtime.Interaction != nil && strings.TrimSpace(runtime.Interaction.Kind) != "" {
						kind = strings.TrimSpace(runtime.Interaction.Kind)
					}
					runtime.Emit(&orchestratorevents.ProductInteractionEvent{
						Product: "work",
						Kind:    kind,
						Payload: map[string]interface{}{"operation": "created", "project_id": id, "name": name, "icon": identity.Icon, "workspace_path": workspacePath},
					})
				}
				result, err := json.MarshalIndent(map[string]interface{}{
					"status":         "created",
					"project_id":     id,
					"name":           name,
					"icon":           identity.Icon,
					"workspace_path": workspacePath,
					"message":        "The Crew was created. Select it from the top project menu to open its persistent conversation.",
				}, "", "  ")
				return string(result), err
			},
		}, nil
	}
}

// RegisterAgentProfileRuntime connects Crew's durable project tools and
// chat-configurable identity to the provider runtime.
func RegisterAgentProfileRuntime(registry *agentprofiles.Registry, workspaceAPIURL string) error {
	if err := registry.RegisterToolFactory("work.set-identity", workIdentityFactory(workspaceAPIURL)); err != nil {
		return err
	}
	if err := registry.RegisterToolFactory("work.create-project", createCrewProjectFactory(workspaceAPIURL)); err != nil {
		return err
	}
	if err := registry.RegisterToolFactory("work.custom-commands", workCustomCommandsFactory(workspaceAPIURL)); err != nil {
		return err
	}
	return registry.RegisterPromptVariables("work", func(ctx context.Context, runtime agentprofiles.RuntimeContext) (map[string]string, error) {
		client := workspace.NewClient(
			workspaceAPIURL,
			workspace.WithUserID(runtime.UserID),
			workspace.WithExtraEnv(map[string]string{"MCP_SESSION_ID": runtime.SessionID}),
		)
		result, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: path.Join(runtime.WorkspacePath, "product.json")})
		if err != nil {
			return nil, fmt.Errorf("read Crew identity: %w", err)
		}
		var manifest workProjectManifest
		if err := json.Unmarshal([]byte(result.Content), &manifest); err != nil {
			return nil, fmt.Errorf("decode Crew identity: %w", err)
		}
		identity := ""
		role := ""
		if manifest.Identity != nil {
			identity = renderWorkIdentity(*manifest.Identity)
			role = normalizeWorkIdentity(*manifest.Identity).Role
		}
		purpose := strings.TrimSpace(manifest.Description)
		if purpose != "" {
			if identity != "" {
				identity = "Purpose: " + purpose + "\n" + identity
			} else {
				identity = "Purpose: " + purpose
			}
		}
		// Always return the key because the Crew prompt uses missingkey=error.
		// WORK_IDENTITY_KEY carries just role and purpose so the agent
		// session fingerprint relaunches retained sessions when the Crew's
		// identity changes, without churning on cosmetic icon/name edits.
		return map[string]string{"WORK_IDENTITY": identity, "WORK_IDENTITY_KEY": role + "\n" + purpose}, nil
	})
}

package workproduct

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
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
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/mediapresentation"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
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
var registerFeatureSkillsMu sync.Mutex
var registeredFeatureSkills = map[string]bool{}

var productSkills = []agentprofiles.SkillFileBinding{
	{Name: "work-ui-control", Path: "skills/work-ui-control/SKILL.md"},
	{Name: "background-work", Path: "skills/background-work/SKILL.md"},
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

func customCommandMarkdown(name, description, icon, prompt string, modes []string, showOnCard bool) string {
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
	if showOnCard {
		out.WriteString("\nshow_on_card: true")
	}
	out.WriteString("\n---\n\n")
	out.WriteString(strings.TrimSpace(prompt))
	out.WriteString("\n")
	return out.String()
}

func workCustomCommandsFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return CustomCommandsFactory(workspaceAPIURL, "Crew project")
}

// CustomCommandsFactory is the project slash-command tool shared by the
// project products; projectNoun names the project in its description.
func CustomCommandsFactory(workspaceAPIURL, projectNoun string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		commandsPath := path.Join(runtime.WorkspacePath, commands.CustomCommandsSubPath)
		return agentprofiles.ToolSpec{
			Name: "manage_custom_commands", Category: "custom_commands",
			Description: "List, create, update, or delete reusable slash commands for this " + projectNoun + " when the user asks. Updates preserve omitted description, prompt, icon and show_on_card. Set show_on_card=true only for the few commands the person wants as a button on each reply card (default false; most commands stay slash-only); it can be turned off again. Every Crew starts with one card command, \"Check this change\", which can be renamed or deleted. Commands appear immediately in this project's slash menu. Product commands are immutable and cannot be changed with this tool.",
			Parameters: map[string]interface{}{
				"type": "object", "additionalProperties": false,
				"properties": map[string]interface{}{
					"action":       map[string]interface{}{"type": "string", "enum": []string{"list", "create", "update", "delete"}},
					"name":         map[string]interface{}{"type": "string", "description": "Slash command name, without the slash."},
					"description":  map[string]interface{}{"type": "string", "description": "Short text shown in the slash menu."},
					"prompt":       map[string]interface{}{"type": "string", "description": "Prompt submitted by the command. {{context}} inserts the text the user types with the command; put it on its own line (optionally after a \"Label:\" line) so it disappears when nothing is typed. It is appended automatically when missing."},
					"icon":         map[string]interface{}{"type": "string", "description": commands.IconDescription},
					"show_on_card": map[string]interface{}{"type": "boolean", "description": "Show this command as a button on each reply card. Default false. Turn on only for the few commands people want one click away."},
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
					description, prompt, icon := "", "", "terminal"
					modes := []string{"multi-agent"}
					showOnCard := false
					if action == "update" {
						existing, err := commands.GetCommandAt(workspaceAPIURL, commandsPath, name)
						if err != nil {
							return "", err
						}
						description, prompt, icon = existing.Frontmatter.Description, existing.Content, existing.Frontmatter.Icon
						modes = existing.Frontmatter.Modes
						showOnCard = existing.Frontmatter.ShowOnCard
					}
					if value, ok := args["show_on_card"].(bool); ok {
						showOnCard = value
					}
					if value, ok := args["description"].(string); ok {
						description = value
					}
					if value, ok := args["prompt"].(string); ok {
						prompt = value
					}
					if value, ok := args["icon"].(string); ok {
						icon = value
					}
					if strings.TrimSpace(description) == "" || strings.TrimSpace(prompt) == "" {
						return "", fmt.Errorf("description and prompt are required")
					}
					content := customCommandMarkdown(name, description, icon, prompt, modes, showOnCard)
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
		registerProductSkillsErr = RegisterFeatureSkills("work", "Crew")
		if registerProductSkillsErr == nil {
			registerProductSkillsErr = agentprofiles.RegisterEmbeddedSkills(productConfigFiles, []agentprofiles.SkillFileBinding{
				{Name: "crew-run", Description: "Use a Crew in Run mode: inspect, answer, run permitted workflow triggers and suggest changes to its owner.", Path: "skills/crew-run/SKILL.md"},
				{Name: "crew-builder", Path: "skills/crew-builder/SKILL.md"},
				{Name: "crew-agents", Path: "skills/crew-agents/SKILL.md"},
				{Name: "crew-laptop", Path: "skills/crew-laptop/SKILL.md"},
				{Name: "finance-work", Path: "skills/finance-work/SKILL.md"},
				{Name: "customer-work", Path: "skills/customer-work/SKILL.md"},
				{Name: "growth-work", Path: "skills/growth-work/SKILL.md"},
				{Name: "sales-work", Path: "skills/sales-work/SKILL.md"},
				{Name: "team-work", Path: "skills/team-work/SKILL.md"},
				{Name: "software-work", Path: "skills/software-work/SKILL.md"},
				{Name: "store-work", Path: "skills/store-work/SKILL.md"},
				// Cowork (a Code project in Cowork mode): the playbook for a private business assistant.
				{Name: "cowork-assistant", Path: "skills/cowork-assistant/SKILL.md"},
				{Name: "cowork-browser-tasks", Path: "skills/cowork-browser-tasks/SKILL.md"},
				{Name: "cowork-automations", Path: "skills/cowork-automations/SKILL.md"},
				// The person's own AgentWorks Crew (role "agentworks", PLAT-825-99): how it inspects, measures and improves.
				{Name: "agentworks-add-new-thing", Path: "skills/agentworks-add-new-thing/SKILL.md"},
				{Name: "agentworks-check-measurement", Path: "skills/agentworks-check-measurement/SKILL.md"},
				{Name: "agentworks-goal-anatomy", Path: "skills/agentworks-goal-anatomy/SKILL.md"},
				{Name: "agentworks-improve-goals", Path: "skills/agentworks-improve-goals/SKILL.md"},
				{Name: "agentworks-inspect-crew", Path: "skills/agentworks-inspect-crew/SKILL.md"},
				{Name: "agentworks-inspect-goal", Path: "skills/agentworks-inspect-goal/SKILL.md"},
				{Name: "agentworks-inspect-relay", Path: "skills/agentworks-inspect-relay/SKILL.md"},
				{Name: "agentworks-review-architecture", Path: "skills/agentworks-review-architecture/SKILL.md"},
				{Name: "agentworks-review-qa", Path: "skills/agentworks-review-qa/SKILL.md"},
				{Name: "agentworks-review-strategy", Path: "skills/agentworks-review-strategy/SKILL.md"},
				{Name: "agentworks-schedules-watchers", Path: "skills/agentworks-schedules-watchers/SKILL.md"},
				{Name: "agentworks-suggest-and-build", Path: "skills/agentworks-suggest-and-build/SKILL.md"},
			})
		}
	})
	return registerProductSkillsErr
}

// RegisterFeatureSkills renders only the product-specific navigation and
// background execution rules. Panel procedures are shared builtins with the
// same identity and supporting files in every product.
func RegisterFeatureSkills(profileID, productName string) error {
	registerFeatureSkillsMu.Lock()
	defer registerFeatureSkillsMu.Unlock()
	if registeredFeatureSkills[profileID] {
		return nil
	}
	render := func(text string) string {
		return agentprofiles.RenderFeatureText(profileID, productName, text)
	}
	bindings := make([]agentprofiles.SkillFileBinding, 0, len(productSkills))
	for _, binding := range productSkills {
		binding.Name = agentprofiles.FeatureSkillName(profileID, binding.Name)
		binding.Description = render(binding.Description)
		bindings = append(bindings, binding)
	}
	if err := agentprofiles.RegisterEmbeddedSkillsRendered(productConfigFiles, bindings, render); err != nil {
		return err
	}
	registeredFeatureSkills[profileID] = true
	return nil
}

type workIdentity struct {
	Icon string `json:"icon,omitempty"`
	Name string `json:"name,omitempty"`
}

type workProjectManifest struct {
	Description      string        `json:"description,omitempty"`
	ChatInstructions string        `json:"chat_instructions,omitempty"`
	Identity         *workIdentity `json:"identity,omitempty"`
	UpdatedAt        string        `json:"updated_at,omitempty"`
}

const (
	workIdentityIconLimit    = 8
	workIdentityNameLimit    = 60
	workIdentityPurposeLimit = 1000
)

func normalizeWorkIdentity(identity workIdentity) workIdentity {
	identity.Icon = strings.TrimSpace(identity.Icon)
	identity.Name = strings.TrimSpace(identity.Name)
	return identity
}

func renderWorkIdentity(identity workIdentity) string {
	identity = normalizeWorkIdentity(identity)
	if identity.Icon == "" && identity.Name == "" {
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
				"A short name, icon, and purpose keep the agent consistent across project chats, schedules, bots, and background work; they change presentation and behavior, never permissions. " +
				"The purpose is required: ask the user for it before the first save, and refuse to clear it. " +
				"Store it in product.json, preserve omitted fields, and never invent an identity.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"operation": map[string]interface{}{
						"type": "string", "enum": []string{"set", "clear"},
						"description": "Use set to create or update the identity, or clear to remove its icon and name. The purpose is required and clear never removes it.",
					},
					"icon":    map[string]interface{}{"type": "string", "maxLength": workIdentityIconLimit, "description": "One emoji or short glyph. Omit to preserve; pass empty to remove."},
					"name":    map[string]interface{}{"type": "string", "maxLength": workIdentityNameLimit, "description": "Short display name. Omit to preserve; pass empty to remove."},
					"purpose": map[string]interface{}{"type": "string", "maxLength": workIdentityPurposeLimit, "description": "Required: what this Crew is for, in one short line. Saved as the project description. Omit to preserve."},
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
					// Clear removes the name and icon only. The purpose lives in the
					// top-level description and is never touched by clear.
					delete(manifest, "identity")
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
					if purpose == "" {
						return "A purpose is required for the Crew identity. Ask the user what this Crew is for before saving.", nil
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
					return "The project icon and name were removed. The required purpose is preserved and still applies.", nil
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
				"This does not replace or rename the active Crew. After creation, tell the user to select the new Crew from the top project menu; it asks for its purpose on first chat.",
			Parameters: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"name":        map[string]interface{}{"type": "string", "maxLength": workIdentityNameLimit, "description": "Crew display name and project title."},
					"icon":        map[string]interface{}{"type": "string", "maxLength": workIdentityIconLimit, "description": "One emoji or short glyph. Omit to use the name's initial."},
					"description": map[string]interface{}{"type": "string", "maxLength": 1000, "description": "Optional short purpose for this Crew."},
					"root_path":   map[string]interface{}{"type": "string", "description": "Local app only: full path to an existing folder the person chose for this new Crew. Files stay there; Crew resources use .agentworks."},
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
						"browser_mode":                 browser.DefaultMode(),
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
	if err := mediapresentation.Register(registry, workspaceAPIURL); err != nil {
		return err
	}
	if err := registry.RegisterToolFactory("work.set-identity", workIdentityFactory(workspaceAPIURL)); err != nil {
		return err
	}
	if err := registry.RegisterToolFactory("work.create-project", createCrewProjectFactory(workspaceAPIURL)); err != nil {
		return err
	}
	if err := registry.RegisterToolFactory("work.custom-commands", workCustomCommandsFactory(workspaceAPIURL)); err != nil {
		return err
	}
	if err := registry.RegisterToolFactory("work.results", workResultsFactory(workspaceAPIURL)); err != nil {
		return err
	}
	return registry.RegisterPromptVariables("work", ProjectPromptVariables(workspaceAPIURL, false))
}

// ProjectPromptVariables loads the saved identity and purpose for both project origins.
// The caller must first resolve the project and its current access; request-supplied
// prompt variables never replace this read of the admitted project. Code origins
// may lack a legacy product.json; allowMissing keeps those chats usable.
func ProjectPromptVariables(workspaceAPIURL string, allowMissing bool) agentprofiles.PromptVariablesProvider {
	return func(ctx context.Context, runtime agentprofiles.RuntimeContext) (map[string]string, error) {
		client := workspace.NewClient(
			workspaceAPIURL,
			workspace.WithUserID(runtime.UserID),
			workspace.WithExtraEnv(map[string]string{"MCP_SESSION_ID": runtime.SessionID}),
		)
		result, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: path.Join(runtime.WorkspacePath, "product.json")})
		if err != nil {
			if allowMissing && workspace.IsFileNotFound(err) {
				return map[string]string{"WORK_IDENTITY": "", "WORK_IDENTITY_KEY": "\n", "CHAT_INSTRUCTIONS": ""}, nil
			}
			return nil, fmt.Errorf("read Crew identity: %w", err)
		}
		var manifest workProjectManifest
		if err := json.Unmarshal([]byte(result.Content), &manifest); err != nil {
			return nil, fmt.Errorf("decode Crew identity: %w", err)
		}
		identity := ""
		if manifest.Identity != nil {
			identity = renderWorkIdentity(*manifest.Identity)
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
		// Refresh retained sessions for behavioral guidance, not cosmetic edits.
		instructions := strings.TrimSpace(manifest.ChatInstructions)
		key := purpose
		if instructions != "" {
			key += "\nchat_instructions:\n" + instructions
		}
		return map[string]string{"WORK_IDENTITY": identity, "WORK_IDENTITY_KEY": key, "CHAT_INSTRUCTIONS": workflowtypes.ChatInstructionsPrompt(instructions)}, nil
	}
}

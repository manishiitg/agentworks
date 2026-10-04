package codeproduct

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// ProfileID is the Code product and profile id.
const ProfileID = "code"

// ProjectsRoot is where a user's Code workspaces live, below their own tree.
const ProjectsRoot = workspaceref.CodeProjectsRoot

const nameLimit = 60

// BuiltinAgentProfile returns the code profile.
func BuiltinAgentProfile() agentprofiles.Profile {
	manifest := mustCodeManifest()
	profile := manifest.Profile
	profile.SystemPromptTemplate = renderProductPrompt()
	return profile
}

func BuiltinAgentProfiles() []agentprofiles.Profile {
	return []agentprofiles.Profile{BuiltinAgentProfile()}
}

// RegisterProductSkills registers Code's own rendering of the shared
// project-feature skills (code-mcp, code-skills, ...): the same templates as
// Crew's, named for this product with the name from product.yaml.
func RegisterProductSkills() error {
	if err := workproduct.RegisterProductSkills(); err != nil {
		return err
	}
	return workproduct.RegisterFeatureSkills(ProfileID, mustCodeManifest().Profile.Name)
}

var projectSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func projectSlug(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = projectSlugPattern.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		return "code"
	}
	return name
}

// NewProjectFiles returns the workflow.json and product.json a new Code
// workspace starts with. A Code has a name only: no identity, role or purpose.
func NewProjectFiles(id, name string, now time.Time) (workspacePath string, workflowManifest, productManifest map[string]interface{}) {
	stamp := now.UTC().Format(time.RFC3339)
	workspacePath = path.Join(ProjectsRoot, projectSlug(name)+"-"+id[:8])
	workflowManifest = map[string]interface{}{
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
		"created_at":             stamp,
		"updated_at":             stamp,
	}
	productManifest = map[string]interface{}{
		"schema_version": 1,
		"product":        ProfileID,
		"id":             id,
		"title":          name,
		"session_id":     ProfileID + ":project:" + id,
		"created_at":     stamp,
		"updated_at":     stamp,
	}
	return workspacePath, workflowManifest, productManifest
}

// ValidateName checks a Code workspace name.
func ValidateName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "name is required"
	}
	if utf8.RuneCountInString(name) > nameLimit {
		return fmt.Sprintf("the name must be at most %d characters", nameLimit)
	}
	return ""
}

func createProjectFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		// No session id: the new workspace is a sibling outside the active
		// one's folder guard. X-User-ID keeps it in the caller's own tree.
		client := workspace.NewClient(workspaceAPIURL, workspace.WithUserID(runtime.UserID))
		return agentprofiles.ToolSpec{
			Name:        "create_code_workspace",
			Category:    "code_projects",
			Description: "Create another private Code workspace for the signed-in user when they ask. It gets its own files, chat and runtime settings, and does not replace this one. After creating it, tell the user to pick it from the top workspace menu.",
			Parameters: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string", "maxLength": nameLimit, "description": "Workspace name."},
				},
				"required": []string{"name"},
			},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				name, _ := args["name"].(string)
				name = strings.TrimSpace(name)
				if reason := ValidateName(name); reason != "" {
					return reason, nil
				}
				id := uuid.NewString()
				workspacePath, workflowManifest, productManifest := NewProjectFiles(id, name, time.Now())
				writeJSON := func(filePath string, value interface{}) error {
					encoded, err := json.MarshalIndent(value, "", "  ")
					if err != nil {
						return err
					}
					_, err = client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: filePath, Content: string(encoded) + "\n"})
					return err
				}
				if err := writeJSON(path.Join(workspacePath, "workflow.json"), workflowManifest); err != nil {
					return "", fmt.Errorf("create Code runtime configuration: %w", err)
				}
				if _, err := client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: path.Join(workspacePath, "code", ".gitkeep"), Content: ""}); err != nil {
					return "", fmt.Errorf("create Code folder: %w", err)
				}
				if err := writeJSON(path.Join(workspacePath, "product.json"), productManifest); err != nil {
					return "", fmt.Errorf("create Code project configuration: %w", err)
				}
				if runtime.Emit != nil {
					kind := "project_created"
					if runtime.Interaction != nil && strings.TrimSpace(runtime.Interaction.Kind) != "" {
						kind = strings.TrimSpace(runtime.Interaction.Kind)
					}
					runtime.Emit(&orchestratorevents.ProductInteractionEvent{
						Product: ProfileID,
						Kind:    kind,
						Payload: map[string]interface{}{"operation": "created", "project_id": id, "name": name, "workspace_path": workspacePath},
					})
				}
				result, err := json.MarshalIndent(map[string]interface{}{
					"status":         "created",
					"project_id":     id,
					"name":           name,
					"workspace_path": workspacePath,
					"message":        "The Code workspace was created. Pick it from the top workspace menu to open it.",
				}, "", "  ")
				return string(result), err
			},
		}, nil
	}
}

// RegisterAgentProfileRuntime connects Code's project tools. Code has no
// identity, so it registers no prompt variables.
func RegisterAgentProfileRuntime(registry *agentprofiles.Registry, workspaceAPIURL string) error {
	if err := registry.RegisterToolFactory("code.create-project", createProjectFactory(workspaceAPIURL)); err != nil {
		return err
	}
	return registry.RegisterToolFactory("code.custom-commands", workproduct.CustomCommandsFactory(workspaceAPIURL, "Code workspace"))
}

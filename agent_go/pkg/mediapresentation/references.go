package mediapresentation

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	agentprofiles "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/presentations"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

var characterImageExtensions = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true}

// presentationActivity converts the declarative copy in product.yaml into
// the typed wire event. The transcript then has no kind-specific copy or
// destination table to keep in sync with the product.
func presentationActivity(binding *agentprofiles.PresentationBinding) *orchestratorevents.PresentationActivity {
	if binding == nil || binding.Activity == nil {
		return nil
	}
	return &orchestratorevents.PresentationActivity{
		Label:       strings.TrimSpace(binding.Activity.Label),
		Destination: strings.TrimSpace(binding.Activity.Destination),
		Detail:      strings.TrimSpace(binding.Activity.Detail),
	}
}

// showReferenceFactory exposes the visual-development evidence that a sequence
// will be conditioned on: locations, wardrobe, props, and planned boundary
// frames. These are deliberately distinct from characters. A location board is
// not a character, and presenting it as one made the only review surface lie
// about what the user was approving.
func showReferenceFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		client := workspace.NewClient(
			workspaceAPIURL,
			workspace.WithUserID(runtime.UserID),
			workspace.WithExtraEnv(map[string]string{"MCP_SESSION_ID": runtime.SessionID}),
		)
		projectRoot := profileWorkspaceRoot(runtime.UserID, runtime.WorkspacePath)
		return agentprofiles.ToolSpec{
			Name: "show_reference", Category: "presentation_tools",
			Description: "Present a reviewable visual-development reference in the Production panel before footage uses it: a location/background, wardrobe/prop board, sequence start frame, or planned end frame. Call it for every generated reference the user must approve. Repeating the same image path updates that presentation.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"path":  map[string]interface{}{"type": "string", "description": "Project-relative path to the generated reference image"},
				"title": map[string]interface{}{"type": "string", "description": "Human-readable reference title"},
				"role":  map[string]interface{}{"type": "string", "description": "One of location, wardrobe, prop, start_frame, end_frame, or continuity"},
				"note":  map[string]interface{}{"type": "string", "description": "What later shots must preserve"},
			}, "required": []string{"path", "title", "role"}},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				if runtime.Presentation == nil || strings.TrimSpace(runtime.Presentation.Kind) == "" || presentationActivity(runtime.Presentation) == nil {
					return "", fmt.Errorf("show_reference: profile did not declare a presentation kind and activity for this tool")
				}
				imagePath, err := cleanProjectPath(stringArg(args, "path"))
				if err != nil || !characterImageExtensions[strings.ToLower(filepath.Ext(imagePath))] {
					return "show_reference requires a project-relative reference image (.png, .jpg, or .webp).", nil
				}
				title := strings.TrimSpace(stringArg(args, "title"))
				if title == "" {
					title = filepath.Base(imagePath)
				}
				role := strings.TrimSpace(stringArg(args, "role"))
				if role == "" {
					return "show_reference requires a reference role.", nil
				}
				resolvedPath, data, err := resolveWorkspaceEvidence(ctx, client, projectRoot, imagePath, imagePath)
				if err != nil || len(data) == 0 {
					return "The visual reference is missing or empty: " + imagePath, nil
				}
				event, err := persistPresentation(ctx, client, presentations.Presentation{
					Kind: runtime.Presentation.Kind, IdentityKey: resolvedPath, Title: title,
					WorkspacePath: runtime.WorkspacePath, SessionID: runtime.SessionID,
					Activity:  presentationActivity(runtime.Presentation),
					Payload:   map[string]interface{}{"path": resolvedPath, "role": role, "note": strings.TrimSpace(stringArg(args, "note"))},
					Resources: []map[string]string{{"kind": "workspace.file", "path": resolvedPath, "role": "primary"}},
				})
				if err != nil {
					return "", fmt.Errorf("persist visual reference presentation: %w", err)
				}
				if runtime.Emit != nil {
					runtime.Emit(&event)
				}
				return fmt.Sprintf("Showing %q in the References panel.", title), nil
			},
		}, nil
	}
}

// Character specs and their reference images are surfaced by the agent rather
// than discovered by the UI, and that is the whole point: the same production
// writes them to work/characters/ in direct chat and into a step's own folder
// as a workflow stage, so a panel that globbed one fixed path would go blank
// in the other mode. The agent already knows where it put them.
//
// Unlike show_video there is no QA gate here. A character reference is
// pre-production, shown precisely so the user can reject a face before dozens
// of shots are generated from it -- gating it behind quality-report.json would
// make it useless for the one job it has. What is enforced is that both files
// actually exist and are non-empty, since a character presented without its
// reference image is the exact failure this is meant to prevent.
func showCharacterFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		client := workspace.NewClient(
			workspaceAPIURL,
			workspace.WithUserID(runtime.UserID),
			workspace.WithExtraEnv(map[string]string{"MCP_SESSION_ID": runtime.SessionID}),
		)
		projectRoot := profileWorkspaceRoot(runtime.UserID, runtime.WorkspacePath)
		return agentprofiles.ToolSpec{
			Name: "show_character", Category: "presentation_tools",
			Description: "Present a recurring character, presenter, or product in the product Characters panel, with the reference image every later shot of it will be conditioned on. Call this once the spec and reference image exist and before generating shots that use them, so the user can approve the subject's appearance while changing it is still cheap. Repeating the same character name updates the existing presentation.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"name":       map[string]interface{}{"type": "string", "description": "The character/subject name, used as its stable identity"},
				"image_path": map[string]interface{}{"type": "string", "description": "Project-relative path to the generated reference image"},
				"spec_path":  map[string]interface{}{"type": "string", "description": "Project-relative path to the written character spec (.md)"},
				"model":      map[string]interface{}{"type": "string", "description": "Resolved model id this character's whole arc is committed to"},
				"provider":   map[string]interface{}{"type": "string", "description": "fal-ai or google-ai"},
				"note":       map[string]interface{}{"type": "string"},
			}, "required": []string{"name", "image_path", "spec_path"}},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				if runtime.Presentation == nil || strings.TrimSpace(runtime.Presentation.Kind) == "" || presentationActivity(runtime.Presentation) == nil {
					return "", fmt.Errorf("show_character: profile did not declare a presentation kind and activity for this tool")
				}
				name := strings.TrimSpace(stringArg(args, "name"))
				if name == "" {
					return "show_character requires the character's name.", nil
				}
				imagePath, err := cleanProjectPath(stringArg(args, "image_path"))
				if err != nil || !characterImageExtensions[strings.ToLower(filepath.Ext(imagePath))] {
					return "show_character requires a project-relative reference image (.png, .jpg, or .webp).", nil
				}
				specPath, err := cleanProjectPath(stringArg(args, "spec_path"))
				if err != nil || strings.ToLower(filepath.Ext(specPath)) != ".md" {
					return "show_character requires a project-relative .md character spec.", nil
				}

				// Resolve both against the same reference so a workflow stage's
				// step-folder-relative paths work exactly like a chat session's
				// project-relative ones.
				resolvedImage, imageData, err := resolveWorkspaceEvidence(ctx, client, projectRoot, imagePath, specPath)
				if err != nil || len(imageData) == 0 {
					return "The reference image is missing or empty: " + imagePath, nil
				}
				resolvedSpec, specData, err := resolveWorkspaceEvidence(ctx, client, projectRoot, specPath, specPath)
				if err != nil || len(specData) == 0 {
					return "The character spec is missing or empty: " + specPath, nil
				}

				event, err := persistPresentation(ctx, client, presentations.Presentation{
					Kind:          runtime.Presentation.Kind,
					IdentityKey:   name,
					Title:         name,
					WorkspacePath: runtime.WorkspacePath,
					SessionID:     runtime.SessionID,
					Activity:      presentationActivity(runtime.Presentation),
					Payload: map[string]interface{}{
						"name":       name,
						"image_path": resolvedImage,
						"spec_path":  resolvedSpec,
						"spec":       string(specData),
						"model":      strings.TrimSpace(stringArg(args, "model")),
						"provider":   strings.TrimSpace(stringArg(args, "provider")),
						"note":       strings.TrimSpace(stringArg(args, "note")),
					},
					Resources: []map[string]string{
						{"kind": "workspace.file", "path": resolvedImage, "role": "primary"},
						{"kind": "workspace.file", "path": resolvedSpec, "role": "spec"},
					},
				})
				if err != nil {
					return "", fmt.Errorf("persist character presentation: %w", err)
				}
				if runtime.Emit != nil {
					runtime.Emit(&event)
				}
				return fmt.Sprintf("Showing %q in the Characters panel.", name), nil
			},
		}, nil
	}
}

// The written artifacts -- brief, script, shot list -- are what the user
// actually reviews and approves between stages, and until now they were
// reachable only by finding the right file in a tree of step folders. Showing
// them is the same mechanism as a character: the agent names the artifact it
// just produced, so it works in either execution mode.
func showDocumentFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		client := workspace.NewClient(
			workspaceAPIURL,
			workspace.WithUserID(runtime.UserID),
			workspace.WithExtraEnv(map[string]string{"MCP_SESSION_ID": runtime.SessionID}),
		)
		projectRoot := profileWorkspaceRoot(runtime.UserID, runtime.WorkspacePath)
		return agentprofiles.ToolSpec{
			Name: "show_document", Category: "presentation_tools",
			Description: "Present a written production artifact -- a brief, script, shot list, or character sheet -- in the product Documents panel so the user can read and approve it without hunting for the file. Call this when a stage finishes the artifact it owes. Repeating the same path updates the existing presentation.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"path":  map[string]interface{}{"type": "string", "description": "Project-relative path to the .md artifact"},
				"title": map[string]interface{}{"type": "string", "description": "Human-readable name, e.g. \"Script\" or \"Shot list\""},
				"note":  map[string]interface{}{"type": "string", "description": "What the user should look at or decide"},
			}, "required": []string{"path", "title"}},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				if runtime.Presentation == nil || strings.TrimSpace(runtime.Presentation.Kind) == "" || presentationActivity(runtime.Presentation) == nil {
					return "", fmt.Errorf("show_document: profile did not declare a presentation kind and activity for this tool")
				}
				docPath, err := cleanProjectPath(stringArg(args, "path"))
				if err != nil || strings.ToLower(filepath.Ext(docPath)) != ".md" {
					return "show_document requires a project-relative .md path.", nil
				}
				resolvedPath, data, err := resolveWorkspaceEvidence(ctx, client, projectRoot, docPath, docPath)
				if err != nil || len(data) == 0 {
					return "The document is missing or empty: " + docPath, nil
				}
				title := strings.TrimSpace(stringArg(args, "title"))
				if title == "" {
					title = filepath.Base(resolvedPath)
				}

				event, err := persistPresentation(ctx, client, presentations.Presentation{
					Kind:          runtime.Presentation.Kind,
					IdentityKey:   resolvedPath,
					Title:         title,
					WorkspacePath: runtime.WorkspacePath,
					SessionID:     runtime.SessionID,
					Activity:      presentationActivity(runtime.Presentation),
					Payload: map[string]interface{}{
						"path":     resolvedPath,
						"markdown": string(data),
						"note":     strings.TrimSpace(stringArg(args, "note")),
					},
					Resources: []map[string]string{{"kind": "workspace.file", "path": resolvedPath, "role": "primary"}},
				})
				if err != nil {
					return "", fmt.Errorf("persist document presentation: %w", err)
				}
				if runtime.Emit != nil {
					runtime.Emit(&event)
				}
				return fmt.Sprintf("Showing %q in the Documents panel.", title), nil
			},
		}, nil
	}
}

func stringArg(args map[string]interface{}, key string) string {
	value, _ := args[key].(string)
	return value
}

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// A project's agent cannot read another project's workflow code itself (it sits in the project's private data
// folder), and a script.json copied by hand keeps the source's identity, which the writer refuses. So the server
// copies: the caller must be able to open the source workflow and own the target project. Only the source code
// travels (script.json, Python files, requirements.txt); runs, schedules, webhooks and secret values never do.

// crewWorkflowCopySource resolves a workflow ID the caller can open (as owner, editor or Run person).
func crewWorkflowCopySource(ctx context.Context, claims *UserClaims, id string) (*crewExecutableBinding, string, error) {
	unavailable := fmt.Errorf("the workflow to copy is unavailable; pick one from workspace action=references_list (its id) that you can open")
	id = strings.TrimSpace(id)
	if id == "" || claims == nil || !userAllowedWorkflowID(claims, id) {
		return nil, "", unavailable
	}
	source, err := findCrewExecutableByID(id)
	if err != nil || source == nil || source.Kind != "script" {
		return nil, "", unavailable
	}
	manifest, found, err := ReadWorkflowManifest(ctx, source.Root)
	if err != nil || !found || manifest.Kind == "relay" || workflowAccessForManifest(claims, manifest) == WorkflowAccessNone {
		return nil, "", unavailable
	}
	if claims.AccessToken != nil && !claims.AccessToken.AllowsWorkflow(id) {
		return nil, "", unavailable
	}
	return source, manifest.Label, nil
}

// copyCrewWorkflowSource writes the source's code into the freshly created target. Python and requirements go
// first and script.json last, so the saved definition is checked against the copied code.
func copyCrewWorkflowSource(ctx context.Context, source, target *crewExecutableBinding, name, description string) ([]string, error) {
	sourceCode := path.Join(source.Root, "code")
	listed, err := listWorkspaceFilesRecursive(ctx, sourceCode)
	if err != nil {
		return nil, fmt.Errorf("could not list the source workflow's files: %w", err)
	}
	copied := []string{}
	for _, name := range listed {
		file := strings.TrimPrefix(strings.TrimPrefix(name, sourceCode), "/")
		if file == "script.json" || !crewScriptEditableFile(file) {
			continue
		}
		content, found, err := readFileFromWorkspace(ctx, sourceCode+"/"+file)
		if err != nil || !found {
			return copied, fmt.Errorf("could not read %s from the source workflow", file)
		}
		if err := writeCrewScriptFile(ctx, target, file, content); err != nil {
			return copied, fmt.Errorf("copy %s: %w", file, err)
		}
		copied = append(copied, file)
	}
	definition, err := readCrewScript(ctx, source.Root)
	if err != nil {
		return copied, fmt.Errorf("could not read the source workflow's script.json: %w", err)
	}
	definition.ID, definition.Name = target.ID, name
	if description != "" {
		definition.Description = description
	}
	if err := saveCrewScriptDefinition(ctx, target, definition, nil); err != nil {
		return copied, fmt.Errorf("copy script.json: %w", err)
	}
	return append(copied, "script.json"), nil
}

// crewWorkflowCopyReply tells the agent what came across and what the person still sets up here.
func crewWorkflowCopyReply(created []byte, copied []string, secrets []string) (string, error) {
	var out map[string]interface{}
	if err := json.Unmarshal(created, &out); err != nil {
		return "", err
	}
	out["copied_files"] = copied
	note := "Copied the workflow's code. Runs, schedules, webhooks and functions were not copied; add them here if needed. It has not been run."
	if len(secrets) > 0 {
		out["secrets_needed"] = secrets
		note += " It uses secrets by name; select them for this project before running."
	}
	out["note"] = note
	encoded, err := json.Marshal(out)
	return string(encoded), err
}

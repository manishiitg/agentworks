package server

import (
	"context"
	"encoding/json"
	"strings"
)

// Reads per-user workflow access grants to config/workflow-user-permissions.json.
// File shape: { "user-key": "read|write|owner", ... }. Keys are normalized (lowercase
// trimmed) and matched against UserID, Username, or Email at lookup time. File-backed
// grants override the env-var grants in workflow_permissions.go.

func workflowUserPermissionsFilePath() string {
	return "config/workflow-user-permissions.json"
}

func readWorkflowUserPermissionsFile() (map[string]WorkflowAccessLevel, error) {
	data, exists, err := readFileFromWorkspace(context.Background(), workflowUserPermissionsFilePath())
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]WorkflowAccessLevel{}, nil
	}
	var raw map[string]string
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, err
	}
	out := make(map[string]WorkflowAccessLevel, len(raw))
	for k, v := range raw {
		key := normalizeWorkflowPermissionKey(k)
		if key == "" {
			continue
		}
		level, ok := parseWorkflowAccessLevel(v)
		if !ok {
			continue
		}
		out[key] = level
	}
	return out, nil
}

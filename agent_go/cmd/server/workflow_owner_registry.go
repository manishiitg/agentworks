package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A Goal's owner for its selected Vault secrets is SERVER-CONTROLLED, like a Crew's (project_owner_registry.go):
// workflow.json is writable by editors and by Builder turns running a shell in the workflow folder, so its created_by
// can never decide whose Vault a run reads (PLAT-856-217, reviewer 2026-10-20). The record lives in the app's state
// area (<state root>/ownership/workflows.json: workflow folder "Workflow/<name>" -> creator; keyed by folder, since the
// manifest ID is in the same user-writable file), out of reach of the workspace proxy
// and of any CLI's folder guard. It is written when a workflow is created, copied or created as a Relay, and filled
// once for existing workflows from their current created_by (backfillWorkflowOwners, at startup, after a backup).
// An entry is never overwritten with another owner.

const workflowOwnerRegistryFile = "workflows.json"

var workflowOwnersMu sync.Mutex

func workflowOwnersPath() string {
	if testing.Testing() && strings.TrimSpace(os.Getenv("AGENTWORKS_STATE_ROOT")) == "" {
		return ""
	}
	root, err := workflowCLIStateRoot()
	if err != nil || strings.TrimSpace(root) == "" {
		return ""
	}
	return filepath.Join(filepath.Clean(root), projectOwnerRegistryDir, workflowOwnerRegistryFile)
}

func loadWorkflowOwnersLocked(path string) (map[string]string, error) {
	owners := map[string]string{}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return owners, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &owners); err != nil {
		return nil, err
	}
	return owners, nil
}

// workflowOwnerKey is a workflow's folder, "Workflow/<name>", from any path inside it ("" when not a workflow).
func workflowOwnerKey(workspacePath string) string {
	parts := strings.SplitN(strings.Trim(filepath.ToSlash(strings.TrimSpace(workspacePath)), "/"), "/", 3)
	if len(parts) < 2 || parts[0] != "Workflow" || parts[1] == "" || strings.HasPrefix(parts[1], ".") {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// workflowOwner is the registered creator of a workflow, or "" when it has none.
func workflowOwner(workspacePath string) string {
	path := workflowOwnersPath()
	key := workflowOwnerKey(workspacePath)
	if path == "" || key == "" {
		return ""
	}
	workflowOwnersMu.Lock()
	defer workflowOwnersMu.Unlock()
	owners, err := loadWorkflowOwnersLocked(path)
	if err != nil {
		log.Printf("[WORKFLOW_OWNERS] cannot read %s: %v", path, err)
		return ""
	}
	return owners[key]
}

// registerWorkflowOwner records a workflow's creator once; an existing entry is kept.
func registerWorkflowOwner(workspacePath, owner string) {
	path := workflowOwnersPath()
	key, owner := workflowOwnerKey(workspacePath), strings.TrimSpace(owner)
	if path == "" || key == "" || owner == "" {
		return
	}
	workflowOwnersMu.Lock()
	defer workflowOwnersMu.Unlock()
	owners, err := loadWorkflowOwnersLocked(path)
	if err != nil {
		log.Printf("[WORKFLOW_OWNERS] cannot read %s: %v", path, err)
		return
	}
	if current, exists := owners[key]; exists {
		if current != owner {
			log.Printf("[WORKFLOW_OWNERS] %s stays registered to %s (not %s)", key, current, owner)
		}
		return
	}
	owners[key] = owner
	encoded, err := json.MarshalIndent(owners, "", "  ")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o700)
	}
	if err == nil {
		err = writeRegistryFileAtomically(path, encoded)
	}
	if err != nil {
		log.Printf("[WORKFLOW_OWNERS] cannot register %s: %v", key, err)
	}
}

// backfillWorkflowOwners registers every existing workflow that has no entry yet from its current created_by. It runs
// at startup; once a workflow is registered its file can no longer change the answer.
func backfillWorkflowOwners(ctx context.Context) {
	if workflowOwnersPath() == "" {
		return
	}
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		log.Printf("[WORKFLOW_OWNERS] backfill skipped: %v", err)
		return
	}
	added := 0
	for _, workflow := range discovered {
		if workflow.Manifest == nil || strings.TrimSpace(workflow.Manifest.CreatedBy) == "" {
			continue
		}
		if workflowOwner(workflow.WorkspacePath) == "" {
			registerWorkflowOwner(workflow.WorkspacePath, workflow.Manifest.CreatedBy)
			added++
		}
	}
	if added > 0 {
		log.Printf("[WORKFLOW_OWNERS] backfilled %d workflow owner(s)", added)
	}
}

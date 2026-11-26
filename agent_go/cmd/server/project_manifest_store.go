package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// mutateProjectRuntimeManifest serializes read-modify-write patches to a
// project's runtime manifest (workflow.json for Crew/Code projects,
// product.json for fixed-workspace products) behind the shared per-manifest
// mutex. Only the keys touched by mutate change; every other top-level and
// capabilities field is preserved untouched.
func mutateProjectRuntimeManifest(
	ctx context.Context,
	profileID, workspacePath string,
	mutate func(manifest, capabilities map[string]any) (bool, error),
) error {
	manifestPath := projectRuntimeManifestPath(profileID, workspacePath)
	lock := productConversationRegistryMutex(manifestPath)
	lock.Lock()
	defer lock.Unlock()

	raw, resolvedPath, err := ensureProjectRuntimeManifest(ctx, profileID, workspacePath)
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return fmt.Errorf("decode product manifest: %w", err)
	}
	if manifest == nil {
		manifest = map[string]any{}
	}
	capabilities, _ := manifest["capabilities"].(map[string]any)
	if capabilities == nil {
		capabilities = map[string]any{}
	}
	changed, err := mutate(manifest, capabilities)
	if err != nil || !changed {
		return err
	}
	manifest["capabilities"] = capabilities
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode product manifest: %w", err)
	}
	return writeFileToWorkspace(ctx, resolvedPath, string(encoded)+"\n")
}

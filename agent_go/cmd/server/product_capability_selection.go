package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
)

func projectRuntimeManifestPath(profileID, workspacePath string) string {
	name := "product.json"
	if isProjectProfileID(profileID) {
		name = "workflow.json"
	}
	return filepath.ToSlash(filepath.Join(workspacePath, name))
}

func readProjectRuntimeManifest(ctx context.Context, profileID, workspacePath string) (string, bool, error) {
	raw, found, err := readFileFromWorkspace(ctx, projectRuntimeManifestPath(profileID, workspacePath))
	if err != nil || found || !isProjectProfileID(profileID) {
		return raw, found, err
	}
	return readFileFromWorkspace(ctx, filepath.ToSlash(filepath.Join(workspacePath, "product.json")))
}

func workRuntimeManifestFromLegacy(raw string) (map[string]interface{}, error) {
	var legacy map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
		return nil, err
	}
	manifest := map[string]interface{}{
		"schema_version": 1,
		"id":             legacy["id"],
		"label":          legacy["title"],
		"capabilities":   map[string]interface{}{},
		"schedules":      []interface{}{},
		"triggers":       []interface{}{},
		"created_at":     legacy["created_at"],
		"updated_at":     legacy["updated_at"],
	}
	for _, key := range []string{"capabilities", "schedules", "triggers"} {
		if value, ok := legacy[key]; ok {
			manifest[key] = value
		}
	}
	if value, ok := legacy["workflow_context_paths"]; ok {
		manifest["workflow_context_paths"] = value
	} else if capabilities, ok := legacy["capabilities"].(map[string]interface{}); ok {
		if value, ok := capabilities["workflow_context_paths"]; ok {
			manifest["workflow_context_paths"] = value
			delete(capabilities, "workflow_context_paths")
		}
	}
	capabilities, _ := manifest["capabilities"].(map[string]interface{})
	if capabilities == nil {
		capabilities = map[string]interface{}{}
		manifest["capabilities"] = capabilities
	}
	defaults := map[string]interface{}{
		"selected_tools":               []interface{}{},
		"selected_global_secret_names": []interface{}{},
		"browser_mode":                 browser.DefaultMode(),
		"use_code_execution_mode":      false,
	}
	for key, value := range defaults {
		if _, exists := capabilities[key]; !exists {
			capabilities[key] = value
		}
	}
	return manifest, nil
}

func ensureProjectRuntimeManifest(ctx context.Context, profileID, workspacePath string) (string, string, error) {
	manifestPath := projectRuntimeManifestPath(profileID, workspacePath)
	raw, found, err := readFileFromWorkspace(ctx, manifestPath)
	if err != nil {
		return "", manifestPath, err
	}
	if found {
		return raw, manifestPath, nil
	}
	// Fixed-workspace products never run a "create
	// project" step that would leave a manifest behind, so one must be created
	// here on first use -- unlike Work, there is no legacy product.json to
	// migrate from. Without this, every read/write of a secret or app selection
	// for such a profile failed with "product manifest not
	// found" forever, silently: a secret saved via the secret tools was stored
	// but never attached to the conversation, so it never reached the agent's
	// environment (confirmed live: a product's agent_browser tool could not
	// see a parent-saved portal password).
	if !isProjectProfileID(profileID) {
		manifest := map[string]interface{}{
			"schema_version": 1,
			"id":             profileID,
			"capabilities":   map[string]interface{}{},
			"created_at":     time.Now().UTC().Format(time.RFC3339),
			"updated_at":     time.Now().UTC().Format(time.RFC3339),
		}
		encoded, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return "", manifestPath, err
		}
		raw = string(encoded) + "\n"
		if err := writeFileToWorkspace(ctx, manifestPath, raw); err != nil {
			return "", manifestPath, err
		}
		return raw, manifestPath, nil
	}
	legacyRaw, legacyFound, err := readFileFromWorkspace(ctx, filepath.ToSlash(filepath.Join(workspacePath, "product.json")))
	if err != nil || !legacyFound {
		if err == nil {
			err = fmt.Errorf("product manifest not found")
		}
		return "", manifestPath, err
	}
	manifest, err := workRuntimeManifestFromLegacy(legacyRaw)
	if err != nil {
		return "", manifestPath, fmt.Errorf("decode legacy product manifest: %w", err)
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", manifestPath, err
	}
	raw = string(encoded) + "\n"
	if err := writeFileToWorkspace(ctx, manifestPath, raw); err != nil {
		return "", manifestPath, err
	}
	// Strip the runtime fields from product.json only after workflow.json is
	// durable. Failure here is safe: workflow.json is already authoritative and
	// the remaining legacy fields are ignored.
	var metadata map[string]interface{}
	if json.Unmarshal([]byte(legacyRaw), &metadata) == nil {
		for _, key := range []string{"capabilities", "schedules", "triggers", "workflow_context_paths"} {
			delete(metadata, key)
		}
		metadata["updated_at"] = time.Now().UTC().Format(time.RFC3339)
		if encodedMetadata, encodeErr := json.MarshalIndent(metadata, "", "  "); encodeErr == nil {
			_ = writeFileToWorkspace(ctx, filepath.ToSlash(filepath.Join(workspacePath, "product.json")), string(encodedMetadata)+"\n")
		}
	}
	return raw, manifestPath, nil
}

// productSelectedGlobalSecrets reads the explicit global-secret allowlist for
// a product project. Product selections are always explicit: a missing legacy
// field or JSON null both mean none, so future global secrets are never granted
// to a Crew merely because its manifest predates this capability.
func productSelectedGlobalSecrets(ctx context.Context, profileID, workspacePath string) (*[]string, error) {
	raw, found, err := readProjectRuntimeManifest(ctx, profileID, workspacePath)
	if err != nil {
		return nil, err
	}
	names := []string{}
	if found {
		var manifest map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
			return nil, fmt.Errorf("decode product manifest: %w", err)
		}
		capabilities, _ := manifest["capabilities"].(map[string]interface{})
		if value := capabilities["selected_global_secret_names"]; value != nil {
			items, ok := value.([]interface{})
			if !ok {
				return nil, fmt.Errorf("product capabilities.selected_global_secret_names must be an array or null")
			}
			for _, item := range items {
				name, ok := item.(string)
				if ok {
					names = appendUniqueStrings(names, strings.TrimSpace(name))
				}
			}
		}
	}
	// GitHub is selected by this project, independently of other Vault apps.
	if owner, ok := crewProjectOwnerID(workspacePath); ok {
		rec, err := projectGitHubConnection(ctx, owner, workspacePath)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			// Do not let an older explicitly selected token outrank the selected account.
			githubNames := map[string]bool{}
			for _, saved := range gitHubConnections(owner) {
				githubNames[saved.SecretName] = true
			}
			filtered := names[:0]
			for _, name := range names {
				if !githubNames[name] && vaultSecretShortName(name) != "GITHUB_TOKEN" {
					filtered = append(filtered, name)
				}
			}
			names = appendUniqueStrings(filtered, rec.SecretName)
		}
	}

	sort.Strings(names)
	return &names, nil
}

func updateProductSelectedGlobalSecrets(ctx context.Context, profileID, workspacePath string, mutate func([]string) []string) error {
	raw, manifestPath, err := ensureProjectRuntimeManifest(ctx, profileID, workspacePath)
	if err != nil {
		return err
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return fmt.Errorf("decode product manifest: %w", err)
	}
	capabilities, _ := manifest["capabilities"].(map[string]interface{})
	if capabilities == nil {
		capabilities = map[string]interface{}{}
	}
	current := []string{}
	if items, ok := capabilities["selected_global_secret_names"].([]interface{}); ok {
		for _, item := range items {
			if name, ok := item.(string); ok {
				current = appendUniqueStrings(current, strings.TrimSpace(name))
			}
		}
	}
	next := mutate(current)
	canonical := make([]string, 0, len(next))
	for _, name := range next {
		canonical = appendUniqueStrings(canonical, strings.TrimSpace(name))
	}
	sort.Strings(canonical)
	capabilities["selected_global_secret_names"] = canonical
	manifest["capabilities"] = capabilities
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode product manifest: %w", err)
	}
	return writeFileToWorkspace(ctx, manifestPath, string(encoded)+"\n")
}

func productSelectedSkills(ctx context.Context, profileID, workspacePath string) ([]string, error) {
	raw, found, err := readProjectRuntimeManifest(ctx, profileID, workspacePath)
	if err != nil {
		return nil, err
	}
	if !found {
		return []string{}, nil
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return nil, fmt.Errorf("decode product manifest: %w", err)
	}
	capabilities, _ := manifest["capabilities"].(map[string]interface{})
	items, _ := capabilities["selected_skills"].([]interface{})
	names := make([]string, 0, len(items))
	for _, item := range items {
		if name, ok := item.(string); ok {
			names = appendUniqueStrings(names, strings.TrimSpace(name))
		}
	}
	sort.Strings(names)
	return names, nil
}

func updateProductSelectedSkills(ctx context.Context, profileID, workspacePath string, mutate func([]string) []string) error {
	raw, manifestPath, err := ensureProjectRuntimeManifest(ctx, profileID, workspacePath)
	if err != nil {
		return err
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return fmt.Errorf("decode product manifest: %w", err)
	}
	capabilities, _ := manifest["capabilities"].(map[string]interface{})
	if capabilities == nil {
		capabilities = map[string]interface{}{}
	}
	current := []string{}
	if items, ok := capabilities["selected_skills"].([]interface{}); ok {
		for _, item := range items {
			if name, ok := item.(string); ok {
				current = appendUniqueStrings(current, strings.TrimSpace(name))
			}
		}
	}
	next := mutate(current)
	canonical := make([]string, 0, len(next))
	for _, name := range next {
		canonical = appendUniqueStrings(canonical, strings.TrimSpace(name))
	}
	sort.Strings(canonical)
	capabilities["selected_skills"] = canonical
	manifest["capabilities"] = capabilities
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode product manifest: %w", err)
	}
	return writeFileToWorkspace(ctx, manifestPath, string(encoded)+"\n")
}

// productSelectedServers is a Crew's app selection as the agent runtime takes it (runtimeAppSelection); initialized
// reports whether the Crew has saved one yet.
func productSelectedServers(ctx context.Context, profileID, workspacePath string) ([]string, bool, error) {
	names, chosenOnly, initialized, err := productAppSelectionRecord(ctx, profileID, workspacePath)
	if err != nil || !initialized {
		return nil, initialized, err
	}
	return runtimeAppSelection(names, chosenOnly), true, nil
}

// productAppSelection is a Crew's saved app list and its "chosen Vault apps only" setting.
func productAppSelection(ctx context.Context, profileID, workspacePath string) ([]string, bool, error) {
	names, chosenOnly, _, err := productAppSelectionRecord(ctx, profileID, workspacePath)
	return names, chosenOnly, err
}

// productAppSelectionRecord reads capabilities.selected_servers and chosen_vault_apps_only from the product's
// canonical runtime manifest. Work project MCP selection is durable project state, distinct from the platform-wide
// connection overlay.
func productAppSelectionRecord(ctx context.Context, profileID, workspacePath string) ([]string, bool, bool, error) {
	raw, found, err := readProjectRuntimeManifest(ctx, profileID, workspacePath)
	if err != nil {
		return nil, false, false, err
	}
	if !found {
		// No manifest yet means "never initialized", not an error: the first
		// selection write creates it (ensureProjectRuntimeManifest).
		return nil, false, false, nil
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return nil, false, false, fmt.Errorf("decode product manifest: %w", err)
	}
	capabilities, _ := manifest["capabilities"].(map[string]interface{})
	chosenOnly, _ := capabilities["chosen_vault_apps_only"].(bool)
	value, initialized := capabilities["selected_servers"]
	if !initialized {
		return nil, chosenOnly, false, nil
	}
	items, ok := value.([]interface{})
	if !ok {
		return nil, chosenOnly, true, fmt.Errorf("product capabilities.selected_servers must be an array")
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		if name, ok := item.(string); ok {
			names = appendUniqueStrings(names, strings.TrimSpace(name))
		}
	}
	sort.Strings(names)
	return savedAppSelection(names), chosenOnly, true, nil
}

// updateProductSelectedServers saves a Crew's app list; chosenOnly, when given, sets "Use all my apps
// automatically" off (true) or on (false).
func updateProductSelectedServers(ctx context.Context, profileID, workspacePath string, mutate func([]string) []string, chosenOnly ...bool) error {
	raw, manifestPath, err := ensureProjectRuntimeManifest(ctx, profileID, workspacePath)
	if err != nil {
		return err
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return fmt.Errorf("decode product manifest: %w", err)
	}
	capabilities, _ := manifest["capabilities"].(map[string]interface{})
	if capabilities == nil {
		capabilities = map[string]interface{}{}
	}
	current := []string{}
	if items, ok := capabilities["selected_servers"].([]interface{}); ok {
		for _, item := range items {
			if name, ok := item.(string); ok {
				current = appendUniqueStrings(current, strings.TrimSpace(name))
			}
		}
	}
	next := savedAppSelection(mutate(savedAppSelection(current)))
	canonical := make([]string, 0, len(next))
	for _, name := range next {
		canonical = appendUniqueStrings(canonical, strings.TrimSpace(name))
	}
	sort.Strings(canonical)
	capabilities["selected_servers"] = canonical
	if len(chosenOnly) > 0 {
		if chosenOnly[0] {
			capabilities["chosen_vault_apps_only"] = true
		} else {
			delete(capabilities, "chosen_vault_apps_only")
		}
	}
	manifest["capabilities"] = capabilities
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode product manifest: %w", err)
	}
	return writeFileToWorkspace(ctx, manifestPath, string(encoded)+"\n")
}

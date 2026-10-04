package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

var knowledgebaseIntegrationMu sync.Mutex

type knowledgeProject struct {
	Workspace, Path, ID, Kind, Version string
	Raw                                map[string]json.RawMessage
	Bindings                           []knowledgebase.Binding
	Audience, Owners, Reserved         []string
	Shared                             bool
}

func knowledgeHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func knowledgeProjectLoad(ctx context.Context, userID, workspace string, requireOwner bool) (*knowledgeProject, error) {
	kind, root := common.ClassifySessionWorkspace(userID, workspace)
	if kind == common.SessionWorkspaceUnknown || isCodeProjectPath(workspace) {
		return nil, fmt.Errorf("an owned workflow or Crew workspace is required")
	}
	if kind == common.SessionWorkspaceCrewProject {
		owner := userID
		if ref, ok := workspaceref.Parse(workspace); ok && ref.HasOwner() {
			owner = ref.Owner()
		}
		root = workspaceref.MustParse(root).PhysicalKeepOwner(owner)
	}
	base, err := filepath.EvalSymlinks(stepworkflow.GetPromptDocsRoot())
	if err != nil {
		return nil, err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(base, filepath.FromSlash(root), "workflow.json"))
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("workspace is outside the installation")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p := &knowledgeProject{Workspace: root, Path: path, Kind: string(kind)}
	if err = json.Unmarshal(b, &p.Raw); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(p.Raw["id"], &p.ID); err != nil || p.ID == "" {
		return nil, fmt.Errorf("workspace requires a stable ID")
	}
	if value := p.Raw["shared_knowledgebase"]; value != nil {
		if err = json.Unmarshal(value, &p.Bindings); err != nil {
			return nil, err
		}
	}
	var mode string
	_ = json.Unmarshal(p.Raw["knowledgebase_mode"], &mode)
	p.Shared = mode == "shared"
	if kind == common.SessionWorkspaceWorkflow {
		var manifest WorkflowManifest
		if err = json.Unmarshal(b, &manifest); err != nil {
			return nil, err
		}
		p.Owners = append([]string{}, manifest.effectiveOwners()...)
		p.Audience = append(append(append([]string{}, p.Owners...), manifest.effectiveEditors()...), manifest.effectiveReaders()...)
		for _, source := range manifest.KnowledgebaseSources {
			p.Reserved = append(p.Reserved, source.Alias)
		}
		for _, attachment := range manifest.CrewAttachments {
			p.Reserved = append(p.Reserved, attachment.Alias)
		}
	} else {
		owner, ok := crewProjectOwnerID(root)
		if !ok {
			return nil, fmt.Errorf("Crew ownership is unavailable")
		}
		productData, err := os.ReadFile(filepath.Join(filepath.Dir(path), "product.json"))
		if err != nil {
			return nil, fmt.Errorf("Crew stable identity is unavailable: %w", err)
		}
		var product productProjectManifest
		if err := json.Unmarshal(productData, &product); err != nil || product.ID != p.ID {
			return nil, fmt.Errorf("Crew runtime identity does not match its product manifest")
		}
		p.Owners = []string{owner}
		p.Audience = append([]string{}, p.Owners...)
		if projectSharingEnabled() {
			directory, err := loadUserDirectory()
			if err != nil || directory == nil {
				return nil, fmt.Errorf("Crew audience is unavailable")
			}
			for _, user := range directory.Users {
				if !user.Disabled && userAllowedProduct(&UserClaims{UserID: user.ID, Username: user.Username}, "work") {
					p.Audience = append(p.Audience, user.ID)
				}
			}
		}
	}
	if len(p.Owners) == 0 {
		return nil, fmt.Errorf("account-visible workspaces must be claimed before sharing knowledge")
	}
	if requireOwner && !containsID(p.Owners, userID) {
		return nil, &knowledgebase.Error{Code: "FORBIDDEN", Message: "Only a workspace owner can configure or migrate its knowledge."}
	}
	if err = knowledgebase.ValidateBindings(p.Bindings, p.Reserved); err != nil {
		return nil, err
	}
	p.Audience = common.DeduplicateStrings(p.Audience)
	sort.Strings(p.Audience)
	p.Version = knowledgeHash(p.Raw)
	return p, nil
}

func knowledgebaseRuntimePolicy(ctx context.Context, userID string, principal *knowledgebase.Principal, args map[string]any) error {
	session, _ := ctx.Value(common.ChatSessionIDKey).(string)
	if session == "" {
		session, _ = ctx.Value(common.WorkflowSessionIDKey).(string)
	}
	cfg := common.GetSessionShellConfig(session)
	if cfg == nil {
		return nil
	}
	workspace := cfg.WorkflowPath
	if workspace == "" {
		workspace = cfg.WorkingDir
	}
	kind, _ := common.ClassifySessionWorkspace(userID, workspace)
	if kind == common.SessionWorkspaceUnknown || isCodeProjectPath(workspace) {
		return nil
	}
	project, err := knowledgeProjectLoad(ctx, userID, workspace, false)
	if err != nil {
		return err
	}
	originalBindingsHash := knowledgeHash(project.Bindings)
	policy := &knowledgebase.BindingPolicy{Bindings: append([]knowledgebase.Binding{}, project.Bindings...), Audience: project.Audience}
	if alias, _ := args["binding_alias"].(string); alias != "" {
		policy.Bindings = nil
		for _, b := range project.Bindings {
			if b.Alias == alias {
				policy.Bindings = append(policy.Bindings, b)
			}
		}
		if len(policy.Bindings) == 0 {
			return fmt.Errorf("shared binding alias is unavailable")
		}
	}
	if cfg.ReadOnlyAccess || cfg.CrewReader || cfg.Env["SHARED_KB_STEP_ACCESS"] == "read" {
		for i := range policy.Bindings {
			policy.Bindings[i].Access = "read"
		}
	}
	if cfg.Env["WORKFLOW_KB_ACCESS"] == "none" || cfg.Env["SHARED_KB_STEP_ACCESS"] == "none" {
		policy.Bindings = nil
	}
	principal.BindingPolicy = policy
	recheck := principal.Recheck
	principal.Recheck = func(ctx context.Context) error {
		if recheck != nil {
			if err := recheck(ctx); err != nil {
				return err
			}
		}
		fresh, err := knowledgeProjectLoad(ctx, userID, project.Workspace, false)
		if err != nil {
			return err
		}
		if fresh.ID != project.ID || knowledgeHash(fresh.Bindings) != originalBindingsHash || knowledgeHash(fresh.Audience) != knowledgeHash(policy.Audience) {
			return &knowledgebase.Error{Code: "FORBIDDEN", Message: "Shared Knowledge Base bindings or audience changed; retry under the current configuration."}
		}
		return nil
	}
	// Give folder-scoped operations a safe default. Entry/receipt locators are
	// still checked against every binding by the domain authorizer.
	action, _ := args["action"].(string)
	needsFolder := action == "folders" || action == "entries" || action == "search" || action == "status" || action == "inspect" || action == "create" || action == "create_folder"
	if needsFolder && args["folder_id"] == nil && args["folder_path"] == nil {
		alias, _ := args["binding_alias"].(string)
		var selected *knowledgebase.Binding
		for i := range policy.Bindings {
			if alias == policy.Bindings[i].Alias || alias == "" && len(policy.Bindings) == 1 {
				selected = &policy.Bindings[i]
			}
		}
		if selected == nil {
			return &knowledgebase.Error{Code: "INVALID_ARGUMENT", Message: "Select a configured binding_alias or explicit folder scope."}
		}
		args["folder_id"] = selected.FolderID
		delete(args, "binding_alias")
	}
	return nil
}

func knowledgebaseDispatch(ctx context.Context, service *knowledgebase.Service, p knowledgebase.Principal, userID, tool string, args map[string]any) (any, error) {
	action, _ := args["action"].(string)
	if tool == "manage_knowledgebase_access" && (action == "inspect_project" || action == "bind_project" || action == "unbind_project") {
		if err := knowledgebase.ValidateToolArguments(tool, args); err != nil {
			return nil, err
		}
		if !p.AccessOnly {
			return nil, &knowledgebase.Error{Code: "FORBIDDEN", Message: "Only the access builder can configure project bindings."}
		}
		if action == "inspect_project" {
			project, err := knowledgeProjectLoad(ctx, p.IdentityID, args["workspace_path"].(string), true)
			if err != nil {
				return nil, err
			}
			return map[string]any{"manifest_version": project.Version, "shared_knowledgebase": project.Bindings, "audience": project.Audience, "knowledgebase_mode": project.Shared}, nil
		}
		if _, err := service.ReserveIntegrationRequest(ctx, p, tool, args); err != nil {
			return nil, err
		}
		return knowledgebaseBindProject(ctx, service, p, args)
	}
	if tool == "update_knowledgebase" && strings.HasPrefix(action, "migration_") {
		if err := knowledgebase.ValidateToolArguments(tool, args); err != nil {
			return nil, err
		}
		if p.AccessOnly {
			return nil, &knowledgebase.Error{Code: "FORBIDDEN", Message: "The access builder cannot import content."}
		}
		if _, err := service.ReserveIntegrationRequest(ctx, p, tool, args); err != nil {
			return nil, err
		}
		return knowledgeMigration(ctx, service, p, args)
	}
	copy := make(map[string]any, len(args))
	for key, value := range args {
		copy[key] = value
	}
	if !p.AccessOnly {
		if err := knowledgebaseRuntimePolicy(ctx, userID, &p, copy); err != nil {
			return nil, err
		}
	}
	return service.CallTool(ctx, p, tool, copy)
}

func knowledgebaseBindProject(ctx context.Context, service *knowledgebase.Service, p knowledgebase.Principal, args map[string]any) (any, error) {
	knowledgebaseIntegrationMu.Lock()
	defer knowledgebaseIntegrationMu.Unlock()
	unlock, err := knowledgeIntegrationLock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	key, err := service.ReserveIntegrationRequest(ctx, p, "manage_knowledgebase_access", args)
	if err != nil {
		return nil, err
	}
	root, err := knowledgeIntegrationRoot()
	if err != nil {
		return nil, err
	}
	intentPath := filepath.Join(root, "binding_"+key+".json")
	var intent struct {
		Hash   string
		Before string
		After  string
		Result map[string]any
	}
	if data, readErr := os.ReadFile(intentPath); readErr == nil {
		if err := json.Unmarshal(data, &intent); err != nil {
			return nil, err
		}
		if intent.Hash != knowledgeHash(args) {
			return nil, fmt.Errorf("request ID reuse")
		}
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	}
	workspace, _ := args["workspace_path"].(string)
	project, err := knowledgeProjectLoad(ctx, p.IdentityID, workspace, true)
	if err != nil {
		return nil, err
	}
	if intent.Hash != "" && project.Version == intent.After {
		return intent.Result, nil
	}
	if p.Recheck != nil {
		if err := p.Recheck(ctx); err != nil {
			return nil, err
		}
	}
	if args["expected_manifest_version"] != project.Version {
		return nil, &knowledgebase.Error{Code: "VERSION_CONFLICT", Message: "The project configuration changed; inspect and retry."}
	}
	alias, _ := args["alias"].(string)
	bindings := []knowledgebase.Binding{}
	for _, b := range project.Bindings {
		if b.Alias != alias {
			bindings = append(bindings, b)
		}
	}
	if args["action"] == "bind_project" {
		folder, _ := args["folder_id"].(string)
		access, _ := args["access"].(string)
		binding := knowledgebase.Binding{Alias: alias, FolderID: folder, Access: access}
		if _, err := service.ResolveBinding(ctx, p, binding, project.Audience); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if project.Shared && len(bindings) == 0 {
		return nil, fmt.Errorf("rollback migration before removing the last shared binding")
	}
	if args["replace_legacy_alias"] == true && args["action"] == "bind_project" {
		var attachments []struct {
			Alias string `json:"alias"`
		}
		if raw := project.Raw["crew_attachments"]; raw != nil {
			if err := json.Unmarshal(raw, &attachments); err != nil {
				return nil, err
			}
		}
		for _, attachment := range attachments {
			if attachment.Alias == alias {
				return nil, fmt.Errorf("Crew workspace attachment aliases cannot be replaced by a knowledge binding")
			}
		}
		var sources []map[string]any
		if raw := project.Raw["knowledgebase_sources"]; raw != nil {
			if err := json.Unmarshal(raw, &sources); err != nil {
				return nil, err
			}
		}
		kept := sources[:0]
		removed := false
		for _, source := range sources {
			if source["alias"] == alias {
				removed = true
				continue
			}
			kept = append(kept, source)
		}
		if !removed {
			return nil, fmt.Errorf("no legacy knowledge source has that alias")
		}
		project.Raw["knowledgebase_sources"], _ = json.Marshal(kept)
		reserved := []string{}
		for _, a := range project.Reserved {
			if a != alias {
				reserved = append(reserved, a)
			}
		}
		project.Reserved = reserved
	}
	if err := knowledgebase.ValidateBindings(bindings, project.Reserved); err != nil {
		return nil, err
	}
	value, _ := json.Marshal(bindings)
	project.Raw["shared_knowledgebase"] = value
	intent.Hash = knowledgeHash(args)
	intent.Before = args["expected_manifest_version"].(string)
	intent.After = knowledgeHash(project.Raw)
	intent.Result = map[string]any{"shared_knowledgebase": bindings, "manifest_version": intent.After}
	if err := knowledgeSavePrivate(intentPath, intent); err != nil {
		return nil, err
	}
	if err := knowledgeProjectSave(project, args["expected_manifest_version"].(string)); err != nil {
		return nil, err
	}
	return intent.Result, nil
}

func knowledgeProjectSave(project *knowledgeProject, expected string) error {
	unlock, err := knowledgeManifestLock(project.Path)
	if err != nil {
		return err
	}
	defer unlock()
	b, err := os.ReadFile(project.Path)
	if err != nil {
		return err
	}
	var current map[string]json.RawMessage
	if err := json.Unmarshal(b, &current); err != nil {
		return err
	}
	if knowledgeHash(current) != expected {
		return &knowledgebase.Error{Code: "VERSION_CONFLICT", Message: "The project configuration changed."}
	}
	data, err := json.MarshalIndent(project.Raw, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(project.Path), ".kb-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), project.Path); err != nil {
		return err
	}
	invalidateWorkflowManifestIndex()
	return knowledgeSyncDirectory(filepath.Dir(project.Path))
}

// Include the knowledge cutover in retained CLI policy keys, so idle native
// processes relaunch with current prompts and filesystem deny paths.
func knowledgeRuntimeConfigKey(workspace string) string {
	if workspace == "" {
		return ""
	}
	base, err := filepath.EvalSymlinks(stepworkflow.GetPromptDocsRoot())
	if err != nil {
		return ""
	}
	target, err := filepath.EvalSymlinks(filepath.Join(base, workspace, "workflow.json"))
	if err != nil || !workflowkb.Within(base, target) {
		return ""
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return ""
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil || raw == nil {
		return "invalid-knowledge-config"
	}
	blocked := workflowkb.LegacyKnowledgeBlocks(base, workspace)
	if raw["shared_knowledgebase"] == nil && raw["knowledgebase_mode"] == nil && len(blocked) == 0 {
		return ""
	}
	return knowledgeHash([]any{raw["shared_knowledgebase"], raw["knowledgebase_mode"], blocked})
}

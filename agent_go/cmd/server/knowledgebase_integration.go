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

// Only the authenticated external MCP transport can authorize owner migrations.
type knowledgebaseMigrationAuthorityKey struct{}

type knowledgeProject struct {
	Workspace, Path, ID, Kind, Version string
	Raw                                map[string]json.RawMessage
	Audience, Owners, Reserved         []string
	Shared                             bool
	// Access is the explicit Brain access setting: "off", "read" or "write"; empty when never set. A legacy "folders"
	// value is read as "write" (folder bindings were removed, PLAT-628).
	Access string
}

// BrainMode is the project's effective Brain access: off, read or write, always limited by the owner's (and every
// output reader's) folder roles. Unset, and the removed "folders" mode, are "write" (owner decisions 2026-10-05 and
// PLAT-628: Brain is open by default; each step's description says which folders it reads and writes).
func (p *knowledgeProject) BrainMode() string {
	switch p.Access {
	case "off", "read":
		return p.Access
	}
	return "write"
}

// knowledgeAudienceAdmins lists the audience members who are enabled administrators: Brain treats them as Owner of every
// folder, so they never narrow what a project may use.
func knowledgeAudienceAdmins(audience []string) []string {
	var admins []string
	for _, id := range audience {
		if acc := userAccessForClaims(&UserClaims{UserID: id}); acc.Admin && !acc.Disabled {
			admins = append(admins, id)
		}
	}
	return admins
}

func knowledgeHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func knowledgeProjectLoad(ctx context.Context, userID, workspace string, requireOwner bool) (*knowledgeProject, error) {
	kind, root := common.ClassifySessionWorkspace(userID, workspace)
	if kind == common.SessionWorkspaceUnknown {
		return nil, fmt.Errorf("an owned workflow, Crew or Code workspace is required")
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
	var mode string
	_ = json.Unmarshal(p.Raw["knowledgebase_mode"], &mode)
	p.Shared = mode == "shared"
	if value := p.Raw["brain_access"]; value != nil {
		if err = json.Unmarshal(value, &p.Access); err != nil || p.Access != "" && p.Access != "off" && p.Access != "read" && p.Access != "write" && p.Access != "folders" {
			return nil, fmt.Errorf("brain_access must be off, read, write or folders")
		}
	}
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
		// A Code project is private to its owner, so its owner is its whole audience; a Crew can be shared.
		if projectSharingEnabled() && !isCodeProjectPath(root) {
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
	p.Audience = common.DeduplicateStrings(p.Audience)
	sort.Strings(p.Audience)
	p.Version = knowledgeHash(p.Raw)
	return p, nil
}

// Project authoring scopes are independent of KB content scopes. An owned
// project outside the token's workflow/Crew allowlist remains unavailable.
func knowledgeProjectConnectionCheck(ctx context.Context, project *knowledgeProject) error {
	if claims := GetUserFromContext(ctx); claims != nil {
		product := "work"
		if isCodeProjectPath(project.Workspace) {
			product = "code"
		}
		if project.Kind == string(common.SessionWorkspaceWorkflow) {
			product = "agentworks"
			var kind string
			_ = json.Unmarshal(project.Raw["kind"], &kind)
			if kind == "relay" {
				product = "relays"
			}
			if !userAllowedWorkflowID(claims, project.ID) {
				return &knowledgebase.Error{Code: "FORBIDDEN", Message: "This account cannot access the source workflow."}
			}
		}
		if !userAllowedProduct(claims, product) {
			return &knowledgebase.Error{Code: "FORBIDDEN", Message: "The source project product is unavailable to this account."}
		}
	}
	if claims := GetUserFromContext(ctx); claims != nil && claims.AccessToken != nil {
		token := claims.AccessToken
		allowed := token.BuilderAccess() && token.AllowsWorkflow(project.ID)
		if project.Kind != string(common.SessionWorkspaceWorkflow) {
			allowed = token.Allows("crews:read") && token.Allows("crews:write") && token.AllowsCrew(project.ID)
		}
		// A Code project is private and has no connection scope: it is set up in the app or its own Code chat.
		if isCodeProjectPath(project.Workspace) {
			allowed = false
		}
		if !allowed {
			return &knowledgebase.Error{Code: "FORBIDDEN", Message: "This connection does not authorize this project."}
		}
	}
	return nil
}

func knowledgebaseRuntimePolicy(ctx context.Context, userID string, principal *knowledgebase.Principal, args map[string]any) error {
	session, _ := ctx.Value(common.ChatSessionIDKey).(string)
	if session == "" {
		session, _ = ctx.Value(common.WorkflowSessionIDKey).(string)
	}
	cfg := common.GetSessionShellConfig(session)
	if cfg == nil {
		if session != "" {
			return &knowledgebase.Error{Code: "FORBIDDEN", Message: "Session knowledge policy is unavailable."}
		}
		return nil
	}
	workspace := cfg.WorkflowPath
	if workspace == "" {
		workspace = cfg.WorkingDir
	}
	// The person's own Brain chat acts with their own folder roles, exactly like their MCP connection (PLAT-618).
	if isBrainChatWorkspace(userID, workspace) {
		return nil
	}
	kind, _ := common.ClassifySessionWorkspace(userID, workspace)
	if kind == common.SessionWorkspaceUnknown {
		return &knowledgebase.Error{Code: "FORBIDDEN", Message: "A workflow, Crew or Code session is required."}
	}
	project, err := knowledgeProjectLoad(ctx, userID, workspace, false)
	if err != nil {
		return err
	}
	mode := project.BrainMode()
	if mode == "off" {
		return &knowledgebase.Error{Code: "FORBIDDEN", Message: "Brain access is off for this project."}
	}
	originalModeHash := knowledgeHash(mode)
	policy := &knowledgebase.BindingPolicy{Audience: project.Audience, Admins: knowledgeAudienceAdmins(project.Audience), ReadAll: mode == "read", WriteAll: mode == "write"}
	delete(args, "binding_alias") // folder bindings were removed (PLAT-628); a stale alias selects nothing
	if cfg.ReadOnlyAccess || cfg.CrewReader || cfg.Env["SHARED_KB_STEP_ACCESS"] == "read" {
		if policy.WriteAll {
			policy.WriteAll, policy.ReadAll = false, true
		}
	}
	// A step (or session) with no Brain access gets none in any mode, including Read and Read & write.
	if cfg.Env["WORKFLOW_KB_ACCESS"] == "none" || cfg.Env["SHARED_KB_STEP_ACCESS"] == "none" {
		policy.ReadAll, policy.WriteAll = false, false
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
		if fresh.ID != project.ID || knowledgeHash(fresh.BrainMode()) != originalModeHash || knowledgeHash(fresh.Audience) != knowledgeHash(policy.Audience) {
			return &knowledgebase.Error{Code: "FORBIDDEN", Message: "The project's Brain access or audience changed; retry under the current configuration."}
		}
		return nil
	}
	return nil
}

func knowledgebaseDispatch(ctx context.Context, service *knowledgebase.Service, p knowledgebase.Principal, userID, tool string, args map[string]any) (any, error) {
	tool = knowledgebase.CanonicalToolName(tool)
	action, _ := args["action"].(string)
	if tool == knowledgebase.ToolAccess && (action == "inspect_project" || action == "set_project_access") {
		if err := knowledgebase.ValidateToolArguments(tool, args); err != nil {
			return nil, err
		}
		if !p.AccessOnly {
			return nil, &knowledgebase.Error{Code: "FORBIDDEN", Message: "An authorized project setup connection is required."}
		}
		if action == "inspect_project" {
			project, err := knowledgeProjectLoad(ctx, p.IdentityID, args["workspace_path"].(string), true)
			if err != nil {
				return nil, err
			}
			if err := knowledgeProjectConnectionCheck(ctx, project); err != nil {
				return nil, err
			}
			if p.Recheck != nil {
				if err := p.Recheck(ctx); err != nil {
					return nil, err
				}
			}
			return map[string]any{"manifest_version": project.Version, "brain_access": project.BrainMode(), "audience": project.Audience, "knowledgebase_mode": project.Shared}, nil
		}
		if _, err := service.ReserveIntegrationRequest(ctx, p, tool, args); err != nil {
			return nil, err
		}
		return knowledgebaseBindProject(ctx, service, p, args)
	}
	if tool == knowledgebase.ToolUpdate && strings.HasPrefix(action, "migration_") {
		if authorized, _ := ctx.Value(knowledgebaseMigrationAuthorityKey{}).(bool); !authorized {
			return nil, &knowledgebase.Error{Code: "FORBIDDEN", Message: "Migration requires an explicitly authorized external owner connection."}
		}
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
	key, err := service.ReserveIntegrationRequest(ctx, p, knowledgebase.ToolAccess, args)
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
	if err := knowledgeProjectConnectionCheck(ctx, project); err != nil {
		return nil, err
	}
	if p.Recheck != nil {
		if err := p.Recheck(ctx); err != nil {
			return nil, err
		}
	}
	if intent.Hash != "" && project.Version == intent.After {
		return intent.Result, nil
	}
	if args["expected_manifest_version"] != project.Version {
		return nil, &knowledgebase.Error{Code: "VERSION_CONFLICT", Message: "The project configuration changed; inspect and retry."}
	}
	if args["action"] != "set_project_access" {
		return nil, &knowledgebase.Error{Code: "INVALID_ARGUMENT", Message: "Folder bindings were removed: set the project's Brain access, and say in each step's description which folders it reads and writes."}
	}
	return knowledgebaseSetProjectAccess(project, args, intentPath)
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
	if raw["shared_knowledgebase"] == nil && raw["knowledgebase_mode"] == nil && raw["brain_access"] == nil && len(blocked) == 0 {
		return ""
	}
	return knowledgeHash([]any{raw["shared_knowledgebase"], raw["knowledgebase_mode"], raw["brain_access"], blocked})
}

// knowledgebaseSetProjectAccess saves the project's Brain access setting under the same manifest version check and
// request journal. No mode grants anything: the person's and every output reader's folder roles still decide, and
// "read" cannot write.
func knowledgebaseSetProjectAccess(project *knowledgeProject, args map[string]any, intentPath string) (any, error) {
	mode, _ := args["mode"].(string)
	if mode != "off" && mode != "read" && mode != "write" {
		return nil, &knowledgebase.Error{Code: "INVALID_ARGUMENT", Message: "mode must be off, read or write."}
	}
	// A migrated project keeps writing its own knowledge in Brain: not off or read-only until the migration is rolled back.
	if project.Shared && mode != "write" {
		return nil, fmt.Errorf("this project's knowledge lives in Brain; roll back the migration before turning Brain access off or read-only")
	}
	if (mode == "read" || mode == "write") && len(project.Audience) == 0 {
		return nil, &knowledgebase.Error{Code: "FORBIDDEN", Message: "Brain read or write access requires an explicit output audience."}
	}
	value, _ := json.Marshal(mode)
	project.Raw["brain_access"] = value
	var intent struct {
		Hash   string
		Before string
		After  string
		Result map[string]any
	}
	intent.Hash = knowledgeHash(args)
	intent.Before = args["expected_manifest_version"].(string)
	intent.After = knowledgeHash(project.Raw)
	intent.Result = map[string]any{"brain_access": mode, "manifest_version": intent.After}
	if err := knowledgeSavePrivate(intentPath, intent); err != nil {
		return nil, err
	}
	if err := knowledgeProjectSave(project, args["expected_manifest_version"].(string)); err != nil {
		return nil, err
	}
	return intent.Result, nil
}

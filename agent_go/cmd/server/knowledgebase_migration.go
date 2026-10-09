package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"
	"golang.org/x/sys/unix"
)

const sharedKnowledgebaseContract = "shared-kb-v1"

type knowledgeImportFile struct {
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	EntryID string `json:"entry_id,omitempty"`
	Version string `json:"version,omitempty"`
}
type knowledgeMigrationReceipt struct {
	Consumers       []string                   `json:"legacy_consumers"`
	ID              string                     `json:"migration_id"`
	RequiredOwners  []string                   `json:"required_folder_owners"`
	RequiredReaders []string                   `json:"required_folder_readers"`
	Owner           string                     `json:"owner"`
	Workspace       string                     `json:"workspace_path"`
	ProjectID       string                     `json:"project_id"`
	PreviewHash     string                     `json:"preview_arguments_hash"`
	ManifestVersion string                     `json:"manifest_version"`
	Original        map[string]json.RawMessage `json:"original_knowledge_configuration"`
	Binding         knowledgebase.Binding      `json:"binding"`
	Destination     string                     `json:"destination"`
	SourceHash      string                     `json:"source_hash"`
	Files           []knowledgeImportFile      `json:"files"`
	Skipped         []string                   `json:"skipped_files"`
	Folders         map[string]string          `json:"created_folders"`
	State           string                     `json:"state"`
	ActiveVersion   string                     `json:"active_manifest_version,omitempty"`
	CutoverTime     string                     `json:"cutover_at,omitempty"`
}

func knowledgeIntegrationRoot() (string, error) {
	cfg, err := knowledgebaseConfig()
	if err != nil {
		return "", err
	}
	root := filepath.Join(cfg.Root, "private", "integration")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return root, nil
}
func knowledgeIntegrationLock() (func(), error) {
	root, err := knowledgeIntegrationRoot()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, "migration.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, &knowledgebase.Error{Code: "BACKUP_BUSY", Message: "Another integration operation is running.", Retryable: true}
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
func knowledgeMigrationPath(id string) (string, error) {
	if !strings.HasPrefix(id, "migration_") || len(id) != len("migration_")+64 || strings.ContainsAny(id, "/\\.") {
		return "", fmt.Errorf("invalid migration ID")
	}
	root, err := knowledgeIntegrationRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, id+".json"), nil
}
func knowledgeSavePrivate(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".integration-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
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
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return knowledgeSyncDirectory(filepath.Dir(path))
}
func knowledgeSaveMigration(r *knowledgeMigrationReceipt) error {
	path, err := knowledgeMigrationPath(r.ID)
	if err != nil {
		return err
	}
	return knowledgeSavePrivate(path, r)
}

func knowledgeReadSource(root, relative string) ([]byte, error) {
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, "../") {
		return nil, fmt.Errorf("invalid source-relative path")
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("source root must be canonical and absolute")
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { unix.Close(fd) }()
	parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(filepath.Join(root, relative)), "/"), "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("invalid source-relative path")
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(fd, part, flags, 0)
		if err != nil {
			return nil, err
		}
		unix.Close(fd)
		fd = next
	}
	f := os.NewFile(uintptr(fd), relative)
	defer f.Close()
	fd = -1
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("source must be a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 10*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 10*1024*1024 {
		return nil, fmt.Errorf("source file exceeds 10 MiB")
	}
	return b, nil
}
func knowledgeReadMigration(id, owner string) (*knowledgeMigrationReceipt, error) {
	path, err := knowledgeMigrationPath(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, &knowledgebase.Error{Code: "NOT_FOUND", Message: "Migration not found."}
	}
	var r knowledgeMigrationReceipt
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Owner != owner {
		return nil, &knowledgebase.Error{Code: "NOT_FOUND", Message: "Migration not found."}
	}
	return &r, nil
}

// Inventory only an owned workspace's knowledgebase directory. Neither the
// caller nor a symlink can choose a host path; learnings are never traversed.
func knowledgeMigrationInventory(project *knowledgeProject) ([]knowledgeImportFile, []string, string, error) {
	root := filepath.Join(filepath.Dir(project.Path), "knowledgebase")
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, "", fmt.Errorf("the workspace requires a real local knowledgebase directory")
	}
	files := []knowledgeImportFile{}
	skipped := []string{}
	all := map[string]string{}
	var total int64
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		count++
		if count > 10000 {
			return fmt.Errorf("migration exceeds 10000 inventory paths")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			skipped = append(skipped, rel+": symlink")
			all[rel] = "symlink"
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("knowledge inventory contains a non-regular file")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > 10*1024*1024 {
			skipped = append(skipped, rel+": exceeds 10 MiB")
			all[rel] = fmt.Sprintf("oversize:%d:%d", info.Size(), info.ModTime().UnixNano())
			return nil
		}
		total += info.Size()
		if total > 100*1024*1024 {
			return fmt.Errorf("migration inventory exceeds 100 MiB; split the source before importing")
		}
		b, err := knowledgeReadSource(root, rel)
		if err != nil {
			return err
		}
		all[rel] = knowledgeHash(string(b))
		if err := knowledgebase.ValidateImportPath(rel); err != nil {
			skipped = append(skipped, rel+": unsupported name or format")
			return nil
		}
		text, err := knowledgebase.NormalizeImportText(string(b))
		if err != nil {
			skipped = append(skipped, rel+": binary or invalid UTF-8")
			return nil
		}
		files = append(files, knowledgeImportFile{Path: rel, Hash: knowledgeHash(text)})
		if len(files) > 10000 {
			return fmt.Errorf("migration exceeds 10000 Markdown entries")
		}
		return nil
	})
	if err != nil {
		return nil, nil, "", err
	}
	sort.Strings(skipped)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, skipped, knowledgeHash(all), nil
}

func knowledgeMigration(ctx context.Context, service *knowledgebase.Service, p knowledgebase.Principal, args map[string]any) (any, error) {
	knowledgebaseIntegrationMu.Lock()
	defer knowledgebaseIntegrationMu.Unlock()
	unlock, err := knowledgeIntegrationLock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	workspace, _ := args["workspace_path"].(string)
	project, err := knowledgeProjectLoad(ctx, p.IdentityID, workspace, true)
	if err != nil {
		return nil, err
	}
	if p.Recheck != nil {
		if err := p.Recheck(ctx); err != nil {
			return nil, err
		}
	}
	action := args["action"].(string)
	if claims := GetUserFromContext(ctx); claims != nil && claims.AccessToken != nil {
		token := claims.AccessToken
		allowed := token.Allows("files:read") && token.AllowsWorkflow(project.ID) && (token.Allows("workflows:read") || token.Allows("runs:execute"))
		allowed = allowed && token.BuilderAccess()
		if project.Kind != "workflow" {
			allowed = token.Allows("crews:read") && token.AllowsCrew(project.ID)
			allowed = allowed && token.Allows("crews:write")
		}
		if !allowed {
			return nil, &knowledgebase.Error{Code: "FORBIDDEN", Message: "This connection does not authorize migration of the source project."}
		}
	}
	if action == "migration_preview" {
		return knowledgeMigrationPreview(ctx, service, p, project, args)
	}
	id, _ := args["migration_id"].(string)
	receipt, err := knowledgeReadMigration(id, p.IdentityID)
	if err != nil {
		return nil, err
	}
	if project.ID != receipt.ProjectID || project.Workspace != receipt.Workspace {
		return nil, fmt.Errorf("migration belongs to another workspace")
	}
	if action == "migration_rollback" {
		return knowledgeMigrationRollback(ctx, service, p, project, receipt)
	}
	if _, err := service.ResolveBinding(ctx, p, receipt.Binding, project.Audience); err != nil {
		return nil, err
	}
	for _, owner := range project.Owners {
		if err := service.RequireIdentityFolderRole(ctx, owner, receipt.Binding.FolderID, "Owner"); err != nil {
			return nil, err
		}
	}
	if action == "migration_cutover" {
		return knowledgeMigrationCutover(ctx, service, p, project, receipt)
	}
	if receipt.State == "ACTIVE" {
		return receipt, nil
	}
	if receipt.State == "ROLLED_BACK" {
		return nil, fmt.Errorf("rolled-back migrations require a fresh preview and destination")
	}
	if project.Version != receipt.ManifestVersion {
		return nil, &knowledgebase.Error{Code: "VERSION_CONFLICT", Message: "Project configuration changed since migration preview."}
	}
	_, _, sourceHash, err := knowledgeMigrationInventory(project)
	if err != nil {
		return nil, err
	}
	if sourceHash != receipt.SourceHash {
		return nil, fmt.Errorf("source knowledge changed; prepare a new migration preview")
	}
	if len(receipt.Skipped) > 0 && args["allow_skipped_files"] != true {
		return nil, fmt.Errorf("review skipped files and explicitly set allow_skipped_files")
	}
	receipt.State = "IMPORTING"
	if err := knowledgeSaveMigration(receipt); err != nil {
		return nil, err
	}
	for i := range receipt.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file := &receipt.Files[i]
		if file.EntryID != "" {
			if err := knowledgeVerifyImported(ctx, service, p, *file); err != nil {
				return nil, err
			}
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(file.Path))
		parent := receipt.Destination
		if dir != "." {
			for _, part := range strings.Split(dir, "/") {
				child := strings.Trim(parent+"/"+part, "/")
				if _, ok := receipt.Folders[child]; !ok {
					result, err := service.CallTool(ctx, p, knowledgebase.ToolUpdate, map[string]any{"action": "create_folder", "folder_path": parent, "name": part, "request_id": "mig_f_" + knowledgeHash([]string{receipt.ID, child})})
					if err != nil {
						return nil, err
					}
					value := knowledgeMap(result)
					receipt.Folders[child], _ = value["folder_id"].(string)
					if err := knowledgeSaveMigration(receipt); err != nil {
						return nil, err
					}
				}
				parent = child
			}
		}
		b, err := knowledgeReadSource(filepath.Join(filepath.Dir(project.Path), "knowledgebase"), file.Path)
		if err != nil {
			return nil, err
		}
		text, err := knowledgebase.NormalizeImportText(string(b))
		if err != nil || knowledgeHash(text) != file.Hash {
			return nil, fmt.Errorf("source knowledge changed during import")
		}
		name := filepath.Base(file.Path)
		title := strings.TrimSuffix(name, ".md")
		result, err := service.CallTool(ctx, p, knowledgebase.ToolUpdate, map[string]any{"action": "create", "folder_path": parent, "filename": name, "type": "note", "title": title, "content": text, "request_id": "mig_e_" + knowledgeHash([]string{receipt.ID, file.Path})})
		if err != nil {
			return nil, err
		}
		value := knowledgeMap(result)
		file.EntryID, _ = value["entry_id"].(string)
		file.Version, _ = value["version"].(string)
		if err := knowledgeSaveMigration(receipt); err != nil {
			return nil, err
		}
	}
	_, _, sourceHash, err = knowledgeMigrationInventory(project)
	if err != nil || sourceHash != receipt.SourceHash {
		return nil, fmt.Errorf("source knowledge changed during import")
	}
	receipt.State = "IMPORTED"
	if err := knowledgeSaveMigration(receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

func knowledgeMap(value any) map[string]any {
	b, _ := json.Marshal(value)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func knowledgeMigrationPreview(ctx context.Context, service *knowledgebase.Service, p knowledgebase.Principal, project *knowledgeProject, args map[string]any) (any, error) {
	id := "migration_" + knowledgeHash([]string{p.IdentityID, project.Workspace, args["request_id"].(string)})
	if previous, err := knowledgeReadMigration(id, p.IdentityID); err == nil {
		if previous.PreviewHash != knowledgeHash(args) {
			return nil, &knowledgebase.Error{Code: "REQUEST_ID_REUSE", Message: "Preview request ID was reused with different arguments."}
		}
		return previous, nil
	}
	if project.Shared {
		return nil, fmt.Errorf("workspace knowledge is already shared")
	}
	binding := knowledgebase.Binding{Alias: args["alias"].(string), FolderID: args["folder_id"].(string), Access: args["access"].(string)}
	if err := knowledgebase.ValidateBindings([]knowledgebase.Binding{binding}, project.Reserved); err != nil {
		return nil, err
	}
	// Import requires Editor even when the final execution binding is read-only.
	writer := binding
	writer.Access = "write"
	destination, err := service.ResolveBinding(ctx, p, writer, project.Audience)
	if err != nil {
		return nil, err
	}
	for _, owner := range project.Owners {
		if err := service.RequireIdentityFolderRole(ctx, owner, binding.FolderID, "Owner"); err != nil {
			return nil, err
		}
	}
	listing, err := service.CallTool(ctx, p, knowledgebase.ToolBrowse, map[string]any{"action": "entries", "folder_id": binding.FolderID, "depth": 1024, "limit": 1})
	if err != nil {
		return nil, err
	}
	items, _ := knowledgeMap(listing)["items"].([]any)
	if len(items) != 0 {
		return nil, fmt.Errorf("migration requires an empty destination folder")
	}
	files, skipped, sourceHash, err := knowledgeMigrationInventory(project)
	if err != nil {
		return nil, err
	}
	original := map[string]json.RawMessage{}
	for _, key := range []string{"shared_knowledgebase", "knowledgebase_mode", "knowledgebase_migration", "knowledgebase_contract_history", "brain_access"} {
		original[key] = project.Raw[key]
	}
	receipt := &knowledgeMigrationReceipt{RequiredOwners: project.Owners, RequiredReaders: project.Audience, ID: id, Owner: p.IdentityID, Workspace: project.Workspace, ProjectID: project.ID, PreviewHash: knowledgeHash(args), ManifestVersion: project.Version, Original: original, Binding: binding, Destination: destination, SourceHash: sourceHash, Files: files, Skipped: skipped, Folders: map[string]string{}, State: "PREVIEWED"}
	receipt.Consumers, err = knowledgeMigrationConsumers(project.ID)
	if err != nil {
		return nil, err
	}
	if err := knowledgeSaveMigration(receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

// Re-scan at cutover: a preview is not authority to disable other projects.
func knowledgeMigrationConsumers(projectID string) ([]string, error) {
	var consumers []string
	registry, discoverErr := workflowkb.Discover(stepworkflow.GetPromptDocsRoot())
	if discoverErr != nil && !os.IsNotExist(discoverErr) {
		return nil, discoverErr
	}
	for _, paths := range registry {
		for _, workspace := range paths {
			consumer, err := workflowkb.ReadManifest(stepworkflow.GetPromptDocsRoot(), workspace)
			if err != nil {
				return nil, err
			}
			for _, source := range consumer.Sources {
				if source.WorkflowID == projectID {
					consumers = append(consumers, workspace+":"+source.Alias)
				}
			}
		}
	}
	sort.Strings(consumers)
	return consumers, nil
}

func knowledgeVerifyImported(ctx context.Context, service *knowledgebase.Service, p knowledgebase.Principal, file knowledgeImportFile) error {
	result, err := service.CallTool(ctx, p, knowledgebase.ToolRead, map[string]any{"action": "read", "entry_id": file.EntryID})
	if err != nil {
		return err
	}
	value := knowledgeMap(result)
	content, _ := value["content"].(string)
	if value["version"] != file.Version || knowledgeHash(content) != file.Hash {
		return &knowledgebase.Error{Code: "VERSION_CONFLICT", Message: "Imported destination content was edited; migration will not overwrite it."}
	}
	return nil
}

func knowledgeMigrationCutover(ctx context.Context, service *knowledgebase.Service, p knowledgebase.Principal, project *knowledgeProject, r *knowledgeMigrationReceipt) (any, error) {
	if runningServerAPI != nil && runningServerAPI.findRunningTrackedExecutionForWorkspaceWhere(project.Workspace, nil) != nil {
		return nil, fmt.Errorf("stop active project executions before knowledge cutover")
	}
	for _, raw := range []string{"schedules", "triggers"} {
		var entries []map[string]any
		if data := project.Raw[raw]; data != nil {
			if err := json.Unmarshal(data, &entries); err != nil {
				return nil, err
			}
			for _, entry := range entries {
				if entry["enabled"] == true {
					return nil, fmt.Errorf("pause enabled schedules and triggers before knowledge cutover")
				}
			}
		}
	}
	if r.State == "ACTIVE" {
		if project.Version != r.ActiveVersion {
			return nil, fmt.Errorf("active project changed; inspect its migration state")
		}
		return r, nil
	}
	consumers, err := knowledgeMigrationConsumers(project.ID)
	if err != nil {
		return nil, err
	}
	if len(consumers) != 0 {
		return nil, fmt.Errorf("rebind legacy consumers before cutover: %v", consumers)
	}
	if r.State != "IMPORTED" && r.State != "CUTOVER_PENDING" {
		return nil, fmt.Errorf("import must finish before cutover")
	}
	if r.State == "CUTOVER_PENDING" && project.Version == r.ActiveVersion {
		r.State = "ACTIVE"
		if err := knowledgeSaveMigration(r); err != nil {
			return nil, err
		}
		return r, nil
	}
	// Only the knowledge configuration must match the preview. The cutover requires schedules to be paused first, and
	// pausing them edits the manifest, so comparing the whole manifest made the documented order impossible (server A
	// 2026-10-06). Concurrent edits are still caught by the save below, which checks the current version.
	if project.Version != r.ManifestVersion && !knowledgeConfigMatches(project, r.Original) {
		return nil, &knowledgebase.Error{Code: "VERSION_CONFLICT", Message: "The project's knowledge configuration changed since the migration preview."}
	}
	_, _, hash, err := knowledgeMigrationInventory(project)
	if err != nil || hash != r.SourceHash {
		return nil, fmt.Errorf("source knowledge changed before cutover")
	}
	for _, file := range r.Files {
		if err := knowledgeVerifyImported(ctx, service, p, file); err != nil {
			return nil, err
		}
	}
	if err := knowledgeMigrationCheckScripts(project); err != nil {
		return nil, err
	}
	expectedVersion := project.Version
	// The imported folder is not bound to the project (bindings were removed, PLAT-628): the project gets Read & write,
	// limited by its owner's folder roles, and its steps name the folder in their descriptions (the receipt's folder).
	project.Raw["brain_access"], _ = json.Marshal("write")
	project.Raw["knowledgebase_mode"], _ = json.Marshal("shared")
	r.CutoverTime = time.Now().UTC().Format(time.RFC3339Nano)
	project.Raw["knowledgebase_migration"], _ = json.Marshal(map[string]any{"contract": sharedKnowledgebaseContract, "migration_id": r.ID, "source_hash": r.SourceHash, "folder_id": r.Binding.FolderID, "applied_at": r.CutoverTime})
	var history []map[string]any
	if prior := project.Raw["knowledgebase_contract_history"]; prior != nil {
		if err := json.Unmarshal(prior, &history); err != nil {
			return nil, err
		}
	}
	history = append(history, map[string]any{"version": sharedKnowledgebaseContract, "applied_at": r.CutoverTime, "migration_id": r.ID})
	project.Raw["knowledgebase_contract_history"], _ = json.Marshal(history)
	r.ActiveVersion = knowledgeHash(project.Raw)
	r.State = "CUTOVER_PENDING"
	if err := knowledgeSaveMigration(r); err != nil {
		return nil, err
	}
	if p.Recheck != nil {
		if err := p.Recheck(ctx); err != nil {
			return nil, err
		}
	}
	consumers, err = knowledgeMigrationConsumers(project.ID)
	if err != nil {
		return nil, err
	}
	if len(consumers) != 0 {
		return nil, fmt.Errorf("rebind legacy consumers before cutover: %v", consumers)
	}
	if err := knowledgeProjectSave(project, expectedVersion); err != nil {
		return nil, err
	}
	r.State = "ACTIVE"
	if err := knowledgeSaveMigration(r); err != nil {
		return nil, err
	}
	return r, nil
}

// Raw shell-path scripts cannot transparently become MCP consumers. Refuse
// cutover until their owner adapts them, just like other contract migrations.
func knowledgeMigrationCheckScripts(project *knowledgeProject) error {
	for _, root := range []string{"code", "planning"} {
		base := filepath.Join(filepath.Dir(project.Path), root)
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("authored code contains a symlink; review it before cutover")
			}
			if entry.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".py" && filepath.Ext(path) != ".sh" && filepath.Ext(path) != ".js" && filepath.Ext(path) != ".ts" {
				return nil
			}
			rel, err := filepath.Rel(filepath.Dir(project.Path), path)
			if err != nil {
				return err
			}
			b, err := knowledgeReadSource(filepath.Dir(project.Path), rel)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), "knowledgebase/") || strings.Contains(string(b), "WORKFLOW_KB_") {
				return fmt.Errorf("adapt legacy knowledge-path scripts to MCP before cutover")
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func knowledgeMigrationRollback(ctx context.Context, service *knowledgebase.Service, p knowledgebase.Principal, project *knowledgeProject, r *knowledgeMigrationReceipt) (any, error) {
	if runningServerAPI != nil && runningServerAPI.findRunningTrackedExecutionForWorkspaceWhere(project.Workspace, nil) != nil {
		return nil, fmt.Errorf("stop active project executions before rollback")
	}
	if r.State == "ROLLED_BACK" {
		return r, nil
	}
	if r.State != "ACTIVE" && r.State != "CUTOVER_PENDING" && r.State != "ROLLBACK_PENDING" {
		return nil, fmt.Errorf("only cut-over migrations can roll back")
	}
	if r.State == "ROLLBACK_PENDING" && project.Version == r.ManifestVersion {
		r.State = "ROLLED_BACK"
		if err := knowledgeSaveMigration(r); err != nil {
			return nil, err
		}
		return r, nil
	}
	if project.Version != r.ActiveVersion {
		return nil, &knowledgebase.Error{Code: "VERSION_CONFLICT", Message: "Project configuration changed after cutover; rollback will not overwrite it."}
	}
	for key, value := range r.Original {
		if value == nil {
			delete(project.Raw, key)
		} else {
			project.Raw[key] = value
		}
	}
	r.ManifestVersion = knowledgeHash(project.Raw)
	r.State = "ROLLBACK_PENDING"
	if err := knowledgeSaveMigration(r); err != nil {
		return nil, err
	}
	if p.Recheck != nil {
		if err := p.Recheck(ctx); err != nil {
			return nil, err
		}
	}
	if err := knowledgeProjectSave(project, r.ActiveVersion); err != nil {
		return nil, err
	}
	r.State = "ROLLED_BACK"
	if err := knowledgeSaveMigration(r); err != nil {
		return nil, err
	}
	return r, nil
}

func knowledgeSyncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// knowledgeConfigMatches reports whether the project's knowledge configuration is what the migration preview recorded.
func knowledgeConfigMatches(project *knowledgeProject, original map[string]json.RawMessage) bool {
	if len(original) == 0 {
		return false
	}
	for key, value := range original {
		if knowledgeHash(project.Raw[key]) != knowledgeHash(value) {
			return false
		}
	}
	return true
}

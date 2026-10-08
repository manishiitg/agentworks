package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

type accessibleProjectIdentity struct {
	Name string `json:"name"`
	Icon string `json:"icon"`
}

func accessibleProjectIcon(icon, name string) string {
	if icon = strings.TrimSpace(icon); icon != "" {
		return icon
	}
	for _, char := range strings.TrimSpace(name) {
		return strings.ToUpper(string(char))
	}
	return "?"
}

func listAccessibleCrewProjects(ctx context.Context, userID, query string) ([]map[string]interface{}, error) {
	// List the caller's own Crews (logical Chats/Work/projects/<x> paths) and every other owner's Crews that are
	// shared with others (physical _users/<owner>/... or Crew/<x> paths), all read-write for Crew-to-Crew work.
	items, err := listCrewProjectsForOwner(ctx, userID, userID, query)
	if err != nil {
		return nil, err
	}
	for _, ownerID := range crewProjectOwnerCandidates(userID) {
		others, err := listCrewProjectsForOwner(ctx, userID, ownerID, query)
		if err != nil {
			continue
		}
		items = append(items, others...)
	}
	sort.Slice(items, func(i, j int) bool {
		left := strings.ToLower(fmt.Sprint(items[i]["name"], "\n", items[i]["workspace_path"]))
		right := strings.ToLower(fmt.Sprint(items[j]["name"], "\n", items[j]["workspace_path"]))
		return left < right
	})
	return items, nil
}

// listCrewProjectsForOwner lists one owner's Crew projects as seen by userID.
func listCrewProjectsForOwner(ctx context.Context, userID, ownerID, query string) ([]map[string]interface{}, error) {
	root := agentProfileRuntimeWorkspace(ownerID, "Chats/Work/projects")
	if sanitizeUserIDForPath(ownerID) != sanitizeUserIDForPath(userID) && root == agentProfileRuntimeWorkspace(userID, "Chats/Work/projects") {
		// Single-account layouts share one Chats root; don't list it twice.
		return nil, nil
	}
	store := defaultProductProjectStore()
	paths, exists, err := store.listPaths(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("list Crew projects: %w", err)
	}
	rootPrefix := strings.TrimSuffix(filepath.ToSlash(root), "/") + "/"
	if !exists {
		paths = nil
	}
	// Crews that moved to the shared root (PLAT-442 step 4) belong to their manifest owner.
	shared, err := listSharedCrewManifestPaths(ctx, store, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list Crew projects: %w", err)
	}
	paths = append(paths, shared...)
	if len(paths) == 0 {
		return []map[string]interface{}{}, nil
	}
	access, ownerLabel := "owner", "you"
	if sanitizeUserIDForPath(ownerID) != sanitizeUserIDForPath(userID) {
		access, ownerLabel = "write", ownerID
		if record := directoryUserFor(ownerID, "", ""); record != nil && strings.TrimSpace(record.Username) != "" {
			ownerLabel = record.Username
		}
	}
	seen := map[string]bool{}
	items := make([]map[string]interface{}, 0)
	for _, candidate := range paths {
		candidate = filepath.ToSlash(strings.TrimSpace(candidate))
		if (!strings.HasPrefix(candidate, rootPrefix) && !strings.HasPrefix(candidate, workspaceref.SharedCrewRoot+"/")) || !strings.HasSuffix(candidate, "/product.json") || seen[candidate] {
			continue
		}
		seen[candidate] = true
		// Another owner's Crew is listed only while it is shared with others (not made private, sharing on).
		if access != "owner" && !crewSharedWithOthers(strings.TrimSuffix(candidate, "/product.json")) {
			continue
		}
		raw, found, readErr := store.read(ctx, candidate)
		if readErr != nil {
			return nil, fmt.Errorf("read Crew manifest %s: %w", candidate, readErr)
		}
		if !found {
			continue
		}
		var manifest productProjectManifest
		if json.Unmarshal([]byte(raw), &manifest) != nil || !strings.EqualFold(strings.TrimSpace(manifest.Product), "work") {
			continue
		}
		id := strings.TrimSpace(manifest.ID)
		name := strings.TrimSpace(manifest.Title)
		if id == "" || name == "" {
			continue
		}
		identityName := strings.TrimSpace(manifest.Identity.Name)
		if identityName == "" {
			identityName = name
		}
		workspacePath := canonicalChatHistoryWorkspacePath(userID, filepath.ToSlash(filepath.Dir(candidate)))
		identityIcon := accessibleProjectIcon(manifest.Identity.Icon, identityName)
		haystack := strings.ToLower(strings.Join([]string{id, name, identityName, identityIcon, workspacePath}, "\n"))
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		items = append(items, map[string]interface{}{
			"id":   id,
			"name": name,
			"identity": accessibleProjectIdentity{
				Name: identityName,
				Icon: identityIcon,
			},
			"workspace_path": workspacePath,
			"access":         access,
			"owner":          ownerLabel,
		})
	}
	return items, nil
}

func updateWorkSessionWorkflowGuard(sessionID string, add []string, remove ...string) {
	cfg := common.GetSessionShellConfig(sessionID)
	if cfg == nil {
		return
	}
	removeSet := map[string]bool{}
	for _, path := range remove {
		removeSet[strings.TrimSuffix(strings.TrimSpace(path), "/")] = true
	}
	reads := make([]string, 0, len(cfg.ReadPaths)+len(add))
	for _, path := range cfg.ReadPaths {
		if !removeSet[strings.TrimSuffix(strings.TrimSpace(path), "/")] {
			reads = append(reads, path)
		}
	}
	reads = appendUniqueStrings(reads, add...)
	// Crew references are read-write; workflow references stay read-only.
	writes := make([]string, 0, len(cfg.WritePaths)+len(add))
	for _, path := range cfg.WritePaths {
		if !removeSet[strings.TrimSuffix(strings.TrimSpace(path), "/")] {
			writes = append(writes, path)
		}
	}
	crewWrites, _ := splitCrewReferenceFolders(add)
	writes = appendUniqueStrings(writes, crewWrites...)
	common.SetSessionFolderGuard(sessionID, reads, writes)
	// Another Crew's files are shared, its chats are not.
	blocked := make([]string, 0, len(cfg.BlockedPaths)+len(add))
	removedChats := map[string]bool{}
	for _, chats := range foreignCrewChatBlockedPaths("", remove) {
		removedChats[chats] = true
	}
	for _, path := range cfg.BlockedPaths {
		if !removedChats[path] {
			blocked = append(blocked, path)
		}
	}
	blocked = appendUniqueStrings(blocked, foreignCrewChatBlockedPaths("", add)...)
	common.SetSessionFolderGuardBlockedPaths(sessionID, blocked)
	// Other Crews' databases are read-only.
	removedDBs := map[string]bool{}
	for _, db := range foreignCrewDBWriteBlockedPaths("", remove) {
		removedDBs[db] = true
	}
	writeBlocked := make([]string, 0, len(cfg.BlockedWritePaths)+len(add))
	for _, path := range cfg.BlockedWritePaths {
		if !removedDBs[path] {
			writeBlocked = append(writeBlocked, path)
		}
	}
	writeBlocked = appendUniqueStrings(writeBlocked, foreignCrewDBWriteBlockedPaths("", add)...)
	common.SetSessionFolderGuardBlockedWritePaths(sessionID, writeBlocked)
}

// foreignCrewDBWriteBlockedPaths returns, for every Crew root in folders other
// than ownRoot, its db/ folder: a Crew may read another Crew's database but
// writes only its own.
func foreignCrewDBWriteBlockedPaths(ownRoot string, folders []string) []string {
	var blocked []string
	for _, builder := range foreignCrewChatBlockedPaths(ownRoot, folders) {
		blocked = append(blocked, strings.TrimSuffix(builder, "builder/")+"db/")
	}
	return blocked
}

// foreignCrewChatBlockedPaths returns, for every Crew root in folders other
// than ownRoot, the part of that Crew holding its chats (builder/: the
// conversation transcripts, other users' mirrored chats with it, and the
// builder chat state). Crews share their files server-wide, but a Crew's
// conversations stay inside that Crew; these prefixes are hard-denied for
// reads and writes.
func foreignCrewChatBlockedPaths(ownRoot string, folders []string) []string {
	own := strings.Trim(normalizeConversationWorkspace(ownRoot), "/")
	var blocked []string
	for _, folder := range folders {
		folder = strings.TrimSpace(folder)
		if !isCrewProjectPath(folder) {
			continue
		}
		if own != "" && strings.Trim(normalizeConversationWorkspace(folder), "/") == own && crewPathOwnerMatches(ownRoot, folder) {
			continue
		}
		blocked = append(blocked, strings.TrimSuffix(folder, "/")+"/builder/")
	}
	return blocked
}

// crewPathOwnerMatches reports whether two Crew paths name the same owner:
// a physical _users/<owner>/ prefix must agree when both carry one, so a
// same-named Crew of another owner is never treated as this Crew.
func crewPathOwnerMatches(a, b string) bool {
	ownerA, okA := crewProjectOwnerID(strings.Trim(a, "/"))
	ownerB, okB := crewProjectOwnerID(strings.Trim(b, "/"))
	if okA && okB {
		return ownerA == ownerB
	}
	return true
}

// splitCrewReferenceFolders separates Crew project roots (any owner) from
// other reference folders. Crews are shared read-write between Crews, so
// their roots join the write set; workflow roots stay read-only.
func splitCrewReferenceFolders(folders []string) (crewWrite, readOnly []string) {
	for _, folder := range folders {
		if isCrewProjectPath(folder) {
			crewWrite = append(crewWrite, strings.TrimSuffix(strings.TrimSpace(folder), "/")+"/")
			continue
		}
		readOnly = append(readOnly, folder)
	}
	return crewWrite, readOnly
}

// registerAccessibleWorkflowListTool gives product and Builder agents the same
// authorization-aware workflow discovery surface as the AgentWorks picker.
// attachedPaths is optional because only Crew persists workflow references.
func (api *StreamingAPI) registerAccessibleWorkflowListTool(registrar definitionToolRegistrar, userID string, attachedPaths func(context.Context) ([]string, error)) error {
	register := func(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error)) error {
		return registrar.RegisterCustomTool(name, description, parameters, execute, "workflow_discovery_tools")
	}
	claims := &UserClaims{UserID: strings.TrimSpace(userID)}
	if record := directoryUserFor(userID, "", ""); record != nil {
		claims.Username = record.Username
		claims.Email = record.Email
	}
	withClaims := func(ctx context.Context) context.Context {
		copy := *claims
		return context.WithValue(ctx, UserContextKey, &copy)
	}

	return register("list_accessible_workflows", "Search AgentWorks workflows the current user may read and every Crew project on the server (each Crew lists its owner). Each result includes its project name and display identity (identity name and icon). In Crew, pass an exact returned workspace_path to attach_workflow_reference for durable access. An empty query lists everything accessible.", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{"type": "string", "description": "Optional case-insensitive workflow/Crew project name, identity, id, icon, or path search."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		discovered, err := DiscoverWorkflowManifests(ctx)
		if err != nil {
			return "", err
		}
		visible := filterWorkflowManifestsForUser(claims, discovered)
		attached := []string{}
		if attachedPaths != nil {
			attached, err = attachedPaths(ctx)
			if err != nil {
				return "", err
			}
		}
		attachedSet := make(map[string]bool, len(attached))
		for _, path := range attached {
			attachedSet[path] = true
		}
		query, _ := args["query"].(string)
		query = strings.ToLower(strings.TrimSpace(query))
		items := make([]map[string]interface{}, 0, len(visible))
		for _, workflow := range visible {
			label := strings.TrimSpace(workflow.Manifest.Label)
			id := strings.TrimSpace(workflow.Manifest.ID)
			path := strings.TrimSuffix(strings.TrimSpace(workflow.WorkspacePath), "/")
			haystack := strings.ToLower(strings.Join([]string{label, id, path, workflow.Manifest.Icon}, "\n"))
			if query != "" && !strings.Contains(haystack, query) {
				continue
			}
			item := map[string]interface{}{
				"id": id, "name": label, "label": label, "workspace_path": path,
				"identity": accessibleProjectIdentity{Name: label, Icon: accessibleProjectIcon(workflow.Manifest.Icon, label)},
				"access":   string(workflow.MyAccess),
			}
			if attachedPaths != nil {
				item["attached"] = attachedSet[path]
			}
			items = append(items, item)
		}
		sort.Slice(items, func(i, j int) bool {
			left := strings.ToLower(fmt.Sprint(items[i]["label"], "\n", items[i]["workspace_path"]))
			right := strings.ToLower(fmt.Sprint(items[j]["label"], "\n", items[j]["workspace_path"]))
			return left < right
		})
		crews, err := listAccessibleCrewProjects(ctx, userID, query)
		if err != nil {
			return "", err
		}
		if attachedPaths != nil {
			for _, crew := range crews {
				path, _ := crew["workspace_path"].(string)
				crew["attached"] = attachedSet[path]
			}
		}
		encoded, err := json.MarshalIndent(map[string]interface{}{"workflows": items, "crews": crews}, "", "  ")
		return string(encoded), err
	})
}

// registerWorkWorkflowReferenceTools lets the Work assistant discover the
// same authorized workflow set as the AgentWorks picker and persist an exact
// read-only reference in workflow.json. Names are never accepted for mutation:
// the model must first list, disambiguate, and use the returned workspace path.
func (api *StreamingAPI) registerWorkWorkflowReferenceTools(registrar definitionToolRegistrar, userID, sessionID, workspacePath string, readOnly bool) error {
	if err := api.registerAccessibleWorkflowListTool(registrar, userID, func(ctx context.Context) ([]string, error) {
		return readWorkWorkflowReferences(ctx, workspacePath)
	}); err != nil {
		return err
	}
	// Crew Run mode: discovery stays so readers can see what the crew
	// references; attach/detach mutate the crew and go.
	if readOnly {
		return nil
	}
	register := func(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error)) error {
		return registrar.RegisterCustomTool(name, description, parameters, execute, "work_workflow_reference_tools")
	}
	claims := &UserClaims{UserID: strings.TrimSpace(userID)}
	if record := directoryUserFor(userID, "", ""); record != nil {
		claims.Username = record.Username
		claims.Email = record.Email
	}
	withClaims := func(ctx context.Context) context.Context {
		copy := *claims
		return context.WithValue(ctx, UserContextKey, &copy)
	}

	if err := register("attach_workflow_reference", "Attach one accessible AgentWorks workflow or any Crew project on the server to this Crew as durable context: Crew references are read-write, workflow references read-only. Pass only an exact workspace_path returned by list_accessible_workflows, and call only after the user explicitly asks to attach it.", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"workspace_path": map[string]interface{}{"type": "string", "description": "Exact workflow or Crew workspace_path returned by list_accessible_workflows."},
		},
		"required": []string{"workspace_path"},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		path, _ := args["workspace_path"].(string)
		canonicalCurrent := canonicalChatHistoryWorkspacePath(userID, workspacePath)
		if strings.TrimSuffix(strings.TrimSpace(path), "/") == canonicalCurrent {
			return "", fmt.Errorf("a Crew cannot attach itself as reference context")
		}
		authorized, readRoots, err := authorizeWorkflowContextPathsWithReadRoots(ctx, []string{path})
		if err != nil || len(authorized) != 1 {
			if err != nil {
				return "", err
			}
			return "", fmt.Errorf("workflow reference is unavailable")
		}
		paths, err := updateWorkWorkflowReferences(ctx, workspacePath, func(existing []string) ([]string, error) {
			return appendUniqueStrings(existing, authorized[0]), nil
		})
		if err != nil {
			return "", err
		}
		updateWorkSessionWorkflowGuard(sessionID, readRoots)
		api.emitAgentProfileEvent(sessionID, map[string]interface{}{"type": "work_workflow_references_updated", "workflow_context_paths": paths})
		encoded, err := json.MarshalIndent(map[string]interface{}{
			"workflow_context_paths": paths,
			"note":                   "The project is saved as durable context and file access is active now (Crew references are read-write, workflow references read-only).",
		}, "", "  ")
		return string(encoded), err
	}); err != nil {
		return err
	}

	return register("detach_workflow_reference", "Detach one durable AgentWorks workflow or Crew reference from this Crew. Pass the exact saved workspace_path.", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"workspace_path": map[string]interface{}{"type": "string"},
		},
		"required": []string{"workspace_path"},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		path, _ := args["workspace_path"].(string)
		path = strings.TrimSuffix(strings.TrimSpace(path), "/")
		removedReadRoot, validReference := contextReferenceReadRoot(userID, path)
		if !validReference {
			return "", fmt.Errorf("project reference is unavailable")
		}
		paths, err := updateWorkWorkflowReferences(ctx, workspacePath, func(existing []string) ([]string, error) {
			kept := make([]string, 0, len(existing))
			found := false
			for _, current := range existing {
				if current == path {
					found = true
					continue
				}
				kept = append(kept, current)
			}
			if !found {
				return nil, fmt.Errorf("workflow reference %q is not attached", path)
			}
			return kept, nil
		})
		if err != nil {
			return "", err
		}
		updateWorkSessionWorkflowGuard(sessionID, nil, removedReadRoot)
		api.emitAgentProfileEvent(sessionID, map[string]interface{}{"type": "work_workflow_references_updated", "workflow_context_paths": paths})
		return "Project reference detached. Access is revoked now.", nil
	})
}

func readWorkWorkflowReferences(ctx context.Context, workspacePath string) ([]string, error) {
	productRaw, productExists, err := readFileFromWorkspace(ctx, strings.TrimSuffix(strings.TrimSpace(workspacePath), "/")+"/product.json")
	if err != nil {
		return nil, err
	}
	if !productExists {
		return nil, fmt.Errorf("Work product manifest does not exist")
	}
	var productManifest productProjectManifest
	if err := json.Unmarshal([]byte(productRaw), &productManifest); err != nil {
		return nil, fmt.Errorf("decode Work product manifest: %w", err)
	}
	if !isProjectProfileID(productManifest.Product) {
		return nil, fmt.Errorf("workspace is not a Crew or Code project")
	}
	raw, _, err := readProjectRuntimeManifest(ctx, "work", workspacePath)
	if err != nil {
		return nil, err
	}
	var manifest productProjectManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return nil, fmt.Errorf("decode Work runtime manifest: %w", err)
	}
	return canonicalRuntimeSelection(firstNonEmptyStrings(manifest.WorkflowContextPaths, manifest.Capabilities.WorkflowContextPaths)), nil
}

func updateWorkWorkflowReferences(ctx context.Context, workspacePath string, mutate func([]string) ([]string, error)) ([]string, error) {
	manifestPath := projectRuntimeManifestPath("work", workspacePath)
	mutex := productConversationRegistryMutex(manifestPath)
	mutex.Lock()
	defer mutex.Unlock()

	raw, _, err := ensureProjectRuntimeManifest(ctx, "work", workspacePath)
	if err != nil {
		return nil, err
	}
	var typed productProjectManifest
	if err := json.Unmarshal([]byte(raw), &typed); err != nil {
		return nil, fmt.Errorf("decode Work product manifest: %w", err)
	}
	next, err := mutate(canonicalRuntimeSelection(firstNonEmptyStrings(typed.WorkflowContextPaths, typed.Capabilities.WorkflowContextPaths)))
	if err != nil {
		return nil, err
	}
	next = canonicalRuntimeSelection(next)

	var document map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return nil, fmt.Errorf("decode Work product manifest fields: %w", err)
	}
	document["workflow_context_paths"] = next
	document["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Work product manifest: %w", err)
	}
	if err := writeFileToWorkspace(ctx, manifestPath, string(encoded)+"\n"); err != nil {
		return nil, err
	}
	return next, nil
}

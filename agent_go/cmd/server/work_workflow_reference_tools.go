package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

type accessibleProjectIdentity struct {
	Name string `json:"name"`
	Icon string `json:"icon,omitempty"` // workflows only; a Crew is its name and purpose (owner, 5 Nov)
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
	product := crewProduct
	root := agentProfileRuntimeWorkspace(ownerID, product.ProjectsRoot)
	store := defaultProductProjectStore()
	paths, err := listSharedCrewManifestPaths(ctx, store, ownerID, product.ProfileID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	rootPrefix := strings.TrimSuffix(filepath.ToSlash(root), "/") + "/"
	if len(paths) == 0 {
		return []map[string]interface{}{}, nil
	}
	access, ownerLabel := "owner", "you"
	if sanitizeUserIDForPath(ownerID) != sanitizeUserIDForPath(userID) {
		access, ownerLabel = "run", ownerID
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
		if !projectDiscoveryRootIsCurrent(strings.TrimSuffix(candidate, "/product.json")) {
			continue
		}
		// Another owner's Crew is listed only while it is shared with others (not made private, sharing on).
		if access != "owner" && !crewSharedWith(strings.TrimSuffix(candidate, "/product.json"), userID) && !crewIsCoOwner(strings.TrimSuffix(candidate, "/product.json"), userID) {
			continue
		}
		raw, found, readErr := store.read(ctx, candidate)
		if readErr != nil {
			log.Printf("[CREW_LIST] stage=reference manifest=%q cause=%v", candidate, readErr)
			continue
		}
		if !found {
			continue
		}
		var manifest productProjectManifest
		if json.Unmarshal([]byte(raw), &manifest) != nil || !strings.EqualFold(strings.TrimSpace(manifest.Product), product.ProfileID) {
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
		haystack := strings.ToLower(strings.Join([]string{id, name, identityName, workspacePath}, "\n"))
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		// A co-owner builds the Crew with its owner, so the word for their access is the owner's.
		itemAccess := access
		if access != "owner" && crewIsCoOwner(strings.TrimSuffix(candidate, "/product.json"), userID) {
			itemAccess = "owner"
		}
		items = append(items, map[string]interface{}{
			"id":             id,
			"name":           name,
			"identity":       accessibleProjectIdentity{Name: identityName},
			"profile_id":     product.ProfileID,
			"description":    manifest.purpose(),
			"workspace_path": workspacePath,
			"access":         itemAccess,
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
// their roots join the write set; Code and workflow roots stay read-only.
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
// authorization-aware project/workflow discovery surface as the AgentWorks picker.
func (api *StreamingAPI) registerAccessibleWorkflowListTool(registrar definitionToolRegistrar, userID string) error {
	register := func(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error)) error {
		return registrar.RegisterCustomTool(name, description, parameters, execute, "workflow_discovery_tools")
	}
	claims := &UserClaims{UserID: strings.TrimSpace(userID)}
	if record := directoryUserFor(userID, "", ""); record != nil {
		claims.Username = record.Username
		claims.Email = record.Email
	}
	withClaims := func(ctx context.Context) context.Context {
		if current := GetUserFromContext(ctx); current != nil && current.UserID == userID {
			return ctx
		}
		copy := *claims
		return context.WithValue(ctx, UserContextKey, &copy)
	}

	return register("list_accessible_workflows", "Search AgentWorks workflows and projects the current user may access (each project lists its owner, name and workspace_path). An empty query lists everything accessible.", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{"type": "string", "description": "Optional case-insensitive workflow or project name, identity, id, icon, or path search."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		discovered, err := DiscoverWorkflowManifests(ctx)
		if err != nil {
			return "", err
		}
		visible := filterWorkflowManifestsForUser(claims, discovered)
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
			items = append(items, map[string]interface{}{
				"id": id, "name": label, "label": label, "workspace_path": path,
				"identity": accessibleProjectIdentity{Name: label, Icon: accessibleProjectIcon(workflow.Manifest.Icon, label)},
				"access":   string(workflow.MyAccess),
			})
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
		out := map[string]interface{}{"workflows": items, "crews": crews}
		encoded, err := json.MarshalIndent(out, "", "  ")
		return string(encoded), err
	})
}

// registerWorkWorkflowReferenceTools exposes authorized project/workflow discovery.
// Cross-project file and agent access is per-turn via #project references; durable project linking is retired.
func (api *StreamingAPI) registerWorkWorkflowReferenceTools(registrar definitionToolRegistrar, userID, sessionID, workspacePath string, readOnly bool) error {
	return api.registerAccessibleWorkflowListTool(registrar, userID)
}

// Project discovery shares audience rules while keeping the two origin grants.
func listAccessibleWorkspaceProjects(ctx context.Context, claims *UserClaims, query string) ([]map[string]interface{}, error) {
	if claims == nil {
		return nil, nil
	}
	rows := []map[string]interface{}{}
	if !workspaceOriginAllowed(claims, crewProfileID) {
		return rows, nil
	}
	for _, owner := range append([]string{claims.UserID}, crewProjectOwnerCandidates(claims.UserID)...) {
		items, err := listCrewProjectsForOwner(ctx, claims.UserID, owner, query)
		if err != nil {
			return nil, err
		}
		rows = append(rows, items...)
	}
	return rows, nil
}

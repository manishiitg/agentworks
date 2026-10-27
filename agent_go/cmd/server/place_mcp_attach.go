package server

import (
	"context"
	"fmt"
	"log"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The server-owned attachment index links a user's private MCP to a project.
// Only that owner can see or run it, even when the project is shared. New
// connections reuse the user's private store across projects; legacy per-place
// stores remain sealed at their original paths. Shared connections live in Vault.

const placeMCPAttachmentsFile = "attachments.json"

type placeMCPAttachment struct {
	Owner      string `json:"owner"`
	Scope      string `json:"scope,omitempty"`
	Server     string `json:"server"`
	AttachedAt string `json:"attached_at,omitempty"`
	// StoreRoot preserves legacy encrypted-store identity before alias folding.
	StoreRoot string `json:"store_root,omitempty"`
}

// New connections reuse the owner's private store across projects. Existing
// place stores keep their paths (encrypted files use path-bound AAD).
func attachmentStore(a placeMCPAttachment, root string) string {
	if a.Scope == "user" {
		return a.Owner
	}
	if a.StoreRoot != "" {
		return placeMCPStoreID(a.Owner, a.StoreRoot)
	}
	return placeMCPStoreID(a.Owner, projectBindingRoot(root))
}
func storeForAttachment(owner, server, root string) string {
	items, _ := placeMCPAttachmentsFor(root)
	for _, a := range items {
		if a.Owner == owner && a.Server == server {
			return attachmentStore(a, root)
		}
	}
	return owner
}

// placeMCPStoreID is the personal-store id of owner's connections in root.
func placeMCPStoreID(owner, root string) string {
	return "place:" + owner + ":" + root
}

func placeMCPAttachmentsPath() (string, error) {
	root, err := mcpConnectionsRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, placeMCPAttachmentsFile), nil
}

// cleanAttachRoot normalizes a workflow or Crew root. It returns "" for
// any path that is not one.
func cleanAttachRoot(raw string) string {
	root := strings.Trim(strings.TrimSpace(raw), "/")
	if root == "" || strings.ContainsAny(root, "\\\x00") || path.Clean(root) != root {
		return ""
	}
	segments := strings.Split(root, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return ""
		}
	}

	switch {
	case len(segments) == 2 && segments[0] == "Workflow" && !strings.HasPrefix(segments[1], "."):
		return root
	case len(segments) == 2 && segments[0] == crewSharedRootName:

		return root
	}
	return ""
}

// placeRootOf returns the workflow or Crew root a path lies in (a run
// folder or file inside it included), or "".
func placeRootOf(raw string) string {
	segments := strings.Split(strings.Trim(strings.TrimSpace(raw), "/"), "/")
	switch {
	case len(segments) >= 2 && (segments[0] == "Workflow" || segments[0] == crewSharedRootName):
		return cleanAttachRoot(strings.Join(segments[:2], "/"))
	}
	return ""
}

func readPlaceMCPAttachmentsLocked() (map[string][]placeMCPAttachment, error) {
	file, err := placeMCPAttachmentsPath()
	if err != nil {
		return nil, err
	}
	all := map[string][]placeMCPAttachment{}
	if err := readPlaceMCPJSON(file, &all); err != nil {
		return nil, err
	}

	return all, nil
}

func writePlaceMCPAttachmentsLocked(all map[string][]placeMCPAttachment) error {
	file, err := placeMCPAttachmentsPath()
	if err != nil {
		return err
	}
	return writePlaceMCPJSON(file, all)
}

// userClaimsForDirectoryID builds the claims access checks need (access
// lists name usernames and emails) for a user who is not the caller. A
// disabled account has none.
func userClaimsForDirectoryID(userID string) *UserClaims {
	claims := &UserClaims{UserID: userID, Username: userID}
	if dir, err := loadUserDirectory(); err == nil && dir != nil {
		if rec := dir.byID(userID); rec != nil {
			if rec.Disabled {
				return nil
			}
			claims.Username, claims.Email = rec.Username, rec.Email
		}
	}
	return claims
}

// placeMCPCanAttach reports whether userID may add connections to root:
// they must be able to edit that workflow, or own that Crew or Code. It is
// checked on add and again on every use, so losing edit access stops their
// connection there at once.
func placeMCPCanAttach(ctx context.Context, userID, root string) bool {
	root = cleanAttachRoot(root)
	if root == "" || strings.TrimSpace(userID) == "" {
		return false
	}
	claims := userClaimsForDirectoryID(userID)
	if claims == nil {
		return false
	}
	if strings.HasPrefix(root, "Workflow/") {
		level, _ := workflowAccessForWorkspacePath(ctx, claims, root)
		return level == WorkflowAccessOwner || level == WorkflowAccessWrite
	}
	ref, ok := resolveCrewPath(ctx, userID, root)
	return ok && crewAccessFor(claims, ref) == crewAccessOwner
}

// forgetPlaceMCP drops owner's server from root's index.
func forgetPlaceMCP(owner, server, root string) error {
	placeMCPMu.Lock()
	defer placeMCPMu.Unlock()
	all, err := readPlaceMCPAttachmentsLocked()
	if err != nil {
		return err
	}
	kept := all[root][:0]
	for _, a := range all[root] {
		if a.Owner == owner && a.Server == server {
			continue
		}
		kept = append(kept, a)
	}
	if len(kept) == 0 {
		delete(all, root)
	} else {
		all[root] = kept
	}
	return writePlaceMCPAttachmentsLocked(all)
}

// placeMCPAttachmentsFor lists root's connections, sorted by name.
func placeMCPAttachmentsFor(root string) ([]placeMCPAttachment, error) {
	root = cleanAttachRoot(root)
	if root == "" {
		return nil, nil
	}
	placeMCPMu.Lock()
	all, err := readPlaceMCPAttachmentsLocked()
	placeMCPMu.Unlock()
	if err != nil {
		return nil, err
	}
	list := append([]placeMCPAttachment(nil), all[root]...)
	sort.Slice(list, func(i, j int) bool {
		if list[i].Server != list[j].Server {
			return list[i].Server < list[j].Server
		}
		return list[i].Owner < list[j].Owner
	})
	return list, nil
}

// removePlaceMCP removes owner's connection from root with its login.
func removePlaceMCP(owner, name, root string) error {
	store := storeForAttachment(owner, name, root)
	if err := forgetPlaceMCP(owner, name, root); err != nil {
		return err
	}
	if store == owner {
		return nil
	} // Detach here; keep the private account for other projects.
	if err := removePlaceMCPServer(store, name); err != nil {
		log.Printf("[PLACE_MCP] remove %s from %s: %v", name, root, err)
	}
	closePlaceMCPConnection(store, name)
	_ = forgetPlaceMCPLogin(store, name)
	return nil
}

// forgetPlaceConnections removes every connection of a deleted workflow, Crew
// or Code, with their logins.
func forgetPlaceConnections(root string) {
	root = cleanAttachRoot(root)
	if root == "" {
		return
	}
	attachments, err := placeMCPAttachmentsFor(root)
	if err != nil {
		log.Printf("[PLACE_MCP] connections of deleted %s: %v", root, err)
		return
	}
	for _, a := range attachments {
		if err := removePlaceMCP(a.Owner, a.Server, root); err != nil {
			log.Printf("[PLACE_MCP] remove %s from deleted %s: %v", a.Server, root, err)
		}
	}
}

// errPlaceMCPUnavailable is returned by the bridge for a personal-shaped
// server name that is not one of this place's connections.
var errPlaceMCPUnavailable = fmt.Errorf("this MCP connection is not available here")

// projectSecretValue reads one secret of a workflow, Crew or Code from the
// shared project secret store (the same store Setup > Secrets writes).
func (api *StreamingAPI) projectSecretValue(root, name string) (string, error) {
	if api.chatStore == nil {
		return "", fmt.Errorf("project secrets are unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	secrets, err := api.ensureSharedWorkflowSecrets(ctx, root, api.projectSecretsOwner(mcpCaller(ctx), root))
	if err != nil {
		return "", err
	}
	for _, secret := range secrets {
		if secret.Name == name {
			return decryptSharedWorkflowSecret(root, secret)
		}
	}
	return "", fmt.Errorf("this connection needs the secret %q; add it under Setup > Secrets", name)
}

// placeAttachmentNamed finds the connection of root called name (its plain or internal name).
func placeAttachmentNamed(root, name string) (placeMCPAttachment, bool) {
	attachments, _ := placeMCPAttachmentsFor(root)
	for _, a := range attachments {
		internal, _, err := placeMCPServerConfig(attachmentStore(a, root), a.Server)
		if a.Server == name || strings.EqualFold(a.Server, name) || (err == nil && internal == name) {
			return a, true
		}
	}
	return placeMCPAttachment{}, false
}

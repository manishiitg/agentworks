package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/policy"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

// validateAccessPackage checks exact approved tool versions and paths before
// permissions are applied. Unsupported schemas fail closed.
func (a *Admin) validateAccessPackage(p access.Package) error {
	return a.Store.ValidateAccessPackage(p, a.WorkspaceID)
}

func (a *Admin) accessRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/admin/groups/{id}/servers/{cid}/access", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !a.Store.RemoveGroupConnectorAccess(a.WorkspaceID, r.PathValue("id"), r.PathValue("cid"), adminActor(r)) {
			writeErr(w, http.StatusNotFound, errors.New("unknown group or connector"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	// Use runtime authorization to report effective access, including policy
	// ownership and revoked rules that override older unrestricted grants.
	mux.HandleFunc("/api/admin/groups/{id}/permissions", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		group, ok := a.Store.GetGroup(r.PathValue("id"))
		if !ok || group.WorkspaceID != a.WorkspaceID {
			writeErr(w, http.StatusNotFound, errors.New("unknown group"))
			return
		}
		permissions := a.groupPermissions(group.ID)
		writeJSON(w, http.StatusOK, map[string]any{"permissions": permissions})
	}))
	mux.HandleFunc("/api/admin/access/packages", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"packages": a.Store.ListAppliedPackages(a.WorkspaceID)})
		case http.MethodPost:
			var p access.Package
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&p); err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			p.WorkspaceID = a.WorkspaceID
			if p.ID == "" {
				p.ID = newID("ap")
			}
			if err := a.validateAccessPackage(p); err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			saved, err := a.Store.SaveAccessPackage(p, p.Version, adminActor(r))
			if err != nil {
				status := http.StatusBadRequest
				if errors.Is(err, store.ErrPolicyConflict) {
					status = http.StatusConflict
				}
				if a.Store.PersistenceError() != nil {
					status = http.StatusServiceUnavailable
				}
				writeErr(w, status, err)
				return
			}
			writeJSON(w, http.StatusOK, saved)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	mux.HandleFunc("/api/admin/access/packages/{id}/revoke", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		revoked, ok := a.Store.RevokePackage(a.WorkspaceID, r.PathValue("id"))
		if !ok {
			writeErr(w, http.StatusNotFound, errors.New("published package not found"))
			return
		}
		a.Store.AppendPolicyEvent(a.WorkspaceID, store.PolicyEvent{At: time.Now().UTC(), Actor: adminActor(r), Action: "revoke", PackageID: revoked.ID, Version: revoked.Version})
		writeJSON(w, http.StatusOK, revoked)
	}))
	mux.HandleFunc("/api/admin/access/history", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": a.Store.ListPolicyEvents(a.WorkspaceID)})
	}))

}

// The product proxy replaces this header after checking the central admin role.
// Only requests authenticated with the backend service credential reach here.
func adminActor(r *http.Request) string {
	if actor := strings.TrimSpace(r.Header.Get("X-CapLayer-Actor")); actor != "" {
		return actor
	}
	return "local-admin"
}

type groupPermission struct {
	PublicName  string `json:"public_name"`
	ConnectorID string `json:"connector_id"`
	Allowed     bool   `json:"allowed"`
	Governed    bool   `json:"governed"`
	Source      string `json:"source"`
	Assigned    bool   `json:"assigned"`
}

type groupConnectorSummary struct {
	ConnectorID       string `json:"connector_id"`
	Name              string `json:"name"`
	ServerGrantActive bool   `json:"server_grant_active"`
	ServerReadOnly    bool   `json:"server_read_only,omitempty"`
	Assigned          bool   `json:"assigned"`
	AllowedToolCount  int    `json:"allowed_tool_count"`
	TotalToolCount    int    `json:"total_tool_count"`
}

func (a *Admin) groupAccessSummary(groupID string, permissions []groupPermission) []groupConnectorSummary {
	byConnector := map[string][]groupPermission{}
	for _, permission := range permissions {
		byConnector[permission.ConnectorID] = append(byConnector[permission.ConnectorID], permission)
	}
	summary := []groupConnectorSummary{}
	for _, connector := range a.Store.ListConnectors(a.WorkspaceID) {
		name := connector.Label
		if name == "" {
			name = connector.Provider
		}
		if name == "" {
			name = connector.ID
		}
		entry := groupConnectorSummary{ConnectorID: connector.ID, Name: name, ServerGrantActive: a.Store.GroupHasServer(groupID, connector.ID), ServerReadOnly: a.Store.GroupServerReadOnly(groupID, connector.ID)}
		entry.Assigned = entry.ServerGrantActive
		for _, permission := range byConnector[connector.ID] {
			entry.TotalToolCount++
			if permission.Allowed {
				entry.AllowedToolCount++
			}
			entry.Assigned = entry.Assigned || permission.Assigned
		}
		summary = append(summary, entry)
	}
	return summary
}

// Shared by the UI and builder; counts include whole-server grants and policies.
func (a *Admin) groupPermissions(groupID string) []groupPermission {
	permissions := []groupPermission{}
	assignedRules := map[string]bool{}
	for _, p := range a.Store.ListAppliedPackages(a.WorkspaceID) {
		if p.GroupID == groupID && p.Status == "published" {
			for _, rule := range p.Rules {
				assignedRules[rule.PublicName] = true
			}
		}
	}
	for _, tool := range a.Store.ListTools(a.WorkspaceID) {
		_, governed := a.Store.PolicyForTool(a.WorkspaceID, tool.PublicName)
		_, err := policy.Authorize(a.Store, auth.Identity{WorkspaceID: a.WorkspaceID, ViaGroup: groupID}, tool.PublicName)
		source := ""
		if governed {
			source = "policy"
		} else if a.Store.GroupServerAllows(groupID, tool) {
			source = "server"
		} else if a.Store.GroupHasTool(groupID, tool.PublicName) {
			source = "tool"
		}
		assigned := assignedRules[tool.PublicName] || a.Store.GroupServerAllows(groupID, tool) || a.Store.GroupHasTool(groupID, tool.PublicName)
		permissions = append(permissions, groupPermission{tool.PublicName, tool.ConnectorID, err == nil, governed, source, assigned})
	}
	return permissions
}

type userToolAccess struct {
	PublicName  string   `json:"public_name"`
	ConnectorID string   `json:"connector_id"`
	Allowed     bool     `json:"allowed"`
	Governed    bool     `json:"governed"`
	Via         []string `json:"via"`
}

// userAccess answers "what can this person reach in Vault": their groups,
// every tool with whether the same check as a real call allows it, and which
// groups (or an old direct grant) give it. Governed tools are also limited
// by their rules' conditions at call time.
func (a *Admin) userAccess(userID string) (map[string]any, error) {
	user, ok := a.Store.GetUser(userID)
	if !ok || user.WorkspaceID != a.WorkspaceID {
		return nil, errors.New("unknown user; they appear after their first sign-in to Vault")
	}
	groups := a.Store.GroupsOf(userID)
	direct := a.Store.UserGrantsFor(userID)
	directSet := map[string]bool{}
	for _, name := range direct {
		directSet[name] = true
	}
	tools := []userToolAccess{}
	allowedCount := 0
	for _, tool := range a.Store.ListTools(a.WorkspaceID) {
		_, err := policy.Authorize(a.Store, auth.Identity{WorkspaceID: a.WorkspaceID, UserID: userID}, tool.PublicName)
		_, governed := a.Store.PolicyForTool(a.WorkspaceID, tool.PublicName)
		via := []string{}
		for _, group := range groups {
			if a.Store.GroupServerAllows(group, tool) {
				level := " (whole server)"
				if a.Store.GroupServerReadOnly(group, tool.ConnectorID) {
					level = " (whole server, read-only)"
				}
				via = append(via, group+level)
			} else if a.Store.GroupHasTool(group, tool.PublicName) {
				via = append(via, group)
			}
		}
		if directSet[tool.PublicName] {
			via = append(via, "direct grant (old; move to a group)")
		}
		if err == nil {
			allowedCount++
		}
		if err == nil || len(via) > 0 {
			tools = append(tools, userToolAccess{tool.PublicName, tool.ConnectorID, err == nil, governed, via})
		}
	}
	return map[string]any{"user": user, "groups": groups, "direct_grants": direct, "allowed_tool_count": allowedCount, "tools": tools}, nil
}

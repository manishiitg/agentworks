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
		type permission struct {
			PublicName string `json:"public_name"`
			Allowed    bool   `json:"allowed"`
			Governed   bool   `json:"governed"`
			Source     string `json:"source"`
			Assigned   bool   `json:"assigned"`
		}
		permissions := []permission{}
		assignedRules := map[string]bool{}
		for _, p := range a.Store.ListAppliedPackages(a.WorkspaceID) {
			if p.GroupID == group.ID && p.Status == "published" {
				for _, rule := range p.Rules {
					assignedRules[rule.PublicName] = true
				}
			}
		}
		for _, tool := range a.Store.ListTools(a.WorkspaceID) {
			_, governed := a.Store.PolicyForTool(a.WorkspaceID, tool.PublicName)
			_, err := policy.Authorize(a.Store, auth.Identity{WorkspaceID: a.WorkspaceID, ViaGroup: group.ID}, tool.PublicName)
			source := ""
			if governed {
				source = "policy"
			} else if a.Store.GroupHasServer(group.ID, tool.ConnectorID) {
				source = "server"
			} else if a.Store.GroupHasTool(group.ID, tool.PublicName) {
				source = "tool"
			}
			assigned := assignedRules[tool.PublicName] || a.Store.GroupHasServer(group.ID, tool.ConnectorID) || a.Store.GroupHasTool(group.ID, tool.PublicName)
			permissions = append(permissions, permission{tool.PublicName, err == nil, governed, source, assigned})
		}
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

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
// any draft can become live policy. Unsupported schemas fail closed.
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
		for _, p := range a.Store.ListPackages(a.WorkspaceID) {
			if p.GroupID == group.ID && (p.Status == "published" || p.Status == "draft") {
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
			writeJSON(w, http.StatusOK, map[string]any{"packages": a.Store.ListPackages(a.WorkspaceID)})
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
			version := p.Version
			saved, ok := a.Store.SavePackageDraft(p, version)
			if !ok {
				writeErr(w, http.StatusConflict, errors.New("package changed; reload before editing"))
				return
			}
			a.Store.AppendPolicyEvent(a.WorkspaceID, store.PolicyEvent{At: time.Now().UTC(), Actor: adminActor(r), Action: "save_draft", PackageID: saved.ID, Version: saved.Version})
			writeJSON(w, http.StatusOK, saved)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	mux.HandleFunc("/api/admin/access/packages/{id}/publish", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Version int `json:"version"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, errors.New("invalid publish request"))
			return
		}
		p, ok := a.Store.GetPackageDraft(a.WorkspaceID, r.PathValue("id"))
		if !ok {
			writeErr(w, http.StatusNotFound, errors.New("draft not found"))
			return
		}
		if in.Version != 0 && in.Version != p.Version {
			writeErr(w, http.StatusConflict, errors.New("draft changed; reload before publishing"))
			return
		}
		if err := a.validateAccessPackage(p); err != nil {
			writeErr(w, http.StatusConflict, err)
			return
		}
		published, ok := a.Store.PublishPackage(a.WorkspaceID, p.ID, p.Version)
		if !ok {
			writeErr(w, http.StatusConflict, errors.New("draft changed; validate again"))
			return
		}
		a.Store.AppendPolicyEvent(a.WorkspaceID, store.PolicyEvent{At: time.Now().UTC(), Actor: adminActor(r), Action: "publish", PackageID: published.ID, Version: published.Version})
		writeJSON(w, http.StatusOK, published)
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
	mux.HandleFunc("/api/admin/access/packages/{id}/simulate", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		p, ok := a.Store.GetPackageDraft(a.WorkspaceID, r.PathValue("id"))
		if !ok {
			writeErr(w, http.StatusNotFound, errors.New("draft not found"))
			return
		}
		var in struct {
			PublicName string         `json:"public_name"`
			Arguments  map[string]any `json:"arguments"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		t, ok := a.Store.GetTool(in.PublicName)
		if !ok || t.WorkspaceID != a.WorkspaceID || t.Status != store.StatusActive || t.ApprovedFingerprint != t.Fingerprint {
			writeErr(w, http.StatusConflict, errors.New("tool is not an approved active version"))
			return
		}
		if err := a.Gateway.ValidateArguments(t, in.Arguments); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"allowed": false, "reason": "arguments do not match approved tool schema"})
			return
		}
		allowed := false
		for _, rule := range p.Rules {
			if rule.PublicName != in.PublicName {
				continue
			}
			if rule.Fingerprint != t.Fingerprint {
				break
			}
			allowed = true
			for _, condition := range rule.Conditions {
				if !access.Match(condition, in.Arguments) {
					allowed = false
					break
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]bool{"allowed": allowed})
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

package admin

import (
	"crypto/subtle"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/policy"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"net/http"
	"strings"
)

// Only service-authenticated requests may bind a centrally validated user.
func (a *Admin) bindPlatformUser(r *http.Request) error {
	if r.Header.Get("X-Vault-Platform-User") != "1" {
		return nil
	}
	return a.Store.EnsurePlatformUser(a.WorkspaceID, r.Header.Get("X-CapLayer-Actor"))
}

// These routes are service-only, with an identity supplied by the host after
// its central account checks. No upstream URL, bearer, or OAuth config leaves here.
func (a *Admin) runtimeRoutes(mux *http.ServeMux) {
	serviceAuth := func(r *http.Request) bool {
		return a.HumanToken != "" && r.Header.Get("Cookie") == "" && r.Header.Get("Origin") == "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.HumanToken)) == 1
	}
	identity := func(r *http.Request) (auth.Identity, string, bool) {
		actor := r.Header.Get("X-CapLayer-Actor")
		valid := serviceAuth(r) && validID.MatchString(actor) && r.Header.Get("X-Vault-Connector") != ""
		if valid && a.bindPlatformUser(r) != nil {
			valid = false
		}
		return auth.Identity{UserID: actor, WorkspaceID: a.WorkspaceID, ClientID: "agentworks"}, r.Header.Get("X-Vault-Connector"), valid
	}
	if a.Gateway != nil {
		mux.Handle("/api/admin/runtime/external-mcp", a.Gateway.ExternalProductHandler(func(r *http.Request) (auth.Identity, string, bool) {
			actor, client := r.Header.Get("X-CapLayer-Actor"), r.Header.Get("X-Vault-OAuth-Client")
			valid := serviceAuth(r) && validID.MatchString(actor) && strings.HasPrefix(client, "mcp_client_") && len(client) == 75 && r.Header.Get("X-Vault-Platform-User") == "1"
			if valid && a.bindPlatformUser(r) != nil {
				valid = false
			}
			return auth.Identity{UserID: actor, WorkspaceID: a.WorkspaceID, ClientID: client}, "", valid
		}))
		mux.Handle("/api/admin/runtime/mcp", a.Gateway.ProductHandler(identity))
	} else {
		mux.HandleFunc("/api/admin/runtime/mcp", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "MCP service unavailable", http.StatusServiceUnavailable)
		})
	}
	mux.HandleFunc("/api/admin/runtime/servers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		actor := r.Header.Get("X-CapLayer-Actor")
		if !serviceAuth(r) || !validID.MatchString(actor) {
			w.WriteHeader(401)
			return
		}
		if err := a.bindPlatformUser(r); err != nil {
			http.Error(w, "platform permissions unavailable", 503)
			return
		}
		id := auth.Identity{UserID: actor, WorkspaceID: a.WorkspaceID, ClientID: "agentworks"}
		type tool struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		}
		type row struct {
			ID       string `json:"id"`
			Label    string `json:"label"`
			Provider string `json:"provider"`
			Tools    []tool `json:"tools"`
		}
		type group struct {
			ID          string                 `json:"id"`
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			Servers     []row                  `json:"servers"`
			Secrets     []store.SecretResource `json:"secrets"`
		}
		if a.Store.PersistenceError() != nil {
			http.Error(w, "permissions unavailable", 503)
			return
		}
		groups := []group{}
		memberships := []string{}
		if user, ok := a.Store.GetUser(actor); ok && user.WorkspaceID == a.WorkspaceID {
			memberships = a.Store.GroupsOf(actor)
		}
		for _, gid := range memberships {
			g, ok := a.Store.GetGroup(gid)
			if ok && g.WorkspaceID == a.WorkspaceID {
				groups = append(groups, group{ID: g.ID, Name: g.Name, Description: g.Description, Servers: []row{}, Secrets: a.Store.ListSecrets(a.WorkspaceID, actor, gid, false)})
			}
		}
		rows := []row{}
		tools := a.Store.ListTools(a.WorkspaceID)
		for _, c := range a.Store.ListConnectors(a.WorkspaceID) {
			if c.Status != store.StatusActive {
				continue
			}
			item := row{ID: c.ID, Label: c.Label, Provider: c.Provider, Tools: []tool{}}
			for _, t := range tools {
				if t.ConnectorID == c.ID && policy.Visible(a.Store, id, t.PublicName) {
					item.Tools = append(item.Tools, tool{t.PublicName, t.Description, t.InputSchema})
				}
			}
			if len(item.Tools) > 0 {
				rows = append(rows, item)
				for i := range groups {
					groupItem := row{ID: c.ID, Label: c.Label, Provider: c.Provider, Tools: []tool{}}
					groupIdentity := id
					groupIdentity.ViaGroup = groups[i].ID
					for _, t := range item.Tools {
						if policy.Visible(a.Store, groupIdentity, t.Name) {
							groupItem.Tools = append(groupItem.Tools, t)
						}
					}
					if len(groupItem.Tools) > 0 {
						groups[i].Servers = append(groups[i].Servers, groupItem)
					}
				}
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"servers": rows, "groups": groups, "secrets": a.Store.ListSecrets(a.WorkspaceID, actor, "", false)})
	})
}

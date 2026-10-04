package admin

import (
	"crypto/subtle"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"net/http"
	"regexp"
)

var secretNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func (a *Admin) secretRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/admin/secrets", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, map[string]any{"secrets": a.Store.ListSecrets(a.WorkspaceID, "", "", true)})
		case http.MethodPost:
			var in struct {
				Secrets []store.SecretResource `json:"secrets"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&in) != nil || len(in.Secrets) > 1024 {
				http.Error(w, "invalid secret metadata", 400)
				return
			}
			for _, row := range in.Secrets {
				if !secretNamePattern.MatchString(row.Name) {
					http.Error(w, "invalid secret name", 400)
					return
				}
			}
			if err := a.Store.RegisterSecrets(a.WorkspaceID, in.Secrets); err != nil {
				http.Error(w, "could not register secret metadata", 503)
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	}))
	mux.HandleFunc("/api/admin/secrets/{name}", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(405)
			return
		}
		if err := a.Store.DeleteSecret(a.WorkspaceID, r.PathValue("name"), r.Header.Get("X-CapLayer-Actor")); err != nil {
			http.Error(w, "could not remove secret permissions", 503)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("/api/admin/groups/{id}/secrets", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		g, ok := a.Store.GetGroup(r.PathValue("id"))
		if !ok || g.WorkspaceID != a.WorkspaceID {
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodGet {
			writeJSON(w, 200, map[string]any{"secrets": a.Store.ListSecrets(a.WorkspaceID, "", g.ID, false)})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var in struct {
			Name    string `json:"name"`
			Allowed bool   `json:"allowed"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !secretNamePattern.MatchString(in.Name) {
			http.Error(w, "invalid secret grant", 400)
			return
		}
		if err := a.Store.SetSecretGrant(a.WorkspaceID, g.ID, in.Name, r.Header.Get("X-CapLayer-Actor"), in.Allowed); err != nil {
			http.Error(w, "unknown group or secret, or persistence unavailable", 400)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("/api/admin/runtime/secrets", func(w http.ResponseWriter, r *http.Request) {
		actor := r.Header.Get("X-CapLayer-Actor")
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		if a.HumanToken == "" || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" || !validID.MatchString(actor) || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.HumanToken)) != 1 {
			w.WriteHeader(401)
			return
		}
		if err := a.bindPlatformUser(r); err != nil {
			http.Error(w, "platform permissions unavailable", 503)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if a.Store.PersistenceError() != nil {
			http.Error(w, "permissions unavailable", 503)
			return
		}
		writeJSON(w, 200, map[string]any{"secrets": a.Store.ListSecrets(a.WorkspaceID, actor, "", false)})
	})
}

package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/catalog"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

// Person-owned vaults (PLAT-507). These routes are service-only: the host calls them with the shared service
// credential and the person it already authenticated (X-CapLayer-Actor, with X-Vault-Platform-User: 1), exactly as the
// runtime routes do. The administrator console cannot reach them. Every change is checked against the vault's owners
// inside the store, so a caller can only act on a vault they own; members can only read.
func (a *Admin) vaultRoutes(mux *http.ServeMux) {
	actorOf := func(r *http.Request) (string, error) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || r.Header.Get("X-Vault-Platform-User") != "1" {
			return "", errors.New("service call with a validated person required")
		}
		actor := strings.TrimSpace(r.Header.Get("X-CapLayer-Actor"))
		if actor == "" {
			return "", errors.New("person required")
		}
		if err := a.bindPlatformUser(r); err != nil {
			return "", errors.New("unknown person")
		}
		return actor, nil
	}
	route := func(pattern string, handler func(w http.ResponseWriter, r *http.Request, actor string)) {
		mux.HandleFunc(pattern, a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
			actor, err := actorOf(r)
			if err != nil {
				writeErr(w, http.StatusForbidden, err)
				return
			}
			handler(w, r, actor)
		}))
	}
	decode := func(w http.ResponseWriter, r *http.Request, into any) bool {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(into); err != nil {
			writeErr(w, 400, err)
			return false
		}
		return true
	}

	route("/api/project-vaults", func(w http.ResponseWriter, r *http.Request, actor string) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			WorkspaceRoot string `json:"workspace_root"`
		}
		if !decode(w, r, &in) {
			return
		}
		root := strings.TrimSpace(in.WorkspaceRoot)
		if root == "" || len(root) > 4096 {
			writeErr(w, 400, errors.New("project root required"))
			return
		}
		sum := sha256.Sum256([]byte(root))
		id := "v-" + hex.EncodeToString(sum[:12])
		parts := strings.Split(strings.TrimRight(root, "/"), "/")
		name := parts[len(parts)-1]
		if len(name) > 90 {
			name = name[:90]
		}
		if err := a.Store.EnsureProjectVault(a.WorkspaceID, actor, id, root, name); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]string{"id": id})
	})
	route("/api/vaults", func(w http.ResponseWriter, r *http.Request, actor string) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, map[string]any{"vaults": a.Store.VaultsFor(a.WorkspaceID, actor), "max_owned": store.MaxVaultsPerOwner})
		case http.MethodPost:
			var in struct{ Name, Description string }
			if !decode(w, r, &in) {
				return
			}
			var b [6]byte
			if _, err := rand.Read(b[:]); err != nil {
				writeErr(w, 500, err)
				return
			}
			id := "v-" + hex.EncodeToString(b[:])
			if err := a.Store.CreateVault(a.WorkspaceID, actor, id, in.Name, in.Description); err != nil {
				writeErr(w, 400, err)
				return
			}
			writeJSON(w, 201, map[string]string{"id": id})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	route("/api/vaults/{id}", func(w http.ResponseWriter, r *http.Request, actor string) {
		id := r.PathValue("id")
		switch r.Method {
		case http.MethodGet:
			for _, v := range a.Store.VaultsFor(a.WorkspaceID, actor) {
				if v.Group.ID == id {
					writeJSON(w, 200, v)
					return
				}
			}
			writeErr(w, 404, errors.New("unknown vault"))
		case http.MethodDelete:
			if err := a.Store.DeleteVault(actor, id); err != nil {
				writeErr(w, 400, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	setPerson := func(owner bool) func(w http.ResponseWriter, r *http.Request, actor string) {
		return func(w http.ResponseWriter, r *http.Request, actor string) {
			var in struct {
				UserID string `json:"user_id"`
			}
			target := r.PathValue("uid")
			switch r.Method {
			case http.MethodPost:
				if !decode(w, r, &in) {
					return
				}
				target = in.UserID
			case http.MethodDelete:
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			add := r.Method == http.MethodPost
			var err error
			if owner {
				err = a.Store.VaultSetOwner(actor, r.PathValue("id"), target, add)
			} else {
				err = a.Store.VaultSetMember(actor, r.PathValue("id"), target, add)
			}
			if err != nil {
				writeErr(w, 400, err)
				return
			}
			writeJSON(w, 200, map[string]string{"status": "ok"})
		}
	}
	route("/api/vaults/{id}/members", setPerson(false))
	route("/api/vaults/{id}/members/{uid}", setPerson(false))
	route("/api/vaults/{id}/owners", setPerson(true))
	route("/api/vaults/{id}/owners/{uid}", setPerson(true))

	// A vault connection is created from the catalog only (a server that needs a sign-in cannot be created from a bare
	// URL), through the same checks as a platform one, and is the vault's from the moment it exists.
	route("/api/vaults/{id}/connectors", func(w http.ResponseWriter, r *http.Request, actor string) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		vault := r.PathValue("id")
		if !a.Store.VaultOwnedBy(actor, vault) {
			writeErr(w, 403, errors.New("only an owner of this vault can do that"))
			return
		}
		var in struct {
			Provider, Label, Slug, Name, URL, Credential, AuthScheme, Transport string
			Import                                                              bool
			OAuth                                                               bool
		}
		if !decode(w, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		var c store.Connector
		var err error
		switch {
		case in.Import:
			if in.Credential != "" {
				if err = validateConnectorCredential(in.Credential, in.AuthScheme); err != nil {
					writeErr(w, 400, err)
					return
				}
			}
			if err = a.Gateway.ValidateUpstreamURL(in.URL); err == nil {
				key := catalog.Key(in.Name)
				if key == "" {
					key = "imported"
				}
				oauthServer := ""
				if in.OAuth {
					oauthServer = "custom:" + key
				}
				c, err = a.addConnectorRowFor(vault, key, in.Label, in.Slug, in.URL, oauthServer)
				if err == nil && in.Credential != "" {
					err = validateConnectorCredential(in.Credential, in.AuthScheme)
					if err == nil {
						c.AuthScheme = in.AuthScheme
						a.Store.AddConnector(c)
						a.Store.SetConnectorBearer(c.ID, in.Credential)
					}
				}
			}
		case in.URL != "":
			c, err = a.addConnectorCustomFor(ctx, vault, in.Name, in.Label, in.Slug, in.URL, in.Credential, in.AuthScheme, in.Transport)
		default:
			c, err = a.addConnectorFromCatalogFor(ctx, vault, in.Provider, in.Label, in.Slug)
		}
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if in.Transport != "" && in.Transport != "http" && in.Transport != "sse" {
			_ = a.DeleteConnector(c.ID)
			writeErr(w, 400, errors.New("transport must be http or sse"))
			return
		}
		if in.Transport != "" {
			c.Transport = in.Transport
		}
		if in.Import && !in.OAuth {
			c.Status = store.StatusPending
		}
		a.Store.AddConnector(c)
		if err := a.Store.VaultAttachConnector(actor, vault, c.ID); err != nil {
			// Never leave a connection nobody can manage: undo the creation if it cannot be bound.
			_ = a.DeleteConnector(c.ID)
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 201, c)
	})
	route("/api/vaults/{id}/connectors/{cid}/credentials", func(w http.ResponseWriter, r *http.Request, actor string) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !a.Store.VaultConnectorOwnedBy(actor, r.PathValue("id"), r.PathValue("cid")) {
			writeErr(w, 403, errors.New("only a vault owner can update credentials"))
			return
		}
		var in struct{ Credential, AuthScheme string }
		if !decode(w, r, &in) {
			return
		}
		if err := validateConnectorCredential(in.Credential, in.AuthScheme); err != nil {
			writeErr(w, 400, err)
			return
		}
		c, ok := a.Store.GetConnector(r.PathValue("cid"))
		if !ok {
			writeErr(w, 404, errors.New("unknown connector"))
			return
		}
		c.AuthScheme = in.AuthScheme
		c.OAuthServer = ""
		c.OAuthCredentialID = ""
		if err := a.Gateway.ReplaceConnectorCredentials(r.Context(), c, in.Credential); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "connected"})
	})
	// Secrets: the Vault service records only the name and who may use it; the value stays in the host's encrypted store.
	route("/api/vaults/{id}/secrets", func(w http.ResponseWriter, r *http.Request, actor string) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in struct{ Name string }
		if !decode(w, r, &in) {
			return
		}
		if !secretNamePattern.MatchString(in.Name) {
			writeErr(w, 400, errors.New("invalid secret name"))
			return
		}
		if err := a.Store.VaultAddSecret(actor, r.PathValue("id"), in.Name); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 201, map[string]string{"name": in.Name})
	})
	route("/api/vaults/{id}/secrets/{name}", func(w http.ResponseWriter, r *http.Request, actor string) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := a.Store.VaultRemoveSecret(actor, r.PathValue("id"), r.PathValue("name")); err != nil {
			writeErr(w, 400, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	route("/api/vaults/{id}/connectors/{cid}", func(w http.ResponseWriter, r *http.Request, actor string) {
		if r.Method == http.MethodPut {
			if !a.Store.VaultConnectorOwnedBy(actor, r.PathValue("id"), r.PathValue("cid")) {
				writeErr(w, 403, errors.New("only a vault owner can update this connection"))
				return
			}
			var in struct{ URL, Label string }
			if !decode(w, r, &in) {
				return
			}
			c, _ := a.Store.GetConnector(r.PathValue("cid"))
			if in.Label != "" {
				if len(in.Label) > 100 {
					writeErr(w, 400, errors.New("label too long"))
					return
				}
				c.Label = in.Label
			}
			if in.URL != "" && in.URL != c.UpstreamURL {
				if err := a.Gateway.ValidateUpstreamURL(in.URL); err != nil {
					writeErr(w, 400, err)
					return
				}
				c.UpstreamURL = in.URL
				c.OAuthServer = "custom:" + c.Provider
				c.OAuthCredentialID = c.ID
				c.Status = store.StatusAuthRequired
				c.AuthScheme = ""
				a.Gateway.ResetConnectorSession(c.ID)
				a.Store.SetConnectorBearer(c.ID, "")
			}
			a.Store.AddConnector(c)
			writeJSON(w, 200, c)
			return
		}

		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !a.Store.VaultConnectorOwnedBy(actor, r.PathValue("id"), r.PathValue("cid")) {
			writeErr(w, 403, errors.New("only an owner of this vault can remove its connections"))
			return
		}
		if err := a.DeleteConnector(r.PathValue("cid")); err != nil {
			writeErr(w, 400, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	route("/api/vaults/{id}/connectors/{cid}/sync", func(w http.ResponseWriter, r *http.Request, actor string) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !a.Store.VaultConnectorOwnedBy(actor, r.PathValue("id"), r.PathValue("cid")) {
			writeErr(w, 403, errors.New("only an owner of this vault can do that"))
			return
		}
		if err := a.SyncConnector(r.Context(), r.PathValue("cid")); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "synced"})
	})
}

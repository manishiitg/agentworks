package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

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
		var in struct{ Provider, Label, Slug string }
		if !decode(w, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		c, err := a.addConnectorFromCatalogFor(ctx, vault, in.Provider, in.Label, in.Slug)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := a.Store.VaultAttachConnector(actor, vault, c.ID); err != nil {
			// Never leave a connection nobody can manage: undo the creation if it cannot be bound.
			_ = a.DeleteConnector(c.ID)
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 201, c)
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

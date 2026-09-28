package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"

	"github.com/gorilla/mux"
)

// Named OAuth client registry: CRUD over the OAuth app registrations Gmail
// connections authorize under (services/gmail_oauth_clients.go). Separate
// from GmailConnectionRoutes because a client is shared across many
// connections — it is not itself a sending identity.

// GmailOAuthClientResponse is the wire shape of one named client. The client
// secret is never returned; only an id for display and disambiguation.
type GmailOAuthClientResponse struct {
	Name      string `json:"name"`
	ClientID  string `json:"client_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// GmailOAuthClientsResponse is the list payload.
type GmailOAuthClientsResponse struct {
	Clients []GmailOAuthClientResponse `json:"clients"`
}

// GmailOAuthClientCreateRequest carries the uploaded client_secret.json inline
// as a JSON value, plus the required name. replace must be explicitly true to
// overwrite an existing name — the default is refuse, which is the actual fix
// for the old shared-file overwrite bug.
type GmailOAuthClientCreateRequest struct {
	Name             string          `json:"name"`
	ClientSecretJSON json.RawMessage `json:"client_secret_json"`
	Replace          bool            `json:"replace,omitempty"`
}

// GmailOAuthClientImportLegacyRequest names the client the box's existing
// shared client_secret.json should be registered as.
type GmailOAuthClientImportLegacyRequest struct {
	Name string `json:"name"`
}

// GmailOAuthClientImportLegacyResponse reports what the one-time migration did.
type GmailOAuthClientImportLegacyResponse struct {
	Client                GmailOAuthClientResponse `json:"client"`
	ConnectionsBackfilled int                      `json:"connections_backfilled"`
}

func projectGmailOAuthClient(c services.GmailOAuthClient) GmailOAuthClientResponse {
	out := GmailOAuthClientResponse{Name: c.Name, ClientID: c.ClientID}
	if !c.CreatedAt.IsZero() {
		out.CreatedAt = c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if !c.UpdatedAt.IsZero() {
		out.UpdatedAt = c.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return out
}

// GmailOAuthClientRoutes wires the named OAuth client registry API.
func GmailOAuthClientRoutes(router *mux.Router, api *StreamingAPI) {
	r := router.PathPrefix("/api/human-feedback/gmail/oauth-clients").Subrouter()
	r.HandleFunc("", listGmailOAuthClientsHandler(api)).Methods("GET")
	r.HandleFunc("", createGmailOAuthClientHandler(api)).Methods("POST", "OPTIONS")
	r.HandleFunc("/import-legacy", requireAdminWrite(importLegacyGmailOAuthClientHandler(api))).Methods("POST", "OPTIONS")
	r.HandleFunc("/{name}", requireAdminWrite(deleteGmailOAuthClientHandler(api))).Methods("DELETE", "OPTIONS")
}

func listGmailOAuthClientsHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clients, err := services.ListOAuthClients()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := GmailOAuthClientsResponse{Clients: make([]GmailOAuthClientResponse, 0, len(clients))}
		for _, c := range clients {
			out.Clients = append(out.Clients, projectGmailOAuthClient(c))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}
}

func createGmailOAuthClientHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		var req GmailOAuthClientCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return
		}
		if len(req.ClientSecretJSON) == 0 {
			http.Error(w, "client_secret_json is required", http.StatusBadRequest)
			return
		}
		// Anyone may register a new OAuth client (a Code owner connecting
		// their own account needs one); replacing one invalidates every
		// connection using it, so only an admin may.
		if req.Replace && !currentUserIsAdmin(r) {
			writeWorkflowPermissionDenied(w, "admin")
			return
		}
		client, err := services.CreateOAuthClient(r.Context(), req.Name, req.ClientSecretJSON, req.Replace)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("[GMAIL] Registered OAuth client %q (client_id %s)", client.Name, client.ClientID)
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(projectGmailOAuthClient(client))
	}
}

func deleteGmailOAuthClientHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		name := mux.Vars(r)["name"]
		if err := services.DeleteOAuthClient(name); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		log.Printf("[GMAIL] Deleted OAuth client %q", name)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"deleted": name})
	}
}

// importLegacyGmailOAuthClientHandler is the one-time migration action: name
// the box's existing shared client_secret.json and backfill it onto every
// connection that predates the named-client registry.
func importLegacyGmailOAuthClientHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		svc, err := ensureGmailService()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to initialize Gmail service: %v", err), http.StatusInternalServerError)
			return
		}
		var req GmailOAuthClientImportLegacyRequest
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
				http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
				return
			}
		}
		client, backfilled, err := svc.ImportLegacyOAuthClient(r.Context(), req.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("[GMAIL] Imported legacy OAuth client as %q, backfilled %d connection(s)", client.Name, backfilled)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(GmailOAuthClientImportLegacyResponse{
			Client:                projectGmailOAuthClient(client),
			ConnectionsBackfilled: backfilled,
		})
	}
}

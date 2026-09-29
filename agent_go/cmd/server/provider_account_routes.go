package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// HTTP surface for provider accounts (docs/design/provider_accounts.md).
// Credentials never leave the server: every response carries metadata only.

// providerAccountView is one account as the caller may see it.
type providerAccountView struct {
	ProviderConnection
	// Kind is installed, admin (a key saved on the Providers page) or user.
	Kind string `json:"kind"`
	// Relation is how the caller reaches the account: server, own,
	// shared_with_you, shared_with_workflow, shared_with_crew, or
	// admin_view (an admin looking at someone else's account).
	Relation  string `json:"relation"`
	OwnerName string `json:"owner_name,omitempty"`
	// Source says where a server account's login or key comes from; never
	// the value.
	Source               string                     `json:"source,omitempty"`
	Availability         *serverAccountAvailability `json:"availability,omitempty"`
	AvailabilityEditable bool                       `json:"availability_editable,omitempty"`
	// Usable reports whether the caller may select the account for the
	// requested workflow, Crew, Code or product.
	Usable       bool `json:"usable"`
	// Configured reports whether the account is set up: signed in, or has
	// a key. Absent when a check could not tell; that never blocks use.
	Configured *bool `json:"configured,omitempty"`
	// Identity is who the account is signed in as (an email or plan name
	// the CLI reports), so people can see whose login a shared account uses.
	Identity string `json:"identity,omitempty"`
	CanManage    bool `json:"can_manage"`
	CanViewUsage bool `json:"can_view_usage"`
}

// serverAccountEnvNames lists the installation variables that carry a
// provider's server credential, in precedence order.
var serverAccountEnvNames = map[string][]string{
	"claude-code": {"CLAUDE_CODE_OAUTH_TOKEN"},
	"codex-cli":   {"CODEX_API_KEY"},
	"cursor-cli":  {"CURSOR_API_KEY"},
	"muse-cli":    {"META_API_KEY"},
	"pi-cli":      {"PI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"},
	"agy-cli":     {"GEMINI_API_KEY"},
}

// serverAccountSource says whether a server account is admin-configured
// (a key saved on the Providers page) or installed, and where its login or
// key comes from.
func serverAccountSource(provider string, stored *StoredProviderKeys) (kind, source string) {
	if stored != nil {
		value := ""
		switch provider {
		case "codex-cli":
			value = stored.CodexCLI
		case "cursor-cli":
			value = stored.CursorCLI
		case "pi-cli":
			value = stored.PiCLI
			if value == "" && len(stored.PiProviderKeys) > 0 {
				value = "set"
			}
		}
		if strings.TrimSpace(value) != "" {
			return "admin", "Admin-configured: key saved on the Providers page"
		}
	}
	for _, name := range serverAccountEnvNames[provider] {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return "installed", "Installation (.env: " + name + ")"
		}
	}
	return "installed", "Installation (CLI login on the server)"
}

func requireSignedIn(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := GetUserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return "", false
	}
	return userID, true
}

func providerEnabled(provider string) bool {
	for _, candidate := range getSupportedProviders() {
		if candidate == provider {
			return true
		}
	}
	return false
}

// serverAccountProviders lists the enabled providers that can be run on a
// named account at all.
func serverAccountProviders() []string {
	providers := []string{}
	for _, provider := range getSupportedProviders() {
		if _, err := connectionCredentialKeys(storedProviderConnection{ProviderConnection: ProviderConnection{Provider: provider, UnderlyingProvider: "placeholder"}, Credential: "placeholder"}); err != nil {
			continue
		}
		providers = append(providers, provider)
	}
	return providers
}

// providerHasUsageCommand reports whether provider has a usage action.
func providerHasUsageCommand(provider string) bool {
	return providerUsageCommands[provider] != ""
}

// GET  /api/provider-connections[?workspace_path=&product=] lists the
// accounts the caller may see, marking which they may use there.
// POST /api/provider-connections adds a user account.
func (api *StreamingAPI) handleProviderConnections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	userID, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		connections, err := api.listProviderAccountViews(r.Context(), userID, currentUserIsAdmin(r), providerAccountScope{Principal: userID, WorkspacePath: r.URL.Query().Get("workspace_path"), Product: r.URL.Query().Get("product")})
		if err != nil {
			http.Error(w, "cannot load provider connections", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"connections": connections})
	case http.MethodPost:
		api.createProviderConnection(w, r, userID)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (api *StreamingAPI) listProviderAccountViews(ctx context.Context, userID string, admin bool, scope providerAccountScope) ([]providerAccountView, error) {
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(ctx)
	providerConnectionsMu.Unlock()
	if err != nil {
		return nil, err
	}
	run := api.describeProviderAccountRun(ctx, scope)
	anyProduct := strings.TrimSpace(scope.WorkspacePath) == "" && strings.TrimSpace(scope.Product) == ""
	stored, _ := LoadProviderKeys(ctx)
	views := []providerAccountView{}
	for _, provider := range serverAccountProviders() {
		availability, err := effectiveServerAccountAvailability(ctx, provider)
		if err != nil {
			return nil, err
		}
		kind, source := serverAccountSource(provider, stored)
		allowed := !personalProviderConnectionsLocked(provider)
		usable := availability.AvailableTo.admits(userID, run.Product)
		if anyProduct {
			usable = false
			for _, product := range []string{productWorkflows, productCrews, productCode} {
				usable = usable || availability.AvailableTo.admits(userID, product)
			}
		}
		view := providerAccountView{
			ProviderConnection: ProviderConnection{PersonalAccountsAllowed: &allowed, ID: "global:" + provider, Provider: provider, DisplayName: "Server account", Scope: "global", AuthMethod: "server"},
			Kind:               kind, Relation: "server", Source: source, Availability: &availability,
			AvailabilityEditable: admin && !availability.Pinned,
			Usable:               usable,
			CanManage:            admin,
			CanViewUsage:         providerHasUsageCommand(provider) && (admin || usable),
		}
		views = append(views, view)
	}
	for _, record := range records {
		if !providerEnabled(record.Provider) {
			continue
		}
		view := providerAccountView{ProviderConnection: record.ProviderConnection, Kind: "user"}
		own := record.OwnerUserID == userID
		switch {
		case own:
			view.Relation = "own"
		default:
			how := api.userAccountAdmission(ctx, record, userID, run)
			switch how {
			case "user":
				view.Relation = "shared_with_you"
			case "workflow":
				view.Relation = "shared_with_workflow"
			case "crew":
				view.Relation = "shared_with_crew"
			}
			// A private account is private to its owner, admins included.
			if view.Relation == "" {
				continue
			}
			view.OwnerName = logUsernameForUserID(record.OwnerUserID)
		}
		view.Usable = !personalProviderConnectionsLocked(record.Provider)
		view.CanManage = own
		view.CanViewUsage = providerHasUsageCommand(record.Provider) && (own || view.Usable)
		if !view.CanManage {
			view.Sharing = nil
			view.OwnerUserID = ""
		} else if view.Sharing == nil {
			view.Sharing = &ProviderConnectionSharing{Mode: providerSharingPrivate}
		}
		views = append(views, view)
	}
	api.fillProviderAccountConfigured(ctx, views, records)
	return views, nil
}

type providerConnectionRequest struct {
	Provider           string                     `json:"provider"`
	DisplayName        *string                    `json:"display_name"`
	Credential         *string                    `json:"credential"`
	UnderlyingProvider string                     `json:"underlying_provider"`
	AuthMethod         string                     `json:"auth_method"`
	Sharing            *ProviderConnectionSharing `json:"sharing"`
	// AvailableTo is the admin setting for a server account; JSON null
	// clears it back to the installation policy.
	AvailableTo json.RawMessage `json:"available_to"`
}

func validProviderAccountName(name *string) (string, error) {
	if name == nil {
		return "", fmt.Errorf("account name is required (maximum 120 characters)")
	}
	trimmed := strings.TrimSpace(*name)
	if trimmed == "" || len(trimmed) > 120 {
		return "", fmt.Errorf("account name is required (maximum 120 characters)")
	}
	return trimmed, nil
}

func (api *StreamingAPI) createProviderConnection(w http.ResponseWriter, r *http.Request, userID string) {
	var request providerConnectionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&request); err != nil {
		http.Error(w, "invalid connection request", http.StatusBadRequest)
		return
	}
	name, err := validProviderAccountName(request.DisplayName)
	if err != nil {
		http.Error(w, "connection name is required (maximum 120 characters)", http.StatusBadRequest)
		return
	}
	if personalProviderConnectionsLocked(request.Provider) {
		http.Error(w, "personal provider connections are locked by administrator", http.StatusForbidden)
		return
	}
	if !providerEnabled(request.Provider) {
		http.Error(w, "provider is not enabled", http.StatusBadRequest)
		return
	}
	sharing, err := api.normalizeProviderSharing(r.Context(), userID, request.Sharing)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	credential := ""
	if request.Credential != nil {
		credential = strings.TrimSpace(*request.Credential)
	}
	record := storedProviderConnection{ProviderConnection: ProviderConnection{ID: uuid.NewString(), Provider: request.Provider, DisplayName: name, OwnerUserID: userID, Scope: "user", AuthMethod: "api_key", UnderlyingProvider: strings.TrimSpace(request.UnderlyingProvider), UpdatedAt: time.Now().UTC(), Sharing: sharing}, Credential: credential}
	if request.AuthMethod == "cli_login" {
		record.AuthMethod = "cli_login"
		record.Credential = ""
	} else if request.Provider == "claude-code" {
		record.AuthMethod = "oauth_token"
	}
	if _, err := connectionCredentialKeys(record); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	providerConnectionsMu.Lock()
	defer providerConnectionsMu.Unlock()
	records, err := loadProviderConnections(r.Context())
	if err != nil {
		http.Error(w, "cannot load provider connections", http.StatusInternalServerError)
		return
	}
	if err := saveProviderConnections(r.Context(), append(records, record)); err != nil {
		http.Error(w, "cannot save provider connection", http.StatusInternalServerError)
		return
	}
	log.Printf("[PROVIDER_ACCOUNT] %s added %s account %s (%s, %s)", userID, record.Provider, record.ID, record.AuthMethod, sharingSummary(record.Sharing))
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(record.ProviderConnection)
}

func sharingSummary(sharing *ProviderConnectionSharing) string {
	if sharing == nil || sharing.Mode != providerSharingShared {
		return "private"
	}
	return fmt.Sprintf("shared with %d workflows, %d Crews, %d people", len(sharing.Workflows), len(sharing.Crews), len(sharing.Users))
}

// PATCH  /api/provider-connections/{id} edits an account: a user account's
// name, credential or sharing (owner or admin), or a server account's
// "Available to" (admin, unless the installation pins it).
// DELETE /api/provider-connections/{id} removes a user account (owner or
// admin) and its login files.
func (api *StreamingAPI) handleProviderConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	userID, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	id := mux.Vars(r)["connectionID"]
	admin := currentUserIsAdmin(r)
	if strings.HasPrefix(id, "global:") {
		api.updateServerAccount(w, r, strings.TrimPrefix(id, "global:"), admin)
		return
	}
	var request providerConnectionRequest
	if r.Method != http.MethodDelete {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&request) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
	}
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(r.Context())
	providerConnectionsMu.Unlock()
	if err != nil {
		http.Error(w, "cannot load connections", http.StatusInternalServerError)
		return
	}
	index := -1
	for i := range records {
		if records[i].ID == id && records[i].OwnerUserID == userID {
			index = i
		}
	}
	if index < 0 {
		http.Error(w, "connection unavailable", http.StatusNotFound)
		return
	}
	target := records[index]
	var sharing *ProviderConnectionSharing
	if r.Method != http.MethodDelete {
		if personalProviderConnectionsLocked(target.Provider) {
			http.Error(w, "personal connections are locked", http.StatusForbidden)
			return
		}
		// Sharing is checked against what the OWNER can see, also when an
		// admin edits it.
		if request.Sharing != nil {
			if sharing, err = api.normalizeProviderSharing(r.Context(), target.OwnerUserID, request.Sharing); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
	}
	providerConnectionsMu.Lock()
	defer providerConnectionsMu.Unlock()
	// Re-read under the lock so a concurrent edit is not lost.
	records, err = loadProviderConnections(r.Context())
	if err != nil {
		http.Error(w, "cannot load connections", http.StatusInternalServerError)
		return
	}
	for i := range records {
		record := &records[i]
		if record.ID != id || (record.OwnerUserID != userID && !admin) {
			continue
		}
		if r.Method == http.MethodDelete {
			history, err := loadProviderConnectionHistory(r.Context())
			if err != nil {
				http.Error(w, "cannot load account history", http.StatusInternalServerError)
				return
			}
			// A prior attempt may have saved history before the active registry
			// write failed. Replace that entry when the deletion is retried.
			replaced := false
			for j := range history {
				if history[j].ID == record.ID {
					history[j] = providerConnectionHistory{ID: record.ID, Provider: record.Provider, DisplayName: record.DisplayName, OwnerUserID: record.OwnerUserID}
					replaced = true
					break
				}
			}
			if !replaced {
				history = append(history, providerConnectionHistory{ID: record.ID, Provider: record.Provider, DisplayName: record.DisplayName, OwnerUserID: record.OwnerUserID})
			}
			if err := saveProviderConnectionHistory(r.Context(), history); err != nil {
				http.Error(w, "cannot save account history", http.StatusInternalServerError)
				return
			}
			records = append(records[:i], records[i+1:]...)
		} else {
			if request.DisplayName != nil {
				name, err := validProviderAccountName(request.DisplayName)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				record.DisplayName = name
			}
			if request.Credential != nil {
				record.Credential = strings.TrimSpace(*request.Credential)
				if _, err := connectionCredentialKeys(*record); err != nil {
					http.Error(w, "invalid credential", http.StatusBadRequest)
					return
				}
			}
			if request.Sharing != nil {
				record.Sharing = sharing
			}
			record.UpdatedAt = time.Now().UTC()
		}
		if saveProviderConnections(r.Context(), records) != nil {
			http.Error(w, "cannot save connections", http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodDelete {
			// Stop the CLIs still running on this account before its HOME
			// (their login files) goes away.
			closed := api.closeSessionsOnProviderAccount(id)
			if closed > 0 {
				log.Printf("[PROVIDER_ACCOUNT] closed %d session(s) on removed account %s", closed, id)
			}
			if home, err := providerConnectionHome(id); err == nil {
				_ = os.RemoveAll(filepath.Dir(home))
			}
			log.Printf("[PROVIDER_ACCOUNT] %s removed account %s (owner %s)", userID, id, target.OwnerUserID)
		} else if request.Sharing != nil {
			log.Printf("[PROVIDER_ACCOUNT] %s set sharing of account %s (owner %s): %s", userID, id, target.OwnerUserID, sharingSummary(sharing))
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Error(w, "connection unavailable", http.StatusNotFound)
}

func (api *StreamingAPI) updateServerAccount(w http.ResponseWriter, r *http.Request, provider string, admin bool) {
	if r.Method != http.MethodPatch {
		http.Error(w, "the server account cannot be removed here", http.StatusMethodNotAllowed)
		return
	}
	if !admin {
		writeWorkflowPermissionDenied(w, "admin")
		return
	}
	if !providerEnabled(provider) {
		http.Error(w, "provider is not enabled", http.StatusBadRequest)
		return
	}
	var request providerConnectionRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&request) != nil || len(request.AvailableTo) == 0 {
		http.Error(w, "available_to is required", http.StatusBadRequest)
		return
	}
	current, err := effectiveServerAccountAvailability(r.Context(), provider)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if current.Pinned {
		http.Error(w, "the installation pins who may use this account; change AGENTWORKS_PROVIDER_POLICY instead", http.StatusConflict)
		return
	}
	var next *providerAvailability
	if strings.TrimSpace(string(request.AvailableTo)) != "null" {
		next = &providerAvailability{}
		if err := json.Unmarshal(request.AvailableTo, next); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	providerAccountSettingsMu.Lock()
	defer providerAccountSettingsMu.Unlock()
	settings, err := loadProviderAccountSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if settings.AvailableTo == nil {
		settings.AvailableTo = map[string]*providerAvailability{}
	}
	if next == nil {
		delete(settings.AvailableTo, provider)
	} else {
		settings.AvailableTo[provider] = next
	}
	// A product default must stay usable by its whole product.
	effective := providerAvailability{All: true}
	if next != nil {
		effective = *next
	} else if policy, _ := installationProviderPolicy(); policy != nil {
		if entry, ok := policy[provider]; ok {
			effective = *entry.AvailableTo
		}
	}
	if defaults, err := effectiveProductDefaults(r.Context()); err == nil {
		for product, value := range defaults {
			if value.Provider == provider && !effective.admitsProduct(product) {
				http.Error(w, fmt.Sprintf("%s is the %s default; keep it available to %s or change that default first", provider, productDisplayName(product), productDisplayName(product)), http.StatusBadRequest)
				return
			}
		}
	}
	if err := saveProviderAccountSettings(r.Context(), settings); err != nil {
		http.Error(w, "cannot save provider settings", http.StatusInternalServerError)
		return
	}
	log.Printf("[PROVIDER_ACCOUNT] %s set who may use the %s server account", GetUserIDFromContext(r.Context()), provider)
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/provider-connections/share-targets lists what the caller may
// share an account with: workflows and Crews they can see, and people.
func (api *StreamingAPI) handleProviderShareTargets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	userID, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	type target struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Owner string `json:"owner,omitempty"`
		Email string `json:"email,omitempty"`
		Self  bool   `json:"self,omitempty"`
	}
	claims := principalClaims(userID)
	workflows := []target{}
	if discovered, err := DiscoverWorkflowManifests(r.Context()); err == nil {
		for _, item := range discovered {
			if item.Manifest == nil || strings.TrimSpace(item.Manifest.ID) == "" {
				continue
			}
			if !userAllowedWorkflowID(claims, item.Manifest.ID) || workflowAccessForManifest(claims, item.Manifest) == WorkflowAccessNone {
				continue
			}
			name := strings.TrimSpace(item.Manifest.Label)
			if name == "" {
				name = strings.TrimPrefix(item.WorkspacePath, "Workflow/")
			}
			workflows = append(workflows, target{ID: item.Manifest.ID, Name: name})
		}
	}
	crews := []target{}
	if api.agentProfiles != nil && userAllowedProduct(claims, productCrews) {
		if profile, err := api.agentProfiles.Resolve(crewProfileID, 0, userID); err == nil {
			owners := append([]string{sanitizeUserIDForPath(userID)}, crewProjectOwnerCandidates(userID)...)
			seen := map[string]bool{}
			for _, owner := range owners {
				for _, row := range listSharedProjectsForOwner(r.Context(), claims, profile, owner) {
					ref, ok := resolveCrewPath(r.Context(), userID, row.WorkspacePath)
					if !ok || seen[ref.Root] {
						continue
					}
					seen[ref.Root] = true
					name := strings.TrimSpace(row.Title)
					if name == "" {
						name = row.ID
					}
					crews = append(crews, target{ID: ref.Root, Name: name, Owner: logUsernameForUserID(row.OwnerID)})
				}
			}
		}
	}
	// People: the same list every signed-in user already sees in the
	// workflow and Code share dialogs (/api/users/directory: enabled
	// accounts), minus read-only accounts unless the caller is an admin.
	people := []target{}
	admin := currentUserIsAdmin(r)
	if dir, err := loadUserDirectory(); err == nil && dir != nil {
		for i := range dir.Users {
			rec := dir.Users[i]
			if rec.Disabled || (!admin && roleForRecord(&rec) == UserRoleViewer) {
				continue
			}
			// The caller is listed too, marked self: sharing your own account
			// hides it (you always have it), but an admin narrowing the shared
			// server account may pick themselves.
			people = append(people, target{ID: rec.ID, Name: rec.Username, Email: rec.Email, Self: rec.ID == userID})
		}
	}
	for _, list := range [][]target{workflows, crews, people} {
		sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"workflows": workflows, "crews": crews, "users": people})
}

// GET /api/provider-accounts/product-defaults shows each product's default
// provider, model and account. PUT sets the admin's defaults (products the
// installation pins stay as they are).
func (api *StreamingAPI) handleProductDefaults(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, ok := requireSignedIn(w, r); !ok {
		return
	}
	if r.Method == http.MethodPut {
		if !currentUserIsAdmin(r) {
			writeWorkflowPermissionDenied(w, "admin")
			return
		}
		var request struct {
			ProductDefaults map[string]*productDefault `json:"product_defaults"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&request) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		installed, err := installationProductDefaults()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		providerAccountSettingsMu.Lock()
		defer providerAccountSettingsMu.Unlock()
		settings, err := loadProviderAccountSettings(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if settings.ProductDefaults == nil {
			settings.ProductDefaults = map[string]productDefault{}
		}
		for rawProduct, value := range request.ProductDefaults {
			product, ok := normalizePolicyProduct(rawProduct)
			if !ok {
				http.Error(w, fmt.Sprintf("unknown product %q", rawProduct), http.StatusBadRequest)
				return
			}
			if installed[product].Pinned {
				http.Error(w, fmt.Sprintf("the installation pins the %s default", productDisplayName(product)), http.StatusConflict)
				return
			}
			if value == nil {
				delete(settings.ProductDefaults, product)
				continue
			}
			value.Provider, value.Model, value.Account, value.Pinned = strings.TrimSpace(value.Provider), strings.TrimSpace(value.Model), strings.TrimSpace(value.Account), false
			availability, err := effectiveServerAccountAvailability(r.Context(), value.Provider)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := validateProductDefault(product, *value, availability.AvailableTo); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := api.productDefaultAllowedByProfile(product, *value); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			settings.ProductDefaults[product] = *value
		}
		if err := saveProviderAccountSettings(r.Context(), settings); err != nil {
			http.Error(w, "cannot save provider settings", http.StatusInternalServerError)
			return
		}
	} else if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defaults, err := effectiveProductDefaults(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"product_defaults": productDefaultsResponse(defaults)})
}

// productDefaultsResponse is the wire shape: connection_id is always the
// provider's server account.
func productDefaultsResponse(defaults map[string]productDefault) map[string]map[string]any {
	out := make(map[string]map[string]any, len(defaults))
	for product, value := range defaults {
		out[product] = map[string]any{"provider": value.Provider, "model_id": value.Model, "connection_id": "global:" + value.Provider, "pinned": value.Pinned}
	}
	return out
}

// closeSessionsOnProviderAccount stops every retained session whose last
// turn ran on account id and returns how many it stopped.
func (api *StreamingAPI) closeSessionsOnProviderAccount(id string) int {
	type target struct{ session, provider string }
	targets := []target{}
	api.lastQueryMu.RLock()
	for session, request := range api.lastQueryRequests {
		provider, connectionID := queryTurnConnection(request)
		// The server account also runs every turn that names no account.
		serverTurn := strings.HasPrefix(id, "global:") && provider == strings.TrimPrefix(id, "global:") && connectionID == ""
		if connectionID == id || serverTurn {
			targets = append(targets, target{session, provider})
		}
	}
	api.lastQueryMu.RUnlock()
	for _, t := range targets {
		api.interruptWorkflowPolicySession(t.session, t.provider)
	}
	return len(targets)
}

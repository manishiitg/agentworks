package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
)

// Provider accounts (docs/design/provider_accounts.md).
//
// Two kinds of account per provider:
//   - the server account ("global:<provider>"): installed by the deployment
//     (.env, the CLI login in the service HOME) or admin-configured (a key
//     saved on the Providers page). Who may use it comes from the
//     installation policy AGENTWORKS_PROVIDER_POLICY, which an admin may
//     override from the UI unless the installation pins it;
//   - user accounts: added by a person, private or shared with workflows,
//     Crews and people.
//
// connectionAPIKeys admits an account for one run scope on every turn; a
// denied account errors and never falls back to another account.

const providerAccountSettingsPath = "config/provider-account-settings.json"

// Product IDs a policy or a product default may name. agentworks is
// workflows, work is Crews, code is Code.
const (
	productWorkflows = "agentworks"
	productCrews     = "work"
	productCode      = "code"
)

var providerAccountSettingsMu sync.Mutex

// ---- availability ----------------------------------------------------------

// providerAvailability is who may use a server account: everyone, admins,
// or any mix of products and people (email, username or user id).
type providerAvailability struct {
	All      bool
	Admins   bool
	Products []string
	Users    []string
}

type providerAvailabilityObject struct {
	Admins   bool     `json:"admins,omitempty"`
	Products []string `json:"products,omitempty"`
	Users    []string `json:"users,omitempty"`
}

func (a providerAvailability) MarshalJSON() ([]byte, error) {
	if a.All {
		return json.Marshal("all")
	}
	if a.Admins && len(a.Products) == 0 && len(a.Users) == 0 {
		return json.Marshal("admins")
	}
	return json.Marshal(providerAvailabilityObject{Admins: a.Admins, Products: a.Products, Users: a.Users})
}

func (a *providerAvailability) UnmarshalJSON(data []byte) error {
	var word string
	if json.Unmarshal(data, &word) == nil {
		switch strings.ToLower(strings.TrimSpace(word)) {
		case "all", "everyone":
			*a = providerAvailability{All: true}
			return nil
		case "admins":
			*a = providerAvailability{Admins: true}
			return nil
		}
		return fmt.Errorf(`available_to must be "all", "admins", or an object with products and users`)
	}
	var object providerAvailabilityObject
	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf(`available_to must be "all", "admins", or an object with products and users`)
	}
	result := providerAvailability{Admins: object.Admins}
	for _, product := range object.Products {
		normalized, ok := normalizePolicyProduct(product)
		if !ok {
			return fmt.Errorf("available_to names an unknown product %q (use agentworks, work or code)", strings.TrimSpace(product))
		}
		result.Products = appendUniqueFold(result.Products, normalized)
	}
	for _, user := range object.Users {
		if user = strings.TrimSpace(user); user != "" {
			result.Users = appendUniqueFold(result.Users, user)
		}
	}
	if !result.Admins && len(result.Products) == 0 && len(result.Users) == 0 {
		return fmt.Errorf("available_to names nobody; use \"admins\" to keep an account for admins only")
	}
	*a = result
	return nil
}

func normalizePolicyProduct(raw string) (string, bool) {
	product := strings.ToLower(strings.TrimSpace(raw))
	switch product {
	case "workflows", "workflow":
		product = productWorkflows
	case "crews", "crew":
		product = productCrews
	}
	if product == "" {
		return "", false
	}
	if product == productWorkflows || product == productCrews || product == productCode {
		return product, true
	}
	for _, known := range knownProductIDs() {
		if strings.EqualFold(known, product) {
			return product, true
		}
	}
	return "", false
}

func appendUniqueFold(values []string, value string) []string {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}

func productDisplayName(product string) string {
	switch strings.ToLower(product) {
	case productWorkflows:
		return "Workflows"
	case productCrews:
		return "Crews"
	case productCode:
		return "Code"
	}
	if product == "" {
		return product
	}
	return strings.ToUpper(product[:1]) + product[1:]
}

// Describe says who may use the account, in words for the UI.
func (a providerAvailability) Describe() string {
	if a.All {
		return "Everyone"
	}
	parts := []string{}
	if a.Admins {
		parts = append(parts, "Admins")
	}
	for _, product := range a.Products {
		parts = append(parts, productDisplayName(product))
	}
	parts = append(parts, a.Users...)
	switch len(parts) {
	case 0:
		return "Nobody"
	case 1:
		if a.Admins && len(a.Products) == 0 && len(a.Users) == 0 {
			return "Admins only"
		}
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
}

// admitsProduct reports whether every principal of product may use the
// account (what a product default needs).
func (a providerAvailability) admitsProduct(product string) bool {
	if a.All {
		return true
	}
	for _, candidate := range a.Products {
		if strings.EqualFold(candidate, product) {
			return true
		}
	}
	return false
}

func (a providerAvailability) admits(principal, product string) bool {
	if a.All {
		return true
	}
	if a.admitsProduct(product) && strings.TrimSpace(principal) != "" {
		return true
	}
	if strings.TrimSpace(principal) == "" {
		return false
	}
	if a.Admins && principalIsAdmin(principal) {
		return true
	}
	return principalMatchesAny(principal, a.Users)
}

// ---- principals ------------------------------------------------------------

// principalClaims builds claims for a run principal from the user directory,
// so identity-keyed grants (username, email) match as they do for requests.
func principalClaims(userID string) *UserClaims {
	userID = strings.TrimSpace(userID)
	claims := &UserClaims{UserID: userID}
	if rec := directoryUserFor(userID, "", ""); rec != nil {
		claims.Username = rec.Username
		claims.Email = rec.Email
	}
	return claims
}

// principalIsAdmin mirrors currentUserIsAdmin for a principal without a
// request (scheduled and triggered runs).
func principalIsAdmin(userID string) bool {
	claims := principalClaims(userID)
	acc := userAccessForClaims(claims)
	if acc.Known {
		return acc.Admin && !acc.Disabled
	}
	return acc.Admin || workflowPermissionInfoForClaims(claims).CanManageWorkflowAccess
}

func principalIdentities(userID string) []string {
	identities := []string{}
	add := func(value string) {
		if value = normalizeWorkflowPermissionKey(value); value != "" {
			identities = appendUniqueFold(identities, value)
		}
	}
	add(userID)
	if rec := directoryUserFor(strings.TrimSpace(userID), "", ""); rec != nil {
		add(rec.ID)
		add(rec.Username)
		add(rec.Email)
	}
	return identities
}

func principalMatchesAny(userID string, list []string) bool {
	if strings.TrimSpace(userID) == "" || len(list) == 0 {
		return false
	}
	identities := principalIdentities(userID)
	for _, entry := range list {
		entry = normalizeWorkflowPermissionKey(entry)
		for _, identity := range identities {
			if entry == identity {
				return true
			}
		}
	}
	return false
}

// ---- installation policy and admin settings --------------------------------

type providerPolicyEntry struct {
	AvailableTo *providerAvailability `json:"available_to"`
	Pinned      bool                  `json:"pinned,omitempty"`
}

// installationProviderPolicy reads AGENTWORKS_PROVIDER_POLICY. Absent means
// every provider is available to everyone (today's behaviour).
func installationProviderPolicy() (map[string]providerPolicyEntry, error) {
	raw := strings.TrimSpace(os.Getenv("AGENTWORKS_PROVIDER_POLICY"))
	if raw == "" {
		return nil, nil
	}
	var parsed map[string]providerPolicyEntry
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("AGENTWORKS_PROVIDER_POLICY is not valid: %w", err)
	}
	policy := make(map[string]providerPolicyEntry, len(parsed))
	for provider, entry := range parsed {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if entry.AvailableTo == nil {
			return nil, fmt.Errorf("AGENTWORKS_PROVIDER_POLICY: %s needs available_to", provider)
		}
		policy[provider] = entry
	}
	return policy, nil
}

type productDefault struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Account  string `json:"account,omitempty"`
	Pinned   bool   `json:"pinned,omitempty"`
}

// installationProductDefaults reads AGENTWORKS_PRODUCT_DEFAULTS.
func installationProductDefaults() (map[string]productDefault, error) {
	raw := strings.TrimSpace(os.Getenv("AGENTWORKS_PRODUCT_DEFAULTS"))
	if raw == "" {
		return nil, nil
	}
	var parsed map[string]productDefault
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("AGENTWORKS_PRODUCT_DEFAULTS is not valid: %w", err)
	}
	defaults := make(map[string]productDefault, len(parsed))
	for product, value := range parsed {
		normalized, ok := normalizePolicyProduct(product)
		if !ok {
			return nil, fmt.Errorf("AGENTWORKS_PRODUCT_DEFAULTS names an unknown product %q", product)
		}
		value.Provider = strings.TrimSpace(value.Provider)
		value.Model = strings.TrimSpace(value.Model)
		value.Account = strings.TrimSpace(value.Account)
		defaults[normalized] = value
	}
	return defaults, nil
}

// providerAccountSettings is the admin-editable, non-secret part: who may
// use each server account and each product's default. Stored in plain JSON.
type providerAccountSettings struct {
	AvailableTo     map[string]*providerAvailability `json:"available_to,omitempty"`
	ProductDefaults map[string]productDefault        `json:"product_defaults,omitempty"`
}

// The settings are cached in memory like the account registry: loaded once
// (at start, else on first use), updated in place on every save through the
// API. A read error with a cached value keeps the cached value.
var providerAccountSettingsCache struct {
	sync.Mutex
	url    string
	loaded bool
	value  providerAccountSettings
}

func cloneProviderAccountSettings(in providerAccountSettings) providerAccountSettings {
	out := providerAccountSettings{}
	if in.AvailableTo != nil {
		out.AvailableTo = make(map[string]*providerAvailability, len(in.AvailableTo))
		for key, value := range in.AvailableTo {
			if value != nil {
				copied := *value
				out.AvailableTo[key] = &copied
			}
		}
	}
	if in.ProductDefaults != nil {
		out.ProductDefaults = make(map[string]productDefault, len(in.ProductDefaults))
		for key, value := range in.ProductDefaults {
			out.ProductDefaults[key] = value
		}
	}
	return out
}

func loadProviderAccountSettings(ctx context.Context) (providerAccountSettings, error) {
	url := getWorkspaceAPIURL()
	providerAccountSettingsCache.Lock()
	cached, hasCache := providerAccountSettingsCache.value, providerAccountSettingsCache.loaded && providerAccountSettingsCache.url == url
	providerAccountSettingsCache.Unlock()
	if hasCache {
		return cloneProviderAccountSettings(cached), nil
	}
	settings := providerAccountSettings{}
	raw, exists, err := readFileFromWorkspace(ctx, providerAccountSettingsPath)
	if err != nil {
		return settings, err
	}
	if exists && strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &settings); err != nil {
			return providerAccountSettings{}, fmt.Errorf("invalid provider account settings")
		}
	}
	providerAccountSettingsCache.Lock()
	providerAccountSettingsCache.url, providerAccountSettingsCache.loaded, providerAccountSettingsCache.value = url, true, cloneProviderAccountSettings(settings)
	providerAccountSettingsCache.Unlock()
	return settings, nil
}

// preloadProviderAccountSettings fills the caches at server start so no
// turn reads them; a failure here only means the first use reads them.
func preloadProviderAccountSettings() {
	if _, err := loadProviderAccountSettings(context.Background()); err != nil {
		log.Printf("[PROVIDER_ACCOUNT] settings not loaded at start (will load on first use): %v", err)
	}
	if _, err := loadProviderConnections(context.Background()); err != nil {
		log.Printf("[PROVIDER_ACCOUNT] account registry not loaded at start (will load on first use): %v", err)
	}
}

func saveProviderAccountSettings(ctx context.Context, settings providerAccountSettings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileToWorkspace(ctx, providerAccountSettingsPath, string(data)); err != nil {
		return err
	}
	providerAccountSettingsCache.Lock()
	providerAccountSettingsCache.url, providerAccountSettingsCache.loaded, providerAccountSettingsCache.value = getWorkspaceAPIURL(), true, cloneProviderAccountSettings(settings)
	providerAccountSettingsCache.Unlock()
	return nil
}

// serverAccountAvailability is the effective "Available to" of a server
// account: a pinned installation entry, else the admin's setting, else the
// installation entry, else everyone.
type serverAccountAvailability struct {
	AvailableTo providerAvailability `json:"available_to"`
	Text        string               `json:"text"`
	Source      string               `json:"source"` // default | installation | admin
	Pinned      bool                 `json:"pinned"`
}

func effectiveServerAccountAvailability(ctx context.Context, provider string) (serverAccountAvailability, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	policy, err := installationProviderPolicy()
	if err != nil {
		return serverAccountAvailability{}, err
	}
	result := serverAccountAvailability{AvailableTo: providerAvailability{All: true}, Source: "default"}
	entry, hasEntry := policy[provider]
	if hasEntry {
		result.AvailableTo, result.Source, result.Pinned = *entry.AvailableTo, "installation", entry.Pinned
	}
	if !result.Pinned {
		// A cached value is always used; only a never-loaded settings file can
		// fail to read. Then the installation policy (or everyone, without
		// one) applies, so a deployment with no settings never fails a turn.
		settings, settingsErr := loadProviderAccountSettings(ctx)
		if settingsErr != nil {
			log.Printf("[PROVIDER_ACCOUNT] admin settings unreadable, using the installation policy for %s: %v", provider, settingsErr)
		}
		if override := settings.AvailableTo[provider]; override != nil {
			result.AvailableTo, result.Source = *override, "admin"
		}
	}
	result.Text = result.AvailableTo.Describe()
	return result, nil
}

// effectiveProductDefaults is the installation defaults with the admin's
// settings on top, except where the installation pins a product.
func effectiveProductDefaults(ctx context.Context) (map[string]productDefault, error) {
	installed, err := installationProductDefaults()
	if err != nil {
		return nil, err
	}
	result := make(map[string]productDefault, len(installed))
	for product, value := range installed {
		result[product] = value
	}
	settings, err := loadProviderAccountSettings(ctx)
	if err != nil {
		return result, err
	}
	for product, value := range settings.ProductDefaults {
		if current, ok := result[product]; ok && current.Pinned {
			continue
		}
		value.Pinned = false
		result[product] = value
	}
	return result, nil
}

// validateProductDefault checks a default against the enabled providers and
// the provider's effective availability for that product.
func validateProductDefault(product string, value productDefault, availability providerAvailability) error {
	if value.Provider == "" || value.Model == "" {
		return fmt.Errorf("the %s default needs a provider and a model", productDisplayName(product))
	}
	enabled := false
	for _, candidate := range getSupportedProviders() {
		if candidate == value.Provider {
			enabled = true
		}
	}
	if !enabled {
		return fmt.Errorf("the %s default names %s, which this server does not enable", productDisplayName(product), value.Provider)
	}
	if value.Account != "" && value.Account != "global:"+value.Provider {
		return fmt.Errorf("the %s default account must be the %s server account (global:%s)", productDisplayName(product), value.Provider, value.Provider)
	}
	if !availability.admitsProduct(product) {
		return fmt.Errorf("the %s default uses %s, but that provider's server account is not available to %s", productDisplayName(product), value.Provider, productDisplayName(product))
	}
	return nil
}

// validateProviderAccountInstallation runs at server start: a broken policy
// or a product default its provider's policy does not admit stops the
// server instead of failing runs later.
func validateProviderAccountInstallation() error {
	policy, err := installationProviderPolicy()
	if err != nil {
		return err
	}
	defaults, err := installationProductDefaults()
	if err != nil {
		return err
	}
	for product, value := range defaults {
		availability := providerAvailability{All: true}
		if entry, ok := policy[strings.ToLower(value.Provider)]; ok {
			availability = *entry.AvailableTo
		}
		if err := validateProductDefault(product, value, availability); err != nil {
			return fmt.Errorf("AGENTWORKS_PRODUCT_DEFAULTS: %w", err)
		}
	}
	return nil
}

// ---- run scope and admission -----------------------------------------------

// providerAccountScope is who runs a turn and where. The principal is the
// person who started the turn; scheduled and triggered runs use the workflow
// owner. WorkspacePath is the run's workflow folder, Crew root or Code root
// (any path inside it works). Product names the surface when the path does
// not (a product chat outside a project).
type providerAccountScope struct {
	Principal     string
	WorkspacePath string
	Product       string
}

// providerAccountRun is what admission needs to know about a scope.
type providerAccountRun struct {
	Product      string
	WorkflowID   string
	WorkflowPath string
	CrewRoot     string
	Label        string
}

func (api *StreamingAPI) describeProviderAccountRun(ctx context.Context, scope providerAccountScope) providerAccountRun {
	run := providerAccountRun{Product: strings.ToLower(strings.TrimSpace(scope.Product)), Label: "this run"}
	path := strings.Trim(filepath.ToSlash(strings.TrimSpace(scope.WorkspacePath)), "/")
	if path != "" {
		if ref, ok := resolveCrewPath(ctx, scope.Principal, path); ok {
			run.Product = productCrews
			run.CrewRoot = ref.Root
			run.Label = "Crew " + filepath.Base(ref.Root)
			return run
		}
		if isCodeProjectPath(path) {
			run.Product = productCode
			run.Label = "this Code"
			return run
		}
		if root := livefeed.WorkflowRoot(path); strings.HasPrefix(root, "Workflow/") {
			run.Product = productWorkflows
			run.WorkflowPath = root
			run.Label = "workflow " + strings.TrimPrefix(root, "Workflow/")
			if manifest, exists, err := ReadWorkflowManifest(ctx, root); err == nil && exists && manifest != nil {
				run.WorkflowID = strings.TrimSpace(manifest.ID)
				if label := strings.TrimSpace(manifest.Label); label != "" {
					run.Label = "workflow " + label
				}
			}
			return run
		}
	}
	if run.Product == "" {
		run.Product = productWorkflows
	}
	return run
}

// providerAccountUnavailable is the one error a denied account produces.
func providerAccountUnavailable(run providerAccountRun) error {
	return fmt.Errorf("this account is no longer available to %s", run.Label)
}

// admitProviderAccount decides whether scope may use account id of provider.
// It returns the stored user account (nil for the server account).
func (api *StreamingAPI) admitProviderAccount(ctx context.Context, scope providerAccountScope, provider, id string) (*storedProviderConnection, error) {
	// A model that names no account keeps today's provider rules; an account
	// named explicitly must belong to an enabled provider.
	if !strings.HasPrefix(id, llmguard.ServerDefaultConnectionPrefix) && !providerEnabled(provider) {
		return nil, fmt.Errorf("provider is not enabled")
	}
	run := api.describeProviderAccountRun(ctx, scope)
	if id == "" || strings.HasPrefix(id, "global:") || strings.HasPrefix(id, llmguard.ServerDefaultConnectionPrefix) {
		if id != "" && id != "global:"+provider && id != llmguard.ServerDefaultConnectionPrefix+provider {
			return nil, fmt.Errorf("provider connection does not match selected provider")
		}
		availability, err := effectiveServerAccountAvailability(ctx, provider)
		if err != nil {
			log.Printf("[PROVIDER_ACCOUNT] %s server account denied for %q: %v", provider, scope.Principal, err)
			return nil, fmt.Errorf("the %s server account policy cannot be read", provider)
		}
		if !availability.AvailableTo.admits(scope.Principal, run.Product) {
			log.Printf("[PROVIDER_ACCOUNT] %s server account denied for %q (%s): available to %s", provider, scope.Principal, run.Product, availability.Text)
			return nil, fmt.Errorf("the %s server account is not available to you here (available to: %s)", provider, availability.Text)
		}
		return nil, nil
	}
	principal := strings.TrimSpace(scope.Principal)
	if principal == "" {
		return nil, fmt.Errorf("provider connection requires an execution principal")
	}
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(ctx)
	providerConnectionsMu.Unlock()
	if err != nil {
		return nil, err
	}
	for i := range records {
		record := records[i]
		if record.ID != id {
			continue
		}
		if record.Provider != provider {
			return nil, fmt.Errorf("provider connection does not match selected provider")
		}
		if personalProviderConnectionsLocked(provider) {
			return nil, fmt.Errorf("personal provider connections are locked by administrator")
		}
		if how := api.userAccountAdmission(ctx, record, principal, run); how != "" {
			if how != "owner" {
				log.Printf("[PROVIDER_ACCOUNT] account %s (owner %s) admitted for %s via %s (%s)", record.ID, record.OwnerUserID, principal, how, run.Label)
			}
			return &record, nil
		}
		log.Printf("[PROVIDER_ACCOUNT] account %s (owner %s) denied for %s (%s)", record.ID, record.OwnerUserID, principal, run.Label)
		return nil, providerAccountUnavailable(run)
	}
	return nil, providerAccountUnavailable(run)
}

// userAccountAdmission returns how a user account is admitted for principal
// in run ("owner", "user", "workflow", "crew"), or "" when it is not.
func (api *StreamingAPI) userAccountAdmission(ctx context.Context, record storedProviderConnection, principal string, run providerAccountRun) string {
	if record.OwnerUserID == principal {
		return "owner"
	}
	sharing := record.Sharing
	if sharing == nil || sharing.Mode != providerSharingShared {
		return ""
	}
	if principalMatchesAny(principal, sharing.Users) {
		return "user"
	}
	claims := principalClaims(principal)
	if run.WorkflowID != "" && containsFold(sharing.Workflows, run.WorkflowID) {
		manifest, exists, err := ReadWorkflowManifest(ctx, run.WorkflowPath)
		if err == nil && exists && userAllowedWorkflowID(claims, run.WorkflowID) && workflowAccessForManifest(claims, manifest) != WorkflowAccessNone {
			return "workflow"
		}
	}
	if run.CrewRoot != "" {
		for _, shared := range sharing.Crews {
			ref, ok := resolveCrewPath(ctx, record.OwnerUserID, shared)
			if !ok || ref.Root != run.CrewRoot {
				continue
			}
			runRef, _ := resolveCrewPath(ctx, principal, run.CrewRoot)
			if crewAccessFor(claims, runRef) != crewAccessNone {
				return "crew"
			}
		}
	}
	return ""
}

func containsFold(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

// ---- sharing -----------------------------------------------------------------

const (
	providerSharingPrivate = "private"
	providerSharingShared  = "shared"
)

// ProviderConnectionSharing says who besides the owner may use a user
// account. Workflows are workflow IDs, Crews are Crew roots, users are
// user IDs.
type ProviderConnectionSharing struct {
	Mode      string   `json:"mode"`
	Workflows []string `json:"workflows,omitempty"`
	Crews     []string `json:"crews,omitempty"`
	Users     []string `json:"users,omitempty"`
}

// normalizeProviderSharing validates a sharing request against what the
// owner can see: only workflows, Crews and people visible to the owner may
// be named. A private account keeps no lists.
func (api *StreamingAPI) normalizeProviderSharing(ctx context.Context, ownerID string, request *ProviderConnectionSharing) (*ProviderConnectionSharing, error) {
	if request == nil {
		return nil, nil
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" || mode == providerSharingPrivate {
		return &ProviderConnectionSharing{Mode: providerSharingPrivate}, nil
	}
	if mode != providerSharingShared {
		return nil, fmt.Errorf("sharing mode must be private or shared")
	}
	if len(request.Workflows)+len(request.Crews)+len(request.Users) > 200 {
		return nil, fmt.Errorf("too many sharing entries")
	}
	owner := principalClaims(ownerID)
	result := &ProviderConnectionSharing{Mode: providerSharingShared}
	if len(request.Workflows) > 0 {
		discovered, err := DiscoverWorkflowManifests(ctx)
		if err != nil {
			return nil, fmt.Errorf("cannot list workflows")
		}
		for _, raw := range request.Workflows {
			id := strings.TrimSpace(raw)
			found := false
			for _, item := range discovered {
				if item.Manifest == nil || !strings.EqualFold(strings.TrimSpace(item.Manifest.ID), id) {
					continue
				}
				if userAllowedWorkflowID(owner, item.Manifest.ID) && workflowAccessForManifest(owner, item.Manifest) != WorkflowAccessNone {
					result.Workflows = appendUniqueFold(result.Workflows, item.Manifest.ID)
					found = true
				}
				break
			}
			if !found {
				return nil, fmt.Errorf("workflow %q is unavailable to you", id)
			}
		}
	}
	for _, raw := range request.Crews {
		ref, ok := resolveCrewPath(ctx, ownerID, raw)
		if !ok || ref.Rest != "" || crewAccessFor(owner, ref) == crewAccessNone {
			return nil, fmt.Errorf("crew %q is unavailable to you", strings.TrimSpace(raw))
		}
		result.Crews = appendUniqueFold(result.Crews, ref.Root)
	}
	for _, raw := range request.Users {
		raw = strings.TrimSpace(raw)
		rec := directoryUserFor(raw, raw, raw)
		if rec == nil || rec.Disabled {
			return nil, fmt.Errorf("person %q is not a known user", raw)
		}
		if rec.ID == ownerID {
			continue
		}
		result.Users = appendUniqueFold(result.Users, rec.ID)
	}
	if len(result.Workflows)+len(result.Crews)+len(result.Users) == 0 {
		return nil, fmt.Errorf("choose at least one workflow, Crew or person to share with, or keep the account private")
	}
	sort.Strings(result.Workflows)
	sort.Strings(result.Crews)
	sort.Strings(result.Users)
	return result, nil
}

// queryProviderAccountScope is the account scope of a /api/query turn: the
// signed-in principal, the workflow folder for a workflow run, else the
// selected folder (Crew or Code root), and the product of its profile.
func queryProviderAccountScope(req QueryRequest, principal string, profile *resolvedAgentProfile, isWorkflowPhase bool, workflowPhaseFolder string) providerAccountScope {
	scope := providerAccountScope{Principal: principal, WorkspacePath: req.SelectedFolder, Product: req.AgentProfileID}
	if isWorkflowPhase && strings.TrimSpace(workflowPhaseFolder) != "" {
		scope.WorkspacePath = workflowPhaseFolder
	}
	if profile != nil && strings.TrimSpace(profile.Definition.Product) != "" {
		scope.Product = profile.Definition.Product
	}
	return scope
}

// queryTurnConnection is the account a query names: the LLM config's
// primary connection, else the request's.
func queryTurnConnection(req QueryRequest) (provider, connectionID string) {
	provider, connectionID = strings.TrimSpace(req.Provider), strings.TrimSpace(req.ConnectionID)
	if req.LLMConfig != nil {
		if p := strings.TrimSpace(req.LLMConfig.Primary.Provider); p != "" {
			provider = p
		}
		if c := strings.TrimSpace(req.LLMConfig.Primary.ConnectionID); c != "" {
			connectionID = c
		}
	}
	return provider, connectionID
}

// queryTurnConnectionForSession is the account a turn runs on: what the
// request names, else what the session's last full request named (a
// lightweight follow-up to a retained CLI carries no model settings).
func (api *StreamingAPI) queryTurnConnectionForSession(req QueryRequest, sessionID string) (provider, connectionID string) {
	provider, connectionID = queryTurnConnection(req)
	if provider != "" || connectionID != "" || api == nil {
		return provider, connectionID
	}
	api.lastQueryMu.RLock()
	previous, ok := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	if ok {
		return queryTurnConnection(previous)
	}
	return "", ""
}

// finalQueryTurnConnection is the account a /api/query turn will run on
// after every override handleQuery applies: a workflow chat that does not
// override its manifest runs on the manifest's model and account (the same
// rule as handleQuery's workflow-phase block).
func (api *StreamingAPI) finalQueryTurnConnection(ctx context.Context, req QueryRequest, sessionID string) (provider, connectionID string) {
	provider, connectionID = api.queryTurnConnectionForSession(req, sessionID)
	if isGlobalLLMConfigLocked() || req.LLMConfig == nil || requestLLMConfigOverridesManifest(req) {
		return provider, connectionID
	}
	workspace := ""
	if strings.TrimSpace(req.PresetQueryID) != "" {
		if resolved, err := api.resolveWorkspacePathFromPreset(ctx, req.PresetQueryID); err == nil {
			workspace = resolved
		}
	}
	if workspace == "" {
		workspace = req.SelectedFolder
	}
	if strings.TrimSpace(workspace) == "" {
		return provider, connectionID
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !found || manifest == nil || manifest.Capabilities.LLMConfig == nil {
		return provider, connectionID
	}
	phaseLLM, _ := workshopResolveLLMConfig(lockedPresetLLMConfig(manifest.Capabilities.LLMConfig))
	if phaseLLM == nil || phaseLLM.Provider == "" || phaseLLM.ModelID == "" {
		return provider, connectionID
	}
	return phaseLLM.Provider, phaseLLM.ConnectionID
}

// delegationProviderAccountScope is a sub-agent's account scope: the
// principal of the parent turn and the parent's workspace and product.
func delegationProviderAccountScope(principal string, parentReq QueryRequest) providerAccountScope {
	return providerAccountScope{Principal: principal, WorkspacePath: parentReq.SelectedFolder, Product: parentReq.AgentProfileID}
}

// serverDefaultConnectionID is the marker a model naming no account resolves
// through (see llmguard.WithServerAccountAdmission).
func serverDefaultConnectionID(provider string) string {
	return llmguard.ServerDefaultConnectionPrefix + provider
}

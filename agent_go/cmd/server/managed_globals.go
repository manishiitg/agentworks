package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

const managedGlobalSecretsUserID = "_system_global_secrets"

var managedGlobalsMu sync.RWMutex
var managedGlobals = map[string]string{}

// managedGlobalsLoaded is set once the stored keys are read at startup, so "no company keys to move" (company_keys.go)
// is never concluded from a store that has not been read yet.
var managedGlobalsLoaded atomic.Bool
var globalSecretNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var errGlobalConflict = errors.New("A global secret with this name already exists")
var errGlobalAdmin = errors.New("Only server admins can manage global secrets")
var errGlobalNotFound = errors.New("Secret not found")

func managedGlobalAAD(name string) []byte { return []byte("agentworks:global-secret:" + name) }

func (api *StreamingAPI) loadManagedGlobalSecrets(ctx context.Context) error {
	managedGlobalsMu.Lock()
	defer managedGlobalsMu.Unlock()
	stored, err := api.chatStore.ListUserSecrets(ctx, managedGlobalSecretsUserID)
	if err != nil {
		return fmt.Errorf("load managed global secrets: %w", err)
	}
	next := map[string]string{}
	for _, secret := range stored {
		value, err := decryptSecretValueWithAAD(secret.EncryptedValue, managedGlobalAAD(secret.Name))
		if err != nil {
			return fmt.Errorf("cannot decrypt managed global secret %q", secret.Name)
		}
		next[secret.Name] = value
	}
	managedGlobals = next
	managedGlobalsLoaded.Store(true)
	return nil
}

func canManageGlobalSecrets(userID string) bool {
	if !productEnabled("mcp-gateway") {
		return false
	}
	access := userAccessForClaims(&UserClaims{UserID: userID})
	return access.Admin && !access.Disabled
}

// Persist first, then publish to the process-wide snapshot used by every
// existing global-secret consumer. Values never appear in API/tool responses.
func (api *StreamingAPI) saveManagedGlobalSecret(ctx context.Context, userID, name, value string, createOnly bool) (resultErr error) {
	defer func() {
		if resultErr == nil {
			if err := syncVaultSecretMetadata(ctx, userID); err != nil {
				resultErr = errors.New("Secret value was saved, but Vault permissions are unavailable. Refresh Vault before assigning access")
			}
		}
	}()
	if !canManageGlobalSecrets(userID) {
		return errGlobalAdmin
	}
	if isVaultSecretName(name) {
		return errors.New("Names starting with VLT_ are reserved for vault secrets")
	}
	return api.storeManagedGlobalSecret(ctx, name, value, createOnly)
}

// storeManagedGlobalSecret encrypts and stores one value in the managed global store, with no role check: the platform
// administrator's save and a vault owner's save (under the vault's reserved name) both call it after their own checks.
func (api *StreamingAPI) storeManagedGlobalSecret(ctx context.Context, name, value string, createOnly bool) error {
	if len(name) > 256 || !globalSecretNamePattern.MatchString(name) || value == "" {
		return errors.New("A valid secret name and non-empty value are required")
	}
	managedGlobalsMu.Lock()
	defer managedGlobalsMu.Unlock()
	if _, exists := managedGlobals[name]; exists && createOnly {
		return errGlobalConflict
	}
	encrypted, err := encryptSecretValueWithAAD(value, managedGlobalAAD(name))
	if err != nil {
		return errors.New("Could not encrypt global secret")
	}
	if err := api.chatStore.UpsertUserSecret(ctx, managedGlobalSecretsUserID, name, encrypted); err != nil {
		return errors.New("Could not persist global secret")
	}
	managedGlobals[name] = value
	return nil
}

// removeManagedGlobalSecretValue deletes a stored value with no role check (a vault owner removing a vault secret).
func (api *StreamingAPI) removeManagedGlobalSecretValue(ctx context.Context, name string) error {
	managedGlobalsMu.Lock()
	defer managedGlobalsMu.Unlock()
	if _, exists := managedGlobals[name]; !exists {
		return errGlobalNotFound
	}
	if err := api.chatStore.DeleteUserSecret(ctx, managedGlobalSecretsUserID, name); err != nil {
		return errors.New("Could not delete global secret")
	}
	delete(managedGlobals, name)
	return nil
}

func (api *StreamingAPI) deleteManagedGlobalSecret(ctx context.Context, userID, name string) error {
	if !canManageGlobalSecrets(userID) {
		return errGlobalAdmin
	}
	managedGlobalsMu.Lock()
	defer managedGlobalsMu.Unlock()
	if _, exists := managedGlobals[name]; !exists {
		return errGlobalNotFound
	}
	if err := revokeVaultSecret(ctx, userID, name); err != nil {
		return err
	}
	if err := api.chatStore.DeleteUserSecret(ctx, managedGlobalSecretsUserID, name); err != nil {
		return errors.New("Could not delete global secret")
	}
	delete(managedGlobals, name)
	return nil
}

func globalSecretError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, errGlobalAdmin) {
		status = http.StatusForbidden
	}
	if errors.Is(err, errGlobalConflict) {
		status = http.StatusConflict
	}
	if errors.Is(err, errGlobalNotFound) {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}

func (api *StreamingAPI) handleManageGlobalSecret(w http.ResponseWriter, r *http.Request) {
	userID := GetUserIDFromContext(r.Context())
	if !canManageGlobalSecrets(userID) {
		globalSecretError(w, errGlobalAdmin)
		return
	}
	if r.Method == http.MethodDelete {
		if err := api.deleteManagedGlobalSecret(r.Context(), userID, r.URL.Query().Get("name")); err != nil {
			globalSecretError(w, err)
			return
		}
	} else {
		var req struct {
			Name           string `json:"name"`
			EncryptedValue string `json:"encrypted_value"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		value, err := decryptSecretValue(req.EncryptedValue, userID)
		if err != nil {
			err = errors.New("Invalid encrypted secret")
		} else {
			err = api.saveManagedGlobalSecret(r.Context(), userID, req.Name, value, false)
		}
		if err != nil {
			globalSecretError(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// canRevealGlobalSecret allows server admins to reveal shared/global secrets
// and allows any signed-in user to reveal secrets in a personal Vault they own.
// External access tokens are never permitted to reveal secret values.
func canRevealGlobalSecret(ctx context.Context, userID, name string) bool {
	if claims := GetUserFromContext(ctx); claims != nil && claims.AccessToken != nil {
		return false
	}
	if canManageGlobalSecrets(userID) {
		return true
	}
	if !isVaultSecretName(name) {
		return false
	}
	list, err := personalVaults(ctx, userID)
	if err != nil {
		return false
	}
	for _, v := range list.Vaults {
		if v.Role == "owner" && slices.Contains(v.SecretNames, name) {
			return true
		}
	}
	return false
}

// handleRevealGlobalSecret returns one secret's value to an authorized viewer
// (server admins for global secrets, and vault owners for their own Vault
// secrets), so the Secrets UI can offer eye-icon reveal and copy on demand.
// GET /api/secrets/global/reveal?name=X
func (api *StreamingAPI) handleRevealGlobalSecret(w http.ResponseWriter, r *http.Request) {
	userID := GetUserIDFromContext(r.Context())
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		if !canManageGlobalSecrets(userID) {
			globalSecretError(w, errGlobalAdmin)
			return
		}
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if !canRevealGlobalSecret(r.Context(), userID, name) {
		globalSecretError(w, errGlobalAdmin)
		return
	}
	for _, secret := range sortedGlobalSecrets() {
		if secret.Name == name {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(secretDecryptResponse{Value: secret.Value})
			return
		}
	}
	globalSecretError(w, errGlobalNotFound)
}

func sortedGlobalSecrets() []globalSecretEntry {
	managedGlobalsMu.RLock()
	defer managedGlobalsMu.RUnlock()
	merged := map[string]globalSecretEntry{}
	for name, value := range managedGlobals {
		merged[name] = globalSecretEntry{Name: name, Value: value, Managed: true}
	}
	result := make([]globalSecretEntry, 0, len(merged))
	for _, secret := range merged {
		result = append(result, secret)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

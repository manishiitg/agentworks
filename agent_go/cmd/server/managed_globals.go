package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const managedGlobalSecretsUserID = "_system_global_secrets"

var managedGlobalsMu sync.RWMutex
var managedGlobals = map[string]string{}
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
	return nil
}

func canManageGlobalSecrets(userID string) bool {
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
	if len(name) > 128 || !globalSecretNamePattern.MatchString(name) || value == "" {
		return errors.New("A valid secret name and non-empty value are required")
	}
	managedGlobalsMu.Lock()
	defer managedGlobalsMu.Unlock()
	for _, secret := range globalSecrets {
		if secret.Name == name {
			return errors.New("This global is configured in the server environment; update it there")
		}
	}
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

func (api *StreamingAPI) deleteManagedGlobalSecret(ctx context.Context, userID, name string) error {
	if !canManageGlobalSecrets(userID) {
		return errGlobalAdmin
	}
	managedGlobalsMu.Lock()
	defer managedGlobalsMu.Unlock()
	for _, secret := range globalSecrets {
		if secret.Name == name {
			return errors.New("This global is configured in the server environment; remove it there")
		}
	}
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

func (api *StreamingAPI) promoteWorkflowSecret(ctx context.Context, userID, workspacePath, name string) error {
	if !canManageGlobalSecrets(userID) {
		return errGlobalAdmin
	}
	claims := &UserClaims{UserID: userID}
	_, readRoots, err := authorizeWorkflowContextPathsWithReadRoots(context.WithValue(ctx, UserContextKey, claims), []string{workspacePath})
	if err != nil {
		return errors.New("Source project or workflow is unavailable")
	}
	workspacePath = readRoots[0]
	secrets, err := api.ensureSharedWorkflowSecrets(ctx, workspacePath, userID)
	if err != nil {
		return errors.New("Could not read source secrets")
	}
	for _, secret := range secrets {
		if secret.Name != name {
			continue
		}
		value, err := decryptSharedWorkflowSecret(workspacePath, secret)
		if err != nil {
			return errors.New("Could not decrypt source secret")
		}
		if err := api.saveManagedGlobalSecret(ctx, userID, name, value, true); err != nil {
			return err
		}
		if err := api.deleteSharedWorkflowSecret(ctx, workspacePath, name, userID); err != nil {
			return errors.New("Global secret was saved, but the source copy could not be removed. Remove the workflow copy to finish promotion")
		}
		return nil
	}
	return errGlobalNotFound
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
			WorkspacePath  string `json:"workspace_path"`
			EncryptedValue string `json:"encrypted_value"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		var err error
		if r.Method == http.MethodPost {
			err = api.promoteWorkflowSecret(r.Context(), userID, req.WorkspacePath, req.Name)
		} else {
			var value string
			value, err = decryptSecretValue(req.EncryptedValue, userID)
			if err != nil {
				err = errors.New("Invalid encrypted secret")
			} else {
				err = api.saveManagedGlobalSecret(r.Context(), userID, req.Name, value, false)
			}
		}
		if err != nil {
			globalSecretError(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleRevealGlobalSecret returns one global's value to admins, so the
// Secrets UI can offer the same eye-icon reveal for globals that
// workflow boxes get through /api/secrets/decrypt. Covers managed and
// environment globals alike.
// GET /api/secrets/global/reveal?name=X
func (api *StreamingAPI) handleRevealGlobalSecret(w http.ResponseWriter, r *http.Request) {
	userID := GetUserIDFromContext(r.Context())
	if !canManageGlobalSecrets(userID) {
		globalSecretError(w, errGlobalAdmin)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
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
	for _, secret := range globalSecrets {
		merged[secret.Name] = secret
	}
	result := make([]globalSecretEntry, 0, len(merged))
	for _, secret := range merged {
		result = append(result, secret)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

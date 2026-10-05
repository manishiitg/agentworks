package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// vaultConfigured is false on servers without Vault (CAPLAYER_SERVICE_URL
// unset). They behave as before Vault existed: every shared secret is usable
// by every user, and nothing is registered or revoked anywhere. A URL that is set
// but unusable stays fail-closed.
func vaultConfigured() bool { return strings.TrimSpace(os.Getenv("CAPLAYER_SERVICE_URL")) != "" }

// The host holds encrypted values. Vault owns metadata and live group grants.
// There is no fallback from a failed permission lookup to server-wide access.
func vaultSecretAdminRequest(ctx context.Context, actor, method, path string, body any) error {
	target, token, err := capLayerServiceConfig()
	if err != nil {
		return errors.New("Vault service is not configured")
	}
	target.Path = strings.TrimRight(target.Path, "/") + path
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-CapLayer-Actor", actor)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: capLayerTransport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("Vault permissions unavailable")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("could not persist Vault secret permissions")
	}
	return nil
}

// Registers names and the managed flag only, never values. Vault grants a
// newly registered name to the Platform group unless an admin removed it there.
func syncVaultSecretMetadata(ctx context.Context, actor string) error {
	if !vaultConfigured() {
		return nil
	}
	rows := []map[string]any{}
	for _, s := range getGlobalSecrets() {
		// The gateway rejects the whole batch for a name it cannot store.
		if len(s.Name) > 128 {
			continue
		}
		// A person-owned vault's secrets are registered by the vault itself, never by this platform-wide sync, which
		// would otherwise grant a stray name to everyone (PLAT-507).
		if isVaultSecretName(s.Name) {
			continue
		}
		rows = append(rows, map[string]any{"name": s.Name, "managed": s.Managed})
	}
	return vaultSecretAdminRequest(ctx, actor, http.MethodPost, "/api/admin/secrets", map[string]any{"secrets": rows})
}

const vaultSecretRegistrationActor = "system:secret-registration"

// startVaultSecretRegistration registers shared secret names at backend start,
// without an administrator opening Vault > Secrets. The gateway may start after
// this process, so it retries (up to a minute apart) until it succeeds. Saving a
// managed secret registers again through syncVaultSecretMetadata.
func startVaultSecretRegistration() {
	if !vaultConfigured() {
		return
	}
	go func() {
		for attempt := 1; ; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := syncVaultSecretMetadata(ctx, vaultSecretRegistrationActor)
			cancel()
			if err == nil {
				log.Printf("[VAULT] registered %d shared secret names", len(getGlobalSecrets()))
				return
			}
			if attempt == 1 || attempt%10 == 0 {
				log.Printf("[VAULT] could not register shared secret names yet (attempt %d): %v", attempt, err)
			}
			delay := time.Duration(attempt) * 5 * time.Second
			if delay > time.Minute {
				delay = time.Minute
			}
			time.Sleep(delay)
		}
	}()
}
func permittedGlobalSecrets(ctx context.Context, userID string) ([]globalSecretEntry, error) {
	all := getGlobalSecrets()
	if len(all) == 0 {
		return []globalSecretEntry{}, nil
	}
	if !vaultConfigured() {
		return all, nil
	}
	data, err := vaultRuntimeRequest(ctx, userID, "/api/admin/runtime/secrets")
	if err != nil {
		return nil, err
	}
	var out struct {
		Secrets []struct {
			Name string `json:"name"`
		} `json:"secrets"`
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("invalid Vault secret permissions")
	}
	allowed := map[string]bool{}
	for _, s := range out.Secrets {
		allowed[s.Name] = true
	}
	rows := []globalSecretEntry{}
	for _, s := range all {
		if allowed[s.Name] {
			rows = append(rows, s)
		}
	}
	return rows, nil
}
func visibleGlobalSecrets(ctx context.Context, userID string) []globalSecretEntry {
	rows, _ := permittedGlobalSecrets(ctx, userID)
	return rows
}
func (api *StreamingAPI) mergeGlobalSecretsFor(ctx context.Context, userID string, scoped []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}, selection *[]string) []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
} {
	if selection == nil || len(*selection) == 0 {
		return scoped
	}
	rows := visibleGlobalSecrets(ctx, userID)
	wanted := map[string]bool{}
	for _, n := range *selection {
		wanted[n] = true
	}
	for _, s := range scoped {
		delete(wanted, s.Name)
	}
	for _, s := range rows {
		if wanted[s.Name] {
			scoped = append(scoped, struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}{s.Name, s.Value})
		}
	}
	return scoped
}

// Removing metadata and grants first ensures a failed value deletion cannot leave usable access.
func revokeVaultSecret(ctx context.Context, actor, name string) error {
	if !vaultConfigured() {
		return nil
	}
	return vaultSecretAdminRequest(ctx, actor, http.MethodDelete, "/api/admin/secrets/"+url.PathEscape(name), nil)
}
func (api *StreamingAPI) handleVaultSecretAccess(w http.ResponseWriter, r *http.Request) {
	actor := GetUserIDFromContext(r.Context())
	if !canManageGlobalSecrets(actor) || !userAllowedProduct(&UserClaims{UserID: actor}, "mcp-gateway") {
		writeUsersError(w, 403, "Vault management requires an administrator account")
		return
	}
	if r.Method == http.MethodGet {
		if err := syncVaultSecretMetadata(r.Context(), actor); err != nil {
			writeUsersError(w, 503, err.Error())
			return
		}
		writeUsersJSON(w, 200, map[string]any{"success": true})
		return
	}
	var in struct {
		GroupID string `json:"group_id"`
		Name    string `json:"name"`
		Allowed bool   `json:"allowed"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`).MatchString(in.GroupID) {
		writeUsersError(w, 400, "invalid group")
		return
	}
	if err := syncVaultSecretMetadata(r.Context(), actor); err != nil {
		writeUsersError(w, 503, err.Error())
		return
	}
	err := vaultSecretAdminRequest(r.Context(), actor, http.MethodPost, "/api/admin/groups/"+url.PathEscape(in.GroupID)+"/secrets", map[string]any{"name": in.Name, "allowed": in.Allowed})
	if err != nil {
		writeUsersError(w, 503, err.Error())
		return
	}
	writeUsersJSON(w, 200, map[string]any{"success": true})
}

// Workflow secrets are loaded later in handleQuery. The first admission check
// must use the same server-resolved workspace and encrypted project store,
// rather than treating the browser's empty decrypted list as authoritative.
func (api *StreamingAPI) validateQuerySecretSelection(ctx context.Context, userID string, req QueryRequest) error {
	scoped := req.DecryptedSecrets
	if (req.AgentMode == "workflow" || req.AgentMode == "workflow_phase") && req.SelectedGlobalSecrets != nil && len(*req.SelectedGlobalSecrets) > 0 {
		workspace := req.SelectedFolder
		// Phase execution prefers the preset; headless execution prefers the
		// explicit folder. Match the respective manifest-loading paths.
		if req.AgentMode == "workflow_phase" || strings.TrimSpace(workspace) == "" {
			if resolved, err := api.resolveWorkspacePathFromPreset(ctx, req.PresetQueryID); err == nil && resolved != "" {
				workspace = resolved
			}
		}
		scoped = api.loadSelectedSecrets(ctx, userID, workspace, *req.SelectedGlobalSecrets)
	}
	return validateVaultSecretSelection(ctx, userID, scoped, req.SelectedGlobalSecrets)
}

// A requested shared secret must resolve or the run stops before any execution.
// A same-named project secret retains the established project precedence.
func validateVaultSecretSelection(ctx context.Context, userID string, scoped []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}, selection *[]string) error {
	if selection == nil || len(*selection) == 0 {
		return nil
	}
	wanted := map[string]bool{}
	for _, name := range *selection {
		wanted[name] = true
	}
	for _, s := range scoped {
		delete(wanted, s.Name)
	}
	if len(wanted) == 0 {
		return nil
	}
	existing := map[string]bool{}
	for _, s := range getGlobalSecrets() {
		existing[s.Name] = true
	}
	// A selected name with no stored value anywhere does not stop the run
	// (owner decision 2026-10-05): $SECRET_<NAME> is empty and the workflow
	// shows a banner (missingSelectedSecrets) so the person can add it.
	for name := range wanted {
		if !existing[name] {
			delete(wanted, name)
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	rows, err := permittedGlobalSecrets(ctx, userID)
	if err != nil {
		return errors.New("Vault secret permissions are unavailable; execution was stopped")
	}
	for _, s := range rows {
		delete(wanted, s.Name)
	}
	for _, name := range *selection {
		if wanted[name] {
			return fmt.Errorf("Your groups do not have access to secret %q. Check Vault > Access", name)
		}
	}
	return nil
}

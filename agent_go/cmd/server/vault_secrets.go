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
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Vault is an internal runtime dependency even when its product screen is off.
// Local installations still require their explicit server-product opt-in.
func vaultConfigured() bool {
	return installationProductAvailable("mcp-gateway") && strings.TrimSpace(os.Getenv("CAPLAYER_SERVICE_URL")) != ""
}

// The host holds encrypted values. Vault owns metadata and live group grants.
// There is no fallback from a failed permission lookup to server-wide access.
func vaultSecretAdminRequest(ctx context.Context, actor, method, path string, body any) error {
	target, token, err := gatewayAdminServiceConfig()
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
	// Turning off a product must never turn off its authorization and thereby
	// expose formerly group-scoped values as legacy unrestricted globals.
	if !productEnabled("mcp-gateway") && !vaultConfigured() {
		return []globalSecretEntry{}, nil
	}
	all := getGlobalSecrets()
	if len(all) == 0 {
		return []globalSecretEntry{}, nil
	}
	if !vaultConfigured() {
		return all, nil
	}
	allowed, err := vaultPermittedSecretNames(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows := []globalSecretEntry{}
	for _, s := range all {
		if allowed[s.Name] {
			rows = append(rows, s)
		}
	}
	return rows, nil
}

// vaultPermittedSecretNames are the secret names Vault lets this person use, whether or not a value is stored yet.
func vaultPermittedSecretNames(ctx context.Context, userID string) (map[string]bool, error) {
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
	return allowed, nil
}

// secretNamesWithoutValue are the permitted names that have no stored value, sorted. Such a name exists in Vault but
// is not loaded, so a Builder's secret list would otherwise never show it and could not say why it cannot be selected.
func secretNamesWithoutValue(allowed map[string]bool, loaded []globalSecretEntry) []string {
	have := map[string]bool{}
	for _, entry := range loaded {
		if strings.TrimSpace(entry.Value) != "" {
			have[entry.Name] = true
		}
	}
	out := []string{}
	for name := range allowed {
		if !have[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// globalSecretNamesWithoutValue is that list for this person; empty when Vault is not in use or cannot be asked.
func globalSecretNamesWithoutValue(ctx context.Context, userID string) []string {
	if !vaultConfigured() {
		return []string{}
	}
	allowed, err := vaultPermittedSecretNames(ctx, userID)
	if err != nil {
		return []string{}
	}
	return secretNamesWithoutValue(allowed, getGlobalSecrets())
}

// lastGrantsLogUnix rate-limits the "runtime grants unavailable" line to one a minute: every run asks again.
var lastGrantsLogUnix atomic.Int64

func visibleGlobalSecrets(ctx context.Context, userID string) []globalSecretEntry {
	rows, err := permittedGlobalSecrets(ctx, userID)
	if err != nil {
		// Without the grants no promoted or shared secret resolves; say so once a minute (no names or values).
		if now := time.Now().Unix(); now-lastGrantsLogUnix.Load() >= 60 {
			lastGrantsLogUnix.Store(now)
			log.Printf("[SECRETS] runtime grants unavailable user=%s err=%v (promoted and shared secrets are not resolved for this run)", userID, err)
		}
	}
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
	taken := map[string]string{} // short name -> the full name that has it
	for _, s := range scoped {
		delete(wanted, s.Name)
		taken[s.Name] = s.Name
	}
	for _, s := range rows {
		if !wanted[s.Name] {
			continue
		}
		// A Vault secret reaches scripts under the name its owner gave it ($SECRET_GITHUB_TOKEN, not
		// $SECRET_VLT_<vault>__GITHUB_TOKEN; owner 2026-10-20), so moving a project secret into Vault keeps every
		// script working. The first secret with a name wins; the picker refuses a second one with the same name.
		name := vaultSecretShortName(s.Name)
		if prior, clash := taken[name]; clash {
			log.Printf("[SECRETS] Two selected secrets share the name %s; using %s, skipping %s", name, prior, s.Name)
			continue
		}
		taken[name] = s.Name
		scoped = append(scoped, struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{name, s.Value})
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
	// Crew owners build with the project's selected credentials, including
	// when their own personal Vault grants differ. Other callers retain their
	// existing admission identity; project access is checked by the turn.
	if isCrewProjectPath(req.SelectedFolder) && crewProjectOwnedByCaller(userID, req.SelectedFolder) {
		owner := api.projectSecretsOwner(userID, req.SelectedFolder)
		if owner != userID {
			log.Printf("[CREW_SECRETS] actor=%s owner=%s surface=chat", userID, owner)
		}
		userID = owner
	}
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

// projectSecretsOwner is the person whose Vault a project's selected secrets come from: the Crew's owner, for everyone
// who uses it (owner-lent, owner 2026-10-20: "if user is sharing with someone .. a crew.. and he know its personal
// mcps.. it upto the user"). For a Goal it is its registered creator (workflow_owner_registry.go); elsewhere the caller.
func (api *StreamingAPI) projectSecretsOwner(userID, workspacePath string) string {
	if owner, ok := crewProjectOwnerID(workspacePath); ok && strings.TrimSpace(owner) != "" {
		return owner
	}
	// A Goal's owner comes only from the server's own record, never from workflow.json (Builder turns can edit it).
	if owner := workflowOwner(workspacePath); owner != "" {
		return owner
	}
	return userID
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
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
	_, err := vaultSecretAdminCall(ctx, actor, method, path, body)
	return err
}

// vaultSecretAdminCall is vaultSecretAdminRequest that also returns the response body (up to 1 MiB).
func vaultSecretAdminCall(ctx context.Context, actor, method, path string, body any) ([]byte, error) {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	status, out, err := vaultServiceCall{Method: method, Path: path, Body: data, Actor: actor, Timeout: 10 * time.Second, Limit: 1 << 20}.do(ctx)
	switch {
	case errors.Is(err, errVaultServiceNotConfigured):
		return nil, err
	case errors.Is(err, errVaultServiceUnavailable):
		return nil, errors.New("Vault permissions unavailable")
	case err != nil || status < 200 || status >= 300:
		return nil, errors.New("could not persist Vault secret permissions")
	}
	return out, nil
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
// managed secret registers again through syncVaultSecretMetadata. once Vault answers, afterwards runs (the startup move
// of project secrets into their owners' Vaults).
func startVaultSecretRegistration(afterwards func()) {
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
				if afterwards != nil {
					afterwards()
				}
				if IsLocalMode() {
					// A Mac that turned its own Vault on keeps every shared secret it had: registration grants each
					// name to the Platform group, which this Mac's person is in.
					check, done := context.WithTimeout(context.Background(), 15*time.Second)
					rows, permErr := permittedGlobalSecrets(check, GetDefaultUserID())
					done()
					log.Printf("[VAULT] this Mac's person may use %d of %d shared secrets err=%v", len(rows), len(getGlobalSecrets()), permErr)
				}
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
	return withCompanyKeys(userID, rows, all), nil // company keys reach everyone (#4148)
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

// actingSecretPicks turns a project's chosen secrets into the ones this person can use: a chosen secret they can see
// stays; one in someone else's personal vault becomes the person's own secret with the same name (owner 19 Nov: everyone
// uses their own vault). Rows are the person's visible secrets (personal and shared vaults); the first match is used.
func actingSecretPicks(rows []globalSecretEntry, wanted map[string]bool) map[string]bool {
	visible := map[string]bool{}
	for _, s := range rows {
		visible[s.Name] = true
	}
	out := map[string]bool{}
	for name := range wanted {
		if visible[name] || !isVaultSecretName(name) {
			out[name] = true
			continue
		}
		short := vaultSecretShortName(name)
		for _, s := range rows {
			if isVaultSecretName(s.Name) && vaultSecretShortName(s.Name) == short {
				out[s.Name] = true
				break
			}
		}
	}
	return out
}

// actingSecretOrigin is the chosen name a mapped secret stands for (same short name), or "".
func actingSecretOrigin(wanted map[string]bool, mapped string) string {
	for name := range wanted {
		if name != mapped && isVaultSecretName(name) && vaultSecretShortName(name) == vaultSecretShortName(mapped) {
			return name
		}
	}
	return ""
}

// mergeGlobalSecretsFor resolves the selected Vault secrets userID may use, each under the name its owner gave it.
func (api *StreamingAPI) mergeGlobalSecretsFor(ctx context.Context, userID string, selection *[]string) []resolvedSecret {
	var scoped []resolvedSecret
	if selection == nil || len(*selection) == 0 {
		return scoped
	}
	ctx = withCrewVaultScopeForNames(ctx, *selection)
	reader := crewVaultReader(ctx, userID) // a User reads the Crew vault as its main owner; GitHub and Team keys stay their own
	rows := visibleGlobalSecrets(ctx, reader)
	if reader != userID {
		rows = crewVaultRowsOnly(ctx, rows)
	}
	wanted := map[string]bool{}
	for _, n := range *selection {
		wanted[n] = true
	}
	githubSecrets := map[string]bool{}
	for _, rec := range gitHubConnections(userID) {
		if wanted[rec.SecretName] {
			githubSecrets[rec.SecretName] = true
		}
	}
	if len(githubSecrets) > 1 {
		log.Printf("[SECRETS] multiple GitHub connections selected; none mapped to GITHUB_TOKEN")
	}
	wanted = actingSecretPicks(rows, wanted)
	taken := map[string]string{} // short name -> the full name that has it
	for _, s := range rows {
		if !wanted[s.Name] {
			continue
		}
		// A Vault secret reaches scripts under the name its owner gave it ($SECRET_GITHUB_TOKEN, not
		// $SECRET_VLT_<vault>__GITHUB_TOKEN; owner 2026-10-20). The first secret with a name wins; the picker refuses a
		// second one with the same name.
		name := vaultSecretShortName(s.Name)
		// A selected saved GitHub connection has a unique encrypted storage name,
		// but reaches its project's shell under the standard GitHub variable.
		if len(githubSecrets) == 1 && githubSecrets[s.Name] {
			name = "GITHUB_TOKEN"
		}
		if prior, clash := taken[name]; clash {
			log.Printf("[SECRETS] Two selected secrets share the name %s; using %s, skipping %s", name, prior, s.Name)
			continue
		}
		taken[name] = s.Name
		scoped = append(scoped, resolvedSecret{Name: name, Value: s.Value})
	}
	return scoped
}

// The first admission check, before handleQuery loads the workspace's settings: the selected secrets are checked
// against whoever lends them, as the turn resolves them (owner-lent).
func (api *StreamingAPI) validateQuerySecretSelection(ctx context.Context, userID string, req QueryRequest) error {
	workspace := req.SelectedFolder
	// Phase execution prefers the preset; headless execution prefers the explicit folder, as the manifest loading does.
	if req.AgentMode == "workflow_phase" || req.AgentMode == "workflow" && strings.TrimSpace(workspace) == "" {
		if resolved, err := api.resolveWorkspacePathFromPreset(ctx, req.PresetQueryID); err == nil && resolved != "" {
			workspace = resolved
		}
	}
	return validateWorkspaceSecretSelection(ctx, api.projectSecretsOwner(userID, workspace), workspace, req.SelectedGlobalSecrets)
}

// A requested Vault secret must resolve or the run stops before any execution.
func validateVaultSecretSelection(ctx context.Context, userID string, selection *[]string) error {
	missing, err := missingVaultSecrets(ctx, userID, selection)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("This uses the secret %s, which is not in your vault. Add your own %s in Vault (My accounts), or ask someone to put it in a shared vault you are in, then ask again", missing[0], missing[0])
	}
	return nil
}

// validateWorkspaceSecretSelection is the turn admission for a selection. In a Crew everyone uses their own Vault
// (owner 19 Nov), so a chosen secret the person does not have is skipped, not a refused turn: it never resolves
// (mergeGlobalSecretsFor injects only the person's visible secrets) and $SECRET_<NAME> stays empty, as for a chosen
// name with no stored value. Elsewhere a selection must resolve.
func validateWorkspaceSecretSelection(ctx context.Context, userID, workspacePath string, selection *[]string) error {
	if _, crew := crewProjectOwnerID(workspacePath); !crew {
		return validateVaultSecretSelection(ctx, userID, selection)
	}
	missing, err := missingVaultSecrets(ctx, userID, selection)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		log.Printf("[VAULT_SECRETS] event=crew_secret_skipped user=%s workspace=%s missing=%s", userID, workspacePath, strings.Join(missing, ","))
	}
	return nil
}

// crewSkippedSecretsNote tells the agent which of a Crew's chosen secrets the person lacks, so it can say so plainly
// instead of failing later with a confusing error. Empty when nothing is missing or outside a Crew.
func crewSkippedSecretsNote(ctx context.Context, userID, workspacePath string, selection *[]string) string {
	if _, crew := crewProjectOwnerID(workspacePath); !crew {
		return ""
	}
	missing, err := missingVaultSecrets(ctx, userID, selection)
	if err != nil || len(missing) == 0 {
		return ""
	}
	names := strings.Join(missing, ", ")
	return "\n\n[This project uses the secret(s) " + names + ", which are not in the vault of the person you are working for, so their $SECRET_ variables are empty in this chat. When a task needs one, or something fails for lack of it, tell the person once in plain words: this project uses NAME, which isn't in your vault, so I ran without it; add your own NAME in Vault (My accounts), or ask someone to put it in a shared vault you are in.]"
}

// missingVaultSecrets lists the short names of selected secrets the person cannot use. A name with no stored value
// anywhere is not missing (owner decision 2026-10-05).
func missingVaultSecrets(ctx context.Context, userID string, selection *[]string) ([]string, error) {
	if selection == nil || len(*selection) == 0 {
		return nil, nil
	}
	wanted := map[string]bool{}
	for _, name := range *selection {
		wanted[name] = true
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
		return nil, nil
	}
	ctx = withCrewVaultScopeForNames(ctx, *selection)
	reader := crewVaultReader(ctx, userID)
	rows, err := permittedGlobalSecrets(ctx, reader)
	if err != nil {
		log.Printf("[VAULT_SECRETS] permission lookup failed user=%s selected=%d err=%v", userID, len(*selection), err)
		return nil, errors.New("Vault secret permissions are unavailable; execution was stopped")
	}
	if reader != userID {
		rows = crewVaultRowsOnly(ctx, rows)
	}
	visible := map[string]bool{}
	for _, s := range rows {
		visible[s.Name] = true
	}
	for name := range actingSecretPicks(rows, wanted) {
		if visible[name] {
			delete(wanted, name)
			delete(wanted, actingSecretOrigin(wanted, name))
		}
	}
	var missing []string
	for _, name := range *selection {
		if wanted[name] {
			missing = append(missing, vaultSecretShortName(name))
		}
	}
	return missing, nil
}

// projectSecretsOwner is the person whose Vault a project's selected secrets and apps come from. In a Crew it is the
// person acting (owner 19 Nov: everyone uses their own personal vault plus the shared vaults they are in, the Crew's
// owner included; this replaces the 20-21 Oct owner-lent rule). A Crew automation acts as the person it runs for.
// For a Goal it is its registered creator (workflow_owner_registry.go); elsewhere the caller.
func (api *StreamingAPI) projectSecretsOwner(userID, workspacePath string) string {
	if _, crew := crewProjectOwnerID(workspacePath); crew {
		return userID
	}
	if binding, err := findCrewExecutable(workspacePath); err == nil && binding != nil {
		return userID // a Crew's own workflow
	}
	// A Goal's owner comes only from the server's own record, never from workflow.json (Builder turns can edit it).
	if owner := workflowOwner(workspacePath); owner != "" {
		return owner
	}
	return userID
}

// Removing metadata and grants first ensures a failed value deletion cannot leave usable access.
func revokeVaultSecret(ctx context.Context, actor, name string) error {
	if !vaultConfigured() {
		return nil
	}
	return vaultSecretAdminRequest(ctx, actor, http.MethodDelete, "/api/admin/secrets/"+url.PathEscape(name), nil)
}

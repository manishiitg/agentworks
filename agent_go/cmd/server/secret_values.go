package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
)

// Adding secret values from the CLI (`agentworks secret set/import`) and from the Vault tool
// (manage_vault_secret_access operation=set_value). Both write through the paths the screens already use and keep their
// permission rules: the Vault screen's administrator check for Vault, update_settings secrets.set (owner only) for a
// workflow, Relay or Crew. Values live in memory for the call; nothing here logs, returns or stores one outside the
// secret stores. Responses carry names and outcomes only.

const (
	maxSecretImportNames = 200
	maxSecretValueBytes  = 64 << 10
)

type secretValueResult struct {
	Name   string `json:"name"`
	Status string `json:"status"` // added, updated, skipped, would_add, would_update
	Reason string `json:"reason,omitempty"`
}

func secretValueError(name, value string) string {
	switch {
	case len(name) > 128 || !globalSecretNamePattern.MatchString(name):
		return "invalid name (letters, digits and underscores, starting with a letter or underscore)"
	case strings.TrimSpace(value) == "":
		return "empty value"
	case len(value) > maxSecretValueBytes:
		return "value too large"
	case strings.ContainsRune(value, 0):
		return "value contains a NUL byte"
	}
	return ""
}

func sortedSecretNames(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// importVaultSecrets stores values as Vault secrets for an administrator, exactly as Vault > Secrets does. Existing names
// are skipped unless overwrite is set.
func (api *StreamingAPI) importVaultSecrets(ctx context.Context, userID string, values map[string]string, overwrite, dryRun bool) ([]secretValueResult, error) {
	if !canManageGlobalSecrets(userID) {
		return nil, errGlobalAdmin
	}
	results := make([]secretValueResult, 0, len(values))
	for _, name := range sortedSecretNames(values) {
		value := values[name]
		if reason := secretValueError(name, value); reason != "" {
			results = append(results, secretValueResult{Name: name, Status: "skipped", Reason: reason})
			continue
		}
		managedGlobalsMu.RLock()
		_, existed := managedGlobals[name]
		managedGlobalsMu.RUnlock()
		switch {
		case existed && !overwrite:
			results = append(results, secretValueResult{Name: name, Status: "skipped", Reason: "already exists"})
		case dryRun && existed:
			results = append(results, secretValueResult{Name: name, Status: "would_update"})
		case dryRun:
			results = append(results, secretValueResult{Name: name, Status: "would_add"})
		default:
			err := api.saveManagedGlobalSecret(ctx, userID, name, value, !overwrite)
			managedGlobalsMu.RLock()
			_, stored := managedGlobals[name]
			managedGlobalsMu.RUnlock()
			status := "added"
			if existed {
				status = "updated"
			}
			switch {
			case err == nil:
				results = append(results, secretValueResult{Name: name, Status: status})
			case errors.Is(err, errGlobalConflict):
				results = append(results, secretValueResult{Name: name, Status: "skipped", Reason: "already exists"})
			case stored && !existed:
				// Saved, but the Vault permission refresh failed afterwards; the value is in.
				results = append(results, secretValueResult{Name: name, Status: status, Reason: err.Error()})
			default:
				results = append(results, secretValueResult{Name: name, Status: "skipped", Reason: err.Error()})
			}
		}
	}
	return results, nil
}

// callExternalToolInternal runs an external tool as the request's own signed-in person, through the same dispatcher MCP
// uses, so scopes, project access and the owner rule are the ones MCP applies. It returns the status and decoded body.
func (api *StreamingAPI) callExternalToolInternal(r *http.Request, name string, args map[string]any) (int, map[string]any) {
	payload, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	req := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", bytes.NewReader(payload)).WithContext(r.Context())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	recorder := httptest.NewRecorder()
	api.handleExternalCall(recorder, req)
	var body map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	return recorder.Code, body
}

func externalErrorMessage(body map[string]any, fallback string) string {
	if failure, ok := body["error"].(map[string]any); ok {
		if message, _ := failure["message"].(string); message != "" {
			return message
		}
	}
	return fallback
}

// importProjectSecrets stores values on a workflow, Relay or Crew through update_settings secrets.set (owner only, and
// the secret is selected for the project, as there). Existing names are skipped unless overwrite is set. A non-200
// answer returns its status and message.
func (api *StreamingAPI) importProjectSecrets(r *http.Request, kind, id string, values map[string]string, overwrite, dryRun bool) ([]secretValueResult, int, string) {
	key := "workflow_id"
	if kind == "crew" {
		key = "crew_id"
	}
	status, view := api.callExternalToolInternal(r, "get_settings", map[string]any{key: id})
	if status != http.StatusOK {
		return nil, status, externalErrorMessage(view, "Could not read that project's settings.")
	}
	stored := map[string]bool{}
	if secrets, ok := view["secrets"].(map[string]any); ok {
		rows, _ := secrets["stored"].([]any)
		for _, row := range rows {
			if entry, ok := row.(map[string]any); ok {
				if name, _ := entry["name"].(string); name != "" {
					stored[name] = true
				}
			}
		}
	}
	results := make([]secretValueResult, 0, len(values))
	toSet := map[string]any{}
	for _, name := range sortedSecretNames(values) {
		value := values[name]
		switch reason := secretValueError(name, value); {
		case reason != "":
			results = append(results, secretValueResult{Name: name, Status: "skipped", Reason: reason})
		case stored[name] && !overwrite:
			results = append(results, secretValueResult{Name: name, Status: "skipped", Reason: "already exists"})
		default:
			outcome := "added"
			if stored[name] {
				outcome = "updated"
			}
			if dryRun {
				outcome = "would_" + map[string]string{"added": "add", "updated": "update"}[outcome]
			} else {
				toSet[name] = value
			}
			results = append(results, secretValueResult{Name: name, Status: outcome})
		}
	}
	if len(toSet) > 0 {
		if status, body := api.callExternalToolInternal(r, "update_settings", map[string]any{key: id, "secrets": map[string]any{"set": toSet}}); status != http.StatusOK {
			return nil, status, externalErrorMessage(body, "The project's secrets could not be saved.")
		}
	}
	return results, http.StatusOK, ""
}

func summarizeSecretResults(results []secretValueResult) map[string]int {
	summary := map[string]int{}
	for _, result := range results {
		summary[result.Status]++
	}
	return summary
}

// handleSecretsImport is the person's own CLI route for `agentworks secret set/import`. Like the Brain secret route it takes
// only the CLI sign-in. The body is {"target":{"kind":"vault|workflow|relay|crew","id":"..."},"secrets":{NAME:value},
// "overwrite":false}; for dry_run send "names" instead of values.
func (api *StreamingAPI) handleSecretsImport(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+cliOAuthAccessPrefix) {
		externalError(w, http.StatusForbidden, "secret_refused", "Add secrets with `agentworks secret set` or `agentworks secret import` in your own terminal, or give the value to your agent to store with its secret tools.")
		return
	}
	claims := GetUserFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	var body struct {
		Target struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"target"`
		Secrets   map[string]string `json:"secrets"`
		Names     []string          `json:"names"`
		Overwrite bool              `json:"overwrite"`
		DryRun    bool              `json:"dry_run"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "target and secrets are required.")
		return
	}
	values := body.Secrets
	if values == nil {
		values = map[string]string{}
	}
	if body.DryRun {
		for _, name := range body.Names {
			if _, ok := values[name]; !ok {
				values[name] = "-"
			}
		}
	}
	if len(values) == 0 || len(values) > maxSecretImportNames {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Send between 1 and 200 secrets.")
		return
	}
	kind, id := strings.TrimSpace(body.Target.Kind), strings.TrimSpace(body.Target.ID)
	var results []secretValueResult
	switch kind {
	case "vault":
		var err error
		if results, err = api.importVaultSecrets(r.Context(), claims.UserID, values, body.Overwrite, body.DryRun); err != nil {
			status := http.StatusForbidden
			if !errors.Is(err, errGlobalAdmin) {
				status = http.StatusBadRequest
			}
			externalError(w, status, "forbidden", err.Error())
			return
		}
	case "workflow", "relay", "crew":
		if id == "" {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "A project id is required for "+kind+".")
			return
		}
		var status int
		var message string
		if results, status, message = api.importProjectSecrets(r, kind, id, values, body.Overwrite, body.DryRun); status != http.StatusOK {
			externalError(w, status, "secret_import_refused", message)
			return
		}
	default:
		externalError(w, http.StatusBadRequest, "invalid_arguments", "target.kind must be vault, workflow, relay or crew.")
		return
	}
	summary := summarizeSecretResults(results)
	if !body.DryRun {
		// User, target and counts only; never a name list and never a value.
		logSecretWrite(claims.UserID, kind+":"+id, len(results), summary, requestLogID(r))
	}
	externalJSON(w, map[string]any{"target": map[string]string{"kind": kind, "id": id}, "results": results, "summary": summary})
}

func logSecretWrite(userID, target string, names int, summary map[string]int, requestID string) {
	log.Printf("[SECRETS] user=%s target=%s names=%d added=%d updated=%d skipped=%d request=%s", userID, target, names, summary["added"], summary["updated"], summary["skipped"], requestID)
}

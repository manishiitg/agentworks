package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/knowledgebaseproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

// brain_admin is the MCP/CLI face of what the Brain page and Brain chat did alone (owner, 2026-10-12: people use their
// own agents for Brain, not the page): backup health and reconcile, Brain's own secrets, the person's Organize Brain
// schedule, and what's new since the person last looked. Every action re-uses the app's own checks.
const brainAdminTool = "brain_admin"

var brainAdminReadActions = map[string]bool{"backup_status": true, "secrets_list": true, "secrets_request_set": true, "schedule_status": true, "whats_new": true}
var brainAdminWriteActions = map[string]bool{
	"backup_reconcile": true, "secrets_set": true, "secrets_delete": true, "mark_seen": true,
	"schedule_enable": true, "schedule_disable": true, "schedule_set_cadence": true, "schedule_set_message": true, "schedule_run_now": true,
}

// brainAdminSensitive are the changes that need the person's own live connection.
var brainAdminSensitive = map[string]bool{"backup_reconcile": true, "secrets_set": true, "secrets_delete": true}

// brainAdminPersonOwn: the connection is the person's own, not a Brain service-account identity, an agent-bound token, a
// folder-capped or access-only connection.
func brainAdminPersonOwn(claims *UserClaims, p knowledgebase.Principal) bool {
	if t := claims.AccessToken; t != nil && (t.KnowledgebaseIdentityID != "" || t.BoundAgentID != "") {
		return false
	}
	return !p.AccessOnly && p.Caps == nil
}

func brainAdminToolAllowed(c *UserClaims) bool {
	if c == nil || !knowledgebaseProductAllowed(c) || !knowledgebaseMCPAllowed(c) {
		return false
	}
	return c.AccessToken == nil || c.AccessToken.Allows("knowledgebase:read")
}

func externalBrainAdminDefinition(add func(name, description string, write, scoped bool, props map[string]any, required ...string)) {
	actions := make([]any, 0, len(brainAdminReadActions)+len(brainAdminWriteActions))
	for _, name := range []string{"backup_status", "backup_reconcile", "secrets_list", "secrets_request_set", "secrets_set", "secrets_delete",
		"schedule_status", "schedule_enable", "schedule_disable", "schedule_set_cadence", "schedule_set_message", "schedule_run_now",
		"whats_new", "mark_seen"} {
		actions = append(actions, name)
	}
	add(brainAdminTool, "Brain administration without the app. backup_status: whether the Git backup is configured and healthy (pending files, last automatic push and its error). "+
		"backup_reconcile (administrators): re-check the backup against the repository. "+
		"secrets_list / secrets_set / secrets_delete: Vault-backed Brain secrets, for example the backup token named by brain_access configure_backup pat_secret; people who own the whole Brain only; names only, values are never returned. "+
		"A secret's VALUE never goes through an agent: secrets_request_set (name) returns the command the person types in their own terminal (agentworks brain secret set NAME, hidden input); then refer to the secret by name. "+
		"backup_reconcile and secrets_delete need the person's own connection. "+
		"schedule_status / schedule_enable / schedule_disable / schedule_set_cadence (cadence_hours) / schedule_set_message (message) / schedule_run_now: your own Organize Brain schedule. "+
		"whats_new: what changed in Brain since you last marked it seen (commits with the files added, updated or removed); mark_seen (commit) records the newest commit you read. "+
		"Reads need knowledgebase:read, changes need knowledgebase:write.", true, false, map[string]any{
		"value":         map[string]any{"type": "string", "minLength": 1, "maxLength": 65536, "description": "secrets_set: user-supplied value, encrypted by Vault and never returned."},
		"action":        map[string]any{"type": "string", "enum": actions},
		"name":          map[string]any{"type": "string", "maxLength": 128, "description": "secrets_request_set / secrets_delete: the secret's name (letters, digits, underscore; starts with a letter)."},
		"cadence_hours": map[string]any{"type": "integer", "minimum": minScheduleCadenceHours, "maximum": maxScheduleCadenceHours, "description": "schedule_set_cadence: hours between runs (24 = daily, 168 = weekly)."},
		"message":       map[string]any{"type": "string", "maxLength": 4000, "description": "schedule_set_message: what each scheduled run should do."},
		"commit":        map[string]any{"type": "string", "maxLength": 64, "description": "mark_seen: a Brain commit id (the head from whats_new)."},
		"limit":         map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "description": "whats_new: most recent commits to return (default 50)."},
	}, "action")
}

func (api *StreamingAPI) externalBrainAdminCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !brainAdminToolAllowed(claims) {
		externalError(w, http.StatusNotFound, "NOT_FOUND", "Brain not found.")
		return
	}
	action := strings.TrimSpace(externalArg(args, "action"))
	if !brainAdminReadActions[action] && !brainAdminWriteActions[action] {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Unknown brain_admin action.")
		return
	}
	if brainAdminWriteActions[action] && claims.AccessToken != nil && !claims.AccessToken.Allows("knowledgebase:write") {
		externalError(w, http.StatusForbidden, "insufficient_scope", "This connection does not allow Brain changes (knowledgebase:write).")
		return
	}
	service, err := knowledgebaseService()
	if err != nil {
		knowledgebaseHTTPError(w, r, "store_open", err)
		return
	}
	if err = knowledgebaseSyncIdentities(r.Context(), service); err != nil {
		knowledgebaseHTTPError(w, r, "identity_sync", err)
		return
	}
	principal := knowledgebasePrincipal(r, claims)
	ctx := r.Context()
	if brainAdminSensitive[action] {
		if !brainAdminPersonOwn(claims, principal) {
			externalError(w, http.StatusForbidden, "FORBIDDEN", "This change needs the person's own connection (not a Brain service account, an agent-bound or folder-capped connection).")
			return
		}
		if principal.Recheck != nil {
			if err := principal.Recheck(ctx); err != nil {
				knowledgebaseHTTPError(w, r, "principal_check", err)
				return
			}
		}
	}
	switch action {
	case "backup_status":
		result, err := service.Call(ctx, principal, "get_knowledgebase_backup_status", map[string]any{})
		if err != nil {
			knowledgebaseHTTPError(w, r, "git_backup_status", err)
			return
		}
		out := map[string]any{"backup": result}
		if last := service.LastAutoPush(); last != nil {
			out["last_automatic_push"] = last
		}
		knowledgebaseWriteJSON(w, map[string]any{"result": out})
	case "backup_reconcile":
		if !currentUserIsAdmin(r) {
			externalError(w, http.StatusForbidden, "FORBIDDEN", "Administrator access is required.")
			return
		}
		result, err := service.ReconcileBackup(ctx, principal)
		if err != nil {
			knowledgebaseHTTPError(w, r, "git_backup_reconcile", err)
			return
		}
		log.Printf("[BRAIN] backup reconcile run by %s over MCP", claims.UserID)
		knowledgebaseWriteJSON(w, map[string]any{"result": result})
	case "secrets_list", "secrets_request_set", "secrets_set", "secrets_delete":
		if !service.CanEditWholeBrain(principal) {
			externalError(w, http.StatusForbidden, "FORBIDDEN", "Brain's secrets are managed by people who own the whole Brain.")
			return
		}
		name := strings.TrimSpace(externalArg(args, "name"))
		switch action {
		case "secrets_set":
			value, _ := args["value"].(string)
			if err := api.setBrainSecret(ctx, name, value); err != nil {
				externalError(w, 400, "invalid_arguments", err.Error())
				return
			}
		case "secrets_request_set":
			if !brainSecretName.MatchString(name) {
				externalError(w, http.StatusBadRequest, "invalid_arguments", "secrets_request_set needs name: letters, digits and underscores, starting with a letter.")
				return
			}
			knowledgebaseWriteJSON(w, map[string]any{"result": map[string]any{
				"command": "agentworks brain secret set " + name,
				"note":    "The person runs this in their own terminal and types the value (hidden). It does not work from an agent's terminal. Then use the name, for example pat_secret=" + name + ".",
			}})
			return
		case "secrets_delete":
			if name == "" {
				externalError(w, http.StatusBadRequest, "invalid_arguments", "secrets_delete needs name.")
				return
			}
			if err := api.deleteSharedWorkflowSecret(ctx, brainSecretsPath, name, ""); err != nil {
				externalError(w, http.StatusBadRequest, "invalid_arguments", err.Error())
				return
			}
			log.Printf("[BRAIN] secret %q deleted by %s over MCP", name, claims.UserID) // the name only
		}
		names, err := api.brainSecretNames(ctx)
		if err != nil {
			externalError(w, http.StatusServiceUnavailable, "unavailable", "Could not list Brain's secrets.")
			return
		}
		knowledgebaseWriteJSON(w, map[string]any{"result": map[string]any{"secrets": names}})
	case "whats_new":
		identity := principal.IdentityID
		seen := service.SeenCommit(identity)
		browse := map[string]any{"action": "changes", "limit": 50}
		if limit, ok := args["limit"].(float64); ok && limit >= 1 && limit <= 200 {
			browse["limit"] = int(limit)
		}
		if seen != "" {
			browse["since"] = seen
		}
		result, err := knowledgebaseDispatch(ctx, service, principal, claims.UserID, knowledgebase.ToolBrowse, browse)
		if err != nil {
			knowledgebaseHTTPError(w, r, "git_history", err)
			return
		}
		knowledgebaseWriteJSON(w, map[string]any{"result": map[string]any{"last_seen": seen, "changes": result}})
	case "mark_seen":
		commit := strings.TrimSpace(externalArg(args, "commit"))
		// The commit must be one of Brain's own (the same check `since` makes), not just a well-formed id.
		if _, err := service.Call(ctx, principal, "list_knowledgebase_changes", map[string]any{"since": commit, "limit": 1}); err != nil {
			knowledgebaseHTTPError(w, r, "git_history", err)
			return
		}
		if err := service.SetSeenCommit(principal.IdentityID, commit); err != nil {
			knowledgebaseHTTPError(w, r, "seen_write", err)
			return
		}
		knowledgebaseWriteJSON(w, map[string]any{"result": map[string]any{"last_seen": service.SeenCommit(principal.IdentityID)}})
	default: // schedule_*
		api.externalBrainSchedule(w, r, action, args)
	}
}

func (api *StreamingAPI) externalBrainSchedule(w http.ResponseWriter, r *http.Request, action string, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if api == nil || api.projectActions == nil {
		externalError(w, http.StatusServiceUnavailable, "unavailable", "Brain schedules are not available on this server.")
		return
	}
	ps, ctx, userID := api.projectActions, r.Context(), claims.UserID
	jobID := productScheduleJobID(knowledgebaseproduct.ProfileID, "organize")
	var job productScheduleJob
	var err error
	switch strings.TrimPrefix(action, "schedule_") {
	case "status":
		job, err = ps.Job(ctx, userID, jobID)
	case "enable", "disable":
		job, err = ps.SetEnabled(ctx, userID, jobID, action == "schedule_enable")
	case "set_cadence":
		hours, ok := args["cadence_hours"].(float64)
		if !ok {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "schedule_set_cadence needs cadence_hours.")
			return
		}
		job, err = ps.SetCadence(ctx, userID, jobID, int(hours))
	case "set_message":
		message, _ := args["message"].(string)
		job, err = ps.SetMessage(ctx, userID, jobID, message)
	case "run_now":
		session, runErr := ps.Trigger(ctx, userID, jobID)
		if runErr != nil {
			externalError(w, http.StatusBadRequest, "invalid_arguments", runErr.Error())
			return
		}
		knowledgebaseWriteJSON(w, map[string]any{"result": map[string]any{"started": true, "session_id": session}})
		return
	default:
		externalError(w, http.StatusBadRequest, "invalid_arguments", fmt.Sprintf("Unknown schedule action %q.", action))
		return
	}
	if err != nil {
		externalError(w, http.StatusBadRequest, "invalid_arguments", err.Error())
		return
	}
	effective := job.Effective()
	knowledgebaseWriteJSON(w, map[string]any{"result": map[string]any{
		"enabled": effective.Enabled, "cadence_hours": effective.CadenceHours, "messages": effective.Messages,
		"last_run_at": job.State.LastRunAt, "last_status": job.State.LastStatus,
	}})
}

// handleBrainSecretSet sets one of Brain's own secrets for the person. It is a route, not a tool: it takes only the person's
// own CLI sign-in (the same rule as attaching a chat), so a secret's value never travels through an agent conversation or a
// connection an agent holds. The CLI reads the value with hidden input.
func (api *StreamingAPI) handleBrainSecretSet(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+cliOAuthAccessPrefix) {
		externalError(w, http.StatusForbidden, "secret_refused", "Set Brain secrets with `agentworks brain secret set NAME` in your own terminal, not through an agent.")
		return
	}
	claims := GetUserFromContext(r.Context())
	if !brainAdminToolAllowed(claims) || (claims.AccessToken != nil && !claims.AccessToken.Allows("knowledgebase:write")) {
		externalError(w, http.StatusForbidden, "insufficient_scope", "This sign-in does not allow Brain changes (knowledgebase:write).")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var body struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "name and value are required.")
		return
	}
	service, err := knowledgebaseService()
	if err != nil {
		knowledgebaseHTTPError(w, r, "store_open", err)
		return
	}
	if err = knowledgebaseSyncIdentities(r.Context(), service); err != nil {
		knowledgebaseHTTPError(w, r, "identity_sync", err)
		return
	}
	principal := knowledgebasePrincipal(r, claims)
	if !brainAdminPersonOwn(claims, principal) || !service.CanEditWholeBrain(principal) {
		externalError(w, http.StatusForbidden, "FORBIDDEN", "Brain's secrets are managed by people who own the whole Brain, on their own connection.")
		return
	}
	if principal.Recheck != nil {
		if err := principal.Recheck(r.Context()); err != nil {
			knowledgebaseHTTPError(w, r, "principal_check", err)
			return
		}
	}
	name := strings.TrimSpace(body.Name)
	if err := api.setBrainSecret(r.Context(), name, body.Value); err != nil {
		externalError(w, http.StatusBadRequest, "invalid_arguments", err.Error())
		return
	}
	log.Printf("[BRAIN] secret %q set by %s from the CLI", name, claims.UserID) // the name only, never the value
	names, err := api.brainSecretNames(r.Context())
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "unavailable", "Could not list Brain's secrets.")
		return
	}
	knowledgebaseWriteJSON(w, map[string]any{"result": map[string]any{"secrets": names}})
}

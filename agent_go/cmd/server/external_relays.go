package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// Relay product.yaml owns admission; these are adapters to existing services.
func externalRelayDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	id := map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9_-]{0,63}$"}
	key := map[string]any{"type": "string", "minLength": 1, "maxLength": 128}
	label := map[string]any{"type": "string", "minLength": 1, "maxLength": 80}
	add("create_relay", "Create a Relay you own, with object INPUT and enabled function trigger. Requires relays:write covering all accessible workflows and account create rights. Reuse submission_id on retries. Build the draft graph with builder_chat; creation does not publish.", false, false, map[string]any{"label": label, "submission_id": key, "output_step_id": id, "function": id}, "label", "submission_id")
	add("update_relay", "Rename a Relay or select its draft output agent. Requires Builder permission and owner/editor access. Published versions stay frozen.", true, true, map[string]any{"label": label, "output_step_id": id})
	add("get_relay_releases", "Inspect a Relay's active and previous published versions.", false, true, nil)
	add("publish_relay", "Publish the validated draft as an immutable Relay version using the existing publisher. Requires Builder permission and owner/editor access. Repeating an unchanged draft returns the same version.", true, true, nil)
	add("test_relay", "Run the current Relay draft with JSON input. Requires Builder permission and owner/editor access. Does not publish or use the active release. Reuse idempotency_key on retries; poll get_relay_run.", true, true, map[string]any{"function": externalString("Enabled function name, default process on creation."), "input": map[string]any{"type": "object"}, "idempotency_key": key}, "function", "input", "idempotency_key")
	add("run_relay", "Run a published Relay version with JSON input. Omit version for the active release. Requires runs:execute and current Relay/function access. Reuse idempotency_key on retries; poll get_relay_run. Does not create Run chat.", false, true, map[string]any{"function": externalString("Published function name."), "input": map[string]any{"type": "object"}, "version": map[string]any{"type": "string", "pattern": "^v[1-9][0-9]*$"}, "idempotency_key": key}, "function", "input", "idempotency_key")
	add("get_relay_run", "Poll your Relay run or draft test by durable run ID. Returns final JSON result, step outputs, status and version (empty for a draft test). Requires runs:execute.", false, true, map[string]any{"run_id": externalString("run_id from test_relay or run_relay.")}, "run_id")
}
func isExternalRelayTool(name string) bool {
	switch name {
	case "create_relay", "update_relay", "get_relay_releases", "publish_relay", "test_relay", "run_relay", "get_relay_run":
		return true
	}
	return false
}
func isExternalRelayAuthoringTool(name string) bool {
	switch name {
	case "create_relay", "update_relay", "publish_relay", "test_relay":
		return true
	}
	return false
}

// Reserve identity in private auth state before workspace writes. A retry after
// partial creation converges on the same Relay without overwriting its graph.
func (api *StreamingAPI) externalCreateRelay(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if claims != nil && claims.AccessToken != nil {
		live, err := activeExternalGrantClaims(r.Context(), claims.AccessToken.ID)
		if err != nil || live.UserID != claims.UserID {
			externalError(w, 403, "grant_unavailable", "Relay authoring grant is no longer active")
			return
		}
		claims = live
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, live))
	}
	if claims == nil || claims.AccessToken == nil || !claims.AccessToken.RelayBuilderAccess() || !claims.AccessToken.AllWorkflows || !userAccessForClaims(claims).CanCreate || !userAllowedProduct(claims, "relays") {
		externalError(w, 403, "forbidden", "Creation needs account create rights, Relay product access and relays:write consent covering all accessible workflows.")
		return
	}
	label := strings.TrimSpace(externalArg(args, "label"))
	if label == "" {
		externalError(w, 400, "invalid_arguments", "label cannot be blank")
		return
	}
	output := externalArg(args, "output_step_id")
	if output == "" {
		output = "answer"
	}
	function := externalArg(args, "function")
	if function == "" {
		function = "process"
	}
	identity := uuid.NewSHA1(uuid.NameSpaceURL, []byte(claims.UserID+"\x00"+claims.AccessToken.ID+"\x00"+externalArg(args, "submission_id"))).String()
	workspace := "Workflow/relay-" + identity
	workflowID := "wf_" + identity
	if !userAllowedWorkflowID(claims, workflowID) {
		externalError(w, 403, "forbidden", "The account workflow allowlist does not permit a new Relay.")
		return
	}
	lock := productConversationRegistryMutex("external-relay-create:" + identity)
	lock.Lock()
	defer lock.Unlock()
	store, err := openExternalBuilderStore()
	if err != nil {
		externalError(w, 503, "storage_unavailable", err.Error())
		return
	}
	defer store.Close()
	if _, err = store.db.ExecContext(r.Context(), `CREATE TABLE IF NOT EXISTS external_relay_creations (identity TEXT PRIMARY KEY,payload TEXT NOT NULL)`); err != nil {
		externalError(w, 503, "storage_unavailable", err.Error())
		return
	}
	fingerprint, _ := json.Marshal(map[string]string{"label": label, "output_step_id": output, "function": function})
	if _, err = store.db.ExecContext(r.Context(), `INSERT OR IGNORE INTO external_relay_creations(identity,payload) VALUES (?,?)`, identity, string(fingerprint)); err != nil {
		externalError(w, 503, "storage_unavailable", err.Error())
		return
	}
	var saved string
	if err = store.db.QueryRowContext(r.Context(), `SELECT payload FROM external_relay_creations WHERE identity=?`, identity).Scan(&saved); err != nil {
		externalError(w, 503, "storage_unavailable", err.Error())
		return
	}
	if saved != string(fingerprint) {
		externalError(w, 409, "submission_conflict", "submission_id was already used with another definition")
		return
	}
	manifest, found, err := ReadWorkflowManifest(r.Context(), workspace)
	if err != nil {
		externalError(w, 503, "workspace_unavailable", err.Error())
		return
	}
	if found {
		if manifest.Kind != "relay" || manifest.ID != workflowID || workflowAccessForManifest(claims, manifest) != WorkflowAccessOwner {
			externalError(w, 409, "creation_conflict", "Reserved Relay identity is unavailable")
			return
		}
		externalJSON(w, map[string]any{"workflow_id": manifest.ID, "manifest": manifest, "duplicate": true})
		return
	}
	manifest = NewWorkflowManifest(label)
	manifest.ID = workflowID
	manifest.Kind = "relay"
	manifest.CreatedBy = claims.UserID
	manifest.Access = &WorkflowAccess{Owners: []string{claims.UserID}, Readers: []string{}}
	manifest.RelayOutputStepID = output
	manifest.Capabilities.BrowserMode = "none"
	manifest.Capabilities.LLMConfig = productDefaultWorkflowLLMConfig(r.Context())
	manifest.Schedules = []WorkflowSchedule{{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity+"/function")).String(), Name: function, ScheduleType: "webhook", Kind: triggerKindFunction, Enabled: true, WorkshopMode: "run", GroupNames: []string{"default"}, Function: &WorkflowFunctionSpec{Name: function, Inputs: []WorkflowFunctionInput{{Name: "INPUT", Type: "object", Required: true}}}}}
	if err = ValidateManifest(manifest); err != nil {
		externalError(w, 400, "invalid_relay", err.Error())
		return
	}
	for _, folder := range []string{workspace, path.Join(workspace, "variables")} {
		if err = createWorkspaceFolder(r.Context(), folder); err != nil {
			externalError(w, 503, "workspace_unavailable", err.Error())
			return
		}
	}
	if err = writeFileToWorkspace(r.Context(), path.Join(workspace, "variables/variables.json"), `{"variables":[{"name":"INPUT","type":"object"}],"groups":[{"name":"default","enabled":true,"values":{"INPUT":"{}"}}]}`); err != nil {
		externalError(w, 503, "workspace_unavailable", err.Error())
		return
	}
	if err = WriteWorkflowManifest(r.Context(), workspace, manifest); err != nil {
		externalError(w, 503, "workspace_unavailable", err.Error())
		return
	}
	externalJSON(w, map[string]any{"workflow_id": manifest.ID, "manifest": manifest, "created": true, "next": "Use builder_chat to build the draft graph. Output agent ID: " + output + "; function: " + function + "."})
}

func (api *StreamingAPI) externalRelayCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any, selected DiscoveredWorkflow) {
	if selected.Manifest.Kind != "relay" || !userAllowedProduct(GetUserFromContext(r.Context()), "relays") {
		externalError(w, 404, "relay_not_found", "Relay does not exist or is not accessible.")
		return
	}
	if isExternalRelayAuthoringTool(name) {
		live, err := validateBuilderGrant(r.Context(), GetUserFromContext(r.Context()), selected.Manifest.ID, selected.WorkspacePath)
		if err != nil {
			externalError(w, 403, "builder_not_authorized", err.Error())
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, live))
	}
	forward := r.Clone(r.Context())
	forward.Header = r.Header.Clone()
	forward = mux.SetURLVars(forward, map[string]string{"id": selected.Manifest.ID, "run": externalArg(args, "run_id")})
	switch name {
	case "update_relay":
		if _, ok := args["label"]; !ok {
			if _, ok := args["output_step_id"]; !ok {
				externalError(w, 400, "invalid_arguments", "Supply label or output_step_id")
				return
			}
		}
		body := map[string]any{"workspace_path": selected.WorkspacePath}
		if label, ok := args["label"]; ok {
			body["label"] = label
		}
		if output, ok := args["output_step_id"]; ok {
			body["relay_output_step_id"] = output
		}
		externalRelayForwardJSON(w, forward, body, api.handleUpdateWorkflowManifest)
	case "publish_relay":
		release, err := publishRelayRelease(r.Context(), selected.WorkspacePath)
		if err != nil {
			externalError(w, 400, "publish_refused", err.Error())
			return
		}
		externalJSON(w, release)
	case "get_relay_releases":
		externalBuilderForward(w, forward, api.handleListRelayReleases)
	case "run_relay", "test_relay":
		if api.scheduler == nil {
			externalError(w, 503, "scheduler_unavailable", "Relay scheduler is unavailable")
			return
		}
		body := map[string]any{"function": args["function"], "input": args["input"], "idempotency_key": args["idempotency_key"]}
		if version, ok := args["version"]; ok {
			body["version"] = version
		}
		externalRelayForwardJSON(w, forward, body, func(w http.ResponseWriter, r *http.Request) { api.startRelayRun(w, r, name == "test_relay") })
	case "get_relay_run":
		if api.scheduler == nil {
			externalError(w, 503, "scheduler_unavailable", "Relay scheduler is unavailable")
			return
		}
		externalBuilderForward(w, forward, api.handleGetRelayRun)
	}
}
func externalRelayForwardJSON(w http.ResponseWriter, r *http.Request, body any, handler http.HandlerFunc) {
	data, err := json.Marshal(body)
	if err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	r.Method = http.MethodPost
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	r.Header.Set("Content-Type", "application/json")
	externalBuilderForward(w, r, handler)
}

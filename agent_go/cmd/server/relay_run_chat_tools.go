package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"

	"github.com/gorilla/mux"
)

// The Flow Run chat (856-78): everyone with access to a Flow who is not its Builder chats in Run mode. They read the
// published source and releases, see every run and result (one company per server), and run a published version. They
// cannot test a draft, publish, edit files, or use a shell, MCP servers or secrets.
const (
	relayReadToolName     = "read_relay"
	relayListRunsToolName = "list_relay_runs"
	relayGetRunToolName   = "get_relay_run"
	relayRunToolName      = "run_relay"
	relayRunSourceLimit   = 64 * 1024
)

// relayRunChatTools is the whole custom-tool surface of a Flow Run chat; it matches the run surface in the Flow product
// manifest, which the tool gate enforces.
var relayRunChatTools = []string{relayReadToolName, relayListRunsToolName, relayGetRunToolName, relayRunToolName}

func (api *StreamingAPI) registerRelayRunChatTools(registrar definitionToolRegistrar, callerID, workspace string) error {
	// access resolves the person and the Flow on every call, so a revoked share ends the chat's reach at once.
	access := func(ctx context.Context) (*UserClaims, *WorkflowManifest, error) {
		claims := userClaimsForDirectoryID(strings.TrimSpace(callerID))
		if claims == nil {
			return nil, nil, fmt.Errorf("your account cannot use this Flow")
		}
		manifest, found, err := ReadWorkflowManifest(ctx, workspace)
		if err != nil || !found || manifest.Kind != "relay" || workflowAccessForManifest(claims, manifest) == WorkflowAccessNone {
			return nil, nil, fmt.Errorf("you can no longer use this Flow")
		}
		return claims, manifest, nil
	}
	version := map[string]interface{}{"type": "string", "pattern": "^v[1-9][0-9]*$", "description": "Published version such as v2. Omit for the active release."}
	jsonResult := func(v interface{}) (string, error) {
		data, err := json.Marshal(v)
		return string(data), err
	}
	if err := registrar.RegisterCustomTool(relayReadToolName,
		"Read this Flow's published side. action=source returns relay.py of a published version (default the active one); action=releases lists the active and previous published versions with their functions. Never the draft.",
		map[string]interface{}{"type": "object", "required": []string{"action"}, "additionalProperties": false, "properties": map[string]interface{}{
			"action":  map[string]interface{}{"type": "string", "enum": []string{"source", "releases"}},
			"version": version,
		}},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			_, manifest, err := access(ctx)
			if err != nil {
				return "", err
			}
			switch action, _ := args["action"].(string); action {
			case "releases":
				releases, err := listRelayReleases(ctx, workspace)
				if err != nil {
					return "", err
				}
				active, _, activeErr := activeRelayRelease(ctx, workspace)
				out := map[string]interface{}{"releases": releases}
				if activeErr == nil && active != nil {
					out["active"] = active.Version
				}
				return jsonResult(out)
			case "source":
				requested, _ := args["version"].(string)
				release, releaseWorkspace, err := resolveRelayRelease(ctx, workspace, strings.TrimSpace(requested))
				if err != nil {
					return "", fmt.Errorf("no published version to read: %w", err)
				}
				source, exists, err := readFileFromWorkspace(ctx, path.Join(releaseWorkspace, "relay.py"))
				if err != nil || !exists {
					return "", fmt.Errorf("this version has no relay.py to read")
				}
				truncated := len(source) > relayRunSourceLimit
				if truncated {
					source = source[:relayRunSourceLimit]
				}
				// A secret value pasted into the source stays private, as in runs.
				masked, _ := maskSecretValues(source, api.relaySecretValues(ctx, workspace, manifest)).(string)
				return jsonResult(map[string]interface{}{"relay": manifest.Label, "version": release.Version, "functions": release.Functions, "relay_py": masked, "truncated": truncated})
			}
			return "", fmt.Errorf("action must be source or releases")
		}, "relay_run_tools"); err != nil {
		return err
	}
	if err := registrar.RegisterCustomTool(relayListRunsToolName,
		"List recorded runs of this Flow, newest first, whoever started them: run id, caller, status, errors, start time, duration, step names and statuses, and the returned result. Pass version for a published version (default: the active release).",
		map[string]interface{}{"type": "object", "additionalProperties": false, "properties": map[string]interface{}{
			"version": version,
			"limit":   map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 50},
		}},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			claims, manifest, err := access(ctx)
			if err != nil {
				return "", err
			}
			limit := 20
			if n, ok := args["limit"].(float64); ok && n >= 1 && n <= 50 {
				limit = int(n)
			}
			ver, err := api.relayRunChatVersion(ctx, workspace, args)
			if err != nil {
				return "", err
			}
			out, _, message := relayRecordedRuns(ctx, claims, workspace, manifest, dashboardFileGuard(claims), limit, ver)
			if message != "" {
				return "", fmt.Errorf("%s", message)
			}
			return jsonResult(api.maskRelaySecrets(ctx, workspace, manifest, out))
		}, "relay_run_tools"); err != nil {
		return err
	}
	if err := registrar.RegisterCustomTool(relayGetRunToolName,
		"Read one run of this Flow by run id or folder name, whoever started it: input, status, error, every step with timings, errors and output, the returned result and its log lines.",
		map[string]interface{}{"type": "object", "required": []string{"run"}, "additionalProperties": false, "properties": map[string]interface{}{
			"run":     map[string]interface{}{"type": "string", "description": "run_id or folder name from list_relay_runs."},
			"version": version,
		}},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			claims, manifest, err := access(ctx)
			if err != nil {
				return "", err
			}
			run, _ := args["run"].(string)
			ver, err := api.relayRunChatVersion(ctx, workspace, args)
			if err != nil {
				return "", err
			}
			out, _, message := relayRecordedRun(ctx, claims, workspace, manifest, dashboardFileGuard(claims), strings.TrimSpace(run), ver)
			if message != "" {
				return "", fmt.Errorf("%s", message)
			}
			return jsonResult(api.maskRelaySecrets(ctx, workspace, manifest, out))
		}, "relay_run_tools"); err != nil {
		return err
	}
	return registrar.RegisterCustomTool(relayRunToolName,
		"Run a published version of this Flow with a JSON input and return its run id (poll with "+relayGetRunToolName+"). The run is yours: it is recorded under your name and uses the owner's published code and credentials. Reuse idempotency_key after an uncertain retry; never run twice for the same work. Only published versions: there is no draft test here.",
		map[string]interface{}{"type": "object", "required": []string{"function", "input", "idempotency_key"}, "additionalProperties": false, "properties": map[string]interface{}{
			"function":        map[string]interface{}{"type": "string", "description": "Published function name (see read_relay releases)."},
			"input":           map[string]interface{}{"type": "object"},
			"version":         version,
			"idempotency_key": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128},
		}},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			claims, manifest, err := access(ctx)
			if err != nil {
				return "", err
			}
			if api.scheduler == nil {
				return "", fmt.Errorf("the Flow runner is unavailable")
			}
			body := map[string]interface{}{"function": args["function"], "input": args["input"], "idempotency_key": args["idempotency_key"]}
			if v, ok := args["version"].(string); ok && strings.TrimSpace(v) != "" {
				body["version"] = strings.TrimSpace(v)
			}
			data, err := json.Marshal(body)
			if err != nil {
				return "", err
			}
			// The same start path as the API: function grants, idempotency scoped to the caller, concurrency limits.
			req := httptest.NewRequest(http.MethodPost, "/api/relays/"+manifest.ID+"/runs", strings.NewReader(string(data)))
			req = mux.SetURLVars(req, map[string]string{"id": manifest.ID})
			req = req.WithContext(context.WithValue(ctx, UserContextKey, claims))
			rec := httptest.NewRecorder()
			api.startRelayRun(rec, req, false)
			if rec.Code >= 300 {
				return "", fmt.Errorf("%s", strings.TrimSpace(rec.Body.String()))
			}
			return rec.Body.String(), nil
		}, "relay_run_tools")
}

// relayRunChatVersion is the published version a list or read refers to: the one asked for, else the active release, never
// the draft (draft test runs belong to the Builder).
func (api *StreamingAPI) relayRunChatVersion(ctx context.Context, workspace string, args map[string]interface{}) (string, error) {
	if v, ok := args["version"].(string); ok && strings.TrimSpace(v) != "" {
		v = strings.TrimSpace(v)
		if !relayVersionPattern.MatchString(v) {
			return "", fmt.Errorf("version must look like v2")
		}
		return v, nil
	}
	release, _, err := activeRelayRelease(ctx, workspace)
	if err != nil || release == nil {
		return "", fmt.Errorf("this Flow has no published version yet")
	}
	return release.Version, nil
}

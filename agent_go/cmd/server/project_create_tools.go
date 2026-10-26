package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Creation is account-scoped. The containing chat never lends its owner's
// identity or expands the authenticated caller's connection consent.
func projectCreateAllowed(c *UserClaims, kind string) bool {
	if c == nil || c.UserID == "" || userAccessForClaims(c).Disabled {
		return false
	}
	switch kind {
	case "goal":
		return externalWorkflowCreationAllowed(c)
	case "crew":
		status, _ := projectCreationGate(c, crewProfileID)
		return status == 0
	case "relay":
		return userAllowedProduct(c, "relays") && userAccessForClaims(c).CanCreateIn("relays") && userAccessForClaims(c).CanEdit && (c.AccessToken == nil || c.AccessToken.RelayBuilderAccess() && c.AccessToken.AllWorkflows)
	}
	return false
}

func projectCreationAvailable(c *UserClaims) bool {
	return projectCreateAllowed(c, "goal") || projectCreateAllowed(c, "crew") || projectCreateAllowed(c, "relay")
}

func projectCreateSpecSchema(kind string) map[string]any {
	switch kind {
	case "goal":
		raw, _ := json.Marshal(workflowCreatorToolSchema())
		var schema map[string]any
		_ = json.Unmarshal(raw, &schema)
		schema["additionalProperties"] = false
		return schema
	case "crew":
		props, _, _ := externalCrewSpecSchemas()
		return map[string]any{"type": "object", "properties": props, "required": []any{"name", "purpose"}, "additionalProperties": false}
	case "relay":
		return map[string]any{"type": "object", "properties": map[string]any{"label": externalString("Flow name."), "function": externalString("Initial function name; defaults to process.")}, "required": []any{"label"}, "additionalProperties": false}
	}
	return nil
}

func projectCreateDefinition() externalTool {
	return externalTool{Name: "create_project", Description: platformtools.ProjectActions[0].Description, mutates: true, InputSchema: map[string]any{
		"type": "object", "additionalProperties": false, "required": []any{"kind", "spec", "idempotency_key"},
		"properties": map[string]any{
			"kind":            map[string]any{"type": "string", "enum": []any{"goal", "crew", "relay"}},
			"spec":            map[string]any{"anyOf": []any{projectCreateSpecSchema("goal"), projectCreateSpecSchema("crew"), projectCreateSpecSchema("relay")}},
			"idempotency_key": map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "One key per intended project. Reuse it only for an unchanged retry, including from another chat or MCP."},
		},
	}}
}

func validateProjectArguments(schema map[string]any, args map[string]any) error {
	// Native schemas may contain Go slices; normalize just as the MCP catalog does.
	raw, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	var doc any
	if err = json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	c := jsonschema.NewCompiler()
	if err = c.AddResource("https://example.com/project-tool.json", doc); err != nil {
		return err
	}
	v, err := c.Compile("https://example.com/project-tool.json")
	if err != nil {
		return err
	}
	return v.Validate(args)
}

func (api *StreamingAPI) externalCreateProject(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if claims != nil && claims.AccessToken != nil {
		live, err := activeExternalGrantClaims(r.Context(), claims.AccessToken.ID)
		if err != nil || live.UserID != claims.UserID {
			externalError(w, 403, "grant_unavailable", "This connection is no longer active. Sign in again.")
			return
		}
		claims = live
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, live))
	}
	kind := externalArg(args, "kind")
	if !projectCreateAllowed(claims, kind) {
		externalError(w, 403, "forbidden", "This account or connection cannot create that kind of project. Use a Builder connection with create permission.")
		return
	}
	if err := validateProjectArguments(projectCreateDefinition().InputSchema, args); err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	spec, _ := args["spec"].(map[string]any)
	if err := validateProjectArguments(projectCreateSpecSchema(kind), spec); err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	// Existing creators normalize their input; never mutate the native call
	// arguments used by an unchanged retry.
	rawSpec, _ := json.Marshal(spec)
	var clonedSpec map[string]any
	_ = json.Unmarshal(rawSpec, &clonedSpec)
	spec = clonedSpec
	identity := uuid.NewSHA1(uuid.NameSpaceURL, []byte("project-create\x00"+claims.UserID+"\x00"+externalArg(args, "idempotency_key"))).String()
	payload, _ := json.Marshal(map[string]any{"kind": kind, "spec": spec})
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(payload))
	lock := productConversationRegistryMutex("project-create:" + identity)
	lock.Lock()
	defer lock.Unlock()
	store, err := openExternalBuilderStore()
	if err != nil {
		externalFailure(w, err)
		return
	}
	defer store.Close()
	if _, err = store.db.ExecContext(r.Context(), `CREATE TABLE IF NOT EXISTS project_creations (identity TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, response TEXT NOT NULL DEFAULT '')`); err != nil {
		externalFailure(w, err)
		return
	}
	if _, err = store.db.ExecContext(r.Context(), `INSERT OR IGNORE INTO project_creations(identity,fingerprint) VALUES (?,?)`, identity, fingerprint); err != nil {
		externalFailure(w, err)
		return
	}
	var previous, response string
	if err = store.db.QueryRowContext(r.Context(), `SELECT fingerprint,response FROM project_creations WHERE identity=?`, identity).Scan(&previous, &response); err != nil {
		externalFailure(w, err)
		return
	}
	if previous != fingerprint {
		externalError(w, 409, "idempotency_conflict", "This key already names a different creation request. Use a new key for a new project.")
		return
	}
	if response != "" {
		var out map[string]any
		if err = json.Unmarshal([]byte(response), &out); err != nil {
			externalFailure(w, err)
			return
		}
		out["duplicate"] = true
		externalJSON(w, out)
		return
	}
	result := httptest.NewRecorder()
	switch kind {
	case "goal":
		api.externalCreateWorkflow(result, r, spec)
	case "relay":
		input := map[string]any{}
		for k, v := range spec {
			input[k] = v
		}
		input["submission_id"] = identity
		api.externalCreateRelay(result, r, input)
	case "crew":
		var value crewSpec
		if err = remarshalExternalArgs(spec, &value); err != nil {
			externalError(w, 400, "invalid_arguments", err.Error())
			return
		}
		id, setupErr := api.createWorkspaceFromSpec(r.Context(), claims, crewProfileID, value, true, identity)
		if id == "" && setupErr != nil {
			externalError(w, 400, "creation_failed", externalCrewCreateError(id, setupErr))
			return
		}
		out := map[string]any{"workspace_id": crewProfileID + ":" + id, "crew_id": id, "id": id, "name": value.Name}
		if setupErr != nil {
			out["warnings"] = []string{projectCreationWarning(setupErr)}
		}
		externalJSON(result, out)
	}
	if result.Code >= 400 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(result.Code)
		_, _ = w.Write(result.Body.Bytes())
		return
	}
	var out map[string]any
	if err = json.Unmarshal(result.Body.Bytes(), &out); err != nil {
		externalFailure(w, err)
		return
	}
	// Keep only the creation receipt, never a manifest or caller-supplied files.
	receipt := map[string]any{"kind": kind, "created": true}
	for _, key := range []string{"workflow_id", "workspace_id", "crew_id", "id", "name", "warnings"} {
		if v, ok := out[key]; ok {
			receipt[key] = v
		}
	}
	encoded, _ := json.Marshal(receipt)
	if _, err = store.db.ExecContext(r.Context(), `UPDATE project_creations SET response=? WHERE identity=?`, string(encoded), identity); err != nil {
		externalFailure(w, err)
		return
	}
	externalJSON(w, receipt)
}

// The same action member is projected into workspace.create in every native
// chat. The external workspace service embeds this definition for action=create.
func (api *StreamingAPI) registerProjectTools(reg definitionToolRegistrar, gate *productToolGate, userID string, caller *UserClaims) error {
	if err := api.registerProjectCopyTool(reg, gate, userID, caller); err != nil {
		return err
	}
	if !projectCreationAvailable(caller) {
		return nil
	}
	def := projectCreateDefinition()
	if gate != nil {
		gate.Declare(def.Name)
	}
	return reg.RegisterCustomTool(def.Name, def.Description, def.InputSchema, func(ctx context.Context, args map[string]any) (string, error) {
		if caller == nil || caller.UserID != userID {
			return "", fmt.Errorf("Open your own chat to create a project")
		}
		if current := GetUserFromContext(ctx); current != nil && current.UserID != userID {
			return "", fmt.Errorf("Open your own chat to create a project")
		}
		r := httptest.NewRequest(http.MethodPost, "/api/external/call", nil).WithContext(context.WithValue(ctx, UserContextKey, caller))
		w := httptest.NewRecorder()
		api.externalCreateProject(w, r, args)
		if w.Code >= 400 {
			return "", fmt.Errorf("%s", strings.TrimSpace(w.Body.String()))
		}
		return w.Body.String(), nil
	}, "workspace_advanced")
}

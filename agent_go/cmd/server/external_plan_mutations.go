package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func externalPlanMutationDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	actions, schemas := []any{"list"}, []any{}
	for _, tool := range stepworkflow.ExternalBuilderPlanToolDefinitions() {
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			panic(err)
		}
		actions = append(actions, tool.Name)
		schemas = append(schemas, schema)
	}
	add("plan", "Edit a workflow plan directly with the existing native Builder validators and plan changelog. action=list returns exact operation schemas; otherwise action is a listed native name and arguments matches that schema. Read get_plan for expected_revision covering plan and configuration. Every mutation needs a unique request_id: identical retries return its durable receipt. Existing Builder authoring consent and current editable owner/editor/team roles apply; readers cannot edit. Flow source uses write_file and relay tools. No model turn or chat is adopted.", true, true, map[string]any{
		"action":            map[string]any{"type": "string", "enum": actions},
		"arguments":         map[string]any{"anyOf": schemas},
		"expected_revision": externalString("Combined revision returned by get_plan."),
		"request_id":        map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
	}, "action")
}

func externalPlanRevision(ctx context.Context, root string) (string, error) {
	var revisions strings.Builder
	for _, p := range externalPlanPaths {
		file, err := externalFileRequest(ctx, wf.Request{Root: root, Operation: "read", Path: p})
		if err != nil {
			return "", err
		}
		revisions.WriteString(p + ":" + file.Revision + "\n")
	}
	return wf.Revision([]byte(revisions.String())), nil
}

func (api *StreamingAPI) externalPlanMutationCall(w http.ResponseWriter, r *http.Request, args map[string]any, workflow DiscoveredWorkflow) {
	claims := GetUserFromContext(r.Context())
	if workflow.Manifest.Kind == "relay" {
		externalError(w, 400, "wrong_product", "Flow uses typed relay tools and write_file for its Python source.")
		return
	}
	if !externalFileAuthoringAllowed(claims, workflow.Manifest) {
		externalError(w, 403, "forbidden", "Current editable workflow authoring authority is required.")
		return
	}
	live, err := validateBuilderGrant(r.Context(), claims, workflow.Manifest.ID, workflow.WorkspacePath)
	if err != nil {
		externalError(w, 403, "forbidden", "Current editable workflow authoring authority is required.")
		return
	}
	ctx := context.WithValue(r.Context(), UserContextKey, live)
	action := externalArg(args, "action")
	if action == "list" {
		if args["arguments"] != nil || args["expected_revision"] != nil || args["request_id"] != nil {
			externalError(w, 400, "invalid_arguments", "list takes no mutation arguments.")
			return
		}
		externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "tools": stepworkflow.ExternalBuilderPlanToolDefinitions()})
		return
	}
	expected, requestID := externalArg(args, "expected_revision"), externalArg(args, "request_id")
	if expected == "" || requestID == "" {
		externalError(w, 400, "invalid_arguments", "expected_revision and request_id are required for plan edits.")
		return
	}
	params, _ := args["arguments"].(map[string]any)
	if params == nil {
		externalError(w, 400, "invalid_arguments", "arguments must match the selected native schema.")
		return
	}
	rootPath := workflow.WorkspacePath
	rootClean, e := wf.CleanRelative(rootPath)
	if e != nil || rootClean != rootPath || len(strings.Split(rootPath, "/")) != 2 || !strings.HasPrefix(rootPath, "Workflow/") {
		externalError(w, 400, "invalid_workspace", "One canonical workflow root is required.")
		return
	}
	base, err := os.OpenRoot(getWorkspaceDocsAbsPath())
	if err != nil {
		externalError(w, 503, "workspace_unavailable", "Direct plan commits require the managed workspace mount.")
		return
	}
	defer base.Close()
	root, err := wf.OpenDirectory(base, rootPath, false)
	if err != nil {
		externalFailure(w, err)
		return
	}
	defer root.Close()
	release, err := teamLock(ctx, rootPath)
	if err != nil {
		externalFailure(w, err)
		return
	}
	defer release()
	ctx = context.WithValue(ctx, teamLockHeldKey{}, rootPath)
	// Re-resolve after acquiring the lock; queued calls cannot retain stale
	// connection consent, account access or project/team membership.
	live, err = validateBuilderGrant(ctx, claims, workflow.Manifest.ID, rootPath)
	if err != nil {
		externalError(w, 403, "forbidden", "Workflow authoring authority changed.")
		return
	}
	ctx = context.WithValue(ctx, UserContextKey, live)
	current, found, err := ReadWorkflowManifest(ctx, rootPath)
	if err != nil || !found || !externalFileAuthoringAllowed(live, current) || current.Kind == "relay" {
		externalError(w, 403, "forbidden", "Current workflow authoring authority changed.")
		return
	}
	if current.Team == nil && api.findRunningTrackedExecutionForWorkspaceWhere(rootPath, trackedExecutionBlocksNewWorkflowBuilderChat) != nil {
		externalError(w, 409, "workflow_busy", "Wait for the current Builder turn before editing this workflow directly.")
		return
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		externalFailure(w, err)
		return
	}
	defer store.Close()
	_, err = store.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS external_plan_receipts (
 user_id TEXT NOT NULL, connection_id TEXT NOT NULL, request_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL, operation_id TEXT NOT NULL, status TEXT NOT NULL,
 response TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
 PRIMARY KEY(user_id,connection_id,request_id))`)
	if err != nil {
		externalFailure(w, err)
		return
	}
	encoded, _ := json.Marshal(args)
	fingerprint := wf.Revision(encoded)
	var oldFingerprint, status, response string
	err = store.db.QueryRowContext(ctx, `SELECT fingerprint,status,response FROM external_plan_receipts WHERE user_id=? AND connection_id=? AND request_id=?`, live.UserID, live.AccessToken.ID, requestID).Scan(&oldFingerprint, &status, &response)
	if err == nil {
		if oldFingerprint != fingerprint {
			externalError(w, 409, "request_conflict", "request_id was used for a different edit.")
			return
		}
		if status == "failed" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(response))
			return
		}
		if status != "completed" {
			externalError(w, 409, "edit_requires_reconciliation", "This edit may have been interrupted; read the plan and changelog before submitting a new request.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		externalFailure(w, err)
		return
	}
	revision, err := externalPlanRevision(ctx, rootPath)
	if err != nil {
		externalFailure(w, err)
		return
	}
	if revision != expected {
		externalError(w, 409, "revision_conflict", "Plan or configuration changed; reread get_plan and reconcile.")
		return
	}
	plan, err := externalFileRequest(ctx, wf.Request{Root: rootPath, Operation: "read", Path: "planning/plan.json"})
	if err != nil {
		externalFailure(w, err)
		return
	}
	ctx = prepareTeamEdit(teamUserContext(ctx, live.UserID), rootPath, teamRevision(plan.Content))
	if action == "migrate_workflow_skills" {
		if current.Team != nil && !containsID(current.effectiveOwners(), live.UserID) {
			externalError(w, 403, "forbidden", "Shared workflow settings require the Leader.")
			return
		}
		ctx = context.WithValue(ctx, workflowSkillsEditKey{}, true)
		ctx = context.WithValue(ctx, teamManifestPermitKey{}, true)
	}
	operationID := uuid.NewString()
	ctx = stepworkflow.WithExternalBuilderPlanOrigin(ctx, operationID, live.AccessToken.ID, live.UserID, live.Username, "")
	ctx = stepworkflow.WithExternalPlanSelectedServers(ctx, current.Capabilities.RuntimeServers())
	var preparationError error
	relative := func(p string, write bool) (string, error) {
		p = filepath.ToSlash(filepath.Clean(p))
		if !strings.HasPrefix(p, rootPath+"/") {
			return "", fmt.Errorf("plan tools cannot access another workspace")
		}
		p = strings.TrimPrefix(p, rootPath+"/")
		clean, e := wf.CleanRelative(p)
		if e != nil || clean != p || clean == "." || wf.Private(clean) || !externalFileGuard(ctx, false).Allows(clean, write) {
			return "", fmt.Errorf("plan path is outside connection file consent")
		}
		if write && !strings.HasPrefix(clean, "planning/") && !(action == "migrate_workflow_skills" && (clean == "workflow.json" || strings.HasPrefix(clean, "skills/"))) {
			return "", fmt.Errorf("plan tools only commit planning artifacts")
		}
		return clean, nil
	}
	read := func(callCtx context.Context, p string) (string, bool, error) {
		if raw, exists, handled := stagedTeamRead(callCtx, p); handled {
			return raw, exists, nil
		}
		rel, e := relative(p, false)
		if e != nil {
			return "", false, e
		}
		if action == "migrate_workflow_skills" {
			raw, exists, e := readWorkflowSkillsFile(root, rel)
			if e == nil {
				rememberWorkflowSkillsRead(callCtx, p, raw, exists)
			}
			return raw, exists, e
		}
		file, e := externalScopedFile(root, rel)
		if e == nil && file.Exists && file.Encoding != "utf-8" {
			e = fmt.Errorf("plan tools require UTF-8 files")
		}
		if e == nil {
			rememberTeamPlanRead(callCtx, p, file.Content)
		}
		return file.Content, file.Exists, e
	}
	nativeRead := func(callCtx context.Context, p string) (string, error) {
		raw, found, e := read(callCtx, p)
		if e == nil && !found {
			e = os.ErrNotExist
		}
		return raw, e
	}
	stage := func(callCtx context.Context, p, raw string) error {
		_, e := relative(p, true)
		if e == nil {
			_, e = stageTeamWrite(callCtx, p, raw)
		}
		if e != nil {
			preparationError = e
		}
		return e
	}
	if action == "migrate_workflow_skills" {
		ctx = stepworkflow.WithWorkflowSkillsFiles(ctx, nativeRead, stage)
	}
	result, err := stepworkflow.ExecuteExternalBuilderPlanTool(ctx, action, params, rootPath, api.logger, nativeRead, stage, nil)
	if err == nil {
		err = preparationError
	}
	if err != nil {
		externalError(w, 400, "plan_edit_rejected", err.Error())
		return
	}
	// Native changelog failures are warnings in a model turn. Here preparation
	// must be complete before any side effect; sticky write errors abort above.
	_, err = store.db.ExecContext(ctx, `INSERT INTO external_plan_receipts VALUES (?,?,?,?,?,'started','',?)`, live.UserID, live.AccessToken.ID, requestID, fingerprint, operationID, time.Now().UnixMilli())
	if err != nil {
		externalError(w, 409, "request_conflict", "Another call already claimed this request; inspect its receipt before retrying.")
		return
	}
	write := func(callCtx context.Context, p, raw string) error {
		if e := callCtx.Err(); e != nil {
			return e
		}
		rel, e := relative(p, true)
		if e != nil {
			return e
		}
		return wf.ReplaceManagedFile(root, rel, []byte(raw))
	}
	remove := func(callCtx context.Context, p string) error {
		rel, e := relative(p, true)
		if e != nil {
			return e
		}
		parent, e := wf.OpenDirectory(root, path.Dir(rel), false)
		if e != nil {
			return e
		}
		defer parent.Close()
		if e = externalNoSymlinks(parent, path.Base(rel)); e != nil {
			return e
		}
		return parent.Remove(path.Base(rel))
	}
	if err = commitTeamEditWithFiles(ctx, result, read, write, remove); err != nil {
		var beforeWrite *teamCommitBeforeWriteError
		if errors.As(err, &beforeWrite) {
			responseBytes, _ := json.Marshal(map[string]any{"error": "plan_commit_rejected", "message": err.Error(), "applied": false, "request_id": requestID, "operation_id": operationID})
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if _, saveErr := store.db.ExecContext(finishCtx, `UPDATE external_plan_receipts SET status='failed',response=? WHERE user_id=? AND connection_id=? AND request_id=?`, string(responseBytes), live.UserID, live.AccessToken.ID, requestID); saveErr != nil {
				externalError(w, 503, "receipt_unavailable", "No write was attempted, but the failed receipt could not be saved. Inspect this request before retrying.")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write(responseBytes)
			return
		}
		externalError(w, 409, "plan_commit_rejected", err.Error())
		return
	}
	revision, err = externalPlanRevision(ctx, rootPath)
	if err != nil {
		externalError(w, 503, "edit_requires_reconciliation", "Plan may be saved; inspect plan/changelog before retrying.")
		return
	}
	responseBytes, _ := json.Marshal(map[string]any{"workflow_id": workflow.Manifest.ID, "request_id": requestID, "operation_id": operationID, "revision": revision, "result": result, "applied": true})
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err = store.db.ExecContext(finishCtx, `UPDATE external_plan_receipts SET status='completed',response=? WHERE user_id=? AND connection_id=? AND request_id=?`, string(responseBytes), live.UserID, live.AccessToken.ID, requestID)
	if err != nil {
		externalError(w, 503, "edit_requires_reconciliation", "Plan saved but receipt is uncertain; inspect plan/changelog before retrying.")
		return
	}
	publishPlanChanged(rootPath + "/planning/plan.json")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(responseBytes)
}

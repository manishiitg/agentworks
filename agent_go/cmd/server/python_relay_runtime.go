package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relaypython"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

func (s *SchedulerService) executePythonRelay(ctx context.Context, sctx *ScheduleContext, runID, runFolder, sessionID string) (runErr error) {
	if sctx.OwnerUserID == "" || s.api == nil {
		return fmt.Errorf("Relay execution identity unavailable")
	}
	ctx = context.WithValue(ctx, common.UserIDKey, sctx.OwnerUserID)
	ctx = context.WithValue(ctx, common.ChatSessionIDKey, sessionID)
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	runPath := path.Join(sctx.WorkspacePath, "runs", runFolder)
	draft, err := relayDraftWorkspaceForRelease(ctx, sctx.WorkspacePath)
	if err != nil {
		return err
	}
	manifest, found, err := ReadWorkflowManifest(ctx, sctx.WorkspacePath)
	if err != nil || !found {
		return fmt.Errorf("Relay manifest unavailable during execution")
	}
	readPaths := []string{sctx.WorkspacePath}
	if isDBOSRelay(manifest) {
		python := strings.TrimSpace(os.Getenv("RELAY_DBOS_PYTHON"))
		if !filepath.IsAbs(python) || filepath.Base(filepath.Dir(python)) != "bin" {
			return fmt.Errorf("DBOS recovery requires RELAY_DBOS_PYTHON to name an absolute interpreter in a managed bin directory")
		}
		// Only the platform-configured interpreter and its packages are readable.
		// Keep its virtual environment outside app-private state; slots retain
		// their normal filesystem isolation and cannot write this runtime.
		readPaths = append(readPaths, filepath.Dir(filepath.Dir(python)))
	}
	common.SetSessionWorkflowPath(sessionID, draft)
	common.SetSessionWorkingDir(sessionID, sctx.WorkspacePath)
	common.SetSessionFolderGuard(sessionID, readPaths, []string{runPath})
	blocked := []string{path.Join(sctx.WorkspacePath, "db"), path.Join(sctx.WorkspacePath, "knowledgebase"), path.Join(sctx.WorkspacePath, "learnings"), path.Join(sctx.WorkspacePath, "secrets")}
	common.SetSessionFolderGuardBlockedPaths(sessionID, blocked)
	common.SetSessionSandbox(sessionID, true, false)
	defer common.ClearSessionShellConfig(sessionID)
	if s.api.eventStore != nil {
		s.api.eventStore.SetSessionOwner(sessionID, sctx.OwnerUserID)
	}
	if s.api.activeSessions != nil {
		s.api.trackActiveSession(sessionID, "relay", sctx.Schedule.Name, sctx.OwnerUserID, "", "webhook", sctx.WorkflowLabel, sctx.OriginSessionID, "relay")
		defer func() {
			status := "completed"
			if runErr != nil {
				status = "error"
			}
			s.api.updateSessionStatus(sessionID, status)
		}()
	}
	s.api.trackWorkflowRunStart(&ActiveWorkflowExecution{QueryID: runID, SessionID: sessionID, Kind: "relay", WorkspacePath: draft, RunFolder: runFolder, UserID: sctx.OwnerUserID, Title: sctx.Schedule.Name, Status: "running", TriggeredBy: "webhook", StartedAt: time.Now().UTC()})
	defer func() {
		status, message := trackedExecutionStatusCompleted, ""
		if runErr != nil {
			status, message = trackedExecutionStatusFailed, runErr.Error()
		}
		s.api.completeTrackedExecution(runID, status, message, nil)
	}()
	client := workspace.NewClient(getWorkspaceAPIURL())
	client.UserID = sctx.OwnerUserID
	for _, folder := range []string{runPath, path.Join(runPath, ".relay_ipc"), path.Join(runPath, "execution")} {
		if err := client.CreateFolder(ctx, folder); err != nil {
			return err
		}
	}
	var variables stepworkflow.VariablesManifest
	if content, exists, err := readFileFromWorkspace(ctx, path.Join(sctx.WorkspacePath, "variables/variables.json")); err != nil {
		return err
	} else if exists {
		if err := json.Unmarshal([]byte(content), &variables); err != nil {
			return fmt.Errorf("Relay configuration: %w", err)
		}
	}
	values, err := stepworkflow.ResolveRelayVariableValues(&variables, "")
	if err != nil {
		return err
	}
	config := make(map[string]interface{}, len(values))
	env := map[string]string{}
	for name, value := range values {
		config[name] = value
		env["VAR_"+name] = value
	}
	var input map[string]interface{}
	if sctx.WebhookInput == nil {
		return fmt.Errorf("Relay INPUT is unavailable")
	}
	if err := json.Unmarshal([]byte(sctx.WebhookInput.Variables["INPUT"]), &input); err != nil || input == nil {
		return fmt.Errorf("Relay INPUT must be an object")
	}
	selected := s.api.loadSelectedSecrets(ctx, sctx.OwnerUserID, sctx.WorkspacePath, sctx.Capabilities.SelectedSecrets)
	if err := validateVaultSecretSelection(ctx, sctx.OwnerUserID, selected, sctx.Capabilities.SelectedGlobalSecretNames); err != nil {
		return err
	}
	for _, secret := range s.api.mergeGlobalSecretsFor(ctx, sctx.OwnerUserID, selected, sctx.Capabilities.SelectedGlobalSecretNames) {
		env["SECRET_"+secret.Name] = secret.Value
	}
	common.SetSessionShellEnv(sessionID, env)
	defer publishPlanChanged(path.Join(draft, "relay.py"))
	cfg := relaypython.Config{
		Client: client, SourcePath: path.Join(sctx.WorkspacePath, "relay.py"), RunPath: runPath, Input: input, Variables: config, Env: env,
		CallAgent: func(callCtx context.Context, call relaypython.Call, tool relaypython.ToolCaller) (interface{}, error) {
			return s.api.callPythonRelayAgent(callCtx, sctx, sessionID, runPath, call, tool)
		},
	}
	if isDBOSRelay(manifest) {
		return s.executeDurablePythonRelay(ctx, sctx, runID, cfg)
	}
	return relaypython.Run(ctx, cfg)
}

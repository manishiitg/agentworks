package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relaypython"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

func isDBOSRelay(m *WorkflowManifest) bool {
	return isPythonRelay(m) && m.RelayDurability == "dbos"
}

// Invocation authority remains live even when the code and configuration are
// pinned to an old release. This also covers Python service-step admission.
func authorizePythonRelayInvocation(ctx context.Context, sctx *ScheduleContext) error {
	draft, err := relayDraftWorkspaceForRelease(ctx, sctx.WorkspacePath)
	if err != nil {
		return err
	}
	live, found, err := ReadWorkflowManifest(ctx, draft)
	if err != nil || !found || !isPythonRelay(live) || live.ID != sctx.WorkflowID || workflowExecutionOwnerUserID(live) != sctx.OwnerUserID || !workflowExecutionAccountActive(sctx.OwnerUserID) {
		return fmt.Errorf("Relay execution owner or identity is no longer available")
	}
	var payload struct {
		Function string        `json:"function"`
		Caller   triggerCaller `json:"relay_trigger_caller"`
		User     string        `json:"relay_caller"`
	}
	if sctx.WebhookInput == nil || json.Unmarshal(sctx.WebhookInput.Payload, &payload) != nil {
		return fmt.Errorf("Relay invocation caller is unavailable")
	}
	if payload.Caller.ID == "" && payload.User != "" {
		payload.Caller = triggerCaller{Type: triggerCallerUser, ID: payload.User}
	}
	trigger, err := findWorkflowFunctionTrigger(live, payload.Function)
	if payload.Caller.ID == "" || err != nil || trigger.ID != sctx.Schedule.ID || !workflowFunctionCallerAllowed(trigger.Function, payload.Caller) {
		return fmt.Errorf("Relay invocation caller is no longer allowed")
	}
	if payload.Caller.Type == triggerCallerUser {
		claims := principalClaims(payload.Caller.ID)
		if workflowAccessForManifest(claims, live) == WorkflowAccessNone || !userAllowedWorkflowID(claims, live.ID) {
			return fmt.Errorf("Relay invocation caller no longer has access")
		}
	}
	return nil
}

func relayDraftSnapshotHash(ctx context.Context, workspace string) (string, error) {
	files, err := listWorkspaceFilesRecursive(ctx, workspace)
	if err != nil {
		return "", err
	}
	content := map[string]string{}
	size := 0
	for _, file := range files {
		relative := strings.TrimPrefix(file, workspace+"/")
		if relative == file || !relaySnapshotFile(relative) {
			continue
		}
		raw, exists, err := readFileFromWorkspace(ctx, file)
		if err != nil || !exists {
			return "", fmt.Errorf("Relay draft file %q unavailable", relative)
		}
		size += len(raw)
		if size > 50*1024*1024 || len(content) >= 5000 {
			return "", fmt.Errorf("Relay draft snapshot is too large")
		}
		content[relative] = raw
	}
	return relaySnapshotHash(content), nil
}

func (s *SchedulerService) executeDurablePythonRelay(ctx context.Context, sctx *ScheduleContext, runID string, cfg relaypython.Config) (runErr error) {
	// A process interruption is transient; invocation admission/budget errors
	// are terminal. Keep the trace shown by Runs consistent with that outcome.
	defer func() {
		if runErr == nil {
			return
		}
		finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		file := path.Join(cfg.RunPath, "relay_trace.json")
		raw, err := cfg.Client.ReadWorkspaceFile(finalCtx, workspace.ReadWorkspaceFileParams{Filepath: file})
		var trace map[string]interface{}
		if err != nil || json.Unmarshal([]byte(raw.Content), &trace) != nil || trace == nil {
			return
		}
		trace["status"], trace["error"] = "failed", runErr.Error()
		if ctx.Err() == context.Canceled {
			trace["status"] = "stopped"
		}
		encoded, err := json.Marshal(trace)
		if err == nil {
			_, _ = cfg.Client.UpdateWorkspaceFile(finalCtx, workspace.UpdateWorkspaceFileParams{Filepath: file, Content: string(encoded)})
		}
	}()
	python := strings.TrimSpace(os.Getenv("RELAY_DBOS_PYTHON"))
	if python == "" {
		return fmt.Errorf("DBOS recovery requires RELAY_DBOS_PYTHON on the workspace runtime; install requirements-dbos.txt")
	}
	s.stateStoreMu.RLock()
	store := s.stateStore
	s.stateStoreMu.RUnlock()
	if store == nil {
		return fmt.Errorf("DBOS recovery requires the durable invocation ledger")
	}
	run, err := store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	var hash string
	var verify func(context.Context) error
	if workflowtypes.RelayReleaseWorkspace(sctx.WorkspacePath) != "" {
		draft, err := relayDraftWorkspaceForRelease(ctx, sctx.WorkspacePath)
		if err != nil {
			return err
		}
		release, releaseWorkspace, err := readRelayRelease(ctx, draft, path.Base(sctx.WorkspacePath))
		if err != nil {
			return err
		}
		hash = release.Hash
		verify = func(ctx context.Context) error { return verifyRelayRelease(ctx, release, releaseWorkspace) }
	} else {
		hash, err = relayDraftSnapshotHash(ctx, sctx.WorkspacePath)
		if err != nil {
			return err
		}
		verify = func(ctx context.Context) error {
			current, err := relayDraftSnapshotHash(ctx, sctx.WorkspacePath)
			if err != nil {
				return err
			}
			if current != hash {
				return fmt.Errorf("Relay draft changed during execution; start a new test")
			}
			return nil
		}
	}
	authorize := func(ctx context.Context) error {
		if err := authorizePythonRelayInvocation(ctx, sctx); err != nil {
			return err
		}
		selected := s.api.loadSelectedSecrets(ctx, sctx.OwnerUserID, sctx.WorkspacePath, sctx.Capabilities.SelectedSecrets)
		if err := validateVaultSecretSelection(ctx, sctx.OwnerUserID, selected, &sctx.Capabilities.SelectedSecrets); err != nil {
			return err
		}
		if err := validateVaultSecretSelection(ctx, sctx.OwnerUserID, selected, sctx.Capabilities.SelectedGlobalSecretNames); err != nil {
			return err
		}
		return verify(ctx)
	}
	input, err := json.Marshal(cfg.Input)
	if err != nil {
		return err
	}
	variables, err := json.Marshal(cfg.Variables)
	if err != nil {
		return err
	}
	binding := schedulerstate.RelayDBOSBinding{RunID: runID, OwnerID: sctx.OwnerUserID, ReleaseHash: hash,
		InputJSON: string(input), VariablesJSON: string(variables), Deadline: run.StartedAt.Add(time.Hour)}
	for {
		if err := authorize(ctx); err != nil {
			return err
		}
		binding, err = store.ReserveRelayDBOSAttempt(ctx, binding)
		if err != nil {
			return err
		}
		// Keep credentials outside the durable binding and refresh their values
		// for each fresh process. Service-step admission rechecks live grants.
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		for key := range cfg.Env {
			if strings.HasPrefix(key, "SECRET_") {
				delete(cfg.Env, key)
			}
		}
		selected := s.api.loadSelectedSecrets(ctx, sctx.OwnerUserID, sctx.WorkspacePath, sctx.Capabilities.SelectedSecrets)
		for _, secret := range s.api.mergeGlobalSecretsFor(ctx, sctx.OwnerUserID, selected, sctx.Capabilities.SelectedGlobalSecretNames) {
			cfg.Env["SECRET_"+secret.Name] = secret.Value
		}
		attemptCtx, cancel := context.WithDeadline(ctx, binding.Deadline)
		cfg.DBOS = &relaypython.DBOSConfig{RunID: runID, ReleaseHash: hash, PythonExecutable: python, Authorize: authorize, Attempt: binding.Attempt}
		err = relaypython.Run(attemptCtx, cfg)
		cancel()
		if err == nil || !relaypython.IsInterrupted(err) || ctx.Err() != nil {
			return err
		}
		s.logf(sctx, "[RELAY] DBOS process attempt %d interrupted; recovering invocation %s from checkpoints", binding.Attempt, runID)
		// Relaunch only a pending workflow. Application errors, uncertain effects,
		// cancellation and expired deadlines never enter this recovery loop.
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Rebuild only from the canonical run, its original frozen manifest and saved
// delivery. No new INPUT, run folder or active-version selection is synthesized.
func dbosRelayResumeContext(ctx context.Context, run schedulerstate.Run) (*ScheduleContext, error) {
	m, found, err := ReadWorkflowManifest(ctx, run.ScopeID)
	if err != nil || !found || !isDBOSRelay(m) {
		return nil, fmt.Errorf("DBOS Relay release is unavailable")
	}
	var trigger *WorkflowSchedule
	for i := range m.Schedules {
		if m.Schedules[i].ID == run.ScheduleID {
			trigger = &m.Schedules[i]
			break
		}
	}
	if trigger == nil || !trigger.Enabled || !trigger.IsFunctionTrigger() {
		return nil, fmt.Errorf("DBOS Relay function is unavailable")
	}
	raw, exists, err := readFileFromWorkspace(ctx, webhookInputPath(run.ScopeID, run.RunID))
	if err != nil || !exists {
		return nil, fmt.Errorf("DBOS invocation delivery is unavailable")
	}
	var input WorkflowWebhookDelivery
	if json.Unmarshal([]byte(raw), &input) != nil || input.RunID != run.RunID {
		return nil, fmt.Errorf("DBOS invocation delivery is invalid")
	}
	sctx := buildScheduleContext(run.ScopeID, m, *trigger)
	sctx.WebhookInput, sctx.TriggerSource = &input, run.TriggerSource
	sctx.DBOSResumeRunID, sctx.DBOSResumeRunFolder = run.RunID, run.RunFolder
	return sctx, nil
}

func (s *SchedulerService) recoverInterruptedDBOSRelays(ctx context.Context) {
	// The workspace shell service may retain a process when its backend caller
	// disappears. Let the old executor release its lock before claiming recovery.
	go func() {
		timer := time.NewTimer(relaypython.RecoveryLeaseGracePeriod)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.recoverDBOSRelaysAfterLease(ctx)
	}()
}

func (s *SchedulerService) recoverDBOSRelaysAfterLease(ctx context.Context) {
	s.stateStoreMu.RLock()
	store := s.stateStore
	s.stateStoreMu.RUnlock()
	if store == nil {
		return
	}
	runs, err := store.InterruptedDBOSRuns(ctx)
	if err != nil {
		scheduleLogf("[RELAY] DBOS recovery lookup: %v", err)
		return
	}
	for _, run := range runs {
		sctx, err := dbosRelayResumeContext(ctx, run)
		if err != nil {
			scheduleLogf("[RELAY] DBOS recovery %s unavailable: %v", run.RunID, err)
			continue
		}
		key := scheduleRuntimeKey(sctx)
		s.runtimeStatesMu.Lock()
		state := s.getRuntimeStateLocked(key)
		if state.LastStatus == "running" {
			s.runtimeStatesMu.Unlock()
			continue
		}
		previous := *state
		runCtx := s.activateScheduleRunLocked(state, run.RunID, time.Now().UTC())
		s.runtimeStatesMu.Unlock()
		if err := s.claimScheduleRun(ctx, sctx, run.RunID, time.Now().UTC()); err != nil {
			s.rollbackScheduleRunActivation(key, run.RunID, previous)
			continue
		}
		go func() { _, _ = s.runJob(runCtx, sctx, run.RunID) }()
	}
}

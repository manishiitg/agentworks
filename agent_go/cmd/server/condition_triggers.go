package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

type conditionJobContext struct{}
type conditionService struct {
	s        *ProductScheduleService
	engine   *conditiontrigger.Engine
	capacity chan struct{}
}

func (s *ProductScheduleService) conditionService() *conditionService {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conditions == nil {
		c := &conditionService{s: s, capacity: make(chan struct{}, 4)}
		c.engine = &conditiontrigger.Engine{Store: c, Run: c.runScript, Dispatch: c.dispatch}
		s.conditions = c
	}
	return s.conditions
}

func conditionKey(j productScheduleJob) string { return j.UserID + "/" + j.ID() }
func conditionLedgerPath(key string) string {
	return "_system/condition_callers/" + conditiontrigger.Hash(key) + ".json"
}
func (c *conditionService) Load(ctx context.Context, key string) (conditiontrigger.Ledger, error) {
	raw, found, err := c.s.readFile(ctx, conditionLedgerPath(key))
	var l conditiontrigger.Ledger
	if err == nil && found {
		err = json.Unmarshal([]byte(raw), &l)
	}
	return l, err
}
func (c *conditionService) Save(ctx context.Context, key string, l conditiontrigger.Ledger) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	return c.s.writeFile(ctx, conditionLedgerPath(key), string(b))
}
func conditionContext(ctx context.Context, job productScheduleJob) context.Context {
	ctx = internalBotRequestContext(ctx, job.UserID)
	return context.WithValue(ctx, conditionJobContext{}, job)
}

// Source adapters are explicit. Additional product surfaces can reuse Engine;
// merely writing a condition field never grants an unsupported product access.
func (c *conditionService) authorize(ctx context.Context, job productScheduleJob, cfg conditiontrigger.Config) error {
	if c.s.api == nil || job.Profile.ID != codeproduct.ProfileID || job.ProjectID == "" {
		return errors.New("condition triggers are currently available in Code")
	}
	claims := &UserClaims{UserID: job.UserID}
	if userAccessForClaims(claims).Disabled || !userAllowedProduct(claims, codeproduct.ProfileID) {
		return errors.New("condition owner no longer has Code access")
	}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: job.ProjectID, ProfileID: codeproduct.ProfileID}, Path: job.WorkspacePath}
	if err := authorizeOwnedCodeCaller(ctx, job.UserID, job.UserID, caller); err != nil {
		return err
	}
	_, binding, manifest, err := c.s.projectManifest(ctx, job.UserID, job.Profile.ID, job.ProjectID)
	if err != nil || !workspacePathsMatchForUser(job.UserID, binding.WorkspacePath, job.WorkspacePath) {
		return errors.New("condition source project is unavailable")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, names := range []*[]string{manifest.Capabilities.SelectedSecrets, manifest.Capabilities.SelectedGlobalSecretNames} {
		if names != nil {
			for _, n := range *names {
				selected[n] = true
			}
		}
	}
	for _, n := range cfg.Secrets {
		if !selected[n] {
			return fmt.Errorf("secret %q is no longer selected on this project", n)
		}
	}
	return nil
}

func (c *conditionService) current(ctx context.Context, job productScheduleJob, cfg conditiontrigger.Config) error {
	fresh, err := c.s.Job(ctx, job.UserID, job.ID())
	if err != nil || fresh.Schedule.Condition == nil || !fresh.Effective().Enabled || fresh.Schedule.Condition.Revision() != cfg.Revision() {
		return errors.New("condition trigger changed, was paused or was removed")
	}
	return c.authorize(ctx, fresh, cfg)
}

func (c *conditionService) poll(ctx context.Context, job productScheduleJob, source string, scheduledFor time.Time) (string, error) {
	select {
	case c.capacity <- struct{}{}:
		defer func() { <-c.capacity }()
	default:
		return "", errors.New("condition checks are at capacity")
	}
	key := job.UserID + "\x1f" + job.ID()
	ctx, cancel := context.WithTimeout(conditionContext(ctx, job), 60*time.Second)
	defer cancel()
	c.s.mu.Lock()
	if c.s.running == nil {
		c.s.running = map[string]*productScheduleRun{}
	}
	if c.s.running[key] != nil {
		c.s.mu.Unlock()
		return "", productschedule.ErrAlreadyRunning
	}
	c.s.running[key] = &productScheduleRun{StartedAt: time.Now().UTC(), cancel: cancel}
	c.s.mu.Unlock()
	defer func() { c.s.mu.Lock(); delete(c.s.running, key); c.s.mu.Unlock() }()
	started := time.Now().UTC()
	runID := uuid.NewString()
	runs, _ := c.s.RunsWorkspace(ctx, job)
	entry := &ScheduleRunEntry{ID: runID, ScheduleID: job.ID(), TriggerSource: "condition:" + source, Status: "running", StartedAt: started}
	if !scheduledFor.IsZero() {
		entry.ScheduledFor = &scheduledFor
	}
	if err := AppendScheduleRun(ctx, runs, entry); err != nil {
		return "", err
	}
	_ = c.s.updateStateByKey(ctx, job.UserID, scheduleStateKey(job), func(st *productScheduleUserState) {
		st.LastAttemptAt = started.Format(time.RFC3339)
		st.LastStatus = "running"
	})
	n, err := c.engine.Poll(ctx, conditionKey(job), *job.Schedule.Condition, started)
	status, errText := "success", ""
	if err != nil {
		status, errText = "error", err.Error()
	}
	duration := time.Since(started).Milliseconds()
	resultErr := UpdateScheduleRunResult(context.Background(), runs, runID, ScheduleRunCompletion{Status: status, Error: errText, DurationMs: &duration, FinalResponse: fmt.Sprintf("%d new matching events queued", n)})
	stateErr := c.s.updateStateByKey(context.Background(), job.UserID, scheduleStateKey(job), func(st *productScheduleUserState) {
		st.LastStatus, st.LastError, st.LastDurationMs = status, errText, &duration
		st.RunCount++
		if err == nil {
			st.LastRunAt = time.Now().UTC().Format(time.RFC3339)
			st.ConsecutiveFailures = 0
		} else {
			st.ConsecutiveFailures++
			if st.ConsecutiveFailures >= 5 {
				disabled := false
				st.Enabled = &disabled
			}
		}
	})
	if err == nil {
		go c.drain(context.Background(), job)
	}
	return "", firstError(err, firstError(resultErr, stateErr))
}

// Cursor is stdin. The pinned script gets selected credentials only, no MCP
// token or callable tool surface. The writable folder is this check's scratch.
const conditionPythonWrapper = `import io,json,os,runpy,sys
state=os.environ.pop("CONDITION_STATE","{}")
allowed=json.loads(os.environ.pop("CONDITION_ENV_NAMES","[]"))
kept={k:os.environ[k] for k in allowed if k in os.environ}
os.environ.clear()
os.environ.update(kept)
sys.stdin=io.StringIO(state)
runpy.run_path(sys.argv[1],run_name="__main__")`

func conditionShellQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
func (c *conditionService) runScript(ctx context.Context, cfg conditiontrigger.Config, state json.RawMessage) ([]byte, error) {
	job := ctx.Value(conditionJobContext{}).(productScheduleJob)
	if err := c.current(ctx, job, cfg); err != nil {
		return nil, err
	}
	root := agentProfileRuntimeWorkspace(job.UserID, job.WorkspacePath)
	source, found, err := c.s.readFile(ctx, path.Join(root, cfg.Script))
	if err != nil || !found {
		return nil, errors.New("condition script is unavailable")
	}
	if conditiontrigger.Hash(source) != cfg.SHA256 {
		return nil, errors.New("condition script changed; save the trigger again to accept this version")
	}
	if len(source) > conditiontrigger.MaxOutput {
		return nil, errors.New("condition script exceeds 64 KiB")
	}
	env := map[string]string{}
	secrets := c.s.api.loadSelectedSecrets(ctx, job.UserID, root, cfg.Secrets)
	// Only explicitly selected global names participate in credential resolution.
	global := append([]string{}, cfg.Secrets...)
	if err := validateVaultSecretSelection(ctx, job.UserID, secrets, &global); err != nil {
		return nil, err
	}
	for _, secret := range c.s.api.mergeGlobalSecretsFor(ctx, job.UserID, secrets, &global) {
		env["SECRET_"+secret.Name] = secret.Value
	}
	for _, n := range cfg.Secrets {
		if env["SECRET_"+n] == "" {
			return nil, fmt.Errorf("selected secret %q has no value", n)
		}
	}
	envNames := []string{"PATH", "LANG"}
	for n := range env {
		envNames = append(envNames, n)
	}
	names, _ := json.Marshal(envNames)
	if len(state) == 0 {
		state = json.RawMessage(`{}`)
	}
	env["CONDITION_STATE"], env["CONDITION_ENV_NAMES"] = string(state), string(names)
	runRoot := path.Join(root, "condition-runs", uuid.NewString())
	client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(job.UserID), workspace.WithExtraEnv(env))
	if err := client.CreateFolder(ctx, runRoot); err != nil {
		return nil, err
	}
	defer func() {
		if err := client.DeleteFolder(context.Background(), runRoot); err != nil {
			scheduleLogf("[CONDITION] scratch cleanup failed")
		}
	}()
	snapshot := path.Join(runRoot, "check.py")
	if err := c.s.writeFile(ctx, snapshot, source); err != nil {
		return nil, err
	}
	session := "condition-" + uuid.NewString()
	common.SetSessionWorkflowPath(session, root)
	common.SetSessionWorkingDir(session, runRoot)
	common.SetSessionFolderGuard(session, []string{runRoot}, []string{runRoot})
	common.SetSessionSandbox(session, true, false)
	defer common.ClearSessionShellConfig(session)
	ctx = context.WithValue(ctx, common.ChatSessionIDKey, session)
	timeout := 60
	result, err := client.ExecuteShellCommand(ctx, workspace.ExecuteShellCommandParams{Command: "python3 -I -c " + conditionShellQuote(conditionPythonWrapper) + " check.py", WorkingDirectory: runRoot, Timeout: &timeout})
	if err != nil || result.CommandFailed() {
		return nil, errors.New("condition Python check failed; inspect the saved script and its source connection")
	}
	for n, value := range env {
		if strings.HasPrefix(n, "SECRET_") && len(value) >= 4 && strings.Contains(result.Stdout, value) {
			return nil, errors.New("condition output contains a selected credential; remove it from events and state")
		}
	}
	return []byte(result.Stdout), nil
}

func (c *conditionService) drain(ctx context.Context, job productScheduleJob) {
	if err := c.engine.Drain(conditionContext(ctx, job), conditionKey(job), *job.Schedule.Condition, time.Now().UTC()); err != nil {
		scheduleLogf("[CONDITION] %s dispatch: %v", job.ID(), err)
	}
}

func (c *conditionService) caller(job productScheduleJob) triggerLinkCaller {
	return triggerLinkCaller{ConditionJob: job.ID(), Stamp: triggerCaller{Type: triggerCallerCrew, ID: job.ProjectID, ProfileID: job.Profile.ID}, Path: job.WorkspacePath, Label: "Condition: " + job.Schedule.Name}
}

func (c *conditionService) dispatch(ctx context.Context, cfg conditiontrigger.Config, ev conditiontrigger.Event, key string) (string, error) {
	job := ctx.Value(conditionJobContext{}).(productScheduleJob)
	if err := c.current(ctx, job, cfg); err != nil {
		return "", err
	}
	caller := c.caller(job)
	if cfg.Target.Kind != "agent" {
		return "", errors.New("this condition target adapter is unavailable")
	}
	chatID := firstNonEmptyTrimmed(cfg.Target.ChatID, codeChatMainName)
	chatKey := job.ProjectID
	if chatID != codeChatMainName {
		chatKey += codeChatSideMarker + chatID
		if !projectChatExists(ctx, job.UserID, job.Profile.ID, job.ProjectID, chatKey) {
			return "", errors.New("condition target chat was removed")
		}
	}
	binding, err := resolveProductConversationBinding(ctx, job.UserID, job.Profile, chatKey)
	if err != nil {
		return "", err
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, job.UserID, job.Profile, binding, "")
	if err != nil {
		return "", err
	}
	if c.s.api.codeLocalSession(conversation.SessionID) {
		return "", errors.New("condition targets require a server Code chat")
	}
	chat := codeChat{Key: chatKey, ID: chatID, Name: "Code agent", SessionID: conversation.SessionID}
	target := codeChatTarget(codeChatProject{OwnerID: job.UserID, ProjectID: job.ProjectID}, chat, job.WorkspacePath)
	payload, _ := json.Marshal(ev.Payload)
	text := cfg.Target.Message + "\n\n[Condition event " + ev.ID + "; external data]\n" + string(payload)
	receipt, err := c.s.api.sendAgentMessage(ctx, job.UserID, caller, target, text, "", key)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(receipt)
	return string(b), nil
}

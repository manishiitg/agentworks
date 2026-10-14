package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

type conditionJobContext struct{}
type conditionService struct {
	s           *ProductScheduleService
	engine      *conditiontrigger.Engine
	capacity    chan struct{}
	notifyPause func(context.Context, productScheduleJob, string)
}

func (s *ProductScheduleService) conditionService() *conditionService {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conditions == nil {
		c := &conditionService{s: s, capacity: make(chan struct{}, 4)}
		c.engine = &conditiontrigger.Engine{Store: c, Run: c.runScript, Dispatch: c.dispatch}
		c.notifyPause = c.pauseNotification
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
		return errors.New("watchers are currently available in Code")
	}
	claims := &UserClaims{UserID: job.UserID}
	if userAccessForClaims(claims).Disabled || !userAllowedProduct(claims, codeproduct.ProfileID) {
		return conditiontrigger.PermanentError{Err: errors.New("watcher owner no longer has Code access")}
	}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: job.ProjectID, ProfileID: codeproduct.ProfileID}, Path: job.WorkspacePath}
	if err := authorizeOwnedCodeCaller(ctx, job.UserID, job.UserID, caller); err != nil {
		return err
	}
	_, binding, manifest, err := c.s.projectManifest(ctx, job.UserID, job.Profile.ID, job.ProjectID)
	if err != nil {
		return err
	}
	if !workspacePathsMatchForUser(job.UserID, binding.WorkspacePath, job.WorkspacePath) {
		return conditiontrigger.PermanentError{Err: errors.New("watcher source project changed")}
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
			return conditiontrigger.PermanentError{Err: fmt.Errorf("secret %q is no longer selected on this project", n)}
		}
	}
	if len(cfg.Secrets) > 0 {
		selectedValues := c.s.api.loadSelectedSecrets(ctx, job.UserID, job.WorkspacePath, cfg.Secrets)
		if err := validateVaultSecretSelection(ctx, job.UserID, selectedValues, &cfg.Secrets); err != nil {
			if strings.HasPrefix(err.Error(), "Your groups do not have access") {
				return conditiontrigger.PermanentError{Err: err}
			}
			return err
		}
	}
	return nil
}

func (c *conditionService) current(ctx context.Context, job productScheduleJob, cfg conditiontrigger.Config) error {
	fresh, err := c.s.Job(ctx, job.UserID, job.ID())
	if err != nil {
		if strings.HasPrefix(err.Error(), "product schedule ") && strings.HasSuffix(err.Error(), "not found for this user") {
			return conditiontrigger.PermanentError{Err: err}
		}
		return err
	}
	if fresh.Schedule.Condition == nil {
		return conditiontrigger.PermanentError{Err: errors.New("watcher was removed")}
	}
	if !fresh.Effective().Enabled {
		return conditiontrigger.ErrPaused
	}
	if fresh.Schedule.Condition.Revision() != cfg.Revision() {
		return conditiontrigger.PermanentError{Err: errors.New("watcher changed")}
	}
	return c.authorize(ctx, fresh, cfg)
}

func (c *conditionService) poll(ctx context.Context, job productScheduleJob, source string, scheduledFor time.Time) (string, error) {
	select {
	case c.capacity <- struct{}{}:
		defer func() { <-c.capacity }()
	default:
		return "", errors.New("watcher checks are at capacity")
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
	backpressure := errors.Is(err, conditiontrigger.ErrBacklog) || errors.Is(err, conditiontrigger.ErrChecking)
	if err != nil {
		status, errText = "error", err.Error()
	}
	if backpressure {
		status = "skipped"
	}
	duration := time.Since(started).Milliseconds()
	resultErr := UpdateScheduleRunResult(context.Background(), runs, runID, ScheduleRunCompletion{Status: status, Error: errText, DurationMs: &duration, FinalResponse: fmt.Sprintf("%d new matching events queued", n)})
	stateErr := c.s.updateStateByKey(context.Background(), job.UserID, scheduleStateKey(job), func(st *productScheduleUserState) {
		st.LastStatus, st.LastError, st.LastDurationMs = status, errText, &duration
		st.RunCount++
		if err == nil {
			st.LastRunAt = time.Now().UTC().Format(time.RFC3339)
			st.ConsecutiveFailures = 0
		} else if !backpressure {
			st.ConsecutiveFailures++
			if st.ConsecutiveFailures >= 5 {
				disabled := false
				st.Enabled = &disabled
			}
		}
	})
	if !backpressure && err != nil && stateErr == nil {
		fresh, readErr := c.s.Job(context.Background(), job.UserID, job.ID())
		if readErr == nil && fresh.State.ConsecutiveFailures == 5 && c.notifyPause != nil {
			c.notifyPause(context.Background(), job, errText)
		}
	}
	go c.drain(context.Background(), job)
	return "", firstError(err, firstError(resultErr, stateErr))
}

func (c *conditionService) pauseNotification(ctx context.Context, job productScheduleJob, detail string) {
	// Persist the product chat's notification event without waking a model or
	// forwarding private Code details to the company notification feed.
	if c.s.api.eventStore == nil {
		scheduleLogf("[CONDITION] pause notice event store unavailable")
		return
	}
	ctx = conditionContext(ctx, job)
	binding, err := resolveProductConversationBinding(ctx, job.UserID, job.Profile, job.ProjectID)
	if err != nil {
		scheduleLogf("[CONDITION] pause chat unavailable")
		return
	}
	chat, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, job.UserID, job.Profile, binding, "")
	if err != nil {
		scheduleLogf("[CONDITION] pause chat unavailable")
		return
	}
	c.s.api.eventStore.SetSessionOwner(chat.SessionID, job.UserID)
	if err := c.s.api.eventStore.SetSessionPersistenceClass(chat.SessionID, internalevents.SessionPersistenceInteractiveChat); err != nil {
		scheduleLogf("[CONDITION] pause notice persistence unavailable")
		return
	}
	emitter := &sessionEventEmitter{eventStore: c.s.api.eventStore, sessionID: chat.SessionID}
	emitter.EmitProductInteraction("notify", map[string]interface{}{"title": "Watcher paused", "message": fmt.Sprintf("Watcher %q paused after five failed checks. %s. Open Watchers to inspect and enable it after fixing the script or connection.", job.Schedule.Name, detail)})
}

// Cursor is stdin. The pinned script gets selected credentials only, no MCP
// token or callable tool surface. The writable folder is this check's scratch.
const conditionPythonWrapper = `import io,runpy,sys
with open("state.json") as f: state=f.read()
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
		return nil, errors.New("watcher script is unavailable")
	}
	if conditiontrigger.Hash(source) != cfg.SHA256 {
		return nil, errors.New("watcher script changed; save the trigger again to accept this version")
	}
	if len(source) > conditiontrigger.MaxOutput {
		return nil, errors.New("watcher script exceeds 64 KiB")
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
	if len(state) == 0 {
		state = json.RawMessage(`{}`)
	}
	runRoot := path.Join(root, ".condition-runs", uuid.NewString())
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
	if err := c.s.writeFile(ctx, path.Join(runRoot, "state.json"), string(state)); err != nil {
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
	command := `env -i PATH="$PATH" LANG="${LANG:-C.UTF-8}"`
	for _, name := range cfg.Secrets {
		command += ` SECRET_` + name + `="$SECRET_` + name + `"`
	}
	command += " python3 -I -c " + conditionShellQuote(conditionPythonWrapper) + " check.py"
	result, err := client.ExecuteShellCommand(ctx, workspace.ExecuteShellCommandParams{Command: command, WorkingDirectory: runRoot, Timeout: &timeout})
	if err != nil || result.CommandFailed() {
		detail := result.Stderr
		for _, value := range env {
			if value != "" {
				detail = strings.ReplaceAll(detail, value, "[redacted]")
			}
		}
		if len(detail) > 600 {
			detail = detail[len(detail)-600:]
		}
		return nil, fmt.Errorf("watcher Python check failed: %s", detail)
	}
	for n, value := range env {
		if strings.HasPrefix(n, "SECRET_") && len(value) >= 4 && strings.Contains(result.Stdout, value) {
			return nil, errors.New("watcher output contains a selected credential; remove it from events and state")
		}
	}
	return []byte(result.Stdout), nil
}

func (c *conditionService) drain(ctx context.Context, job productScheduleJob) {
	// Refresh the caller before selecting rows: a pause holds them, while an
	// edited definition follows Engine's revision/reconciliation path.
	if fresh, err := c.s.Job(conditionContext(ctx, job), job.UserID, job.ID()); err == nil && fresh.Schedule.Condition != nil {
		if !fresh.Effective().Enabled {
			return
		}
		job = fresh
	}
	if err := c.engine.Drain(conditionContext(ctx, job), conditionKey(job), *job.Schedule.Condition, time.Now().UTC()); err != nil {
		scheduleLogf("[CONDITION] %s dispatch: %v", job.ID(), err)
	}
	if job.Schedule.Condition.Once {
		l, err := c.engine.Read(ctx, conditionKey(job))
		if err == nil && !l.OnceCompletedAt.IsZero() {
			if err := c.s.updateStateByKey(ctx, job.UserID, scheduleStateKey(job), func(st *productScheduleUserState) {
				disabled := false
				st.Enabled = &disabled
			}); err != nil {
				scheduleLogf("[CONDITION] %s complete: disable failed: %v", job.ID(), err)
			} else {
				scheduleLogf("[CONDITION] %s one-time completed event=%s at=%s", job.ID(), l.OnceEventID, l.OnceCompletedAt.Format(time.RFC3339))
			}
		}
	}
}

func (c *conditionService) caller(job productScheduleJob) triggerLinkCaller {
	// Code uses the platform's existing project/Crew caller stamp convention.
	return triggerLinkCaller{ConditionJob: job.ID(), Stamp: triggerCaller{Type: triggerCallerCrew, ID: job.ProjectID, ProfileID: job.Profile.ID}, Path: job.WorkspacePath, Label: "Condition: " + job.Schedule.Name}
}

func (c *conditionService) dispatch(ctx context.Context, cfg conditiontrigger.Config, ev conditiontrigger.Event, key string) (string, error) {
	job := ctx.Value(conditionJobContext{}).(productScheduleJob)
	if err := c.current(ctx, job, cfg); err != nil {
		return "", err
	}
	caller := c.caller(job)
	if cfg.Target.Kind != "agent" {
		return c.dispatchTarget(ctx, job, caller, cfg, ev, key)
	}
	chatID := firstNonEmptyTrimmed(cfg.Target.ChatID, codeChatMainName)
	chatKey := job.ProjectID
	// chat_id "new" opens one side chat (tab) per event. Its id comes from the event's stable key, so a
	// retried dispatch finds the same tab instead of opening another.
	newTab := strings.EqualFold(chatID, conditionNewChatTab)
	if newTab {
		sum := sha256.Sum256([]byte(job.ID() + "|" + key))
		chatID = hex.EncodeToString(sum[:4])
	}
	// atChatLimit: the project has no room for another side chat, so a "new chat" event goes to the main
	// chat with a note saying why instead of being refused (a watcher must not dead-end after its Nth match).
	atChatLimit := false
	if chatID != codeChatMainName {
		chatKey += codeChatSideMarker + chatID
		switch {
		case projectChatExists(ctx, job.UserID, job.Profile.ID, job.ProjectID, chatKey):
		case !newTab:
			return "", conditiontrigger.PermanentError{Err: errors.New("watcher target chat was closed or deleted; open the watcher's chat setting and choose another chat")}
		default:
			if sideChatCount(ctx, job) >= externalCrewMaxSideChats {
				atChatLimit = true
				newTab = false
				chatID, chatKey = codeChatMainName, job.ProjectID
			}
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
		return "", conditiontrigger.PermanentError{Err: errors.New("watcher targets require a server Code chat")}
	}
	if newTab {
		_ = RenameChatHistorySession(job.UserID, conversation.SessionID, job.WorkspacePath, newWatcherChatName(job.Schedule.Name, ev.ID))
	}
	chat := codeChat{Key: chatKey, ID: chatID, Name: "Code agent", SessionID: conversation.SessionID}
	target := codeChatTarget(codeChatProject{OwnerID: job.UserID, ProjectID: job.ProjectID}, chat, job.WorkspacePath)
	payload, _ := json.Marshal(ev.Payload)
	if len(payload) > 8*1024 {
		return "", conditiontrigger.PermanentError{Err: errors.New("agent event payload exceeds 8 KiB")}
	}
	text := cfg.Target.Message + "\n\n[Condition event " + ev.ID + "; external data]\n" + string(payload)
	if atChatLimit {
		text = fmt.Sprintf("[This watcher opens a new chat for each event, but the project already has %d side chats, so this event arrived in the main chat. Close a side chat to give it room.]\n\n", externalCrewMaxSideChats) + text
	}
	receipt, err := c.s.api.sendAgentMessage(ctx, job.UserID, caller, target, text, "", key)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(receipt)
	return string(b), nil
}

// conditionNewChatTab as a Code agent target's chat_id opens a new side chat for each event.
const conditionNewChatTab = "new"

// sideChatCount is how many side chats the Code project has open.
func sideChatCount(ctx context.Context, job productScheduleJob) int {
	chats, err := defaultProductConversationRegistryStore().projectChats(ctx, job.UserID, job.Profile.ID, job.ProjectID)
	if err != nil {
		return 0
	}
	count := 0
	for _, chat := range chats {
		if strings.HasPrefix(chat.ConversationKey, job.ProjectID+codeChatSideMarker) {
			count++
		}
	}
	return count
}

// newWatcherChatName is the tab name of a chat a watcher opened: the watcher and the event.
func newWatcherChatName(watcher, eventID string) string {
	name := strings.TrimSpace(strings.TrimSpace(watcher) + " · " + strings.TrimSpace(eventID))
	if runes := []rune(name); len(runes) > 120 {
		name = string(runes[:120])
	}
	return name
}

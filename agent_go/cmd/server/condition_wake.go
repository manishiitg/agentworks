package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// A webhook is only a wake signal. The detector fetches all new source changes
// using its existing cursor; neither raw webhook data nor credentials are saved.
// The same record locates the person-owned caller and recovers pending wakes.
type conditionWake struct {
	Owner       string    `json:"owner"`
	JobID       string    `json:"job_id"`
	Received    uint64    `json:"received"`
	Checked     uint64    `json:"checked"`
	NextCheckAt time.Time `json:"next_check_at,omitempty"`
}

const conditionWakeSpacing = 5 * time.Second

func conditionWakeID(job productScheduleJob) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("watcher:"+conditionKey(job))).String()
}
func conditionWakePath(id string) string { return "_system/condition_wakes/" + id + ".json" }
func (c *conditionService) wakeLock(id string) *sync.Mutex {
	m, _ := c.wakeLocks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}
func (c *conditionService) readWake(ctx context.Context, id string) (conditionWake, bool, error) {
	raw, found, err := c.s.readFile(ctx, conditionWakePath(id))
	var wake conditionWake
	if err == nil && found {
		err = json.Unmarshal([]byte(raw), &wake)
	}
	return wake, found, err
}
func (c *conditionService) saveWake(ctx context.Context, id string, wake conditionWake) error {
	raw, err := json.Marshal(wake)
	if err != nil {
		return err
	}
	return c.s.writeFile(ctx, conditionWakePath(id), string(raw))
}
func conditionJobView(job productScheduleJob) map[string]interface{} {
	out := conditionView(job.Schedule)
	if job.Schedule.Condition.WakesOnEvents() {
		out["event_path"] = "/api/hooks/" + conditionWakeID(job)
	}
	return out
}

// Resolve as the owner on every request: rotating/removing a secret or
// revoking its Vault grant changes webhook admission immediately.
func (c *conditionService) secretEnvironment(ctx context.Context, job productScheduleJob, cfg conditiontrigger.Config) (map[string]string, error) {
	root := agentProfileRuntimeWorkspace(job.UserID, job.WorkspacePath)
	secrets := c.s.api.loadSelectedSecrets(ctx, job.UserID, root, cfg.Secrets)
	names := append([]string{}, cfg.Secrets...)
	if err := validateVaultSecretSelection(ctx, job.UserID, secrets, &names); err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, secret := range c.s.api.mergeGlobalSecretsFor(ctx, job.UserID, secrets, &names) {
		env["SECRET_"+secret.Name] = secret.Value
	}
	for _, name := range cfg.Secrets {
		if env["SECRET_"+name] == "" {
			return nil, fmt.Errorf("selected secret %q has no value", name)
		}
	}
	return env, nil
}
func (c *conditionService) ensureWakeReceiver(ctx context.Context, job productScheduleJob) error {
	if _, err := c.secretEnvironment(ctx, job, *job.Schedule.Condition); err != nil {
		return err
	}
	id := conditionWakeID(job)
	m := c.wakeLock(id)
	m.Lock()
	defer m.Unlock()
	_, found, err := c.readWake(ctx, id)
	if err != nil || found {
		return err
	}
	return c.saveWake(ctx, id, conditionWake{Owner: job.UserID, JobID: job.ID()})
}

// receiveWebhookWake participates in the existing product webhook receiver.
// false means the UUID is not a Watcher and ordinary product delivery proceeds.
func (c *conditionService) receiveWebhookWake(w http.ResponseWriter, r *http.Request, id string) bool {
	wake, found, err := c.readWake(r.Context(), id)
	if !found && err == nil {
		return false
	}
	refuse := func(reason string) bool {
		scheduleLogf("[WATCHER_WAKE] watcher=%s source=webhook result=refused reason=%s", id, reason)
		http.Error(w, "trigger not found", http.StatusNotFound)
		return true
	}
	if err != nil {
		return refuse("unavailable")
	}
	ctx := internalBotRequestContext(r.Context(), wake.Owner)
	job, err := c.s.Job(ctx, wake.Owner, wake.JobID)
	if err != nil || job.Schedule.Condition == nil || !job.Schedule.Condition.WakesOnEvents() || conditionWakeID(job) != id {
		return refuse("unavailable")
	}
	cfg := *job.Schedule.Condition
	if err := c.current(ctx, job, cfg); err != nil {
		return refuse("access_or_disabled")
	}
	body, err := readWebhookJSONBody(w, r)
	if err != nil {
		return true
	}
	env, err := c.secretEnvironment(ctx, job, cfg)
	if err != nil {
		return refuse("secret_unavailable")
	}
	auth := cfg.EventAuthMode
	if auth == "" {
		auth = "bearer"
	}
	if !verifyWebhookRequest(&WorkflowWebhookConfig{AuthMode: auth}, env["SECRET_"+cfg.EventSecret], r, body) {
		return refuse("credentials")
	}
	if auth == "github" && r.Header.Get("X-GitHub-Event") == "ping" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "pong"})
		return true
	}
	coalesced, err := c.wake(ctx, job)
	if err != nil {
		scheduleLogf("[WATCHER_WAKE] watcher=%s source=webhook result=retry reason=persist", id)
		w.Header().Set("Retry-After", "5")
		http.Error(w, "could not save event; retry the delivery", http.StatusServiceUnavailable)
		return true
	}
	result := "queued"
	if coalesced {
		result = "coalesced"
	}
	scheduleLogf("[WATCHER_WAKE] watcher=%s source=webhook result=%s reason=notification", id, result)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "accepted", "coalesced": coalesced})
	return true
}

// wake is a generic entry point for already authenticated source adapters.
// Accept only after durable storage. The current source/target permissions
// remain enforced by the existing detector and dispatcher at execution.
func (c *conditionService) wake(ctx context.Context, job productScheduleJob) (bool, error) {
	id := conditionWakeID(job)
	m := c.wakeLock(id)
	m.Lock()
	wake, found, err := c.readWake(ctx, id)
	coalesced := wake.Received > wake.Checked
	if err == nil && !found {
		err = errors.New("watcher event receiver unavailable")
	}
	if err == nil {
		wake.Received++
		err = c.saveWake(ctx, id, wake)
	}
	m.Unlock()
	if err == nil {
		go c.processWake(context.Background(), job)
	}
	return coalesced, err
}

// One worker per watcher; a burst during a check becomes one follow-up check.
// The scheduler calls this too, recovering accepted wakes after restart/failure.
func (c *conditionService) processWake(ctx context.Context, job productScheduleJob) {
	source := "webhook"
	if job.Schedule.Condition != nil && job.Schedule.Condition.CheckMode == "inbox" {
		source = "inbox"
	}
	id := conditionWakeID(job)
	if _, loaded := c.wakeWorkers.LoadOrStore(id, true); loaded {
		return
	}
	owned := true
	defer func() {
		if owned {
			c.wakeWorkers.Delete(id)
		}
	}()
	for {
		m := c.wakeLock(id)
		m.Lock()
		wake, found, err := c.readWake(ctx, id)
		if err == nil && found && wake.Checked >= wake.Received {
			// Release ownership under the same lock as incoming wakes. A wake
			// after this read must be able to start a fresh worker immediately.
			c.wakeWorkers.Delete(id)
			owned = false
			m.Unlock()
			return
		}
		m.Unlock()
		if err != nil || !found {
			scheduleLogf("[WATCHER_WAKE] watcher=%s source=%s result=retry reason=read", id, source)
			return
		}
		if delay := time.Until(wake.NextCheckAt); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		fresh, err := c.s.Job(conditionContext(ctx, job), job.UserID, job.ID())
		if err != nil || fresh.Schedule.Condition == nil || !fresh.Schedule.Condition.WakesOnChanges() {
			return
		}
		if err := c.current(conditionContext(ctx, fresh), fresh, *fresh.Schedule.Condition); err != nil {
			scheduleLogf("[WATCHER_WAKE] watcher=%s source=%s result=refused reason=access_or_disabled", id, source)
			return
		}
		pause, _ := LoadSchedulerConfig(ctx)
		if pause.ProductPaused(fresh.Profile.ID) {
			return
		}
		m.Lock()
		wake, found, err = c.readWake(ctx, id)
		if err == nil && found {
			wake.NextCheckAt = time.Now().UTC().Add(conditionWakeSpacing)
			err = c.saveWake(ctx, id, wake)
		}
		m.Unlock()
		if err != nil || !found {
			return
		}
		if _, err := c.poll(ctx, fresh, source, time.Time{}); err != nil {
			scheduleLogf("[WATCHER_WAKE] watcher=%s source=%s result=retry reason=check", id, source)
			// A timed/manual check can be in flight too. Keep its notification
			// pending and retry after the burst spacing, without an extra tick.
			if errors.Is(err, productschedule.ErrAlreadyRunning) || errors.Is(err, conditiontrigger.ErrChecking) {
				continue
			}
			return
		}
		moreInbox := false
		if fresh.Schedule.Condition.CheckMode == "inbox" {
			ledger, readErr := c.engine.Read(conditionContext(ctx, fresh), conditionKey(fresh))
			if readErr != nil {
				return
			}
			page, readErr := c.inboxSnapshot(ctx, fresh, ledger.State)
			if readErr != nil {
				return
			}
			moreInbox = len(page.Messages) > 0
		}
		m.Lock()
		latest, found, err := c.readWake(ctx, id)
		if err == nil && found {
			latest.Checked = wake.Received
			if moreInbox && latest.Received == latest.Checked {
				latest.Received++
			}
			err = c.saveWake(ctx, id, latest)
		}
		m.Unlock()
		if err != nil || !found {
			scheduleLogf("[WATCHER_WAKE] watcher=%s source=%s result=retry reason=checkpoint", id, source)
			return
		}
		scheduleLogf("[WATCHER_WAKE] watcher=%s source=%s result=checked reason=notification", id, source)
	}
}

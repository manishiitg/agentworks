package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"path"
)

func inboxWatcherWorkspace(job productScheduleJob) string {
	if job.WatcherSource != nil {
		return job.WatcherSource.Path
	}
	return job.WorkspacePath
}

// Internal mailbox events reuse the durable Watcher wake/ledger; they expose no public webhook or mailbox credential.
func (api *StreamingAPI) wakeAgentInboxWatchers(ctx context.Context, inbox gmailinbound.Inbox) error {
	if IsLocalMode() || api.productSchedules == nil {
		return nil
	}
	c := api.productSchedules.conditionService()
	for _, owner := range conditionCallerOwners() {
		jobs, err := c.s.JobsForUser(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: owner}), owner)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			cfg := job.Schedule.Condition
			if cfg == nil || cfg.CheckMode != "inbox" || !job.Effective().Enabled || cfg.PersonalGmail() != inbox.PersonalGmail() {
				continue
			}
			if !inbox.PersonalGmail() && (job.Profile.ID != inbox.ProfileID || job.ProjectID != inbox.ProjectID) {
				continue
			}
			ownerCtx := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: owner})
			var current gmailinbound.Inbox
			var err error
			if cfg.PersonalGmail() {
				current, err = c.personalGmailSource(ownerCtx, job, *cfg)
			} else {
				current, err = api.resolveAgentEmailInbox(ownerCtx, inboxWatcherWorkspace(job))
			}
			if err != nil {
				continue
			}
			if current.ID != inbox.ID {
				continue
			}
			if err = c.ensureWakeReceiver(ctx, job); err != nil {
				return err
			}
			if _, err = c.wake(ctx, job); err != nil {
				return err
			}
		}
	}
	return nil
}
func (c *conditionService) inboxSnapshot(ctx context.Context, job productScheduleJob, state json.RawMessage) (gmailinbound.Page, error) {
	return c.readInboxSnapshot(ctx, job, state, true)
}
func (c *conditionService) readInboxSnapshot(ctx context.Context, job productScheduleJob, state json.RawMessage, content bool) (gmailinbound.Page, error) {
	if IsLocalMode() || c.s.api == nil || c.s.api.gmailInbound == nil {
		return gmailinbound.Page{}, fmt.Errorf("Agent inbox Watchers require a server deployment")
	}
	var inbox gmailinbound.Inbox
	var err error
	if job.Schedule.Condition != nil && job.Schedule.Condition.PersonalGmail() {
		inbox, err = c.personalGmailSource(ctx, job, *job.Schedule.Condition)
	} else {
		inbox, err = c.s.api.resolveAgentEmailInbox(internalBotRequestContext(ctx, job.UserID), inboxWatcherWorkspace(job))
	}
	if err != nil {
		return gmailinbound.Page{}, err
	}
	var cursor struct {
		After int64 `json:"inbox_cursor"`
	}
	if len(state) > 0 {
		if err = json.Unmarshal(state, &cursor); err != nil {
			return gmailinbound.Page{}, err
		}
	}
	page, err := c.s.api.gmailInbound.Store.Messages(ctx, inbox.ID, cursor.After, "", 20)
	if err != nil || !inbox.PersonalGmail() || !content {
		return page, err
	}
	_, email, err := userInboxClient(job.UserID)
	if err != nil {
		return gmailinbound.Page{}, err
	}
	client, err := c.s.api.gmailInbound.Client(ctx, gmailinbound.Mailbox{OwnerID: job.UserID, ConnectionID: inbox.ConnectionID, Email: email})
	if err != nil {
		return gmailinbound.Page{}, err
	}
	items := []gmailinbound.Item{}
	for _, item := range page.Messages {
		if item.ReceivedAt < job.Schedule.Condition.EmailSince {
			continue
		}
		raw, err := client.Message(ctx, item.ID)
		if errors.Is(err, gmailinbound.ErrMessageGone) {
			continue
		}
		if err != nil {
			return gmailinbound.Page{}, err
		}
		message, err := raw.Parse(email)
		if err != nil {
			return gmailinbound.Page{}, fmt.Errorf("Gmail message could not be read")
		}
		items = append(items, gmailinbound.Item{Sequence: item.Sequence, Message: message})
	}
	if _, err := c.personalGmailSource(ctx, job, *job.Schedule.Condition); err != nil {
		return gmailinbound.Page{}, err
	}
	page.Messages = items
	return page, nil
}
func (c *conditionService) writeInboxSnapshot(ctx context.Context, job productScheduleJob, cfg conditiontrigger.Config, state json.RawMessage, runRoot string) (int64, error) {
	if cfg.CheckMode != "inbox" {
		return 0, nil
	}
	page, err := c.inboxSnapshot(ctx, job, state)
	if err != nil {
		return 0, err
	}
	raw, err := json.Marshal(page)
	if err != nil {
		return 0, err
	}
	if err = c.s.writeFile(ctx, path.Join(runRoot, "inbox.json"), string(raw)); err != nil {
		return 0, err
	}
	return page.NextCursor, nil
}

// The platform advances the inbox cursor only together with the Watcher's durable events.
// Detector authors filter email; they don't implement mailbox bookkeeping.
func inboxWatcherOutput(raw []byte, cursor int64) ([]byte, error) {
	out, err := conditiontrigger.Parse(raw)
	if err != nil {
		return nil, err
	}
	state := map[string]interface{}{}
	if len(out.State) > 0 && string(out.State) != "null" {
		if err = json.Unmarshal(out.State, &state); err != nil {
			return nil, fmt.Errorf("Inbox Watcher state must be a JSON object")
		}
	}
	if state == nil {
		state = map[string]interface{}{}
	}
	state["inbox_cursor"] = cursor
	out.State, err = json.Marshal(state)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// Personal mail never uses the Crew inbox identity or shared workspace. Resolve
// the caller's own selected account every time, including before queued delivery.
func (c *conditionService) personalGmailSource(ctx context.Context, job productScheduleJob, cfg conditiontrigger.Config) (gmailinbound.Inbox, error) {
	if job.WatcherSource == nil || job.Profile.ID != crewProfileID || c.s.api == nil {
		return gmailinbound.Inbox{}, fmt.Errorf("Personal Gmail Watchers are available for your own Crews")
	}
	target, _, err := c.s.api.agentEmailCrew(internalBotRequestContext(ctx, job.UserID), inboxWatcherWorkspace(job))
	if err != nil {
		return gmailinbound.Inbox{}, err
	}
	if target.OwnerID != job.UserID {
		return gmailinbound.Inbox{}, fmt.Errorf("Only this Crew's owner can watch their personal Gmail")
	}
	client, _, err := userInboxClient(job.UserID)
	if err != nil {
		return gmailinbound.Inbox{}, err
	}
	if cfg.EmailAccountID != client.ConnectionID {
		return gmailinbound.Inbox{}, fmt.Errorf("Your Gmail inbox account changed. Create a new Watcher for the newly selected account")
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("personal-gmail:"+job.UserID+":"+client.ConnectionID)).String()
	return gmailinbound.Inbox{ID: id, ProfileID: gmailinbound.PersonalGmailProfile, ProjectID: job.UserID, OwnerID: job.UserID, ConnectionID: client.ConnectionID}, nil
}
func (c *conditionService) ensurePersonalGmailSource(ctx context.Context, inbox gmailinbound.Inbox) (gmailinbound.Inbox, error) {
	_, email, err := userInboxClient(inbox.OwnerID)
	if err != nil {
		return inbox, err
	}
	if err = c.s.api.gmailInbound.Store.SetMailbox(ctx, inbox.ConnectionID, inbox.OwnerID, email); err != nil {
		return inbox, err
	}
	return c.s.api.gmailInbound.Store.EnsureInbox(ctx, inbox)
}

// Reconcile the owner's private source immediately after a Watcher change.
// Inactive sources leave no broad mailbox query; dedicated aliases still poll.
func (c *conditionService) syncPersonalGmailOwner(ctx context.Context, owner string) ([]gmailinbound.Inbox, error) {
	ownerCtx := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: owner})
	jobs, err := c.s.JobsForUser(ownerCtx, owner)
	if err != nil {
		return nil, err
	}
	var inboxes []gmailinbound.Inbox
	for _, job := range jobs {
		cfg := job.Schedule.Condition
		if cfg == nil || !cfg.PersonalGmail() || !job.Effective().Enabled {
			continue
		}
		if err := c.authorize(ownerCtx, job, *cfg); err != nil {
			continue
		}
		inbox, err := c.personalGmailSource(ownerCtx, job, *cfg)
		if err != nil {
			continue
		}
		inbox, err = c.ensurePersonalGmailSource(ownerCtx, inbox)
		if err != nil {
			return nil, err
		}
		inboxes = append(inboxes, inbox)
	}
	if len(inboxes) == 0 {
		return nil, c.s.api.gmailInbound.Store.DisableInbox(ctx, gmailinbound.PersonalGmailProfile, owner, owner)
	}
	return inboxes, nil
}
func (api *StreamingAPI) syncPersonalGmailWatchers(ctx context.Context, active map[string]bool) error {
	if api.productSchedules == nil {
		return nil
	}
	c := api.productSchedules.conditionService()
	for _, owner := range conditionCallerOwners() {
		inboxes, err := c.syncPersonalGmailOwner(ctx, owner)
		if err != nil {
			return err
		}
		for _, inbox := range inboxes {
			active[inbox.ID] = true
		}
	}
	return nil
}

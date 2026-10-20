package server

import (
	"context"
	"encoding/json"
	"fmt"
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
		jobs, err := c.s.JobsForUser(internalBotRequestContext(ctx, owner), owner)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			cfg := job.Schedule.Condition
			if cfg == nil || cfg.CheckMode != "inbox" || !job.Effective().Enabled || job.Profile.ID != inbox.ProfileID || job.ProjectID != inbox.ProjectID {
				continue
			}
			current, err := api.resolveAgentEmailInbox(internalBotRequestContext(ctx, owner), inboxWatcherWorkspace(job))
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
	if IsLocalMode() || c.s.api == nil || c.s.api.gmailInbound == nil {
		return gmailinbound.Page{}, fmt.Errorf("Agent inbox Watchers require a server deployment")
	}
	inbox, err := c.s.api.resolveAgentEmailInbox(internalBotRequestContext(ctx, job.UserID), inboxWatcherWorkspace(job))
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
	return c.s.api.gmailInbound.Store.Messages(ctx, inbox.ID, cursor.After, "", 20)
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

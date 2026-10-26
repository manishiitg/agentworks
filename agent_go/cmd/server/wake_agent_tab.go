package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// A workflow can name a Crew tab (notifications.wake_tab) to be told straight
// away when one of its scheduled runs ends badly, so a Crew that looks after
// the workflow reacts in minutes instead of at its next daily check. The tab
// is woken by running that tab's own recurring check once, now, with a message
// that says which run to look at: the same chat delivery a schedule uses, no
// new channel. Routine good runs never wake anyone.

const wakeAgentTabCooldown = 20 * time.Minute

var wakeAgentTabLast = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

// wakeReasonForRun says how a finished run ended badly, or "" when it did not.
// error and partial are failures the scheduler already recorded; a success with
// no run evidence means the schedule fired but no workflow actually ran.
func wakeReasonForRun(status string, producedRunEvidence bool) string {
	switch status {
	case "error", "failed":
		return "failed"
	case "partial":
		return "stopped partway"
	case "blocked", "waiting_for_platform":
		return "did not start"
	case "no_run":
		return "finished without actually running the workflow"
	case "success":
		if !producedRunEvidence {
			return "finished without actually running the workflow"
		}
	}
	return ""
}

// wakeAgentTab messages the workflow's wake tab when its run ended badly. It
// never changes the run's recorded result and never blocks the scheduler.
func (s *SchedulerService) wakeAgentTab(ctx context.Context, sctx *ScheduleContext, manifest *WorkflowManifest, reason, errMsg, runID string) {
	if s == nil || s.api == nil || s.api.projectActions == nil || manifest == nil || sctx == nil {
		return
	}
	if sctx.WebhookInput != nil {
		return
	}
	notifications := manifest.Capabilities.Notifications
	if notifications == nil || notifications.WakeTab == nil {
		return
	}
	if reason == "" {
		return
	}
	wake := *notifications.WakeTab
	owner := strings.TrimSpace(sctx.OwnerUserID)
	if owner == "" || strings.TrimSpace(wake.CrewID) == "" || strings.TrimSpace(wake.ChatID) == "" {
		return
	}
	cooldownKey := sctx.WorkspacePath + "\x1f" + wake.CrewID + "\x1f" + wake.ChatID
	wakeAgentTabLast.Lock()
	if last, ok := wakeAgentTabLast.at[cooldownKey]; ok && time.Since(last) < wakeAgentTabCooldown {
		wakeAgentTabLast.Unlock()
		return
	}
	wakeAgentTabLast.at[cooldownKey] = time.Now()
	wakeAgentTabLast.Unlock()

	label := firstNonEmptyString(manifest.Label, sctx.WorkflowLabel, sctx.WorkspacePath)
	message := fmt.Sprintf("A scheduled run of %q just %s (schedule %q, run %s).", label, reason, sctx.Schedule.Name, runID)
	if detail := strings.TrimSpace(errMsg); detail != "" {
		if len([]rune(detail)) > 600 {
			detail = string([]rune(detail)[:600]) + "…"
		}
		message += " The run reported: " + detail
	}
	message += "\n\nLook at it now, as your daily check says: read this Goal's notes, follow the matching inspect skill, find the cause from the run records, save what you learn, and report here. If only the owner can unblock it, say so in one line."

	go func() {
		bg := internalBotRequestContext(context.WithoutCancel(ctx), owner)
		if err := s.api.projectActions.wakeCrewTab(bg, owner, wake.CrewID, wake.ChatID, message); err != nil {
			scheduleLogf("[WAKE-TAB] workflow=%s crew=%s chat=%s run=%s not delivered: %v", sctx.WorkspacePath, wake.CrewID, wake.ChatID, runID, err)
			return
		}
		scheduleLogf("[WAKE-TAB] workflow=%s crew=%s chat=%s run=%s woken (%s)", sctx.WorkspacePath, wake.CrewID, wake.ChatID, runID, reason)
	}()
}

// wakeCrewTab runs the owner's recurring check for one Crew tab once, now, with
// the given message in place of its saved one. chatID "main" (as workspace
// action=chats names it) is the owner's main chat, woken through a recurring
// check that runs there. If the chat is busy the message waits behind the live
// turn. A paused check stays paused.
func (s *ProjectActionDispatcher) wakeCrewTab(ctx context.Context, owner, crewID, chatID, message string) error {
	jobs, err := s.JobsForUser(ctx, owner)
	if err != nil {
		return err
	}
	main := strings.TrimSpace(chatID) == "main"
	tabKey := strings.TrimSpace(crewID) + codeChatSideMarker + strings.TrimSpace(chatID)
	for _, job := range jobs {
		if job.ProjectID != strings.TrimSpace(crewID) || job.Schedule.Condition != nil {
			continue
		}
		if main {
			if job.Schedule.Isolated || strings.TrimSpace(job.Schedule.ChatKey) != "" {
				continue
			}
		} else if scheduleChatKey(job) != tabKey {
			continue
		}
		if job.Schedule.CronExpression == "" || !job.Effective().Enabled {
			continue
		}
		wake := job
		wake.State.Messages = []string{message}
		_, err := s.runWithOptions(ctx, wake, "wake", time.Time{}, productScheduleRunOptions{Detach: true, AllowQueue: true})
		if errors.Is(err, ErrProductRunQueued) {
			return nil
		}
		return err
	}
	return fmt.Errorf("no enabled recurring check in that chat; create one first (a schedule that runs in that chat)")
}

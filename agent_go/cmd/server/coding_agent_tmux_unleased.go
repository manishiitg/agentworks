package server

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"
)

// A coding-agent pane gets a terminal lease when its first terminal event is observed. A turn that fails before Muse's prompt is confirmed
// (or any turn that emits no terminal event) never gets one, so the lease-based reaper cannot see its pane and it stays alive for good
// (30 panes and 8.7 GB after a day of failed scheduled ticks on one host). This backstop closes such a pane once it has had no activity for the
// same one-hour ceiling the reaper uses: it is named like a coding-agent pane, no lease knows it, nobody is attached to it.

// unleasedTmuxSweepEnabled is off in tests: they run on machines that have real panes.
var unleasedTmuxSweepEnabled = true

type tmuxSessionActivity struct {
	Name     string
	Created  time.Time
	Activity time.Time
	Attached bool
}

func parseTmuxSessionActivity(output string) []tmuxSessionActivity {
	var sessions []tmuxSessionActivity
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			continue
		}
		created, _ := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
		activity, _ := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64)
		attached, _ := strconv.Atoi(strings.TrimSpace(fields[3]))
		sessions = append(sessions, tmuxSessionActivity{
			Name:     strings.TrimSpace(fields[0]),
			Created:  time.Unix(created, 0),
			Activity: time.Unix(activity, 0),
			Attached: attached > 0,
		})
	}
	return sessions
}

// staleUnleasedCodingAgentTmux names the panes to close: coding-agent panes no lease knows, with nobody attached, idle for idleTimeout.
func staleUnleasedCodingAgentTmux(sessions []tmuxSessionActivity, leased map[string]bool, now time.Time, idleTimeout time.Duration) []string {
	var stale []string
	for _, session := range sessions {
		if !isCodingAgentTmuxSessionName(session.Name) || leased[session.Name] || session.Attached {
			continue
		}
		last := session.Activity
		if last.Before(session.Created) {
			last = session.Created
		}
		if last.IsZero() || now.Before(last.Add(idleTimeout)) {
			continue
		}
		stale = append(stale, session.Name)
	}
	return stale
}

func (api *StreamingAPI) cleanupUnleasedCodingAgentTmuxSessions(now time.Time) int {
	if !unleasedTmuxSweepEnabled {
		return 0
	}
	registry := api.ensureTerminalLeaseRegistry()
	if registry == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
	defer cancel()
	out, err := runTerminalTmuxOutputCommand(ctx, "list-sessions", "-F", "#{session_name}\t#{session_created}\t#{session_activity}\t#{session_attached}")
	if err != nil {
		if !isMissingTmuxTargetError(err) {
			log.Printf("[TMUX_REAPER] unleased pane scan failed: %v", err)
		}
		return 0
	}
	leased := map[string]bool{}
	for _, lease := range registry.List() {
		if name := strings.TrimSpace(lease.TmuxSession); name != "" {
			leased[name] = true
		}
	}
	closed := 0
	for _, name := range staleUnleasedCodingAgentTmux(parseTmuxSessionActivity(out), leased, now, codingAgentTmuxOrphanIdleTimeout()) {
		if !closeCodingAgentTmuxSessionByName(name, "stale coding-agent tmux cleanup: no lease, idle") {
			continue
		}
		closed++
		log.Printf("[TMUX_REAPER] Closed idle coding-agent tmux session %q: no terminal lease ever knew it (its turn emitted no terminal event)", name)
	}
	return closed
}

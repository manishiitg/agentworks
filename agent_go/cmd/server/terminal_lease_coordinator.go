package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminalleases"
)

const (
	tmuxOptionRunloopInstanceID     = "@runloop_instance_id"
	tmuxOptionRunloopOwnerPID       = "@runloop_owner_pid"
	tmuxOptionRunloopOwnerStartedAt = "@runloop_owner_started_at"
	tmuxOptionRunloopHeartbeat      = "@runloop_heartbeat"
	terminalLeaseTagAttempts        = 3
)

type ownedTmuxSession struct {
	Name           string
	InstanceID     string
	OwnerPID       int
	OwnerStartedAt int64
	Heartbeat      int64
}

func (api *StreamingAPI) ensureTerminalLeaseRegistry() *terminalleases.Registry {
	if api == nil {
		return nil
	}
	api.terminalLeaseMux.Lock()
	defer api.terminalLeaseMux.Unlock()
	if api.terminalLeaseRegistry == nil {
		now := time.Now()
		api.terminalLeaseRegistry = terminalleases.NewRegistry(
			fmt.Sprintf("%d-%d", os.Getpid(), now.UnixNano()),
			os.Getpid(),
			now,
		)
		if api.terminalStore != nil {
			for _, snapshot := range api.terminalStore.ListRaw("") {
				api.terminalLeaseRegistry.Observe(snapshot, snapshot.UpdatedAt)
			}
		}
	}
	return api.terminalLeaseRegistry
}

func markTerminalLeaseOwnership(lease terminalleases.Lease) {
	markTerminalLeaseOwnershipAt(lease, time.Now())
}

func markTerminalLeaseOwnershipAt(lease terminalleases.Lease, now time.Time) {
	if strings.TrimSpace(lease.TmuxSession) == "" || lease.OwnerPID <= 0 || strings.TrimSpace(lease.InstanceID) == "" {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	values := [][2]string{
		{tmuxOptionRunloopInstanceID, lease.InstanceID},
		{tmuxOptionRunloopOwnerPID, strconv.Itoa(lease.OwnerPID)},
		{tmuxOptionRunloopOwnerStartedAt, strconv.FormatInt(lease.OwnerStartedAt.Unix(), 10)},
		{tmuxOptionRunloopHeartbeat, strconv.FormatInt(now.Unix(), 10)},
	}
	for attempt := 1; attempt <= terminalLeaseTagAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
		var tagErr error
		for _, pair := range values {
			if err := runTerminalTmuxCommand(ctx, "", "set-option", "-t", lease.TmuxSession, pair[0], pair[1]); err != nil {
				tagErr = err
				break
			}
		}
		cancel()
		if tagErr == nil || isMissingTmuxTargetError(tagErr) {
			return
		}
		if attempt == terminalLeaseTagAttempts {
			log.Printf("[TERMINAL_LEASE] failed to tag tmux=%q after %d attempts: %v", lease.TmuxSession, attempt, tagErr)
			return
		}
		time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
	}
}

func (api *StreamingAPI) heartbeatTerminalLeases(now time.Time) {
	registry := api.ensureTerminalLeaseRegistry()
	if registry == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	for _, lease := range registry.List() {
		if lease.ProcessState == terminalleases.ProcessClosed || strings.TrimSpace(lease.TmuxSession) == "" {
			continue
		}
		// Refresh every ownership field, not only the heartbeat. This repairs a
		// partial initial tag write instead of leaving an unowned process forever.
		markTerminalLeaseOwnershipAt(lease, now)
	}
}

func tmuxSessionOwnedByRegistry(ctx context.Context, tmuxSession string, registry *terminalleases.Registry) bool {
	if registry == nil || strings.TrimSpace(tmuxSession) == "" {
		return false
	}
	out, err := runTerminalTmuxOutputCommand(ctx, "display-message", "-p", "-t", tmuxSession,
		"#{session_name}\t#{@runloop_instance_id}\t#{@runloop_owner_pid}\t#{@runloop_owner_started_at}\t#{@runloop_heartbeat}")
	if err != nil {
		return false
	}
	rows := parseOwnedTmuxSessions(out)
	if len(rows) != 1 {
		return false
	}
	owner := rows[0]
	return owner.Name == strings.TrimSpace(tmuxSession) &&
		owner.InstanceID == registry.InstanceID() &&
		owner.OwnerPID == registry.OwnerPID() &&
		absInt64(owner.OwnerStartedAt-registry.OwnerStartedAt().Unix()) <= 2
}

// sweepOrphanedOwnedTmuxSessions only removes sessions carrying Runloop owner
// metadata whose owner PID is no longer alive. Untagged legacy sessions and
// sessions owned by another live backend are deliberately preserved.
func sweepOrphanedOwnedTmuxSessions(ctx context.Context) int {
	out, err := runTerminalTmuxOutputCommand(ctx, "list-sessions", "-F",
		"#{session_name}\t#{@runloop_instance_id}\t#{@runloop_owner_pid}\t#{@runloop_owner_started_at}\t#{@runloop_heartbeat}")
	if err != nil {
		if !isMissingTmuxTargetError(err) {
			log.Printf("[TERMINAL_LEASE] startup ownership scan failed: %v", err)
		}
		return 0
	}

	closed := 0
	for _, session := range parseOwnedTmuxSessions(out) {
		if !isCodingAgentTmuxSessionName(session.Name) || session.InstanceID == "" || session.OwnerPID <= 0 {
			continue
		}
		if ownedTmuxOwnerIsAlive(session) {
			continue
		}
		if err := runTerminalTmuxCommand(ctx, "", "kill-session", "-t", session.Name); err != nil && !isMissingTmuxTargetError(err) {
			log.Printf("[TERMINAL_LEASE] startup orphan kill failed tmux=%q owner_pid=%d: %v", session.Name, session.OwnerPID, err)
			continue
		}
		closed++
		log.Printf("[TERMINAL_LEASE] recovered orphan tmux=%q instance=%q owner_pid=%d", session.Name, session.InstanceID, session.OwnerPID)
	}
	return closed
}

func parseOwnedTmuxSessions(output string) []ownedTmuxSession {
	var sessions []ownedTmuxSession
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			continue
		}
		ownerPID, _ := strconv.Atoi(strings.TrimSpace(fields[2]))
		ownerStartedAt, _ := strconv.ParseInt(strings.TrimSpace(fields[3]), 10, 64)
		heartbeat, _ := strconv.ParseInt(strings.TrimSpace(fields[4]), 10, 64)
		sessions = append(sessions, ownedTmuxSession{
			Name:           strings.TrimSpace(fields[0]),
			InstanceID:     strings.TrimSpace(fields[1]),
			OwnerPID:       ownerPID,
			OwnerStartedAt: ownerStartedAt,
			Heartbeat:      heartbeat,
		})
	}
	return sessions
}

func processIsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func ownedTmuxOwnerIsAlive(session ownedTmuxSession) bool {
	if !processIsAlive(session.OwnerPID) {
		return false
	}
	if session.OwnerStartedAt <= 0 {
		return true
	}
	startedAt, ok := processStartedAt(session.OwnerPID)
	if !ok {
		// Fail safe: an unreadable but live PID is not enough evidence to kill.
		return true
	}
	return absInt64(startedAt.Unix()-session.OwnerStartedAt) <= 2
}

func processStartedAt(pid int) (time.Time, bool) {
	if pid <= 0 {
		return time.Time{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
	defer cancel()
	psCmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	psCmd.Env = minimalChildEnv()
	out, err := psCmd.Output()
	if err != nil {
		return time.Time{}, false
	}
	startedAt, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.TrimSpace(string(out)), time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return startedAt, true
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

package knowledgebase

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AutoPushStatus is the last automatic push of Brain's folder, kept in Brain's private folder for the backup status.
type AutoPushStatus struct {
	At     time.Time `json:"at"`
	OK     bool      `json:"ok"`
	Pushed string    `json:"pushed,omitempty"` // the commit now on the remote branch
	Error  string    `json:"error,omitempty"`
}

var autoPushMu sync.Mutex

func (s *Service) autoPushStatusPath() string { return filepath.Join(s.private, "auto-push.json") }

// LastAutoPush reports the last automatic push, or nil when none has run.
func (s *Service) LastAutoPush() *AutoPushStatus {
	b, err := os.ReadFile(s.autoPushStatusPath())
	if err != nil {
		return nil
	}
	var st AutoPushStatus
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	return &st
}

// AutoPush pushes Brain's folder to its configured remote once its last commit is older than quiet, so the backup
// follows every save without anyone asking (owner, 2026-10-08). Every save is already a commit authored by the
// person who made it; this only delivers them. It never force-pushes: when the remote branch has commits Brain's
// folder does not, it stops and records why, and nothing is lost on either side. It returns whether it pushed.
func (s *Service) AutoPush(ctx context.Context, quiet time.Duration, now time.Time) (bool, error) {
	autoPushMu.Lock()
	defer autoPushMu.Unlock()
	f, err := s.GitFolder()
	if err != nil || f.Remote == "" {
		return false, nil // no backup configured: nothing to do
	}
	token := s.GitToken(f)
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = s.live
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", GitTokenEnv+"="+token, "BRAIN_GIT_USERNAME="+f.Username)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	head, err := git("rev-parse", "--verify", "-q", "HEAD")
	if err != nil || head == "" {
		return false, nil // nothing committed yet
	}
	if ts, err := git("log", "-1", "--format=%ct"); err == nil {
		if secs, perr := strconv.ParseInt(ts, 10, 64); perr == nil && now.Sub(time.Unix(secs, 0)) < quiet {
			return false, nil // still being edited: push once it is quiet
		}
	}
	if last := s.LastAutoPush(); last != nil && last.OK && last.Pushed == head {
		return false, nil
	}
	fail := func(message string) (bool, error) {
		s.saveAutoPush(AutoPushStatus{At: now, Error: message})
		return false, errors.New(message)
	}
	remoteRef := "refs/remotes/origin/" + f.Branch
	if out, err := git("fetch", "-q", "origin", "+refs/heads/"+f.Branch+":"+remoteRef); err != nil && !strings.Contains(out, "couldn't find remote ref") {
		return fail("fetch failed: " + lastLine(out))
	}
	remoteTip, _ := git("rev-parse", "--verify", "-q", remoteRef)
	if remoteTip == head {
		s.saveAutoPush(AutoPushStatus{At: now, OK: true, Pushed: head})
		return false, nil
	}
	if remoteTip != "" {
		if _, err := git("merge-base", "--is-ancestor", remoteTip, head); err != nil {
			return fail("the remote " + f.Branch + " has commits Brain's folder does not (edited elsewhere); not pushing, resolve once from the Brain chat")
		}
	}
	if out, err := git("push", "-q", "origin", "HEAD:refs/heads/"+f.Branch); err != nil {
		hint := ""
		if token == "" && f.PATSecret != "" {
			hint = " (the backup token " + f.PATSecret + " is not set in Brain's secrets)"
		}
		return fail("push failed: " + lastLine(out) + hint)
	}
	s.saveAutoPush(AutoPushStatus{At: now, OK: true, Pushed: head})
	return true, nil
}

func (s *Service) saveAutoPush(st AutoPushStatus) {
	b, _ := json.Marshal(st)
	tmp := s.autoPushStatusPath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.autoPushStatusPath())
	}
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if len(line) > 300 {
		line = line[:300]
	}
	return line
}

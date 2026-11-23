// Package stalldiag names what a coding-CLI pane shows when a turn has produced nothing for a while,
// without ever logging what the person or the model wrote.
package stalldiag

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
)

// knownPrompts are fixed phrases of the CLIs' own screens (sign-in, trust, update, approval, limit, picker). Only the
// phrase NAME is ever reported when a pane line contains one; no pane text is logged.
var knownPrompts = []string{
	"sign in with", "press enter to continue", "provide your own api key", "do you trust", "trust the files",
	"trust this folder", "update available", "update now", "approve", "allow this", "permission", "do you want to",
	"yes, proceed", "usage limit", "hit your", "rate limit", "credit balance", "model picker", "select model",
	"switch to", "keep current model", "esc to interrupt", "working (", "thinking", "queued input", "pane is dead",
	"not logged in", "login", "log in", "session expired", "api error",
}

// Result is what a pane shows, content-free: the NAMES of the known-prompt patterns it matched (fixed strings from
// the list above, never pane text), the number of non-empty lines, and a short hash of the whole pane. A pane line
// itself is never part of a Result, because the model's prose and the person's words can contain the same words.
type Result struct {
	Patterns []string
	Lines    int
	Hash     string
}

// Describe reduces a pane to a Result. Only lines containing a known phrase are quoted.
func Describe(pane string) Result {
	var r Result
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(pane, "\r", ""), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		r.Lines++
		lower := strings.ToLower(trimmed)
		for _, phrase := range knownPrompts {
			if strings.Contains(lower, phrase) {
				if !seen[phrase] {
					seen[phrase] = true
					r.Patterns = append(r.Patterns, phrase)
				}
				break
			}
		}
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(pane)))
	r.Hash = fmt.Sprintf("%x", sum[:4])
	return r
}

// Summary is Describe as one log fragment.
func Summary(pane string) string {
	r := Describe(pane)
	prompt := "none"
	if len(r.Patterns) > 0 {
		prompt = strings.Join(r.Patterns, "|")
	}
	return fmt.Sprintf("prompt=%q lines=%d lines_hash=%s", prompt, r.Lines, r.Hash)
}

// Watch logs one "[TURN] silent" line per quiet stretch: when a pane has not changed for `after`, the process-alive
// answer and the pane summary. A changed pane re-arms it. Not safe for concurrent use by one watcher.
type Watch struct {
	Provider string
	Session  string
	After    time.Duration
	last     string
	since    time.Time
	logged   bool
	mu       sync.Mutex
}

// Observe records the current pane at now and logs when the quiet stretch has reached After.
func (w *Watch) Observe(now time.Time, pane string, alive bool) {
	w.ObserveSignal(now, pane, alive, func() string { return Summary(pane) })
}

// ObserveSignal is Observe for anything whose change means progress (a pane, a log file's size and time). detail is
// called only when the line is logged and must return content-free text (numbers and fixed words).
func (w *Watch) ObserveSignal(now time.Time, signal string, alive bool, detail func() string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	after := w.After
	if after <= 0 {
		after = 2 * time.Minute
	}
	key := strings.TrimSpace(signal)
	if w.since.IsZero() || key != w.last {
		w.last, w.since, w.logged = key, now, false
		return
	}
	if !w.logged && now.Sub(w.since) >= after {
		w.logged = true
		extra := ""
		if detail != nil {
			extra = detail()
		}
		log.Printf("[TURN] silent session=%s app_session=%s provider=%s alive=%t quiet=%s %s",
			w.Session, ownerOf(w.Session), w.Provider, alive, now.Sub(w.since).Round(time.Second), extra)
	}
}

// ownerOf returns the app session that owns a terminal session (the owner tag the adapters set on it), so a search by
// the chat's session id finds the line. A name that is not a terminal session (the Muse retained path already uses
// the owner id) comes back as itself.
func ownerOf(session string) string {
	if strings.TrimSpace(session) == "" {
		return "-"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "show-options", "-v", "-t", session, tmuxinput.OwnerSessionOption).Output()
	if owner := strings.TrimSpace(string(out)); err == nil && owner != "" {
		return owner
	}
	return session
}

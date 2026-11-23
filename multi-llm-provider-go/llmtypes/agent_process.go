package llmtypes

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// AgentProcess records the life of one native coding-agent process (Claude
// SDK, Codex app-server, Cursor SDK, Muse, Antigravity, Pi) so restarts can be
// measured: every provider logs the same line,
//
//	[AGENT_PROCESS] provider=<p> session=<s> event=launch|reuse|exit reason=<r> uptime_s=<n> turns=<n>
//
// and counts launches, reuses and relaunch reasons for the day
// (AgentProcessStatsToday). Reasons are field names or short codes, never
// values. A relaunch should only happen for a real binary, argv, account,
// environment or confinement change (#2886).
type AgentProcess struct {
	provider string
	session  string
	started  time.Time
	mu       sync.Mutex
	turns    int
	exited   bool
}

// AgentProcessLogger is the logging interface adapters already carry.
type AgentProcessLogger interface {
	Infof(format string, args ...any)
}

// NewAgentProcess starts tracking a process. It logs nothing until Launched.
func NewAgentProcess(provider, session string) *AgentProcess {
	return &AgentProcess{provider: provider, session: session, started: time.Now()}
}

// Launched logs the launch (the process's first turn) with why a new process
// was needed: "first" when the session had none, otherwise the change that
// ruled out the previous one.
func (p *AgentProcess) Launched(logger AgentProcessLogger, reason string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.turns++
	turns := p.turns
	p.mu.Unlock()
	reason = agentProcessReason(reason, "first")
	agentProcessStats.add(p.provider, "launch", reason)
	p.log(logger, "launch", reason, turns)
}

// Reused logs a turn served by the already running process.
func (p *AgentProcess) Reused(logger AgentProcessLogger, reason string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.turns++
	turns := p.turns
	p.mu.Unlock()
	reason = agentProcessReason(reason, "launch_unchanged")
	agentProcessStats.add(p.provider, "reuse", reason)
	p.log(logger, "reuse", reason, turns)
}

// Exited logs the end of the process once; later calls are ignored.
func (p *AgentProcess) Exited(logger AgentProcessLogger, reason string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.exited {
		p.mu.Unlock()
		return
	}
	p.exited = true
	turns := p.turns
	p.mu.Unlock()
	reason = agentProcessReason(reason, "process_exited")
	agentProcessStats.add(p.provider, "exit", reason)
	p.log(logger, "exit", reason, turns)
}

func (p *AgentProcess) log(logger AgentProcessLogger, event, reason string, turns int) {
	session := p.session
	if session == "" {
		session = "-"
	}
	line := fmt.Sprintf("[AGENT_PROCESS] provider=%s session=%s event=%s reason=%s uptime_s=%d turns=%d",
		p.provider, session, event, reason, int64(time.Since(p.started).Seconds()), turns)
	if logger != nil {
		logger.Infof("%s", line)
		return
	}
	log.Print(line)
}

// agentProcessReason keeps the reason to one short token: the first changed
// field of a list ("launch_changed:a,b" -> "launch_changed:a"), no spaces.
func agentProcessReason(reason, fallback string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fallback
	}
	if prefix, fields, ok := strings.Cut(reason, ":"); ok {
		first, _, _ := strings.Cut(fields, ",")
		reason = prefix + ":" + strings.TrimSpace(first)
	}
	reason = strings.Join(strings.Fields(reason), "_")
	if len(reason) > 80 {
		reason = reason[:80]
	}
	return reason
}

// AgentProcessProviderStats is one provider's counts for the day.
type AgentProcessProviderStats struct {
	Provider string `json:"provider"`
	Launches int    `json:"launches"`
	Reuses   int    `json:"reuses"`
	Exits    int    `json:"exits"`
	// LaunchReasons counts why each launch happened ("first" is a new
	// session, anything else is a relaunch), most frequent first.
	LaunchReasons []AgentProcessReasonCount `json:"launch_reasons,omitempty"`
}

// AgentProcessReasonCount is one reason and how often it occurred.
type AgentProcessReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// AgentProcessStats is the day's counts across providers.
type AgentProcessStats struct {
	Day       string                      `json:"day"`
	Providers []AgentProcessProviderStats `json:"providers"`
}

type agentProcessCounts struct {
	launches, reuses, exits int
	reasons                 map[string]int
}

var agentProcessStats = &agentProcessTally{now: time.Now}

type agentProcessTally struct {
	mu        sync.Mutex
	now       func() time.Time
	day       string
	providers map[string]*agentProcessCounts
}

func (t *agentProcessTally) add(provider, event, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollLocked()
	c := t.providers[provider]
	if c == nil {
		c = &agentProcessCounts{reasons: map[string]int{}}
		t.providers[provider] = c
	}
	switch event {
	case "launch":
		c.launches++
		c.reasons[reason]++
	case "reuse":
		c.reuses++
	case "exit":
		c.exits++
	}
}

func (t *agentProcessTally) rollLocked() {
	day := t.now().Format("2006-01-02")
	if t.day != day || t.providers == nil {
		t.day = day
		t.providers = map[string]*agentProcessCounts{}
	}
}

func (t *agentProcessTally) snapshot(topReasons int) AgentProcessStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollLocked()
	out := AgentProcessStats{Day: t.day, Providers: []AgentProcessProviderStats{}}
	for provider, c := range t.providers {
		row := AgentProcessProviderStats{Provider: provider, Launches: c.launches, Reuses: c.reuses, Exits: c.exits}
		for reason, n := range c.reasons {
			row.LaunchReasons = append(row.LaunchReasons, AgentProcessReasonCount{Reason: reason, Count: n})
		}
		sort.Slice(row.LaunchReasons, func(i, j int) bool {
			if row.LaunchReasons[i].Count != row.LaunchReasons[j].Count {
				return row.LaunchReasons[i].Count > row.LaunchReasons[j].Count
			}
			return row.LaunchReasons[i].Reason < row.LaunchReasons[j].Reason
		})
		if topReasons > 0 && len(row.LaunchReasons) > topReasons {
			row.LaunchReasons = row.LaunchReasons[:topReasons]
		}
		out.Providers = append(out.Providers, row)
	}
	sort.Slice(out.Providers, func(i, j int) bool { return out.Providers[i].Provider < out.Providers[j].Provider })
	return out
}

// AgentProcessStatsToday reports today's launches, reuses and exits per
// provider in this server process, with the top launch reasons. Counts start
// again at local midnight and when the server restarts.
func AgentProcessStatsToday(topReasons int) AgentProcessStats {
	return agentProcessStats.snapshot(topReasons)
}

// AgentProcessDigests hashes each launch setting on its own, so a relaunch can
// name the settings that changed (names only, never values).
func AgentProcessDigests(config map[string]any) map[string][32]byte {
	parts := make(map[string][32]byte, len(config))
	for key, value := range config {
		raw, err := json.Marshal(value)
		if err != nil {
			raw = []byte(fmt.Sprintf("%T", value))
		}
		parts[key] = sha256.Sum256(raw)
	}
	return parts
}

// AgentProcessChanged names the settings that differ, sorted and joined with
// commas ("" when none or when there is no earlier launch to compare).
func AgentProcessChanged(old, current map[string][32]byte) string {
	if old == nil {
		return ""
	}
	var names []string
	for key, digest := range current {
		if prior, ok := old[key]; !ok || prior != digest {
			names = append(names, key)
		}
	}
	for key := range old {
		if _, ok := current[key]; !ok {
			names = append(names, key)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

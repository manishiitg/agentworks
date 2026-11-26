package llmtypes

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type agentProcessTestLogger struct{ lines []string }

func (l *agentProcessTestLogger) Infof(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func TestAgentProcessLogsAndCounts(t *testing.T) {
	day := time.Date(2026, 11, 16, 10, 0, 0, 0, time.Local)
	saved := agentProcessStats
	agentProcessStats = &agentProcessTally{now: func() time.Time { return day }}
	defer func() { agentProcessStats = saved }()

	logger := &agentProcessTestLogger{}
	first := NewAgentProcess("claude-code", "chat-1")
	first.Launched(logger, "")
	first.Reused(logger, "")
	first.Exited(logger, "stopped")
	first.Exited(logger, "stopped") // once only
	second := NewAgentProcess("claude-code", "chat-1")
	second.Launched(logger, "launch_changed:option:mcp_config,environment")
	NewAgentProcess("codex-cli", "chat-2").Launched(logger, "")

	if len(logger.lines) != 5 {
		t.Fatalf("lines = %d, want 5: %q", len(logger.lines), logger.lines)
	}
	for i, want := range []string{
		"[AGENT_PROCESS] provider=claude-code session=chat-1 event=launch reason=first uptime_s=0 turns=1",
		"[AGENT_PROCESS] provider=claude-code session=chat-1 event=reuse reason=launch_unchanged uptime_s=0 turns=2",
		"[AGENT_PROCESS] provider=claude-code session=chat-1 event=exit reason=stopped uptime_s=0 turns=2",
		"[AGENT_PROCESS] provider=claude-code session=chat-1 event=launch reason=launch_changed:option:mcp_config uptime_s=0 turns=1",
	} {
		if logger.lines[i] != want {
			t.Fatalf("line %d = %q, want %q", i, logger.lines[i], want)
		}
	}

	stats := AgentProcessStatsToday(5)
	if stats.Day != "2026-11-16" || len(stats.Providers) != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	claude := stats.Providers[0]
	if claude.Provider != "claude-code" || claude.Launches != 2 || claude.Reuses != 1 || claude.Exits != 1 || len(claude.LaunchReasons) != 2 {
		t.Fatalf("claude stats = %+v", claude)
	}

	day = day.Add(24 * time.Hour)
	if got := AgentProcessStatsToday(5); got.Day != "2026-11-17" || len(got.Providers) != 0 {
		t.Fatalf("next day stats = %+v, want empty", got)
	}
}

func TestAgentProcessReasonKeepsFirstFieldName(t *testing.T) {
	for in, want := range map[string]string{
		"": "fallback",
		"launch_changed:option:attached_skills,environment": "launch_changed:option:attached_skills",
		"new conversation":                 "new_conversation",
		"process_config_changed:HOME,PATH": "process_config_changed:HOME",
	} {
		if got := agentProcessReason(in, "fallback"); got != want {
			t.Fatalf("agentProcessReason(%q) = %q, want %q", in, got, want)
		}
	}
	if got := agentProcessReason(strings.Repeat("x", 200), ""); len(got) != 80 {
		t.Fatalf("long reason length = %d", len(got))
	}
}

func TestAgentProcessChangedNamesSettings(t *testing.T) {
	old := AgentProcessDigests(map[string]any{"model": "a", "system": "x", "env": []string{"K=1"}})
	now := AgentProcessDigests(map[string]any{"model": "a", "system": "y", "env": []string{"K=2"}, "skills": 1})
	if got := AgentProcessChanged(old, now); got != "env,skills,system" {
		t.Fatalf("changed = %q", got)
	}
	if got := AgentProcessChanged(nil, now); got != "" {
		t.Fatalf("changed without earlier launch = %q", got)
	}
}

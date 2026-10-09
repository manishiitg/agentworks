package server

import (
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

// terminalCLIUsage is a coding CLI's own usage report for one tmux pane, as
// read from its local files (never a model call).
type terminalCLIUsage struct {
	meta         map[string]interface{} // status_extras / rate_limit_windows
	inputTokens  int
	outputTokens int
	cacheRead    int
	totalInput   int
	totalOutput  int
	costUSD      float64
	readAt       time.Time
	found        bool
}

// terminalCLIUsageTTL bounds how often a pane's files are re-read while the
// UI polls the terminal list.
const terminalCLIUsageTTL = 5 * time.Second

var terminalCLIUsageCache = struct {
	sync.Mutex
	byTmux map[string]terminalCLIUsage
}{byTmux: map[string]terminalCLIUsage{}}

// readTerminalCLIUsage reads the CLI's usage for a pane. A var so tests can
// stub it.
var readTerminalCLIUsage = func(tmux string) terminalCLIUsage {
	switch {
	case strings.HasPrefix(tmux, "mlp-claude-code-"):
		status, ok := claudecode.StatusLineForTmuxSession(tmux)
		if !ok {
			return terminalCLIUsage{}
		}
		meta := map[string]interface{}{}
		for _, key := range []string{llmtypes.StatusExtrasMetaKey, llmtypes.RateLimitWindowsMetaKey} {
			if value, present := status.Metadata[key]; present {
				meta[key] = value
			}
		}
		return terminalCLIUsage{
			meta: meta, inputTokens: status.InputTokens, outputTokens: status.OutputTokens,
			cacheRead: status.CacheReadInputTokens, totalInput: status.TotalInputTokens,
			totalOutput: status.TotalOutputTokens, costUSD: status.CostUSD, found: true,
		}
	case strings.HasPrefix(tmux, "mlp-codex-cli-"):
		meta, ok := codexcli.StatusMetaForTmuxSession(tmux)
		return terminalCLIUsage{meta: meta, found: ok}
	}
	return terminalCLIUsage{}
}

func cachedTerminalCLIUsage(tmux string, now time.Time) terminalCLIUsage {
	terminalCLIUsageCache.Lock()
	cached, ok := terminalCLIUsageCache.byTmux[tmux]
	terminalCLIUsageCache.Unlock()
	if ok && now.Sub(cached.readAt) < terminalCLIUsageTTL {
		return cached
	}
	usage := readTerminalCLIUsage(tmux)
	usage.readAt = now
	terminalCLIUsageCache.Lock()
	terminalCLIUsageCache.byTmux[tmux] = usage
	terminalCLIUsageCache.Unlock()
	return usage
}

// withTerminalCLIUsage fills a terminal's usage from the coding CLI's own
// report. Launch-only turns (Crew chats) hand the reply to the transcript
// streamer, so the adapter never streams this for them and the hover showed
// no plan usage (server A 2026-09-27). The CLI's plan windows replace older ones;
// counters the stream already recorded are kept.
func withTerminalCLIUsage(snapshot terminals.Snapshot) terminals.Snapshot {
	tmux := strings.TrimSpace(snapshot.TmuxSession)
	if tmux == "" {
		return snapshot
	}
	usage := cachedTerminalCLIUsage(tmux, time.Now())
	if !usage.found {
		return snapshot
	}
	status := snapshot.Status
	if len(usage.meta) > 0 {
		meta := make(map[string]interface{}, len(status.StatusMeta)+len(usage.meta))
		for key, value := range status.StatusMeta {
			meta[key] = value
		}
		for key, value := range usage.meta {
			meta[key] = value
		}
		status.StatusMeta = meta
	}
	if status.InputTokens == 0 && status.TotalInputTokens == 0 {
		status.InputTokens = usage.inputTokens
		status.OutputTokens = usage.outputTokens
		status.CacheReadInputTokens = usage.cacheRead
		status.TotalInputTokens = usage.totalInput
		status.TotalOutputTokens = usage.totalOutput
	}
	if status.CostUSD == 0 {
		status.CostUSD = usage.costUSD
	}
	snapshot.Status = status
	return snapshot
}

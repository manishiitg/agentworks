package server

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Provider subscription windows are account state, not chat token totals. Keep
// every model/window label; the compact chat chip intentionally drops some.
type accountLimitSnapshot struct {
	Windows   []llmtypes.AccountUsageWindow `json:"windows"`
	CheckedAt time.Time                     `json:"checked_at"`
	Source    string                        `json:"source"`
}

type accountLimitCache struct {
	sync.Mutex
	snapshots map[string]accountLimitSnapshot
}

var subscriptionLimits = accountLimitCache{snapshots: map[string]accountLimitSnapshot{}}

func accountLimitKey(provider, account string) string {
	if account == "server-default:"+provider {
		account = "global:" + provider
	}
	return provider + "|" + strings.TrimSpace(account)
}

func cloneAccountLimitSnapshot(s accountLimitSnapshot) accountLimitSnapshot {
	s.Windows = append([]llmtypes.AccountUsageWindow{}, s.Windows...)
	for i := range s.Windows {
		if s.Windows[i].ResetsAt != nil {
			reset := *s.Windows[i].ResetsAt
			s.Windows[i].ResetsAt = &reset
		}
	}
	return s
}

func (c *accountLimitCache) get(provider, account string) (accountLimitSnapshot, bool) {
	c.Lock()
	defer c.Unlock()
	s, ok := c.snapshots[accountLimitKey(provider, account)]
	return cloneAccountLimitSnapshot(s), ok
}

// A slow probe started before a live event must not overwrite the event when
// its request finally returns. Observed timestamps are server-owned, not CLI text.
func (c *accountLimitCache) put(provider, account, source string, observed time.Time, windows []llmtypes.AccountUsageWindow) bool {
	if provider == "" || account == "" || observed.IsZero() || (source != "probe" && source != "event") {
		return false
	}
	var usage llmtypes.AccountUsage
	for _, window := range windows {
		reset := time.Time{}
		if window.ResetsAt != nil {
			reset = *window.ResetsAt
		}
		percent := window.UsedPercent
		usage.AddWindow(window.Label, &percent, reset)
	}
	s := cloneAccountLimitSnapshot(accountLimitSnapshot{Windows: usage.Windows, CheckedAt: observed, Source: source})
	c.Lock()
	key := accountLimitKey(provider, account)
	if previous, ok := c.snapshots[key]; ok && !observed.After(previous.CheckedAt) {
		c.Unlock()
		return false
	}
	if c.snapshots == nil {
		c.snapshots = map[string]accountLimitSnapshot{}
	}
	c.snapshots[key] = s
	c.Unlock()
	log.Printf("[ACCOUNT_LIMITS] account=%s provider=%s source=%s windows=%d", account, provider, source, len(s.Windows))
	// No account IDs, labels, owners or figures go over the global notice bus.
	// Every reader must re-authorize on the account-limits endpoint.
	livefeed.Default.Publish(livefeed.AccountLimits, "")
	return true
}

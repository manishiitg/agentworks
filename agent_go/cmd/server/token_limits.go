package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// Per-user daily and weekly token limits on the shared server accounts
// (PLAT-683, docs/DECISIONS.md 2026-10-07).
//
// An admin sets the limits on the person's users.json record. Only tokens a
// person spends on a server account (ledger account_id "global:<provider>")
// count; their own accounts and accounts shared to them never count and are
// never limited. Day and week are UTC; a week starts on Monday. At or over a
// limit, admission of a server account (admitProviderAccount, which every
// turn and model start goes through) is refused; a running turn is not
// stopped.

// UserTokenLimits is an admin-set cap on server-account tokens. Zero (or
// absent) is unlimited.
type UserTokenLimits struct {
	Daily  int64 `json:"daily,omitempty"`
	Weekly int64 `json:"weekly,omitempty"`
}

// normalized drops negative values and returns nil when nothing is limited.
func (l *UserTokenLimits) normalized() *UserTokenLimits {
	if l == nil {
		return nil
	}
	out := UserTokenLimits{Daily: max(l.Daily, 0), Weekly: max(l.Weekly, 0)}
	if out.Daily == 0 && out.Weekly == 0 {
		return nil
	}
	return &out
}

const (
	serverAccountIDPrefix  = "global:"
	tokenLimitTimezone     = "UTC"
	sharedTokenUsageTTL    = 20 * time.Second
	tokenLimitWarnFraction = 0.8
)

// tokenLimitNow is the clock limits are measured against; a var for tests.
var tokenLimitNow = time.Now

// tokenLimitWindows is the current UTC day and Monday-start week.
func tokenLimitWindows(now time.Time) (dayStart, dayEnd, weekStart, weekEnd time.Time) {
	now = now.UTC()
	dayStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	weekStart = dayStart.AddDate(0, 0, -((int(dayStart.Weekday()) + 6) % 7))
	return dayStart, dayStart.AddDate(0, 0, 1), weekStart, weekStart.AddDate(0, 0, 7)
}

// sharedAccountTokenUsage is what the admin page and the person's own
// Models panel show.
type sharedAccountTokenUsage struct {
	Timezone     string `json:"timezone"`
	DailyUsed    int64  `json:"daily_used"`
	WeeklyUsed   int64  `json:"weekly_used"`
	DailyLimit   int64  `json:"daily_limit,omitempty"`
	WeeklyLimit  int64  `json:"weekly_limit,omitempty"`
	DayResetsAt  string `json:"day_resets_at"`
	WeekResetsAt string `json:"week_resets_at"`
	// State is "ok", "warning" (80% of a limit) or "over".
	State string `json:"state"`
}

type cachedSharedTokenUsage struct {
	usage    costledger.AccountTokenUsage
	dayStart time.Time
	at       time.Time
}

var sharedTokenUsageCache = struct {
	sync.Mutex
	byUser map[string]cachedSharedTokenUsage
}{byUser: map[string]cachedSharedTokenUsage{}}

func (api *StreamingAPI) tokenLimitLedger() *costledger.Ledger {
	if api != nil && api.costLedger != nil {
		return api.costLedger
	}
	return costledger.DefaultLedger()
}

// sharedAccountTokensUsed is userID's server-account tokens today and this
// week, cached briefly so the per-turn check stays cheap.
func (api *StreamingAPI) sharedAccountTokensUsed(userID string, now time.Time) (costledger.AccountTokenUsage, error) {
	dayStart, _, weekStart, _ := tokenLimitWindows(now)
	sharedTokenUsageCache.Lock()
	cached, ok := sharedTokenUsageCache.byUser[userID]
	sharedTokenUsageCache.Unlock()
	if ok && cached.dayStart.Equal(dayStart) && now.Sub(cached.at) < sharedTokenUsageTTL {
		return cached.usage, nil
	}
	usage, err := api.tokenLimitLedger().AccountTokens(userID, serverAccountIDPrefix, dayStart, weekStart)
	if err != nil {
		return usage, err
	}
	sharedTokenUsageCache.Lock()
	sharedTokenUsageCache.byUser[userID] = cachedSharedTokenUsage{usage: usage, dayStart: dayStart, at: now}
	sharedTokenUsageCache.Unlock()
	return usage, nil
}

// sharedAccountTokenUsageFor reports rec's usage against its limits.
func (api *StreamingAPI) sharedAccountTokenUsageFor(rec *UserRecord) *sharedAccountTokenUsage {
	if rec == nil {
		return nil
	}
	now := tokenLimitNow()
	_, dayEnd, _, weekEnd := tokenLimitWindows(now)
	out := &sharedAccountTokenUsage{Timezone: tokenLimitTimezone, DayResetsAt: dayEnd.Format(time.RFC3339), WeekResetsAt: weekEnd.Format(time.RFC3339), State: "ok"}
	if limits := rec.TokenLimits.normalized(); limits != nil {
		out.DailyLimit, out.WeeklyLimit = limits.Daily, limits.Weekly
	}
	usage, err := api.sharedAccountTokensUsed(rec.ID, now)
	if err != nil {
		log.Printf("[TOKEN_LIMITS] cannot read server-account usage for %s: %v", rec.ID, err)
	}
	out.DailyUsed, out.WeeklyUsed = usage.Day, usage.Week
	for _, pair := range [][2]int64{{out.DailyUsed, out.DailyLimit}, {out.WeeklyUsed, out.WeeklyLimit}} {
		used, limit := pair[0], pair[1]
		if limit <= 0 {
			continue
		}
		if used >= limit {
			out.State = "over"
		} else if out.State == "ok" && float64(used) >= tokenLimitWarnFraction*float64(limit) {
			out.State = "warning"
		}
	}
	return out
}

// sharedAccountTokenLimitError is the refusal of a server account for a
// person at or over a limit.
type sharedAccountTokenLimitError struct{ message string }

func (e *sharedAccountTokenLimitError) Error() string { return e.message }

// sharedAccountTokenLimitRefusal returns the refusal when principal is at or
// over a limit, else nil. A ledger read error is logged and admits: the limit
// is a budget control, and a broken ledger must not stop every shared turn.
func (api *StreamingAPI) sharedAccountTokenLimitRefusal(principal string) error {
	principal = strings.TrimSpace(principal)
	if principal == "" {
		return nil
	}
	rec := directoryUserFor(principal, "", "")
	if rec == nil {
		return nil
	}
	limits := rec.TokenLimits.normalized()
	if limits == nil {
		return nil
	}
	now := tokenLimitNow()
	usage, err := api.sharedAccountTokensUsed(rec.ID, now)
	if err != nil {
		log.Printf("[TOKEN_LIMITS] cannot read server-account usage for %s, admitting: %v", rec.ID, err)
		return nil
	}
	// The weekly limit first: when both are reached it is the later reset.
	if limits.Weekly > 0 && usage.Week >= limits.Weekly {
		return &sharedAccountTokenLimitError{fmt.Sprintf("You have used your weekly limit of %s tokens on the shared accounts (resets Monday 00:00 UTC). Use your own account or ask an admin to raise it.", formatTokenCount(limits.Weekly))}
	}
	if limits.Daily > 0 && usage.Day >= limits.Daily {
		return &sharedAccountTokenLimitError{fmt.Sprintf("You have used your daily limit of %s tokens on the shared accounts (resets at 00:00 UTC). Use your own account or ask an admin to raise it.", formatTokenCount(limits.Daily))}
	}
	return nil
}

// formatTokenCount writes n with thousands separators.
func formatTokenCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// scheduledRunTokenLimitRefusal refuses a scheduled or triggered run whose
// workflow runs on a server account while its execution identity is at or
// over a limit, so the run is marked failed with the reason before it starts.
// A step that starts later still meets the same check at admission.
func (api *StreamingAPI) scheduledRunTokenLimitRefusal(ctx context.Context, sctx *ScheduleContext) error {
	if api == nil || sctx == nil || strings.TrimSpace(sctx.OwnerUserID) == "" {
		return nil
	}
	builder, _ := workshopResolveLLMConfig(lockedPresetLLMConfig(sctx.Capabilities.LLMConfig))
	if builder != nil && builder.Provider != "" && builder.ConnectionID != "" && !strings.HasPrefix(builder.ConnectionID, serverAccountIDPrefix) {
		return nil // the workflow runs on a person's own account
	}
	return api.sharedAccountTokenLimitRefusal(sctx.OwnerUserID)
}

// GET /api/me/token-usage — the caller's server-account usage and limits.
func (api *StreamingAPI) handleMyTokenUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	userID, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	rec := directoryUserFor(userID, "", "")
	if rec == nil {
		rec = &UserRecord{ID: userID}
	}
	writeUsersJSON(w, http.StatusOK, api.sharedAccountTokenUsageFor(rec))
}

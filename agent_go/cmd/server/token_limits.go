package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Per-user daily and weekly token limits on the shared server accounts
// (PLAT-683, docs/DECISIONS.md 2026-10-07).
//
// Each shared account (global:<provider>) has its own per-person default
// (provider-account-settings.json token_limits) and a person may have an
// override per account (users.json account_token_limits); only that
// account's events count, and reaching it refuses only that account. The
// person's token_limits stay an optional overall cap across all of them
// (PLAT-693).
//
// An admin sets the limits on the person's users.json record. Only tokens a
// person spends on a server account (ledger account_id "global:<provider>")
// count; their own accounts and accounts shared to them never count and are
// never limited. Day and week are UTC; a week starts on Monday. At or over a
// limit, admission of a server account (admitProviderAccount, which every
// turn and model start goes through) is refused; a running turn is not
// stopped.

// UserTokenLimits is an admin-set cap on server-account tokens. Zero (or
// absent) is unlimited. In a person's override of one shared account
// (account_token_limits) a field may also be TokenLimitUnlimited (-1):
// unlimited even when the account has a default, where 0 falls back to the
// default (docs/DECISIONS.md 2026-10-07, PLAT-693). Anywhere else -1 is the
// same as 0.
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

// TokenLimitUnlimited in an account override field means "no limit for this
// person on this account", beating the account default.
const TokenLimitUnlimited int64 = -1

// normalizedOverride is normalized for a per-account override: any negative
// field becomes TokenLimitUnlimited; nil when every field is 0 (all fall back).
func (l *UserTokenLimits) normalizedOverride() *UserTokenLimits {
	if l == nil {
		return nil
	}
	field := func(v int64) int64 {
		if v < 0 {
			return TokenLimitUnlimited
		}
		return v
	}
	out := UserTokenLimits{Daily: field(l.Daily), Weekly: field(l.Weekly)}
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
// Models panel show. The top-level figures are the overall cap across every
// shared account; Accounts has each shared account's own figures.
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
	// DailyViaBot and WeeklyViaBot are the part of the used figures that came
	// from Slack channel bot turns on this person's workflows, Crews or Code.
	DailyViaBot  int64 `json:"daily_via_bot,omitempty"`
	WeeklyViaBot int64 `json:"weekly_via_bot,omitempty"`
	// Accounts is keyed by provider (e.g. "codex-cli"): every shared account
	// the person used this week or that has a limit for them.
	Accounts map[string]*accountTokenUsage `json:"accounts,omitempty"`
}

// accountTokenUsage is one shared account's use and effective limits for a
// person, with the account default and the person's override shown apart.
type accountTokenUsage struct {
	Label       string           `json:"label"`
	DailyUsed   int64            `json:"daily_used"`
	WeeklyUsed  int64            `json:"weekly_used"`
	DailyLimit  int64            `json:"daily_limit,omitempty"`
	WeeklyLimit int64            `json:"weekly_limit,omitempty"`
	Default     *UserTokenLimits `json:"default_limits,omitempty"`
	Override    *UserTokenLimits `json:"override,omitempty"`
	State       string           `json:"state"`
	// DailyViaBot and WeeklyViaBot: see sharedAccountTokenUsage.
	DailyViaBot  int64 `json:"daily_via_bot,omitempty"`
	WeeklyViaBot int64 `json:"weekly_via_bot,omitempty"`
}

type cachedSharedTokenUsage struct {
	// byAccount is keyed by ledger account ID ("global:<provider>").
	byAccount map[string]costledger.AccountTokenUsage
	dayStart  time.Time
	at        time.Time
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

// sharedAccountTokensUsed is userID's tokens today and this week on each
// server account, cached briefly so the per-turn check stays cheap.
func (api *StreamingAPI) sharedAccountTokensUsed(userID string, now time.Time) (map[string]costledger.AccountTokenUsage, error) {
	dayStart, _, weekStart, _ := tokenLimitWindows(now)
	sharedTokenUsageCache.Lock()
	cached, ok := sharedTokenUsageCache.byUser[userID]
	sharedTokenUsageCache.Unlock()
	if ok && cached.dayStart.Equal(dayStart) && now.Sub(cached.at) < sharedTokenUsageTTL {
		return cached.byAccount, nil
	}
	usage, err := api.tokenLimitLedger().AccountTokensByAccount(userID, serverAccountIDPrefix, dayStart, weekStart)
	if err != nil {
		return usage, err
	}
	sharedTokenUsageCache.Lock()
	sharedTokenUsageCache.byUser[userID] = cachedSharedTokenUsage{byAccount: usage, dayStart: dayStart, at: now}
	sharedTokenUsageCache.Unlock()
	return usage, nil
}

func totalAccountTokens(byAccount map[string]costledger.AccountTokenUsage) costledger.AccountTokenUsage {
	var total costledger.AccountTokenUsage
	for _, usage := range byAccount {
		total.Day += usage.Day
		total.Week += usage.Week
		total.ViaBotDay += usage.ViaBotDay
		total.ViaBotWeek += usage.ViaBotWeek
	}
	return total
}

// tokenLimitState is "over" at or past a limit, "warning" from 80%, else "ok".
func tokenLimitState(dailyUsed, dailyLimit, weeklyUsed, weeklyLimit int64) string {
	state := "ok"
	for _, pair := range [][2]int64{{dailyUsed, dailyLimit}, {weeklyUsed, weeklyLimit}} {
		used, limit := pair[0], pair[1]
		if limit <= 0 {
			continue
		}
		if used >= limit {
			return "over"
		}
		if float64(used) >= tokenLimitWarnFraction*float64(limit) {
			state = "warning"
		}
	}
	return state
}

// normalizedAccountTokenLimits drops accounts with no override (every field
// 0); nil when none. An unlimited field (-1) is kept.
func normalizedAccountTokenLimits(in map[string]*UserTokenLimits) map[string]*UserTokenLimits {
	var out map[string]*UserTokenLimits
	for provider, limits := range in {
		if limits = limits.normalizedOverride(); limits != nil {
			if out == nil {
				out = map[string]*UserTokenLimits{}
			}
			out[provider] = limits
		}
	}
	return out
}

// validTokenLimitAccount reports whether provider names a shared server
// account limits may be set for.
func validTokenLimitAccount(provider string) bool {
	for _, known := range supportedLLMProviders {
		if provider == known {
			return true
		}
	}
	return false
}

// applyAccountTokenLimits merges an admin's per-account overrides into rec:
// each named account's override is replaced, one with no limit removed.
func applyAccountTokenLimits(rec *UserRecord, requested map[string]*UserTokenLimits) error {
	if len(requested) == 0 {
		return nil
	}
	next := normalizedAccountTokenLimits(rec.AccountTokenLimits)
	for provider, limits := range requested {
		provider = strings.TrimSpace(provider)
		if !validTokenLimitAccount(provider) {
			return fmt.Errorf("unknown shared account %q", provider)
		}
		if limits = limits.normalizedOverride(); limits == nil {
			delete(next, provider)
			continue
		}
		if next == nil {
			next = map[string]*UserTokenLimits{}
		}
		next[provider] = limits
	}
	rec.AccountTokenLimits = normalizedAccountTokenLimits(next)
	return nil
}

// accountTokenLimitsSummary is a log line like "codex-cli=5000000/0".
func accountTokenLimitsSummary(in map[string]*UserTokenLimits) string {
	in = normalizedAccountTokenLimits(in)
	if len(in) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(in))
	for provider := range in {
		keys = append(keys, provider)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, provider := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d/%d", provider, in[provider].Daily, in[provider].Weekly))
	}
	return strings.Join(parts, ",")
}

// serverAccountTokenLimitDefaults is every shared account's default
// per-person limits (Providers -> the server account -> Limits).
func serverAccountTokenLimitDefaults(ctx context.Context) map[string]*UserTokenLimits {
	settings, err := loadProviderAccountSettings(ctx)
	if err != nil {
		log.Printf("[TOKEN_LIMITS] cannot read the shared account limits: %v", err)
		return nil
	}
	var out map[string]*UserTokenLimits
	for provider, limits := range settings.TokenLimits {
		if limits = limits.normalized(); limits != nil {
			if out == nil {
				out = map[string]*UserTokenLimits{}
			}
			out[provider] = limits
		}
	}
	return out
}

// effectiveAccountTokenLimits is the person's limits on one shared account:
// each set field of their override replaces the account default's field (a
// limit, or TokenLimitUnlimited for none); a 0 field falls back to the default.
func effectiveAccountTokenLimits(defaults *UserTokenLimits, override *UserTokenLimits) *UserTokenLimits {
	out := UserTokenLimits{}
	if defaults = defaults.normalized(); defaults != nil {
		out = *defaults
	}
	if override = override.normalizedOverride(); override != nil {
		if override.Daily != 0 {
			out.Daily = override.Daily
		}
		if override.Weekly != 0 {
			out.Weekly = override.Weekly
		}
	}
	return out.normalized()
}

// sharedAccountLabel is the short name in "the shared Codex account".
func sharedAccountLabel(provider string) string {
	if provider == "agy-cli" {
		return "Antigravity"
	}
	label := providerDisplayLabel(provider)
	label = strings.TrimSuffix(strings.TrimPrefix(label, "OpenAI "), " CLI")
	return label
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
	byAccount, err := api.sharedAccountTokensUsed(rec.ID, now)
	if err != nil {
		log.Printf("[TOKEN_LIMITS] cannot read server-account usage for %s: %v", rec.ID, err)
	}
	total := totalAccountTokens(byAccount)
	out.DailyUsed, out.WeeklyUsed = total.Day, total.Week
	out.DailyViaBot, out.WeeklyViaBot = total.ViaBotDay, total.ViaBotWeek
	out.State = tokenLimitState(out.DailyUsed, out.DailyLimit, out.WeeklyUsed, out.WeeklyLimit)

	defaults := serverAccountTokenLimitDefaults(context.Background())
	overrides := normalizedAccountTokenLimits(rec.AccountTokenLimits)
	providers := map[string]bool{}
	for accountID := range byAccount {
		providers[strings.TrimPrefix(accountID, serverAccountIDPrefix)] = true
	}
	for provider := range defaults {
		providers[provider] = true
	}
	for provider := range overrides {
		providers[provider] = true
	}
	for provider := range providers {
		used := byAccount[serverAccountIDPrefix+provider]
		account := &accountTokenUsage{Label: sharedAccountLabel(provider), DailyUsed: used.Day, WeeklyUsed: used.Week, DailyViaBot: used.ViaBotDay, WeeklyViaBot: used.ViaBotWeek, Default: defaults[provider], Override: overrides[provider]}
		if limits := effectiveAccountTokenLimits(defaults[provider], overrides[provider]); limits != nil {
			account.DailyLimit, account.WeeklyLimit = limits.Daily, limits.Weekly
		}
		account.State = tokenLimitState(account.DailyUsed, account.DailyLimit, account.WeeklyUsed, account.WeeklyLimit)
		if out.Accounts == nil {
			out.Accounts = map[string]*accountTokenUsage{}
		}
		out.Accounts[provider] = account
	}
	return out
}

// sharedAccountTokenLimitError is the refusal of a server account for a
// person at or over a limit.
type sharedAccountTokenLimitError struct{ message string }

func (e *sharedAccountTokenLimitError) Error() string { return e.message }

// tokenLimitHit is the limit a person has reached: one shared account's own
// (provider set) or the overall cap across every shared account, weekly or
// daily.
type tokenLimitHit struct {
	provider string
	overall  bool
	weekly   bool
	limit    int64
}

// sharedAccountTokenLimitHit returns the limit principal is at or over on
// provider's shared account, or their overall cap, else nil. A ledger read
// error is logged and admits: the limit is a budget control, and a broken
// ledger must not stop every shared turn.
func (api *StreamingAPI) sharedAccountTokenLimitHit(ctx context.Context, principal, provider string) *tokenLimitHit {
	principal, provider = strings.TrimSpace(principal), strings.TrimSpace(provider)
	if principal == "" {
		return nil
	}
	rec := directoryUserFor(principal, "", "")
	if rec == nil {
		return nil
	}
	overall := rec.TokenLimits.normalized()
	var account *UserTokenLimits
	if provider != "" {
		account = effectiveAccountTokenLimits(serverAccountTokenLimitDefaults(ctx)[provider], normalizedAccountTokenLimits(rec.AccountTokenLimits)[provider])
	}
	if overall == nil && account == nil {
		return nil
	}
	now := tokenLimitNow()
	byAccount, err := api.sharedAccountTokensUsed(rec.ID, now)
	if err != nil {
		log.Printf("[TOKEN_LIMITS] cannot read server-account usage for %s, admitting: %v", rec.ID, err)
		return nil
	}
	// The weekly limit first: when both are reached it is the later reset.
	if account != nil {
		used := byAccount[serverAccountIDPrefix+provider]
		if account.Weekly > 0 && used.Week >= account.Weekly {
			return &tokenLimitHit{provider: provider, weekly: true, limit: account.Weekly}
		}
		if account.Daily > 0 && used.Day >= account.Daily {
			return &tokenLimitHit{provider: provider, limit: account.Daily}
		}
	}
	if overall != nil {
		usage := totalAccountTokens(byAccount)
		if overall.Weekly > 0 && usage.Week >= overall.Weekly {
			return &tokenLimitHit{overall: true, weekly: true, limit: overall.Weekly}
		}
		if overall.Daily > 0 && usage.Day >= overall.Daily {
			return &tokenLimitHit{overall: true, limit: overall.Daily}
		}
	}
	return nil
}

// sharedAccountTokenLimitRefusal returns the refusal when principal is at or
// over the limit of provider's shared account, or their overall cap across
// every shared account, else nil.
func (api *StreamingAPI) sharedAccountTokenLimitRefusal(ctx context.Context, principal, provider string) error {
	hit := api.sharedAccountTokenLimitHit(ctx, principal, provider)
	switch {
	case hit == nil:
		return nil
	case !hit.overall && hit.weekly:
		return &sharedAccountTokenLimitError{fmt.Sprintf("You have used this week's %s tokens on the shared %s account (resets Monday 00:00 UTC). Switch to another account in Models or ask an admin to raise it.", formatTokenAmount(hit.limit), sharedAccountLabel(hit.provider))}
	case !hit.overall:
		return &sharedAccountTokenLimitError{fmt.Sprintf("You have used today's %s tokens on the shared %s account (resets 00:00 UTC). Switch to another account in Models or ask an admin to raise it.", formatTokenAmount(hit.limit), sharedAccountLabel(hit.provider))}
	case hit.weekly:
		return &sharedAccountTokenLimitError{fmt.Sprintf("You have used your weekly limit of %s tokens on the shared accounts (resets Monday 00:00 UTC). Use your own account or ask an admin to raise it.", formatTokenCount(hit.limit))}
	default:
		return &sharedAccountTokenLimitError{fmt.Sprintf("You have used your daily limit of %s tokens on the shared accounts (resets at 00:00 UTC). Use your own account or ask an admin to raise it.", formatTokenCount(hit.limit))}
	}
}

// botRouteTokenLimitRefusal is sharedAccountTokenLimitRefusal for a Slack
// channel turn billed to owner, worded for the people in the channel (who
// are not the owner and may not have an account). It names the account and
// the reset, never the owner's email.
func (api *StreamingAPI) botRouteTokenLimitRefusal(ctx context.Context, owner, provider string, run providerAccountRun) error {
	hit := api.sharedAccountTokenLimitHit(ctx, owner, provider)
	if hit == nil {
		return nil
	}
	who := "The owner of " + run.Label
	if run.Label == "" || run.Label == "this run" {
		who = "The owner this bot answers for"
	}
	when, resets := "today's", "resets 00:00 UTC"
	if hit.weekly {
		when, resets = "this week's", "resets Monday 00:00 UTC"
	}
	what := fmt.Sprintf("%s %s tokens on the shared %s account", when, formatTokenAmount(hit.limit), sharedAccountLabel(hit.provider))
	if hit.overall {
		period := "daily"
		if hit.weekly {
			period = "weekly"
		}
		what = fmt.Sprintf("their %s limit of %s tokens on the shared accounts", period, formatTokenCount(hit.limit))
	}
	return &sharedAccountTokenLimitError{fmt.Sprintf("%s has used %s (%s); this channel's bot turns count toward their limit. Try again after the reset, or ask them or an admin to raise it.", who, what, resets)}
}

// botRouteTokenOwner is whose shared-account token limits a Slack channel
// turn by principal counts toward: the owner of the workflow (the same
// active owner a scheduled run uses), Crew or Code (the owner registry) the
// route answers for. "" for any turn that is not a Slack channel turn of
// principal, or when no owner resolves (the turn then counts as before).
// Slack DMs and WhatsApp already run as the person and are not bot routes.
func botRouteTokenOwner(ctx context.Context, principal, workspacePath string) string {
	claims := GetUserFromContext(ctx)
	principal = strings.TrimSpace(principal)
	if claims == nil || claims.Provider != "bot_route" || principal == "" || strings.TrimSpace(claims.UserID) != principal {
		return ""
	}
	resourceOwner := principal
	if p := claims.ExecutionPrincipal; p != nil && strings.TrimSpace(p.ResourceOwnerID) != "" {
		resourceOwner = strings.TrimSpace(p.ResourceOwnerID)
	}
	path := strings.Trim(filepath.ToSlash(strings.TrimSpace(workspacePath)), "/")
	if path == "" {
		path = strings.Trim(filepath.ToSlash(strings.TrimSpace(claims.BotRouteWorkspacePath)), "/")
	}
	owner := ""
	switch {
	case path == "":
	case isCodeProjectPath(path):
		// An explicit owner's path is kept; a logical one is placed under the resource owner.
		root := workspaceref.PhysicalPath(resourceOwner, path)
		if ref, ok := workspaceref.Parse(path); ok && ref.HasOwner() {
			root = ref.PhysicalKeepOwner(resourceOwner)
		}
		owner = resolveProjectOwner(ctx, root)
	default:
		if ref, ok := resolveCrewPath(ctx, resourceOwner, path); ok {
			owner = ref.OwnerID
		} else if root := livefeed.WorkflowRoot(path); strings.HasPrefix(root, "Workflow/") {
			if manifest, exists, err := ReadWorkflowManifest(ctx, root); err == nil && exists {
				owner = workflowExecutionOwnerUserID(manifest)
			}
		}
	}
	if owner == "" && resourceOwner != principal {
		owner = resourceOwner
	}
	return directoryUserIDForOwner(owner)
}

// directoryUserIDForOwner maps an owner as a path segment (the sanitized
// form the owner registry and _users/<owner> hold) to the directory user ID
// the ledger and the usage views key on; unchanged when none matches.
func directoryUserIDForOwner(owner string) string {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return ""
	}
	if rec := directoryUserFor(owner, "", ""); rec != nil {
		return rec.ID
	}
	if dir, err := loadUserDirectory(); err == nil && dir != nil {
		for i := range dir.Users {
			if sanitizeUserIDForPath(dir.Users[i].ID) == owner {
				return dir.Users[i].ID
			}
		}
	}
	return owner
}

// tokenLimitOwnerForScope is the person whose limits a turn in scope counts
// toward: scope.TokenOwner, else the bot route owner from ctx, else the
// principal.
func tokenLimitOwnerForScope(ctx context.Context, scope providerAccountScope) string {
	if owner := strings.TrimSpace(scope.TokenOwner); owner != "" {
		return owner
	}
	if owner := botRouteTokenOwner(ctx, scope.Principal, scope.WorkspacePath); owner != "" {
		return owner
	}
	return scope.Principal
}

// formatTokenAmount writes a round limit short ("2M", "2.5M", "500k"), any
// other number with thousands separators.
func formatTokenAmount(n int64) string {
	switch {
	case n >= 1_000_000 && n%100_000 == 0:
		return strconv.FormatFloat(float64(n)/1_000_000, 'f', -1, 64) + "M"
	case n >= 1_000 && n < 1_000_000 && n%100 == 0:
		return strconv.FormatFloat(float64(n)/1_000, 'f', -1, 64) + "k"
	}
	return formatTokenCount(n)
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
	if builder != nil && builder.Provider != "" && builder.ConnectionID != "" && !strings.HasPrefix(builder.ConnectionID, serverAccountIDPrefix) && !strings.HasPrefix(builder.ConnectionID, llmguard.ServerDefaultConnectionPrefix) {
		return nil // the workflow runs on a person's own account
	}
	// The account the run starts on: the workflow's provider, else the
	// Goals default. Unknown leaves only the overall cap.
	provider := ""
	if builder != nil {
		provider = builder.Provider
	}
	if provider == "" {
		if defaults, err := effectiveProductDefaults(ctx); err == nil {
			provider = defaults[productWorkflows].Provider
		}
	}
	return api.sharedAccountTokenLimitRefusal(ctx, sctx.OwnerUserID, provider)
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

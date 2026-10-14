package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
)

// Per-model token limits on a shared server account. An admin gives one model
// of the account (for example a larger, costlier one) its own default
// per-person daily and weekly limit next to the account's. A person at or over
// a model's limit is moved to the next allowed model that is not at its own,
// like a model the account does not allow (resolveAccountModel); with none
// left the turn is refused. Only tokens on that model count toward its limit,
// and they still count toward the account's and the person's overall caps.

type modelUsageCacheEntry struct {
	byModel  map[string]costledger.AccountTokenUsage
	dayStart time.Time
	at       time.Time
}

var modelTokenUsageCache = struct {
	sync.Mutex
	byKey map[string]modelUsageCacheEntry
}{byKey: map[string]modelUsageCacheEntry{}}

// sharedAccountModelTokensUsed is person's tokens today and this week on each
// model of provider's server account (lower-cased model ID), cached briefly.
func sharedAccountModelTokensUsed(person, provider string, now time.Time) (map[string]costledger.AccountTokenUsage, error) {
	dayStart, _, weekStart, _ := tokenLimitWindows(now)
	key := person + "|" + provider
	modelTokenUsageCache.Lock()
	cached, ok := modelTokenUsageCache.byKey[key]
	modelTokenUsageCache.Unlock()
	if ok && cached.dayStart.Equal(dayStart) && now.Sub(cached.at) < sharedTokenUsageTTL {
		return cached.byModel, nil
	}
	ledger := (*StreamingAPI)(nil).tokenLimitLedger()
	all, err := ledger.AccountModelTokens(person, serverAccountIDPrefix, dayStart, weekStart)
	if err != nil {
		return nil, err
	}
	byModel := all[serverAccountIDPrefix+provider]
	modelTokenUsageCache.Lock()
	modelTokenUsageCache.byKey[key] = modelUsageCacheEntry{byModel: byModel, dayStart: dayStart, at: now}
	modelTokenUsageCache.Unlock()
	return byModel, nil
}

// modelAtTokenLimit reports the limit and period when person has used up
// model's own limit on provider's server account.
func modelAtTokenLimit(limits map[string]*UserTokenLimits, usage map[string]costledger.AccountTokenUsage, model string) (limit int64, weekly, over bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	l := limits[model].normalized()
	if l == nil {
		return 0, false, false
	}
	used := usage[model]
	if l.Weekly > 0 && used.Week >= l.Weekly {
		return l.Weekly, true, true
	}
	if l.Daily > 0 && used.Day >= l.Daily {
		return l.Daily, false, true
	}
	return 0, false, false
}

func modelLimitRefusal(provider, model string, limit int64, weekly bool) error {
	period, resets := "today's", "resets 00:00 UTC"
	if weekly {
		period, resets = "this week's", "resets Monday 00:00 UTC"
	}
	return &sharedAccountTokenLimitError{fmt.Sprintf("You have used %s %s tokens of %s on the shared %s account (%s), and no other allowed model is available. This is the per-model limit an admin set on the shared account; your own accounts are not affected. Ask an admin to raise it or switch to your own account.", period, formatTokenAmount(limit), model, sharedAccountLabel(provider), resets)}
}

// isServerAccountConnection reports whether connectionID names the provider's
// server account (empty, global:<provider> or the server-default marker).
func isServerAccountConnection(connectionID string) bool {
	connectionID = strings.TrimSpace(connectionID)
	return connectionID == "" || strings.HasPrefix(connectionID, serverAccountIDPrefix) || strings.HasPrefix(connectionID, llmguard.ServerDefaultConnectionPrefix)
}

// applyModelTokenLimit moves a turn off model when the person is at that
// model's token limit: onto the first allowed model (else the provider's
// known models) that is not at its own. changed reports a substitution.
// A ledger read error is logged and admits, like the other limits.
func applyModelTokenLimit(ctx context.Context, provider, connectionID, model string, allowed []string) (resolved string, changed bool, err error) {
	model = strings.TrimSpace(model)
	if model == "" || provider == "" || !isServerAccountConnection(connectionID) {
		return model, false, nil
	}
	settings, loadErr := loadProviderAccountSettings(ctx)
	limits := settings.ModelTokenLimits[provider]
	if loadErr != nil || len(limits) == 0 {
		return model, false, nil
	}
	person := modelLimitPerson(ctx)
	if person == "" {
		return model, false, nil
	}
	if rec := directoryUserFor(person, "", ""); rec != nil {
		person = rec.ID
	}
	usage, usageErr := sharedAccountModelTokensUsed(person, provider, tokenLimitNow())
	if usageErr != nil {
		log.Printf("[TOKEN_LIMITS] cannot read per-model usage for %s, admitting: %v", person, usageErr)
		return model, false, nil
	}
	limit, weekly, over := modelAtTokenLimit(limits, usage, model)
	if !over {
		return model, false, nil
	}
	candidates := allowed
	if len(candidates) == 0 {
		candidates = knownProviderModelIDs(provider)
	}
	for _, candidate := range candidates {
		if strings.EqualFold(candidate, model) {
			continue
		}
		if _, _, candidateOver := modelAtTokenLimit(limits, usage, candidate); !candidateOver {
			log.Printf("[TOKEN_LIMITS] %s is at the %s limit on %s; running on %s instead", person, model, provider, candidate)
			return candidate, true, nil
		}
	}
	return model, false, modelLimitRefusal(provider, model, limit, weekly)
}

// modelTokenUsage is one person's use of one model against its limit.
type modelTokenUsage struct {
	DailyUsed   int64  `json:"daily_used"`
	WeeklyUsed  int64  `json:"weekly_used"`
	DailyLimit  int64  `json:"daily_limit,omitempty"`
	WeeklyLimit int64  `json:"weekly_limit,omitempty"`
	State       string `json:"state"`
}

// accountModelLimitsView is the per-model default limits of account (every
// account when empty), keyed by provider and then model.
func accountModelLimitsView(settings providerAccountSettings, account string) map[string]map[string]*UserTokenLimits {
	out := map[string]map[string]*UserTokenLimits{}
	for provider, models := range settings.ModelTokenLimits {
		if account == "" || provider == account {
			out[provider] = models
		}
	}
	return out
}

// personModelUsage is rec's use of each limited model of account; nil when no
// account is named or it has no model limits.
func (api *StreamingAPI) personModelUsage(rec *UserRecord, settings providerAccountSettings, account string) map[string]*modelTokenUsage {
	limits := settings.ModelTokenLimits[account]
	if account == "" || len(limits) == 0 || rec == nil {
		return nil
	}
	usage, err := sharedAccountModelTokensUsed(rec.ID, account, tokenLimitNow())
	if err != nil {
		log.Printf("[TOKEN_LIMITS] cannot read per-model usage for %s: %v", rec.ID, err)
		return nil
	}
	out := map[string]*modelTokenUsage{}
	for model, l := range limits {
		l = l.normalized()
		if l == nil {
			continue
		}
		used := usage[model]
		out[model] = &modelTokenUsage{DailyUsed: used.Day, WeeklyUsed: used.Week, DailyLimit: l.Daily, WeeklyLimit: l.Weekly, State: tokenLimitState(used.Day, l.Daily, used.Week, l.Weekly)}
	}
	return out
}

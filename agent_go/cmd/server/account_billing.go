package server

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
)

// How an account is paid for, decided by the server from its saved key or
// login and shown on the account card (#3433).
const (
	billingAPIKey       = "api_key"      // billed per use
	billingSubscription = "subscription" // a plan with usage limits
)

// claudeTokenBilling tells a Claude subscription token (claude setup-token,
// sk-ant-oat…) from an Anthropic API key. Both can be saved where Claude Code
// reads its token.
func claudeTokenBilling(token string) string {
	token = strings.TrimSpace(token)
	switch {
	case token == "":
		return ""
	case strings.HasPrefix(token, "sk-ant-oat"):
		return billingSubscription
	case strings.HasPrefix(token, "sk-ant-"):
		return billingAPIKey
	}
	return billingSubscription
}

// serverAccountBilling is the billing of the server's own account for a
// provider: an API key in the server settings, else the CLI's own login.
func serverAccountBilling(provider string, stored *StoredProviderKeys) string {
	env := func(names ...string) string {
		for _, name := range names {
			if value := strings.TrimSpace(os.Getenv(name)); value != "" {
				return value
			}
		}
		return ""
	}
	storedValue := func(get func(*StoredProviderKeys) string) string {
		if stored == nil {
			return ""
		}
		return strings.TrimSpace(get(stored))
	}
	switch provider {
	case "claude-code":
		if env("ANTHROPIC_API_KEY") != "" {
			return billingAPIKey
		}
		if token := env("CLAUDE_CODE_OAUTH_TOKEN"); token != "" {
			return claudeTokenBilling(token)
		}
		return billingSubscription
	case "codex-cli":
		if storedValue(func(s *StoredProviderKeys) string { return s.CodexCLI }) != "" || env("CODEX_API_KEY", "OPENAI_API_KEY") != "" {
			return billingAPIKey
		}
		return billingSubscription
	case "muse-cli":
		if env("META_API_KEY") != "" {
			return billingAPIKey
		}
		return billingSubscription
	case "cursor-cli":
		// A Cursor API key still draws on the Cursor plan.
		return billingSubscription
	case "pi-cli", "agy-cli":
		return billingAPIKey
	}
	return ""
}

// connectionBilling is the billing of a saved account.
func connectionBilling(record storedProviderConnection) string {
	switch record.AuthMethod {
	case "cli_login", "server_login":
		return billingSubscription
	}
	switch record.Provider {
	case "claude-code":
		return claudeTokenBilling(record.Credential)
	case "cursor-cli":
		return billingSubscription
	case "codex-cli", "muse-cli", "pi-cli", "agy-cli":
		return billingAPIKey
	}
	return ""
}

// accountBilling resolves an account ID (global:<provider> or a saved
// account) to its billing. Cached briefly: the cost ledger asks per model call.
func accountBilling(ctx context.Context, provider, accountID string) string {
	key := provider + "|" + accountID
	accountBillingCache.Lock()
	if hit, ok := accountBillingCache.values[key]; ok && time.Since(hit.at) < time.Minute {
		accountBillingCache.Unlock()
		return hit.billing
	}
	accountBillingCache.Unlock()
	billing := ""
	if p, ok := strings.CutPrefix(accountID, "global:"); ok {
		stored, _ := LoadProviderKeys(ctx)
		billing = serverAccountBilling(p, stored)
	} else if accountID != "" {
		providerConnectionsMu.Lock()
		records, err := loadProviderConnections(ctx)
		providerConnectionsMu.Unlock()
		if err == nil {
			for _, record := range records {
				if record.ID == accountID {
					if record.ViaConnection == "" {
						billing = connectionBilling(record)
					}
					break
				}
			}
		}
	}
	accountBillingCache.Lock()
	accountBillingCache.values[key] = accountBillingHit{billing, time.Now()}
	accountBillingCache.Unlock()
	return billing
}

type accountBillingHit struct {
	billing string
	at      time.Time
}

var accountBillingCache = struct {
	sync.Mutex
	values map[string]accountBillingHit
}{values: map[string]accountBillingHit{}}

// initAccountBillingForCosts makes turns on an API key real spend in the cost
// ledger, not a subscription-equivalent estimate.
func initAccountBillingForCosts() {
	costobserver.SetPerUseAccount(func(provider, accountID string) bool {
		return accountBilling(context.Background(), provider, accountID) == billingAPIKey
	})
}

// accountSpend is what an API-key account has cost so far: the whole account
// for an admin, else the caller's own share.
type accountSpend struct {
	TodayUSD float64 `json:"today_usd"`
	WeekUSD  float64 `json:"week_usd"`
	OwnShare bool    `json:"own_share"`
}

// accountSpends sums this week's and today's ledger cost per account.
func (api *StreamingAPI) accountSpends(viewer string, admin bool) map[string]*accountSpend {
	spends := map[string]*accountSpend{}
	if api.costLedger == nil {
		return spends
	}
	now := time.Now().In(costledger.DayLocation())
	today := costledger.Day(now)
	weekStart := costledger.Day(now.AddDate(0, 0, -((int(now.Weekday()) + 6) % 7)))
	add := func(from string, set func(*accountSpend, float64)) {
		summary, err := api.costLedger.Summarize(from, today)
		if err != nil || summary == nil {
			return
		}
		for key, aggregate := range summary.ByAccountSplit {
			if aggregate == nil || key.AccountID == "" || (!admin && key.UserID != viewer) {
				continue
			}
			spend := spends[key.AccountID]
			if spend == nil {
				spend = &accountSpend{OwnShare: !admin}
				spends[key.AccountID] = spend
			}
			set(spend, aggregate.TotalCostUSD)
		}
	}
	add(weekStart, func(s *accountSpend, v float64) { s.WeekUSD += v })
	add(today, func(s *accountSpend, v float64) { s.TodayUSD += v })
	return spends
}

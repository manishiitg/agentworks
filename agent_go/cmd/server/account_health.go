package server

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
)

// accountSignIn is an AI account's sign-in health, learned from real turns:
// a turn rejected for its login marks the account; the next turn that
// completes on it clears the mark. Kept beside the quota snapshots so one
// list answers "which accounts need a new sign-in, and how much is used".
type accountSignIn struct {
	Needed   bool      `json:"needed"`
	Since    time.Time `json:"since,omitempty"`
	FailedAt time.Time `json:"failed_at,omitempty"`
	Reason   string    `json:"reason,omitempty"`
}

type accountSignInCache struct {
	sync.Mutex
	accounts map[string]accountSignIn
	// lastByOwner is the account each owner's latest turn ran on, so an agent
	// row (the Team agent) can say its own account needs a new sign-in.
	lastByOwner map[string]string
}

var accountSignIns = accountSignInCache{accounts: map[string]accountSignIn{}, lastByOwner: map[string]string{}}

// ownerNeedsSignIn reports whether the owner's latest turn ran on an account
// whose login is currently rejected.
func (c *accountSignInCache) ownerNeedsSignIn(owner string) bool {
	c.Lock()
	defer c.Unlock()
	key, ok := c.lastByOwner[owner]
	return ok && c.accounts[key].Needed
}

func (c *accountSignInCache) noteOwner(owner, provider, account string) {
	c.Lock()
	defer c.Unlock()
	if c.lastByOwner == nil {
		c.lastByOwner = map[string]string{}
	}
	c.lastByOwner[owner] = accountLimitKey(provider, account)
}

func (c *accountSignInCache) get(provider, account string) (accountSignIn, bool) {
	c.Lock()
	defer c.Unlock()
	s, ok := c.accounts[accountLimitKey(provider, account)]
	return s, ok && s.Needed
}

// record stores one turn outcome. A result older than the latest failure is
// ignored, so a slow success from before the login broke cannot clear it.
func (c *accountSignInCache) record(provider, account string, failed bool, reason string, at time.Time) bool {
	if provider == "" || account == "" || at.IsZero() {
		return false
	}
	key := accountLimitKey(provider, account)
	c.Lock()
	previous := c.accounts[key]
	switch {
	case failed:
		next := accountSignIn{Needed: true, Since: previous.Since, FailedAt: at, Reason: reason}
		if !previous.Needed {
			next.Since = at
		}
		c.accounts[key] = next
	case previous.Needed && at.After(previous.FailedAt):
		delete(c.accounts, key)
	default:
		c.Unlock()
		return false
	}
	changed := failed != previous.Needed
	c.Unlock()
	if changed {
		state := "ok"
		if failed {
			state = "sign_in_needed"
		}
		log.Printf("[ACCOUNT_HEALTH] account=%s provider=%s state=%s", account, provider, state)
	}
	// Same re-authorizing notice as quotas: no IDs or owners on the bus.
	livefeed.Default.Publish(livefeed.AccountLimits, "")
	return changed
}

// signInNeededAccounts is how many accounts the caller can see that need a new
// sign-in; the monitor badge and Settings use it.
func signInNeededAccounts(views []accountLimitsView) int {
	n := 0
	for _, view := range views {
		if view.SignIn != nil && view.SignIn.Needed {
			n++
		}
	}
	return n
}

// observeAccountHealth reads main-turn completions. Only the server-owned
// error_code decides (provider text was classified once, by llmerrors).
func (api *StreamingAPI) observeAccountHealth(sessionID string, event storeevents.Event) {
	if event.Type != "unified_completion" || event.Data == nil || event.Timestamp.IsZero() || api.eventStore == nil {
		return
	}
	// A native chat turn's events carry the chat itself as parent ("session:<id>"); anything else is a child run.
	if (event.ParentExecutionID != "" && event.ParentExecutionID != "session:"+sessionID) || (event.ExecutionKind != "" && event.ExecutionKind != "main_agent") {
		return
	}
	var payload struct {
		Status   string `json:"status"`
		Metadata struct {
			Code    string `json:"error_code"`
			Message string `json:"provider_message"`
		} `json:"metadata"`
	}
	raw, err := json.Marshal(event.Data.Data)
	if err != nil || json.Unmarshal(raw, &payload) != nil {
		return
	}
	failed := payload.Metadata.Code == "authentication_failed"
	if !failed && payload.Status != "completed" {
		return
	}
	// A rejected login that is not recorded says why in the log, so the step is never blind.
	skip := func(reason string) {
		if failed {
			log.Printf("[ACCOUNT_HEALTH] session=%s event=%s action=skipped reason=%s", sessionID, event.ID, reason)
		}
	}
	owner := api.eventStore.GetSessionOwner(sessionID)
	if owner == "" {
		skip("no_owner")
		return
	}
	provider, connectionID := api.queryTurnConnectionForSession(QueryRequest{}, sessionID)
	if provider == "" || !providerEnabled(provider) {
		skip("account_unknown")
		return
	}
	if connectionID == "" {
		connectionID = serverDefaultConnectionID(provider)
	}
	ctx, cancel := context.WithTimeout(context.Background(), chatUsageProbeTimeout)
	defer cancel()
	// Only an account the chat's owner may run on can be marked or cleared.
	if _, allowed := api.chatUsageProbeAccount(ctx, owner, provider, connectionID); !allowed {
		skip("account_not_admitted")
		return
	}
	reason := strings.TrimSpace(payload.Metadata.Message)
	if reason == "" {
		reason = "This account's sign-in was rejected."
	}
	accountSignIns.noteOwner(owner, provider, connectionID)
	if accountSignIns.record(provider, connectionID, failed, reason, event.Timestamp) && failed {
		log.Printf("[ACCOUNT_HEALTH] session=%s owner=%s provider=%s event=%s action=marked", sessionID, logUsernameForUserID(owner), provider, event.ID)
	}
}

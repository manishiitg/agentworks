package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// Shared-account token limits over the external API and MCP (PLAT-690; the
// limits themselves are PLAT-683, token_limits.go).
//
//   - get_token_usage reads every active person's server-account tokens
//     against their limits. It is review data: admins and Code reviewers with
//     code:review (the Code review gate), or admins with users:manage.
//   - set_token_limits changes a person's limits. It is an admin write, so it
//     needs users:manage (admins only; never granted to anyone else) and an
//     active admin account, re-checked on every call. It writes through the
//     admin UI's own handler (PUT /api/admin/users/{id}).
//
// Every call, reads included, goes into the Code review audit log with the
// token that made it (get_code_audit shows it). Only id, username, email,
// limits and usage leave the server.

var externalTokenLimitTools = map[string]bool{
	"get_token_usage":  true,
	"set_token_limits": true,
}

func isExternalTokenLimitTool(name string) bool { return externalTokenLimitTools[name] }

// claimsIsAdmin is currentUserIsAdmin for a catalog listing (claims, no
// request).
func claimsIsAdmin(c *UserClaims) bool {
	acc := userAccessForClaims(c)
	if acc.Known {
		return acc.Admin && !acc.Disabled
	}
	return acc.Admin
}

// externalTokenLimitAllowed decides discovery for both tools; the call path
// re-checks with the request (externalTokenLimitCallAllowed).
func externalTokenLimitAllowed(c *UserClaims, name string) bool {
	if c == nil {
		return false
	}
	allows := func(scope string) bool { return c.AccessToken == nil || c.AccessToken.Allows(scope) }
	adminWrite := claimsIsAdmin(c) && allows("users:manage")
	if name == "set_token_limits" {
		return adminWrite
	}
	return adminWrite || (claimsCanReviewCode(c) && allows("code:review"))
}

func externalTokenLimitCallAllowed(r *http.Request, name string) bool {
	c := GetUserFromContext(r.Context())
	if c == nil {
		return false
	}
	allows := func(scope string) bool { return c.AccessToken == nil || c.AccessToken.Allows(scope) }
	adminWrite := currentUserIsAdmin(r) && allows("users:manage")
	if name == "set_token_limits" {
		return adminWrite
	}
	return adminWrite || (currentUserCanReviewCode(r) && allows("code:review"))
}

func externalTokenLimitDefinitions(add func(name, description string, write, scoped bool, props map[string]any, required ...string)) {
	date := map[string]any{"type": "string", "pattern": `^\d{4}-\d{2}-\d{2}$`}
	who := func(props map[string]any) map[string]any {
		props["user_id"] = externalString("Person's user ID (from get_token_usage).")
		props["email"] = map[string]any{"type": "string", "maxLength": 254, "description": "Person's email, instead of user_id."}
		return props
	}
	limit := func(what string) map[string]any {
		return map[string]any{"type": []any{"integer", "null"}, "minimum": 0, "maximum": 1e15, "description": what + " limit in tokens on the shared server accounts; 0 or null is unlimited; omit to leave it unchanged."}
	}
	add("get_token_usage", "Shared server-account token usage per person against their admin-set limits: tokens used today and this week (UTC; weeks start Monday), daily_limit/weekly_limit (0 = unlimited), state ok|warning (80%)|over, and reset times. Only tokens spent on the server's shared accounts count; a person's own accounts never do. Without user_id/email it lists every active person. Pass from/to (YYYY-MM-DD, UTC, inclusive) for each person's shared-account token totals over that range instead. Read-only; every call is recorded in the Code review audit log. Requires code:review and an admin or Code reviewer account, or users:manage and an admin account.", false, false, who(map[string]any{"from": date, "to": date}))
	add("set_token_limits", "Set a person's daily and/or weekly token limit on the shared server accounts (the admin Users page's limits). Integers; 0 or null is unlimited; an omitted field stays as it is. Identify the person by user_id or email. Returns their new limits and current usage. Recorded in the Code review audit log. Requires users:manage and an admin account.", true, false, who(map[string]any{"daily": limit("Daily (UTC day)"), "weekly": limit("Weekly (Monday-start UTC week)")}))
}

// tokenLimitPerson is the only shape a person leaves the server in.
type tokenLimitPerson struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
}

type tokenLimitPersonUsage struct {
	tokenLimitPerson
	DailyLimit  int64  `json:"daily_limit"`
	WeeklyLimit int64  `json:"weekly_limit"`
	DailyUsed   int64  `json:"daily_used"`
	WeeklyUsed  int64  `json:"weekly_used"`
	State       string `json:"state"`
}

func personOf(rec *UserRecord) tokenLimitPerson {
	return tokenLimitPerson{UserID: rec.ID, Username: rec.Username, Email: rec.Email}
}

func (api *StreamingAPI) tokenLimitUsageOf(rec *UserRecord) tokenLimitPersonUsage {
	u := api.sharedAccountTokenUsageFor(rec)
	return tokenLimitPersonUsage{tokenLimitPerson: personOf(rec), DailyLimit: u.DailyLimit, WeeklyLimit: u.WeeklyLimit, DailyUsed: u.DailyUsed, WeeklyUsed: u.WeeklyUsed, State: u.State}
}

// tokenLimitTarget resolves user_id / email to one directory record; both
// may be given only when they name the same person.
func tokenLimitTarget(dir *userDirectory, args map[string]any) (*UserRecord, int, string) {
	id, email := strings.TrimSpace(externalArg(args, "user_id")), strings.TrimSpace(externalArg(args, "email"))
	var rec *UserRecord
	if id != "" {
		rec = dir.byID(id)
	}
	if email != "" {
		byEmail := dir.byEmail(email)
		if rec != nil && byEmail != rec {
			return nil, http.StatusBadRequest, "user_id and email name different people."
		}
		rec = byEmail
	}
	if rec == nil {
		return nil, http.StatusNotFound, "No such person."
	}
	return rec, 0, ""
}

func (api *StreamingAPI) externalTokenLimitCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	if !externalTokenLimitCallAllowed(r, name) {
		msg := "Token usage needs code:review and an admin or Code reviewer account, or users:manage and an admin account."
		if name == "set_token_limits" {
			msg = "Setting token limits needs users:manage and an admin account."
		}
		externalError(w, http.StatusForbidden, "forbidden", msg)
		return
	}
	dir, err := readUserDirectoryFile()
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "users_unavailable", "The user directory is unavailable.")
		return
	}
	named := externalArg(args, "user_id") != "" || externalArg(args, "email") != ""
	if name == "set_token_limits" && !named {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Pass user_id or email.")
		return
	}
	var target *UserRecord
	if named {
		rec, status, msg := tokenLimitTarget(dir, args)
		if rec == nil {
			code := "invalid_arguments"
			if status == http.StatusNotFound {
				code = "not_found"
			}
			externalError(w, status, code, msg)
			return
		}
		target = rec
	}
	if name == "set_token_limits" {
		api.externalSetTokenLimits(w, r, target, args)
		return
	}
	from, to := strings.TrimSpace(externalArg(args, "from")), strings.TrimSpace(externalArg(args, "to"))
	auditTarget := "all"
	if target != nil {
		auditTarget = target.ID
	}
	if from != "" || to != "" {
		auditTarget += " " + from + ".." + to
	}
	if err := recordCodeAdminView(r.Context(), GetUserFromContext(r.Context()), "read_token_usage", "", "", auditTarget); err != nil {
		externalError(w, http.StatusServiceUnavailable, "audit_unavailable", "The Code review audit log is unavailable.")
		return
	}
	people := []*UserRecord{}
	if target != nil {
		people = append(people, target)
	} else {
		for i := range dir.Users {
			if !dir.Users[i].Disabled {
				people = append(people, &dir.Users[i])
			}
		}
	}
	sort.Slice(people, func(i, j int) bool { return people[i].Username < people[j].Username })
	if from != "" || to != "" {
		api.externalTokenUsageRange(w, people, from, to)
		return
	}
	now := tokenLimitNow()
	dayStart, dayEnd, weekStart, weekEnd := tokenLimitWindows(now)
	out := make([]tokenLimitPersonUsage, 0, len(people))
	for _, rec := range people {
		out = append(out, api.tokenLimitUsageOf(rec))
	}
	externalJSON(w, map[string]any{
		"timezone": tokenLimitTimezone, "accounts": "shared server accounts (account IDs " + serverAccountIDPrefix + "*)",
		"day_start": dayStart.Format(time.RFC3339), "day_resets_at": dayEnd.Format(time.RFC3339),
		"week_start": weekStart.Format(time.RFC3339), "week_resets_at": weekEnd.Format(time.RFC3339),
		"people": out,
	})
}

// externalTokenUsageRange totals each person's server-account tokens in a
// date range from the ledger's account-attributed events. A token is input
// (cached input once) plus output, as the limits count it.
func (api *StreamingAPI) externalTokenUsageRange(w http.ResponseWriter, people []*UserRecord, from, to string) {
	ledger := api.tokenLimitLedger()
	if ledger == nil {
		externalError(w, http.StatusServiceUnavailable, "costs_unavailable", "The cost ledger is not initialized.")
		return
	}
	summary, err := ledger.Summarize(from, to)
	if err != nil {
		externalError(w, http.StatusBadRequest, "invalid_arguments", err.Error())
		return
	}
	type totals struct {
		tokenLimitPerson
		Tokens       int64            `json:"tokens"`
		InputTokens  int64            `json:"input_tokens"`
		OutputTokens int64            `json:"output_tokens"`
		Calls        int              `json:"calls"`
		ByAccount    map[string]int64 `json:"by_account"`
	}
	byUser := make(map[string]*totals, len(people))
	out := make([]*totals, 0, len(people))
	for _, rec := range people {
		t := &totals{tokenLimitPerson: personOf(rec), ByAccount: map[string]int64{}}
		byUser[rec.ID] = t
		out = append(out, t)
	}
	for key, agg := range summary.ByAccountSplit {
		t := byUser[key.UserID]
		if t == nil || agg == nil || !strings.HasPrefix(key.AccountID, serverAccountIDPrefix) {
			continue
		}
		in, outTokens := int64(agg.InputTokens), int64(agg.CompletionTokens)
		t.InputTokens += in
		t.OutputTokens += outTokens
		t.Tokens += in + outTokens
		t.Calls += agg.CallCount
		t.ByAccount[key.AccountID] += in + outTokens
	}
	externalJSON(w, map[string]any{"from": summary.From, "to": summary.To, "timezone": tokenLimitTimezone, "accounts": "shared server accounts (account IDs " + serverAccountIDPrefix + "*)", "people": out})
}

// tokenLimitArg reads daily/weekly: absent = keep, null = 0 (unlimited).
func tokenLimitArg(args map[string]any, name string, current int64) (int64, bool) {
	v, present := args[name]
	if !present {
		return current, false
	}
	switch n := v.(type) {
	case nil:
		return 0, true
	case float64:
		return int64(n), true
	case json.Number:
		i, _ := n.Int64()
		return i, true
	case int:
		return int64(n), true
	case int64:
		return n, true
	}
	return current, false
}

func (api *StreamingAPI) externalSetTokenLimits(w http.ResponseWriter, r *http.Request, rec *UserRecord, args map[string]any) {
	current := UserTokenLimits{}
	if limits := rec.TokenLimits.normalized(); limits != nil {
		current = *limits
	}
	daily, setDaily := tokenLimitArg(args, "daily", current.Daily)
	weekly, setWeekly := tokenLimitArg(args, "weekly", current.Weekly)
	if !setDaily && !setWeekly {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Pass daily and/or weekly.")
		return
	}
	if err := recordCodeAdminView(r.Context(), GetUserFromContext(r.Context()), "set_token_limits", "", "", rec.ID+" daily="+strconv.FormatInt(daily, 10)+" weekly="+strconv.FormatInt(weekly, 10)); err != nil {
		externalError(w, http.StatusServiceUnavailable, "audit_unavailable", "The Code review audit log is unavailable.")
		return
	}
	// The admin UI's own write path: same validation, normalization, save
	// and log line.
	body, _ := json.Marshal(map[string]any{"token_limits": UserTokenLimits{Daily: daily, Weekly: weekly}})
	sub := r.Clone(r.Context())
	sub.Method = http.MethodPut
	sub.Body = io.NopCloser(bytes.NewReader(body))
	sub.URL = &url.URL{Path: "/api/admin/users/" + url.PathEscape(rec.ID)}
	sub = mux.SetURLVars(sub, map[string]string{"id": rec.ID})
	result := &externalMCPRecorder{header: http.Header{}}
	requireAdmin(api.handleAdminUpdateUser)(result, sub)
	if result.status != 0 && result.status != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(result.body.Bytes(), &failure)
		if failure.Error == "" {
			failure.Error = http.StatusText(result.status)
		}
		externalError(w, result.status, "upstream_error", failure.Error)
		return
	}
	// Usage is cached per person for a few seconds; answer with a fresh read.
	sharedTokenUsageCache.Lock()
	delete(sharedTokenUsageCache.byUser, rec.ID)
	sharedTokenUsageCache.Unlock()
	dir, err := readUserDirectoryFile()
	if err != nil || dir.byID(rec.ID) == nil {
		externalError(w, http.StatusServiceUnavailable, "users_unavailable", "The limits were saved but the user directory could not be re-read.")
		return
	}
	externalJSON(w, api.tokenLimitUsageOf(dir.byID(rec.ID)))
}

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// One service backs the admin tab and the bullpen_admin tool. It lists safe
// metadata for every person and agent, closes one attachment, or removes a
// stale name. It never reads message text, session ids, host keys or config.
// Every change and its audit row are written in the same store save, so if the
// audit cannot be written the change does not happen.
const (
	bullpenAdminTool       = "bullpen_admin"
	bullpenAdminAuditCap   = 5000
	bullpenAdminRateMax    = 20 // changes per admin per window
	bullpenAdminRateWindow = 10 * time.Minute
	bullpenAdminPage       = 50
	bullpenNoticeCap       = 50 // undismissed notices kept per owner
)

type bullpenAdminAudit struct {
	At           time.Time `json:"at"`
	Admin        string    `json:"admin"`
	Event        string    `json:"event"`
	TargetUser   string    `json:"target_user"`
	TargetAgent  string    `json:"target_agent,omitempty"`
	TargetHandle string    `json:"target_handle,omitempty"`
	Reason       string    `json:"reason"`
	Result       string    `json:"result"`
}

// agentOwnerNotice tells an owner that an administrator changed one of their
// agents. It stays until the owner dismisses it.
type agentOwnerNotice struct {
	ID          string    `json:"notice_id"`
	OwnerUserID string    `json:"owner_user_id"`
	AgentName   string    `json:"agent_name"`
	Text        string    `json:"text"`
	At          time.Time `json:"at"`
}

// Audit rows and notices are kept for 180 days unless the operator sets
// AGENTWORKS_BULLPEN_AUDIT_DAYS.
func bullpenAdminRetention() time.Duration {
	if days, err := strconv.Atoi(strings.TrimSpace(os.Getenv("AGENTWORKS_BULLPEN_AUDIT_DAYS"))); err == nil && days >= 1 && days <= 3650 {
		return time.Duration(days) * 24 * time.Hour
	}
	return 180 * 24 * time.Hour
}

var bullpenAdminLimiter = struct {
	sync.Mutex
	recent map[string][]time.Time
}{recent: map[string][]time.Time{}}

func bullpenAdminRateOK(admin string, now time.Time) bool {
	bullpenAdminLimiter.Lock()
	defer bullpenAdminLimiter.Unlock()
	kept := bullpenAdminLimiter.recent[admin][:0]
	for _, t := range bullpenAdminLimiter.recent[admin] {
		if now.Sub(t) < bullpenAdminRateWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= bullpenAdminRateMax {
		bullpenAdminLimiter.recent[admin] = kept
		return false
	}
	bullpenAdminLimiter.recent[admin] = append(kept, now)
	return true
}

// attachmentHandle is the only way an administrator names an attachment: a
// server-side hash, not the native session id or the lease id.
func attachmentHandle(agentID, attachmentID string) string {
	sum := sha256.Sum256([]byte(agentID + "\x00" + attachmentID))
	return "att-" + hex.EncodeToString(sum[:12])
}

func cleanAdminReason(raw string) (string, bool) {
	reason := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(reason)
	if n < 3 || n > 200 || strings.IndexFunc(reason, func(r rune) bool { return unicode.IsControl(r) }) >= 0 {
		return "", false
	}
	return reason, true
}

// bullpenAdminToolAllowed is the discovery gate; the call path rechecks it.
func bullpenAdminToolAllowed(c *UserClaims) bool {
	if c == nil || !productpolicy.Enabled("bullpen") || !claimsIsAdmin(c) {
		return false
	}
	return c.AccessToken == nil || c.AccessToken.BoundAgentID == "" && c.AccessToken.Allows("users:manage")
}

func (s *agentMessageStore) pruneBullpenAdmin(now time.Time) {
	cutoff := now.Add(-bullpenAdminRetention())
	audit := s.AdminAudit[:0]
	for _, a := range s.AdminAudit {
		if a.At.After(cutoff) {
			audit = append(audit, a)
		}
	}
	if len(audit) > bullpenAdminAuditCap {
		audit = audit[len(audit)-bullpenAdminAuditCap:]
	}
	s.AdminAudit = audit
	notices, perOwner := []agentOwnerNotice{}, map[string]int{}
	for i := len(s.Notices) - 1; i >= 0; i-- { // newest first, so the cap keeps the newest
		n := s.Notices[i]
		if n.At.After(cutoff) && perOwner[n.OwnerUserID] < bullpenNoticeCap {
			perOwner[n.OwnerUserID]++
			notices = append(notices, n)
		}
	}
	for i, j := 0, len(notices)-1; i < j; i, j = i+1, j-1 {
		notices[i], notices[j] = notices[j], notices[i]
	}
	s.Notices = notices
}

func bullpenAdminResult(status int, code, msg string) (map[string]interface{}, int, string, string) {
	return nil, status, code, msg
}

// bullpenAdminAct runs one action as an admin already verified by the caller.
func (api *StreamingAPI) bullpenAdminAct(ctx context.Context, adminID, action string, args map[string]any) (map[string]interface{}, int, string, string) {
	switch action {
	case "list", "audit":
		return api.bullpenAdminRead(ctx, action, args)
	case "close_attachment", "remove_agent":
	default:
		return bullpenAdminResult(http.StatusBadRequest, "invalid_arguments", "action must be list, audit, close_attachment or remove_agent")
	}
	reason, ok := cleanAdminReason(externalArg(args, "reason"))
	if !ok {
		return bullpenAdminResult(http.StatusBadRequest, "reason_required", "Give a reason of 3 to 200 characters; it is recorded with your name.")
	}
	now := time.Now().UTC()
	if !bullpenAdminRateOK(adminID, now) {
		return bullpenAdminResult(http.StatusTooManyRequests, "rate_limited", "Too many administrator changes; wait a few minutes.")
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return bullpenAdminResult(http.StatusServiceUnavailable, "unavailable", "Bullpen administration is unavailable. Try again.")
	}
	label := shareOwnerLabeler()
	save := func(out map[string]interface{}) (map[string]interface{}, int, string, string) {
		s.pruneBullpenAdmin(now)
		if err := saveAgentMessageStore(ctx, s); err != nil {
			return bullpenAdminResult(http.StatusServiceUnavailable, "audit_unavailable", "The change was not made because it could not be recorded. Try again.")
		}
		return out, 0, "", ""
	}
	if action == "close_attachment" {
		handle := externalArg(args, "handle")
		for i := range s.Agents {
			a := &s.Agents[i]
			if a.Attachment == nil || attachmentHandle(a.ID, a.Attachment.ID) != handle {
				continue
			}
			s.AdminAudit = append(s.AdminAudit, bullpenAdminAudit{At: now, Admin: adminID, Event: "attachment_closed", TargetUser: a.UserID, TargetAgent: a.ID, TargetHandle: handle, Reason: reason, Result: "done"})
			s.Notices = append(s.Notices, agentOwnerNotice{ID: "notice-" + uuid.NewString(), OwnerUserID: a.UserID, AgentName: a.Name, At: now, Text: "An administrator disconnected the chat attached to " + a.Name + ". Reason: " + reason + ". Attach it again from your own chat if you want it back."})
			a.Attachment = nil
			return save(map[string]interface{}{"state": "closed", "agent_name": a.Name, "owner": label(a.UserID)})
		}
		for _, row := range s.AdminAudit {
			if row.Event == "attachment_closed" && row.TargetHandle == handle {
				s.AdminAudit = append(s.AdminAudit, bullpenAdminAudit{At: now, Admin: adminID, Event: "attachment_closed", TargetUser: row.TargetUser, TargetAgent: row.TargetAgent, TargetHandle: handle, Reason: reason, Result: "already_closed"})
				return save(map[string]interface{}{"state": "already_closed"})
			}
		}
		s.AdminAudit = append(s.AdminAudit, bullpenAdminAudit{At: now, Admin: adminID, Event: "attachment_closed", TargetHandle: handle, Reason: reason, Result: "not_found"})
		if _, _, code, msg := save(nil); code != "" {
			return bullpenAdminResult(http.StatusServiceUnavailable, code, msg)
		}
		return bullpenAdminResult(http.StatusNotFound, "unknown_attachment", "No attachment with that handle.")
	}
	agentID := externalArg(args, "agent_id")
	for i := range s.Agents {
		a := s.Agents[i]
		if a.ID != agentID {
			continue
		}
		if namedAgentOnline(a) {
			return bullpenAdminResult(http.StatusConflict, "agent_live", "That agent has a live listener. Close its attachment first; only a stale name can be removed.")
		}
		s.Agents = append(s.Agents[:i], s.Agents[i+1:]...)
		for j := range s.Shares {
			if sh := &s.Shares[j]; sh.AgentID == a.ID && sh.RevokedAt.IsZero() {
				sh.RevokedAt = now
				sh.RevocationVersion++
				s.suppressShare(sh.ID)
			}
		}
		s.AdminAudit = append(s.AdminAudit, bullpenAdminAudit{At: now, Admin: adminID, Event: "agent_removed", TargetUser: a.UserID, TargetAgent: a.ID, Reason: reason, Result: "done"})
		s.Notices = append(s.Notices, agentOwnerNotice{ID: "notice-" + uuid.NewString(), OwnerUserID: a.UserID, AgentName: a.Name, At: now, Text: "An administrator removed the stale agent name " + a.Name + ". Reason: " + reason + ". Register it again if you still use it; old conversations are kept but will not reach a new registration."})
		return save(map[string]interface{}{"state": "removed", "agent_name": a.Name, "owner": label(a.UserID)})
	}
	s.AdminAudit = append(s.AdminAudit, bullpenAdminAudit{At: now, Admin: adminID, Event: "agent_removed", TargetAgent: agentID, Reason: reason, Result: "not_found"})
	if _, _, code, msg := save(nil); code != "" {
		return bullpenAdminResult(http.StatusServiceUnavailable, code, msg)
	}
	return bullpenAdminResult(http.StatusNotFound, "unknown_agent", "No agent with that id.")
}

func (api *StreamingAPI) bullpenAdminRead(ctx context.Context, action string, args map[string]any) (map[string]interface{}, int, string, string) {
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	agentMessageMu.Unlock()
	dir, dirErr := loadUserDirectory()
	if err != nil || dirErr != nil || dir == nil {
		return bullpenAdminResult(http.StatusServiceUnavailable, "unavailable", "Bullpen administration is unavailable. Try again.")
	}
	limit := externalInt(args, "limit", bullpenAdminPage)
	if limit < 1 || limit > 100 {
		limit = bullpenAdminPage
	}
	label := shareOwnerLabeler()
	if action == "audit" {
		rows := []map[string]interface{}{}
		for i := len(s.AdminAudit) - 1; i >= 0 && len(rows) < limit; i-- {
			a := s.AdminAudit[i]
			rows = append(rows, map[string]interface{}{"at": a.At, "admin": label(a.Admin), "event": a.Event, "person": label(a.TargetUser), "agent_id": a.TargetAgent, "reason": a.Reason, "result": a.Result})
		}
		return map[string]interface{}{"audit": rows, "retention_days": int(bullpenAdminRetention() / (24 * time.Hour))}, 0, "", ""
	}
	offset, _ := strconv.Atoi(externalArg(args, "cursor"))
	if offset < 0 {
		offset = 0
	}
	query := strings.ToLower(strings.TrimSpace(externalArg(args, "q")))
	people := []map[string]interface{}{}
	for _, u := range dir.Users {
		agents := []map[string]interface{}{} // never nil: a person with no agents encodes as [], not null
		match := query == "" || strings.Contains(strings.ToLower(u.Username), query)
		for _, a := range s.Agents {
			if a.UserID != u.ID {
				continue
			}
			if query != "" && strings.Contains(strings.ToLower(a.Name), query) {
				match = true
			}
			item := map[string]interface{}{"agent_id": a.ID, "agent_name": a.Name, "purpose": a.Purpose, "online": namedAgentOnline(a), "created_at": a.CreatedAt, "attached": a.Attachment != nil}
			if a.Attachment != nil {
				item["provider"] = a.Attachment.Provider
				item["attachment_handle"] = attachmentHandle(a.ID, a.Attachment.ID)
			}
			agents = append(agents, item)
		}
		if !match {
			continue
		}
		sort.Slice(agents, func(i, j int) bool { return agents[i]["agent_name"].(string) < agents[j]["agent_name"].(string) })
		people = append(people, map[string]interface{}{"username": u.Username, "role": roleForRecord(&u), "disabled": u.Disabled, "agents": agents})
	}
	sort.Slice(people, func(i, j int) bool { return people[i]["username"].(string) < people[j]["username"].(string) })
	out := map[string]interface{}{"total": len(people)}
	if offset > len(people) {
		offset = len(people)
	}
	end := offset + limit
	if end >= len(people) {
		end = len(people)
	} else {
		out["next_cursor"] = strconv.Itoa(end)
	}
	out["people"] = people[offset:end]
	return out, 0, "", ""
}

// bullpenAdminClaims admits the signed-in administrator's own browser session.
func (api *StreamingAPI) bullpenAdminClaims(w http.ResponseWriter, r *http.Request, mutate bool) *UserClaims {
	claims := bullpenBrowserClaims(w, r, mutate)
	if claims == nil {
		return nil
	}
	if mutate && (!strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || r.URL.Query().Get("token") != "") || !currentUserIsAdmin(r) {
		externalError(w, http.StatusForbidden, "access_denied", "Bullpen administration is for administrators.")
		return nil
	}
	return claims
}

func (api *StreamingAPI) handleBullpenAdmin(w http.ResponseWriter, r *http.Request) {
	mutate := r.Method == http.MethodPost
	claims := api.bullpenAdminClaims(w, r, mutate)
	if claims == nil {
		return
	}
	args := map[string]any{}
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			args[key] = values[0]
		}
	}
	if mutate {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		if json.NewDecoder(r.Body).Decode(&args) != nil {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "Send a JSON body.")
			return
		}
	}
	action := map[string]string{"/api/bullpen/admin/agents": "list", "/api/bullpen/admin/audit": "audit", "/api/bullpen/admin/attachments/close": "close_attachment", "/api/bullpen/admin/agents/remove": "remove_agent"}[r.URL.Path]
	out, status, code, msg := api.bullpenAdminAct(r.Context(), claims.UserID, action, args)
	if code != "" {
		externalError(w, status, code, msg)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, out)
}

func externalBullpenAdminDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add(bullpenAdminTool, "Bullpen administration: action=list pages through every person and their registered agents (name, purpose, provider, connected or not, created time, an opaque attachment_handle; never messages, session ids, host keys or config; pass q to filter and cursor for the next page); action=audit shows recent administrator changes; action=close_attachment (handle, reason) disconnects one agent's attached chat and tells its owner, who alone may attach again; action=remove_agent (agent_id, reason) removes a stale agent name and is refused while it has a live listener. Changes need a reason of 3 to 200 characters, are recorded with your name, and are limited per administrator. Requires users:manage and an administrator account.", true, false, map[string]any{
		"action":   map[string]any{"type": "string", "enum": []any{"list", "audit", "close_attachment", "remove_agent"}},
		"q":        map[string]any{"type": "string", "maxLength": 64},
		"cursor":   map[string]any{"type": "string", "maxLength": 12},
		"limit":    externalInteger(1, 100),
		"handle":   map[string]any{"type": "string", "maxLength": 40},
		"agent_id": map[string]any{"type": "string", "maxLength": 80},
		"reason":   map[string]any{"type": "string", "maxLength": 200},
	}, "action")
}

func (api *StreamingAPI) externalBullpenAdminCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !bullpenAdminToolAllowed(claims) || !currentUserIsAdmin(r) {
		externalError(w, http.StatusForbidden, "forbidden", "Bullpen administration needs users:manage and an administrator account.")
		return
	}
	out, status, code, msg := api.bullpenAdminAct(r.Context(), claims.UserID, externalArg(args, "action"), args)
	if code != "" {
		externalError(w, status, code, msg)
		return
	}
	externalJSON(w, out)
}

func (api *StreamingAPI) handleBullpenNotices(w http.ResponseWriter, r *http.Request) {
	claims := bullpenBrowserClaims(w, r, false)
	if claims == nil {
		return
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(r.Context())
	agentMessageMu.Unlock()
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "unavailable", "Notices are unavailable. Try again.")
		return
	}
	items := []map[string]interface{}{}
	for _, n := range s.Notices {
		if n.OwnerUserID == claims.UserID {
			items = append(items, map[string]interface{}{"notice_id": n.ID, "agent_name": n.AgentName, "text": n.Text, "at": n.At})
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]interface{}{"notices": items})
}

func (api *StreamingAPI) handleBullpenNoticeDismiss(w http.ResponseWriter, r *http.Request) {
	claims := api.bullpenShareClaims(w, r, true)
	if claims == nil {
		return
	}
	id := mux.Vars(r)["id"]
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(r.Context())
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "unavailable", "Notices are unavailable. Try again.")
		return
	}
	kept, found := s.Notices[:0], false
	for _, n := range s.Notices {
		if n.ID == id && n.OwnerUserID == claims.UserID {
			found = true
			continue
		}
		kept = append(kept, n)
	}
	if !found {
		externalError(w, http.StatusNotFound, "unknown_notice", "No such notice.")
		return
	}
	s.Notices = kept
	if err := saveAgentMessageStore(r.Context(), s); err != nil {
		externalError(w, http.StatusServiceUnavailable, "unavailable", "Notices are unavailable. Try again.")
		return
	}
	externalJSON(w, map[string]interface{}{"notice_id": id, "dismissed": true})
}

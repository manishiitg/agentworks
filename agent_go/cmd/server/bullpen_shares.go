package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// A share lets one other person's agents chat with one of the owner's named
// agents. It is chat only: the text a sender writes never widens it into tool
// or shell authority. Grants live in the message store under agentMessageMu, so
// a revoke and a send are ordered and a send never outlives the grant it used.
const (
	shareCapabilityChat = "chat"
	shareDefaultTTL     = 30 * 24 * time.Hour
	shareMaxTTL         = 365 * 24 * time.Hour
	sharesPerOwner      = 100
	shareAuditKeep      = 2000
	shareTombstoneKeep  = 30 * 24 * time.Hour // revoked or expired grants are dropped after this
	blocksPerOwner      = 500
)

type agentShare struct {
	ID                string    `json:"share_id"`
	OwnerUserID       string    `json:"owner_user_id"`
	AgentID           string    `json:"agent_id"`
	SubjectUserID     string    `json:"subject_user_id"`
	Capability        string    `json:"capability"`
	CreatedAt         time.Time `json:"created_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	RevocationVersion int       `json:"revocation_version"`
	RevokedAt         time.Time `json:"revoked_at,omitempty"`
}

type agentShareBlock struct {
	OwnerUserID   string    `json:"owner_user_id"`
	BlockedUserID string    `json:"blocked_user_id"`
	At            time.Time `json:"at"`
}

// Audit rows hold identifiers and the event only, never message text.
type agentShareAudit struct {
	At      time.Time `json:"at"`
	Event   string    `json:"event"`
	Actor   string    `json:"actor"`
	ShareID string    `json:"share_id,omitempty"`
	Owner   string    `json:"owner"`
	Subject string    `json:"subject"`
	AgentID string    `json:"agent_id,omitempty"`
}

func (s *agentMessageStore) auditShare(event, actor string, owner, subject, shareID, agentID string) {
	s.ShareAudit = append(s.ShareAudit, agentShareAudit{At: time.Now().UTC(), Event: event, Actor: actor, ShareID: shareID, Owner: owner, Subject: subject, AgentID: agentID})
	if len(s.ShareAudit) > shareAuditKeep {
		s.ShareAudit = s.ShareAudit[len(s.ShareAudit)-shareAuditKeep:]
	}
}

// pruneShares drops grants that ended long ago. A missing grant is refused like
// a revoked one, so old conversations cannot come back; the store stays bounded.
func (s *agentMessageStore) pruneShares(now time.Time) {
	kept := s.Shares[:0]
	for _, sh := range s.Shares {
		ended := sh.RevokedAt
		if ended.IsZero() {
			ended = sh.ExpiresAt
		}
		if now.Before(ended.Add(shareTombstoneKeep)) {
			kept = append(kept, sh)
		}
	}
	s.Shares = kept
}

func (s agentMessageStore) shareBlocked(owner, subject string) bool {
	for _, b := range s.Blocks {
		if b.OwnerUserID == owner && b.BlockedUserID == subject {
			return true
		}
	}
	return false
}

// shareAccountActive is true for an account that still exists, is enabled and
// may open Bullpen. A deleted account is unknown to the directory, so it is
// refused instead of defaulting to active. Tests replace it.
var shareAccountActive = func(userID string) bool {
	dir, err := loadUserDirectory()
	if err != nil || dir == nil {
		return false
	}
	rec := dir.byID(userID)
	return rec != nil && !rec.Disabled && userAllowedProduct(&UserClaims{UserID: userID}, "bullpen")
}

// A share whose subject is shareScopeServer lets every signed-in person on this
// server message the agent (until the owner unshares it). It has no expiry and is
// checked in the same places as a one-person grant.
const shareScopeServer = "*"
const serverShareLifetime = 100 * 365 * 24 * time.Hour

func (sh agentShare) serverScope() bool { return sh.SubjectUserID == shareScopeServer }

// shareAllows says whether this grant lets that person message the agent: not the
// owner, not blocked by the owner, and for a server-scope share any active account.
func (s agentMessageStore) shareAllows(sh agentShare, person string) bool {
	if person == "" || person == shareScopeServer || person == sh.OwnerUserID || s.shareBlocked(sh.OwnerUserID, person) {
		return false
	}
	if sh.serverScope() {
		return shareAccountActive(person)
	}
	return sh.SubjectUserID == person
}

func (sh agentShare) live(now time.Time) bool {
	return sh.RevokedAt.IsZero() && now.Before(sh.ExpiresAt)
}

// authorizeShare is the single rule every later send, delivery, read and reply
// check calls: the grant exists with the same revocation version, is not
// revoked or expired, the sender is not blocked, and neither account is disabled.
func (s agentMessageStore) authorizeShare(shareID string, version int, now time.Time) (agentShare, error) {
	denied := fmt.Errorf("shared agent unavailable or access denied")
	for _, sh := range s.Shares {
		if sh.ID != shareID {
			continue
		}
		subjectOK := sh.serverScope() || (!s.shareBlocked(sh.OwnerUserID, sh.SubjectUserID) && shareAccountActive(sh.SubjectUserID))
		if !sh.live(now) || sh.RevocationVersion != version || sh.Capability != shareCapabilityChat || !subjectOK || !shareAccountActive(sh.OwnerUserID) {
			return agentShare{}, denied
		}
		known := false
		for _, a := range s.Agents {
			if a.ID == sh.AgentID && a.UserID == sh.OwnerUserID {
				known = true
			}
		}
		if !known {
			return agentShare{}, denied
		}
		return sh, nil
	}
	return agentShare{}, denied
}

// conversationShareErr is the fence for every operation on a cross-account
// conversation: both ends must carry the same live grant, belong to its owner
// and subject, and be registered agents. Conversations without a grant pass.
func (s agentMessageStore) conversationShareErr(c agentConversation, now time.Time) error {
	a, b := c.Endpoints[0], c.Endpoints[1]
	if a.ShareID == "" && b.ShareID == "" {
		return nil
	}
	if isGroupShareID(a.ShareID) || isGroupShareID(b.ShareID) {
		return s.groupConversationErr(c)
	}
	denied := fmt.Errorf("shared agent unavailable or access denied")
	if a.ShareID != b.ShareID || a.ShareVersion != b.ShareVersion || !a.External || !b.External || a.NamedAgentID == "" || b.NamedAgentID == "" {
		return denied
	}
	sh, err := s.authorizeShare(a.ShareID, a.ShareVersion, now)
	if err != nil {
		return err
	}
	owner, subject := a, b
	if a.UserID != sh.OwnerUserID {
		owner, subject = b, a
	}
	if owner.UserID != sh.OwnerUserID || !s.shareAllows(sh, subject.UserID) || owner.NamedAgentID != sh.AgentID {
		return denied
	}
	for _, ag := range s.Agents {
		if ag.ID == subject.NamedAgentID && ag.UserID == subject.UserID {
			return nil
		}
	}
	return denied
}

// shareSenderAllowed replaces the same-account sender rule: a sender from
// another account is accepted only inside a conversation with a live grant.
func (s agentMessageStore) shareSenderAllowed(c agentConversation, from int, recipientUserID string, now time.Time) bool {
	src := c.Endpoints[from]
	if src.ShareID == "" {
		return sameAccountAgentSender(src, recipientUserID)
	}
	return src.Guest == "" && src.UserID != recipientUserID && c.Endpoints[1-from].UserID == recipientUserID && s.conversationShareErr(c, now) == nil
}

const (
	shareSenderHourly  = 60  // messages one person may send through one grant per hour
	shareSenderPending = 100 // undelivered messages one person may leave for one agent
)

// shareQuotaErr limits the shared person's messages, never the owner's replies.
func (s agentMessageStore) shareQuotaErr(c agentConversation, side int, now time.Time) error {
	src := c.Endpoints[side]
	if src.ShareID == "" {
		return nil
	}
	if isGroupShareID(src.ShareID) {
		return s.groupQuotaErr(c, side, now)
	}
	sh, err := s.authorizeShare(src.ShareID, src.ShareVersion, now)
	if err != nil || src.UserID == sh.OwnerUserID {
		return err
	}
	hourly, pending := 0, 0
	for _, o := range s.Conversations {
		for _, m := range o.Messages {
			from := o.Endpoints[m.From]
			if from.ShareID == "" || from.UserID == sh.OwnerUserID || o.Endpoints[1-m.From].UserID != sh.OwnerUserID {
				continue
			}
			recent := now.Sub(m.SentAt) < time.Hour
			if from.ShareID == sh.ID && from.UserID == src.UserID {
				if recent {
					hourly++
				}
				if m.Delivery == "pending" || m.Delivery == "dispatching" || m.Delivery == "stored" {
					pending++
				}
			}
		}
	}
	if hourly >= shareSenderHourly || pending >= shareSenderPending {
		return fmt.Errorf("shared agent message limit reached; check back later")
	}
	return nil
}

// suppressShare marks undelivered messages of a revoked grant so they are never
// offered later. Admitted and processed receipts stay as they were.
func (s *agentMessageStore) suppressShare(shareID string) {
	for ci := range s.Conversations {
		c := &s.Conversations[ci]
		if c.Endpoints[0].ShareID != shareID {
			continue
		}
		for mi := range c.Messages {
			if m := &c.Messages[mi]; m.Delivery == "stored" || m.Delivery == "pending" || m.Delivery == "dispatching" {
				m.Delivery = "suppressed"
			}
		}
	}
}

// suppressFrom marks undelivered mail one person sent to an owner's shared
// agents, so blocking them also stops what was already queued under a share
// that stays in force for everyone else.
func (s *agentMessageStore) suppressFrom(ownerID, personID string) {
	for ci := range s.Conversations {
		c := &s.Conversations[ci]
		first, second := c.Endpoints[0].UserID, c.Endpoints[1].UserID
		if c.Endpoints[0].ShareID == "" || !(first == ownerID && second == personID || first == personID && second == ownerID) {
			continue
		}
		for mi := range c.Messages {
			if m := &c.Messages[mi]; c.Endpoints[m.From].UserID == personID && (m.Delivery == "stored" || m.Delivery == "pending" || m.Delivery == "dispatching") {
				m.Delivery = "suppressed"
			}
		}
	}
}

// endpointDisplayLabel shows another account's agent as owner/name, using the
// server's own username for the owner and never anything the sender wrote.
func endpointDisplayLabel(e agentMessageEndpoint, viewerUserID string) string {
	if e.ShareID != "" && e.UserID != viewerUserID {
		return shareOwnerLabel(e.UserID) + "/" + e.Label
	}
	return e.Label
}

// crossPersonOwner is the owner's server username when the endpoint belongs to
// another person's shared agent, and empty for same-account senders.
func crossPersonOwner(e agentMessageEndpoint, viewerUserID string) string {
	if e.ShareID != "" && e.UserID != viewerUserID {
		return shareOwnerLabel(e.UserID)
	}
	return ""
}

func (api *StreamingAPI) externalAuthorizeSharedPeer(ctx context.Context, claims *UserClaims, peer agentMessageEndpoint, send bool) error {
	denied := fmt.Errorf("conversation unavailable or access denied")
	if !namedAgentOperationAllowed(claims, send) || peer.NamedAgentID == "" {
		return denied
	}
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return denied
	}
	if isGroupShareID(peer.ShareID) {
		g := s.groupByID(groupIDFromShare(peer.ShareID))
		if g == nil || peer.UserID == claims.UserID || !g.hasMember(peer.NamedAgentID, peer.UserID) || !s.registeredAgent(peer.NamedAgentID, peer.UserID) || !shareAccountActive(peer.UserID) || !shareAccountActive(claims.UserID) {
			return denied
		}
		for _, m := range g.Members {
			if m.UserID == claims.UserID && s.registeredAgent(m.AgentID, m.UserID) {
				return nil // the exact pair is checked again wherever a conversation is used
			}
		}
		return denied
	}
	sh, err := s.authorizeShare(peer.ShareID, peer.ShareVersion, time.Now().UTC())
	if err != nil || peer.UserID == claims.UserID {
		return denied
	}
	pair := (peer.UserID == sh.OwnerUserID && peer.NamedAgentID == sh.AgentID && s.shareAllows(sh, claims.UserID)) ||
		(claims.UserID == sh.OwnerUserID && s.shareAllows(sh, peer.UserID))
	if !pair {
		return denied
	}
	for _, a := range s.Agents {
		if a.ID == peer.NamedAgentID && a.UserID == peer.UserID {
			return nil
		}
	}
	return denied
}

// resolveSharedMessageAgent turns agent:<owner>/<name> into a target bound to
// the owner's account id and the live grant, so a later rename redirects nothing.
func resolveSharedMessageAgent(ctx context.Context, subjectID, senderAgentID, ref string) (triggerTarget, error) {
	denied := fmt.Errorf("named agent unavailable or access denied")
	ownerName, agentName, _ := strings.Cut(ref, "/")
	name, err := normalizeMessageAgentName(agentName)
	dir, dirErr := loadUserDirectory()
	if err != nil || dirErr != nil || dir == nil {
		return triggerTarget{}, denied
	}
	owner := dir.byUsername(ownerName)
	if owner == nil || owner.ID == subjectID {
		return triggerTarget{}, denied
	}
	s, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return triggerTarget{}, err
	}
	now := time.Now().UTC()
	for _, a := range s.Agents {
		if a.UserID != owner.ID || a.Name != name {
			continue
		}
		var best *agentShare
		for i := range s.Shares {
			sh := &s.Shares[i]
			if sh.OwnerUserID == owner.ID && sh.AgentID == a.ID && s.shareAllows(*sh, subjectID) {
				if _, e := s.authorizeShare(sh.ID, sh.RevocationVersion, now); e == nil && (best == nil || sh.CreatedAt.After(best.CreatedAt)) {
					best = sh
				}
			}
		}
		if best != nil {
			return triggerTarget{NamedAgentID: a.ID, Kind: triggerCallerUser, Label: a.Name, ShareOwnerUserID: owner.ID, ShareID: best.ID, ShareVersion: best.RevocationVersion}, nil
		}
		// Two agents in the same group may message each other directly.
		if senderAgentID != "" {
			if g := s.commonGroup(senderAgentID, subjectID, a.ID, owner.ID); g != nil && shareAccountActive(owner.ID) {
				return triggerTarget{NamedAgentID: a.ID, Kind: triggerCallerUser, Label: a.Name, ShareOwnerUserID: owner.ID, ShareID: groupShareID(g.ID), ShareVersion: 1}, nil
			}
		}
		// Groups are open and their members are listed to everyone, so naming the
		// groups this agent is in reveals nothing new and tells the sender how to
		// reach it. An agent in no group stays a neutral denial.
		if groups := s.groupNamesOf(a.ID, owner.ID); len(groups) > 0 && shareAccountActive(owner.ID) {
			return triggerTarget{}, fmt.Errorf("You can message agent:%s/%s after one of your agents joins a group it is in: %s. Type /agentworks share %s in your chat.", ownerName, a.Name, strings.Join(groups, ", "), groups[0])
		}
	}
	return triggerTarget{}, denied
}

// sharedAgentItems lists the agents other people shared with this person, each
// labelled with the owner's server username.
func (s agentMessageStore) sharedAgentItems(userID string, now time.Time) []map[string]interface{} {
	items := []map[string]interface{}{}
	for _, sh := range s.Shares {
		if !s.shareAllows(sh, userID) {
			continue
		}
		if _, err := s.authorizeShare(sh.ID, sh.RevocationVersion, now); err != nil {
			continue
		}
		for _, a := range s.Agents {
			if a.ID != sh.AgentID {
				continue
			}
			owner := shareOwnerLabel(sh.OwnerUserID)
			item := map[string]interface{}{"agent_name": owner + "/" + a.Name, "target": "agent:" + owner + "/" + a.Name, "owner": owner, "shared": true, "capability": sh.Capability, "scope": map[bool]string{true: "everyone", false: "person"}[sh.serverScope()], "purpose": a.Purpose, "online": namedAgentOnline(a)}
			if !sh.serverScope() {
				item["expires_at"] = sh.ExpiresAt
			}
			items = append(items, item)
		}
	}
	return items
}

// incomingShareActivity tells an owner which people's agents messaged their
// agents through a grant: counts and times only, never message text.
func (s agentMessageStore) incomingShareActivity(ownerID string, label func(string) string) []map[string]interface{} {
	type key struct{ person, sender, agent string }
	type tally struct {
		count int
		last  time.Time
	}
	tallies := map[key]*tally{}
	for _, c := range s.Conversations {
		for side, e := range c.Endpoints {
			if e.ShareID == "" || e.UserID != ownerID || c.Endpoints[1-side].UserID == ownerID {
				continue
			}
			other := c.Endpoints[1-side]
			k := key{label(other.UserID), other.Label, e.Label}
			t := tallies[k]
			if t == nil {
				t = &tally{}
				tallies[k] = t
			}
			for _, m := range c.Messages {
				if m.From == 1-side {
					t.count++
					if m.SentAt.After(t.last) {
						t.last = m.SentAt
					}
				}
			}
		}
	}
	out := []map[string]interface{}{}
	for k, t := range tallies {
		out = append(out, map[string]interface{}{"person": k.person, "sender_agent": k.sender, "agent_name": k.agent, "messages": t.count, "last_at": t.last})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["last_at"].(time.Time).After(out[j]["last_at"].(time.Time)) })
	return out
}

// shareOwnerLabel is the only owner name shown to a recipient. It is the
// server's own username for the account id, never an email or sender text.
func shareOwnerLabel(userID string) string { return shareOwnerLabeler()(userID) }

// shareOwnerLabeler loads the directory once and labels many accounts.
func shareOwnerLabeler() func(string) string {
	dir, err := loadUserDirectory()
	return func(userID string) string {
		if userID == shareScopeServer {
			return "everyone"
		}
		if err == nil && dir != nil {
			if rec := dir.byID(userID); rec != nil && rec.Username != "" {
				return rec.Username
			}
		}
		return "unknown"
	}
}

// bullpenShareClaims admits a signed-in browser session whose JWT came in the
// Authorization header. Query-string tokens are refused so a link cannot grant.
func (api *StreamingAPI) bullpenShareClaims(w http.ResponseWriter, r *http.Request, mutate bool) *UserClaims {
	if mutate && (!strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || r.URL.Query().Get("token") != "") {
		externalError(w, http.StatusForbidden, "access_denied", "Sign in from the browser to change sharing.")
		return nil
	}
	return bullpenBrowserClaims(w, r, mutate)
}

func (api *StreamingAPI) handleBullpenShares(w http.ResponseWriter, r *http.Request) {
	claims := api.bullpenShareClaims(w, r, false)
	if claims == nil {
		return
	}
	s, err := readAgentMessageSnapshot(r.Context())
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "shares_unavailable", "Sharing is unavailable. Try again.")
		return
	}
	now := time.Now().UTC()
	label := shareOwnerLabeler()
	shares, blocks := []map[string]interface{}{}, []map[string]interface{}{}
	for _, sh := range s.Shares {
		if sh.OwnerUserID != claims.UserID {
			continue
		}
		name := ""
		for _, a := range s.Agents {
			if a.ID == sh.AgentID {
				name = a.Name
			}
		}
		state := "active"
		if !sh.RevokedAt.IsZero() {
			state = "revoked"
		} else if !sh.live(now) {
			state = "expired"
		}
		row := map[string]interface{}{"share_id": sh.ID, "agent_name": name, "agent_id": sh.AgentID, "person": label(sh.SubjectUserID), "capability": sh.Capability, "created_at": sh.CreatedAt, "scope": map[bool]string{true: "everyone", false: "person"}[sh.serverScope()], "state": state}
		if !sh.serverScope() {
			row["expires_at"] = sh.ExpiresAt // a share with everyone has no expiry
		}
		shares = append(shares, row)
	}
	for _, b := range s.Blocks {
		if b.OwnerUserID == claims.UserID {
			blocks = append(blocks, map[string]interface{}{"person": label(b.BlockedUserID), "blocked_at": b.At})
		}
	}
	sort.Slice(shares, func(i, j int) bool {
		return shares[i]["created_at"].(time.Time).After(shares[j]["created_at"].(time.Time))
	})
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]interface{}{"shares": shares, "blocks": blocks, "shared_with_me": s.sharedAgentItems(claims.UserID, now), "incoming": s.incomingShareActivity(claims.UserID, label)})
}

func (api *StreamingAPI) handleBullpenShareCreate(w http.ResponseWriter, r *http.Request) {
	claims := api.bullpenShareClaims(w, r, true)
	if claims == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var in struct {
		AgentName    string `json:"agent_name"`
		Person       string `json:"person"`
		ExpiresHours int    `json:"expires_in_hours"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Choose an agent and a person.")
		return
	}
	name, err := normalizeMessageAgentName(in.AgentName)
	dir, dirErr := loadUserDirectory()
	if err != nil || dirErr != nil || dir == nil {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Choose an agent and a person.")
		return
	}
	subject := dir.byUsername(in.Person)
	if subject == nil || subject.Disabled || subject.ID == claims.UserID {
		externalError(w, http.StatusBadRequest, "unknown_person", "No active person with that username.")
		return
	}
	ttl := shareDefaultTTL
	if in.ExpiresHours > 0 {
		ttl = shareMaxTTL
		if in.ExpiresHours < int(shareMaxTTL/time.Hour) {
			ttl = time.Duration(in.ExpiresHours) * time.Hour
		}
	}
	out, status, code, msg := api.createShare(r.Context(), claims.UserID, name, subject.ID, ttl)
	if code != "" {
		externalError(w, status, code, msg)
		return
	}
	externalJSON(w, out)
}

func (api *StreamingAPI) createShare(ctx context.Context, owner, agentName, subject string, ttl time.Duration) (map[string]interface{}, int, string, string) {
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return nil, http.StatusServiceUnavailable, "shares_unavailable", "Sharing is unavailable. Try again."
	}
	var agent *namedMessageAgent
	for i := range s.Agents {
		if s.Agents[i].UserID == owner && s.Agents[i].Name == agentName {
			agent = &s.Agents[i]
		}
	}
	if agent == nil {
		return nil, http.StatusNotFound, "unknown_agent", "You have no agent with that name."
	}
	now := time.Now().UTC()
	s.pruneShares(now)
	count := 0
	for _, sh := range s.Shares {
		if sh.OwnerUserID == owner && sh.live(now) {
			count++
			if sh.AgentID == agent.ID && sh.SubjectUserID == subject {
				return nil, http.StatusConflict, "already_shared", "This agent is already shared with that person."
			}
		}
	}
	if count >= sharesPerOwner {
		return nil, http.StatusTooManyRequests, "share_limit", "Revoke a share before adding another."
	}
	// A new grant lifts an earlier block by the same owner on that person.
	blocks := s.Blocks[:0]
	for _, b := range s.Blocks {
		if !(b.OwnerUserID == owner && b.BlockedUserID == subject) {
			blocks = append(blocks, b)
		}
	}
	s.Blocks = blocks
	sh := agentShare{ID: "share-" + uuid.NewString(), OwnerUserID: owner, AgentID: agent.ID, SubjectUserID: subject, Capability: shareCapabilityChat, CreatedAt: now, ExpiresAt: now.Add(ttl), RevocationVersion: 1}
	s.Shares = append(s.Shares, sh)
	s.auditShare("share_created", owner, owner, subject, sh.ID, agent.ID)
	if err := saveAgentMessageStore(ctx, s); err != nil {
		return nil, http.StatusServiceUnavailable, "shares_unavailable", "Sharing is unavailable. Try again."
	}
	return map[string]interface{}{"share_id": sh.ID, "agent_name": agent.Name, "person": shareOwnerLabel(subject), "capability": sh.Capability, "expires_at": sh.ExpiresAt, "state": "active"}, 0, "", ""
}

// handleBullpenShareRevoke ends a grant at once: the version bump makes every conversation
// that remembered the old one fail its next authorizeShare check.
func (api *StreamingAPI) handleBullpenShareRevoke(w http.ResponseWriter, r *http.Request) {
	claims := api.bullpenShareClaims(w, r, true)
	if claims == nil {
		return
	}
	id := mux.Vars(r)["id"]
	if status, code, msg := api.revokeShare(r.Context(), claims.UserID, id); code != "" {
		externalError(w, status, code, msg)
		return
	}
	externalJSON(w, map[string]interface{}{"share_id": id, "state": "revoked"})
}

func (api *StreamingAPI) revokeShare(ctx context.Context, owner, id string) (int, string, string) {
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return http.StatusServiceUnavailable, "shares_unavailable", "Sharing is unavailable. Try again."
	}
	for i := range s.Shares {
		sh := &s.Shares[i]
		if sh.ID != id || sh.OwnerUserID != owner {
			continue
		}
		if sh.RevokedAt.IsZero() {
			sh.RevokedAt = time.Now().UTC()
			sh.RevocationVersion++
			s.suppressShare(sh.ID)
			s.auditShare("share_revoked", owner, sh.OwnerUserID, sh.SubjectUserID, sh.ID, sh.AgentID)
			if err := saveAgentMessageStore(ctx, s); err != nil {
				return http.StatusServiceUnavailable, "shares_unavailable", "Sharing is unavailable. Try again."
			}
		}
		return 0, "", ""
	}
	return http.StatusNotFound, "unknown_share", "No such share."
}

// Blocking a person revokes everything shared with them and refuses new sends.
func (api *StreamingAPI) handleBullpenBlock(w http.ResponseWriter, r *http.Request) {
	claims := api.bullpenShareClaims(w, r, true)
	if claims == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var in struct {
		Person  string `json:"person"`
		Unblock bool   `json:"unblock"`
	}
	dir, dirErr := loadUserDirectory()
	if json.NewDecoder(r.Body).Decode(&in) != nil || dirErr != nil || dir == nil {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Choose a person.")
		return
	}
	subject := dir.byUsername(in.Person)
	if subject == nil || subject.Disabled || subject.ID == claims.UserID {
		externalError(w, http.StatusBadRequest, "unknown_person", "No active person with that username.")
		return
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(r.Context())
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "shares_unavailable", "Sharing is unavailable. Try again.")
		return
	}
	if in.Unblock {
		kept := s.Blocks[:0]
		for _, b := range s.Blocks {
			if !(b.OwnerUserID == claims.UserID && b.BlockedUserID == subject.ID) {
				kept = append(kept, b)
			}
		}
		s.Blocks = kept
		s.auditShare("unblocked", claims.UserID, claims.UserID, subject.ID, "", "")
	} else if !s.shareBlocked(claims.UserID, subject.ID) {
		now := time.Now().UTC()
		owned := 0
		for _, b := range s.Blocks {
			if b.OwnerUserID == claims.UserID {
				owned++
			}
		}
		if owned >= blocksPerOwner {
			externalError(w, http.StatusTooManyRequests, "block_limit", "Unblock someone before blocking another person.")
			return
		}
		s.Blocks = append(s.Blocks, agentShareBlock{OwnerUserID: claims.UserID, BlockedUserID: subject.ID, At: now})
		for i := range s.Shares {
			if sh := &s.Shares[i]; sh.OwnerUserID == claims.UserID && sh.SubjectUserID == subject.ID && sh.RevokedAt.IsZero() {
				sh.RevokedAt = now
				sh.RevocationVersion++
				s.suppressShare(sh.ID)
			}
		}
		s.suppressFrom(claims.UserID, subject.ID)
		s.auditShare("blocked", claims.UserID, claims.UserID, subject.ID, "", "")
	}
	if err := saveAgentMessageStore(r.Context(), s); err != nil {
		externalError(w, http.StatusServiceUnavailable, "shares_unavailable", "Sharing is unavailable. Try again.")
		return
	}
	externalJSON(w, map[string]interface{}{"person": shareOwnerLabel(subject.ID), "blocked": !in.Unblock})
}

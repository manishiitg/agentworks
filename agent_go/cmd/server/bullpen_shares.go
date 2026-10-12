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
		if !sh.live(now) || sh.RevocationVersion != version || sh.Capability != shareCapabilityChat || s.shareBlocked(sh.OwnerUserID, sh.SubjectUserID) ||
			!shareAccountActive(sh.OwnerUserID) || !shareAccountActive(sh.SubjectUserID) {
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

// shareOwnerLabel is the only owner name shown to a recipient. It is the
// server's own username for the account id, never an email or sender text.
func shareOwnerLabel(userID string) string { return shareOwnerLabeler()(userID) }

// shareOwnerLabeler loads the directory once and labels many accounts.
func shareOwnerLabeler() func(string) string {
	dir, err := loadUserDirectory()
	return func(userID string) string {
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
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(r.Context())
	agentMessageMu.Unlock()
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
		shares = append(shares, map[string]interface{}{"share_id": sh.ID, "agent_name": name, "agent_id": sh.AgentID, "person": label(sh.SubjectUserID), "capability": sh.Capability, "created_at": sh.CreatedAt, "expires_at": sh.ExpiresAt, "state": state})
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
	externalJSON(w, map[string]interface{}{"shares": shares, "blocks": blocks})
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
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(r.Context())
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "shares_unavailable", "Sharing is unavailable. Try again.")
		return
	}
	for i := range s.Shares {
		sh := &s.Shares[i]
		if sh.ID != id || sh.OwnerUserID != claims.UserID {
			continue
		}
		if sh.RevokedAt.IsZero() {
			sh.RevokedAt = time.Now().UTC()
			sh.RevocationVersion++
			s.auditShare("share_revoked", claims.UserID, sh.OwnerUserID, sh.SubjectUserID, sh.ID, sh.AgentID)
			if err := saveAgentMessageStore(r.Context(), s); err != nil {
				externalError(w, http.StatusServiceUnavailable, "shares_unavailable", "Sharing is unavailable. Try again.")
				return
			}
		}
		externalJSON(w, map[string]interface{}{"share_id": id, "state": "revoked"})
		return
	}
	externalError(w, http.StatusNotFound, "unknown_share", "No such share.")
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
	if subject == nil || subject.ID == claims.UserID {
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
			}
		}
		s.auditShare("blocked", claims.UserID, claims.UserID, subject.ID, "", "")
	}
	if err := saveAgentMessageStore(r.Context(), s); err != nil {
		externalError(w, http.StatusServiceUnavailable, "shares_unavailable", "Sharing is unavailable. Try again.")
		return
	}
	externalJSON(w, map[string]interface{}{"person": shareOwnerLabel(subject.ID), "blocked": !in.Unblock})
}

package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

type agentConnectionQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Child credentials never acquire the parent tool catalog. Every authentication
// and refresh also checks the parent family, so revocation is immediate.
func bindAgentConnection(ctx context.Context, q agentConnectionQuerier, grant mcpOAuthGrant) (mcpOAuthGrant, error) {
	var parent, name, id string
	err := q.QueryRowContext(ctx, `SELECT parent_family,agent_name,agent_id FROM agent_connections WHERE family_id=?`, grant.FamilyID).Scan(&parent, &name, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return grant, nil
	}
	if err != nil {
		return grant, err
	}
	p, err := scanMCPOAuthGrant(q.QueryRowContext(ctx, `SELECT `+mcpOAuthGrantColumns+` FROM tokens WHERE family_id=? AND revoked_at IS NULL AND expires_at>? ORDER BY expires_at DESC LIMIT 1`, parent, time.Now().Unix()))
	if err != nil {
		return grant, err
	}
	var nested int
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_connections WHERE family_id=?`, parent).Scan(&nested); err != nil {
		return grant, err
	}
	claims, err := accessTokenClaims(mcpOAuthTokenForGrant(p))
	if err != nil || nested != 0 || (p.ClientID != cliOAuthClientID && !isAgentServerLinkClient(p.ClientID)) || p.UserID != grant.UserID || p.Resource != grant.Resource || !namedAgentOperationAllowed(claims, true) {
		return grant, errors.New("agent connection parent is no longer authorized")
	}
	grant.BoundAgentName, grant.BoundAgentID = name, id
	return grant, nil
}
func (s *mcpOAuthStore) bindAgentConnection(ctx context.Context, g mcpOAuthGrant) (mcpOAuthGrant, error) {
	return bindAgentConnection(ctx, s.db, g)
}

func (api *StreamingAPI) createAgentConnection(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken == nil || claims.AccessToken.BoundAgentID != "" || !namedAgentOperationAllowed(claims, true) || !strings.HasPrefix(claims.AccessToken.ID, "oauth-") {
		externalError(w, 403, "attachment_refused", "Only the person's approved CLI login can create an agent connection.")
		return
	}
	agent, err := namedAgentForClaims(r.Context(), claims, externalArg(args, "agent_name"))
	if err != nil {
		externalFailure(w, err)
		return
	}
	if isAgentServerLinkClient(claims.AccessToken.OAuthClientID) {
		origin, e := agentServerLinkOrigin(r.Context(), claims)
		if e != nil || agent.Relay == nil || agent.Relay.Origin != origin {
			externalError(w, 403, "attachment_refused", "A linked server can connect only its own relayed agents.")
			return
		}
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		externalFailure(w, err)
		return
	}
	defer store.Close()
	parent, err := store.ActiveFamily(r.Context(), strings.TrimPrefix(claims.AccessToken.ID, "oauth-"))
	if err != nil || parent.ClientID != cliOAuthClientID && !isAgentServerLinkClient(parent.ClientID) {
		externalError(w, 403, "attachment_refused", "CLI login expired or revoked.")
		return
	}
	grant := parent
	grant.FamilyID = uuid.NewString()
	grant.BoundAgentName = agent.Name
	grant.BoundAgentID = agent.ID
	grant.Scopes = []string{"messages:read", "messages:send"}
	grant.WorkflowIDs = nil
	tx, err := store.db.BeginTx(r.Context(), nil)
	if err != nil {
		externalFailure(w, err)
		return
	}
	defer tx.Rollback()
	// A stable address has one active child per parent. A freshly approved
	// child supersedes abandoned/offline families instead of leaking slots.
	if _, err = tx.ExecContext(r.Context(), `UPDATE tokens SET revoked_at=? WHERE revoked_at IS NULL AND family_id IN (SELECT family_id FROM agent_connections WHERE parent_family=? AND agent_id=?)`, time.Now().Unix(), parent.FamilyID, agent.ID); err != nil {
		externalFailure(w, err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO agent_connections(family_id,parent_family,agent_name,agent_id) VALUES(?,?,?,?)`, grant.FamilyID, parent.FamilyID, agent.Name, agent.ID); err != nil {
		externalFailure(w, err)
		return
	}
	access, refresh, err := issueMCPOAuthPair(r.Context(), tx, &grant)
	if err != nil {
		externalFailure(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		externalFailure(w, err)
		return
	}
	// SQL is committed before changing the message store. An orphan family has no
	// agent permission and cannot use any other tools; revoke it on a failed write.
	agentMessageMu.Lock()
	state, err := readAgentMessageStore(r.Context())
	found := false
	if err == nil {
		for i, a := range state.Agents {
			if a.ID == agent.ID && namedAgentConnectionAllowed(a, claims) {
				// Expired/revoked derived connections must not consume the
				// stable address's grant quota forever.
				a.Connections = slices.DeleteFunc(slices.Clone(a.Connections), func(id string) bool {
					if !strings.HasPrefix(id, "oauth-") {
						return false
					}
					family := strings.TrimPrefix(id, "oauth-")
					var bound string
					if store.db.QueryRowContext(r.Context(), `SELECT agent_id FROM agent_connections WHERE family_id=?`, family).Scan(&bound) != nil {
						return false
					}
					_, e := store.LiveFamily(r.Context(), family)
					return e != nil
				})
				if len(a.Connections) >= 32 {
					break
				}
				state.Agents[i].Connections = slices.Concat(a.Connections, []string{"oauth-" + grant.FamilyID})
				found = true
				break
			}
		}
		if !found {
			err = errors.New("agent changed or connection limit reached")
		}
	}
	if err == nil {
		err = saveAgentMessageStore(r.Context(), state)
	}
	agentMessageMu.Unlock()
	if err != nil {
		_ = store.RevokeFamily(r.Context(), grant.FamilyID, grant.UserID)
		externalFailure(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": 3600, "connection_id": "oauth-" + grant.FamilyID, "agent_id": agent.ID})
}

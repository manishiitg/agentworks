package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Names are account-owned addresses, not device identities or access grants.
// A registration survives reconnects; deleting and recreating a name gets a new
// ID so old conversations cannot silently reach a replacement agent.
type namedMessageAgent struct {
	ID               string                `json:"agent_id"`
	UserID           string                `json:"user_id"`
	Name             string                `json:"name"`
	Connections      []string              `json:"allowed_connection_ids"`
	CreatedAt        time.Time             `json:"created_at"`
	Purpose          string                `json:"purpose,omitempty"`
	PurposeUpdatedAt time.Time             `json:"purpose_updated_at,omitempty"`
	Attachment       *namedAgentAttachment `json:"attachment,omitempty"`
}

func normalizeMessageAgentName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if len(name) < 1 || len(name) > 64 {
		return "", fmt.Errorf("agent_name must contain 1 to 64 ASCII letters, digits, dots, underscores or hyphens")
	}
	for i, c := range name {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return "", fmt.Errorf("agent_name must start with a letter or digit and contain only letters, digits, dots, underscores or hyphens")
	}
	return name, nil
}

func namedAgentConnectionAllowed(a namedMessageAgent, claims *UserClaims) bool {
	if claims == nil || claims.UserID != a.UserID || userAccessForClaims(claims).Disabled {
		return false
	}
	if claims.AccessToken == nil {
		return true
	} // signed-in account owner
	if claims.AccessToken.BoundAgentID != "" && (claims.AccessToken.BoundAgentID != a.ID || claims.AccessToken.BoundAgentName != a.Name) {
		return false
	}
	for _, id := range a.Connections {
		if id == claims.AccessToken.ID && id != "" {
			return true
		}
	}
	return false
}

func namedAgentOperationAllowed(claims *UserClaims, send bool) bool {
	if claims == nil || userAccessForClaims(claims).Disabled {
		return false
	}
	if claims.AccessToken == nil {
		return true
	}
	t := claims.AccessToken
	if t.BoundAgentID != "" {
		return t.Allows("messages:send") || !send && t.Allows("messages:read")
	}
	return t.Allows("crews:run") || t.Allows("runs:execute") || externalCodeRunAllowed(claims) || !send && (t.Allows("crews:read") || t.Allows("workflows:read"))
}

// Read a fresh store snapshot. Mutation callers already hold agentMessageMu;
// authorization fails closed if storage is unavailable.
func namedAgentByID(ctx context.Context, userID, id string) (namedMessageAgent, error) {
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return namedMessageAgent{}, err
	}
	for _, a := range s.Agents {
		if a.ID == id && a.UserID == userID {
			return a, nil
		}
	}
	return namedMessageAgent{}, fmt.Errorf("named agent unavailable or access denied")
}

func namedAgentForClaims(ctx context.Context, claims *UserClaims, name string) (namedMessageAgent, error) {
	name, err := normalizeMessageAgentName(name)
	if err != nil {
		return namedMessageAgent{}, err
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	agentMessageMu.Unlock()
	if err != nil {
		return namedMessageAgent{}, err
	}
	for _, a := range s.Agents {
		if a.UserID == claims.UserID && a.Name == name && namedAgentConnectionAllowed(a, claims) {
			return a, nil
		}
	}
	return namedMessageAgent{}, fmt.Errorf("named agent unavailable or access denied")
}

func resolveNamedMessageAgent(ctx context.Context, userID, name string) (triggerTarget, error) {
	if strings.Contains(name, "/") {
		return resolveSharedMessageAgent(ctx, userID, name)
	}
	name, err := normalizeMessageAgentName(name)
	if err != nil {
		return triggerTarget{}, err
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	agentMessageMu.Unlock()
	if err != nil {
		return triggerTarget{}, err
	}
	if userAccessForClaims(&UserClaims{UserID: userID}).Disabled {
		return triggerTarget{}, fmt.Errorf("named agent unavailable or access denied")
	}
	for _, a := range s.Agents {
		if a.UserID == userID && a.Name == name {
			return triggerTarget{NamedAgentID: a.ID, Kind: triggerCallerUser, Label: a.Name}, nil
		}
	}
	return triggerTarget{}, fmt.Errorf("named agent unavailable or access denied")
}

func listNamedMessageAgents(ctx context.Context, userID string) (map[string]interface{}, error) {
	if userAccessForClaims(&UserClaims{UserID: userID}).Disabled {
		return nil, fmt.Errorf("account is disabled")
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	agentMessageMu.Unlock()
	if err != nil {
		return nil, err
	}
	items := []map[string]interface{}{}
	for _, a := range s.Agents {
		if a.UserID == userID {
			items = append(items, map[string]interface{}{"agent_name": a.Name, "target": "agent:" + a.Name, "created_at": a.CreatedAt, "agent_id": a.ID, "purpose": a.Purpose, "online": namedAgentOnline(a), "shared_with_everyone": len(s.serverSharesOf(a.ID, time.Now().UTC())) > 0, "provider": func() string {
				if a.Attachment != nil {
					return a.Attachment.Provider
				}
				return ""
			}()})
		}
	}
	hydrateOwnerUsage(ctx, userID, items, s.Agents)
	items = append(items, s.sharedAgentItems(userID, time.Now().UTC())...)
	sort.Slice(items, func(i, j int) bool { return items[i]["agent_name"].(string) < items[j]["agent_name"].(string) })
	return map[string]interface{}{"agents": items}, nil
}

func (api *StreamingAPI) externalMessageCaller(ctx context.Context, claims *UserClaims, args map[string]any) (triggerLinkCaller, error) {
	c := externalCrewCaller(claims)
	if name := externalArg(args, "agent_name"); name != "" {
		a, err := namedAgentForClaims(ctx, claims, name)
		if err != nil {
			return c, err
		}
		c.NamedAgentID, c.Label = a.ID, a.Name
	}
	return c, nil
}

func (api *StreamingAPI) externalNamedAgentRegistration(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !namedAgentOperationAllowed(claims, true) {
		externalError(w, 403, "insufficient_scope", "Registering or removing an agent requires a run-enabled connection.")
		return
	}
	name, err := normalizeMessageAgentName(externalArg(args, "agent_name"))
	if err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	// Additional readers must be explicit, live connections of this account.
	var connections []string
	if raw, present := args["allowed_connection_ids"]; present {
		data, _ := json.Marshal(raw)
		if json.Unmarshal(data, &connections) != nil || len(connections) > 32 {
			externalError(w, 400, "invalid_arguments", "allowed_connection_ids must contain at most 32 connection IDs")
			return
		}
		for _, id := range connections {
			c, e := agentMessageConnectionClaims(r.Context(), id)
			if e != nil || c.UserID != claims.UserID || !namedAgentOperationAllowed(c, false) {
				externalError(w, 400, "invalid_arguments", "Every allowed connection must be active, belong to this account and have messaging access.")
				return
			}
		}
	}
	if claims.AccessToken != nil {
		if claims.AccessToken.ID == "" {
			externalError(w, 403, "insufficient_scope", "A durable connection ID is required.")
			return
		}
		connections = append(connections, claims.AccessToken.ID)
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(r.Context())
	if err != nil {
		externalCommunicationError(w, 400, "registration_refused", err)
		return
	}
	index, own := -1, 0
	for i, a := range s.Agents {
		if a.UserID == claims.UserID {
			own++
			if a.Name == name {
				index = i
			}
		}
	}
	if index >= 0 && !namedAgentConnectionAllowed(s.Agents[index], claims) {
		externalError(w, 404, "not_found", "Named agent unavailable or access denied.")
		return
	}
	if externalArg(args, "action") == "unregister" {
		if index < 0 {
			externalError(w, 404, "not_found", "Named agent unavailable or access denied.")
			return
		}
		s.Agents = append(s.Agents[:index], s.Agents[index+1:]...)
	} else {
		if index < 0 {
			if own >= 100 || len(s.Agents) >= agentConversationKeep {
				externalError(w, 400, "capacity_reached", "Named agent directory is full.")
				return
			}
			s.Agents = append(s.Agents, namedMessageAgent{ID: "agent-" + uuid.NewString(), UserID: claims.UserID, Name: name, CreatedAt: time.Now().UTC()})
			index = len(s.Agents) - 1
		} else if _, present := args["allowed_connection_ids"]; !present {
			connections = s.Agents[index].Connections
		}
		s.Agents[index].Connections = connections
		if purpose, present := args["purpose"]; present {
			text, ok := purpose.(string)
			if !ok || utf8.RuneCountInString(strings.TrimSpace(text)) > 512 || strings.ContainsAny(text, "\r\n") {
				externalError(w, 400, "invalid_arguments", "purpose must be a string of at most 512 characters")
				return
			}
			s.Agents[index].Purpose = cleanAgentPurpose(text)
		}
	}
	if err = saveAgentMessageStore(r.Context(), s); err != nil {
		externalCommunicationError(w, 400, "registration_refused", err)
		return
	}
	out := map[string]any{"agent_name": name, "target": "agent:" + name, "status": externalArg(args, "action") + "ed"}
	if externalArg(args, "action") == "register" {
		out["agent_id"] = s.Agents[index].ID
		out["allowed_connection_ids"] = s.Agents[index].Connections
	}
	if claims.AccessToken != nil {
		out["connection_id"] = claims.AccessToken.ID
	}
	externalJSON(w, out)
}

// Purpose is display metadata; controls and invisible formatting must not
// alter a roster or appear as terminal input. Length/line validation is separate.
func cleanAgentPurpose(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, text))
}

// Updating purpose cannot create a name, move an attachment or widen its connections.
func (api *StreamingAPI) externalNamedAgentPurpose(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	purpose, ok := args["purpose"].(string)
	purpose = strings.TrimSpace(purpose)
	if !ok || purpose == "" || utf8.RuneCountInString(purpose) > 512 || strings.ContainsAny(purpose, "\r\n") {
		externalError(w, 400, "invalid_arguments", "purpose must be one line of 1 to 512 characters")
		return
	}
	purpose = cleanAgentPurpose(purpose)
	if purpose == "" {
		externalError(w, 400, "invalid_arguments", "purpose must contain visible text")
		return
	}
	if !namedAgentOperationAllowed(claims, true) {
		externalError(w, 403, "insufficient_scope", "Connection lacks messaging write access.")
		return
	}
	name, err := normalizeMessageAgentName(externalArg(args, "agent_name"))
	if err != nil {
		externalError(w, 400, "invalid_arguments", "Invalid agent name.")
		return
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(r.Context())
	if err != nil {
		externalCommunicationError(w, 400, "purpose_refused", err)
		return
	}
	for i, a := range s.Agents {
		if a.UserID == claims.UserID && a.Name == name && namedAgentConnectionAllowed(a, claims) {
			if token := claims.AccessToken; token != nil && token.BoundAgentID != "" && (a.Attachment == nil || a.Attachment.DeliveryContract != 2 || a.Attachment.ID != externalArg(args, "attachment_id") || a.Attachment.ConnectionID != token.ID) {
				externalError(w, 403, "attachment_refused", "Exact attachment unavailable or changed.")
				return
			}
			if a.Purpose != purpose && time.Since(a.PurposeUpdatedAt) < time.Minute {
				externalError(w, 429, "rate_limited", "Purpose can change at most once per minute.")
				return
			}
			if a.Purpose == purpose {
				externalJSON(w, map[string]any{"agent_name": name, "purpose": purpose, "status": "updated"})
				return
			}
			s.Agents[i].Purpose = purpose
			s.Agents[i].PurposeUpdatedAt = time.Now().UTC()
			if err = saveAgentMessageStore(r.Context(), s); err != nil {
				externalCommunicationError(w, 400, "purpose_refused", err)
				return
			}
			externalJSON(w, map[string]any{"agent_name": name, "purpose": purpose, "status": "updated"})
			return
		}
	}
	externalError(w, 404, "not_found", "Named agent unavailable or access denied.")
}

// A global delivery cursor lets one named mailbox read first contacts from
// multiple conversations without confusing their independent reply cursors.
func (api *StreamingAPI) readNamedAgentMessages(ctx context.Context, claims *UserClaims, args map[string]any) (map[string]interface{}, error) {
	if !namedAgentOperationAllowed(claims, false) {
		return nil, fmt.Errorf("connection lacks messaging read access")
	}
	a, err := namedAgentForClaims(ctx, claims, externalArg(args, "agent_name"))
	if err != nil {
		return nil, err
	}
	after, limit := externalInt(args, "after", 0), externalInt(args, "limit", 30)
	if after < 0 || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("after must be non-negative and limit must be 1 to 100")
	}
	wait := externalInt(args, "wait_seconds", 0)
	if wait < 0 || wait > 25 {
		return nil, fmt.Errorf("wait_seconds must be 0 to 25")
	}
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	seen := int64(-1)
	for {
		version := agentMessageVersion.Load()
		if version == seen && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
			continue
		}
		seen = version
		agentMessageMu.Lock()
		s, e := readAgentMessageStore(ctx)
		agentMessageMu.Unlock()
		if e != nil {
			return nil, e
		}
		current, e := namedAgentByID(ctx, claims.UserID, a.ID)
		if e != nil || !namedAgentConnectionAllowed(current, claims) {
			return nil, fmt.Errorf("named agent unavailable or access denied")
		}
		items := []map[string]interface{}{}
		for _, c := range s.Conversations {
			for side, end := range c.Endpoints {
				if end.NamedAgentID != a.ID || end.UserID != claims.UserID {
					continue
				}
				if e = api.externalAuthorizeMessagePeer(ctx, claims, c.Endpoints[1-side], false); e != nil {
					continue
				}
				for _, m := range c.Messages {
					if m.From == side || m.AgentSequence <= after {
						continue
					}
					position := sort.Search(len(items), func(i int) bool { return items[i]["sequence"].(int) > m.AgentSequence })
					if position >= limit {
						continue
					}
					item := map[string]interface{}{"inbox_id": c.ID, "conversation_id": c.ID, "reply_address": c.ID, "message_id": m.ID, "sequence": m.AgentSequence, "conversation_sequence": m.Sequence, "sender": endpointDisplayLabel(c.Endpoints[m.From], claims.UserID), "message": m.Text, "sent_at": m.SentAt, "delivery": m.Delivery, "live_transport": m.LiveTransport, "native_session_id": m.LiveSession}
					if person := crossPersonOwner(c.Endpoints[m.From], claims.UserID); person != "" {
						item["from_person"], item["untrusted"] = person, true
					}
					items = append(items, nil)
					copy(items[position+1:], items[position:])
					items[position] = item
					if len(items) > limit {
						items = items[:limit]
					}
				}
			}
		}
		// Keep the JSON page below two MiB including envelope/cursor overhead.
		pageBytes, count := 0, 0
		for _, item := range items {
			raw, e := json.Marshal(item)
			if e != nil {
				return nil, e
			}
			if pageBytes+len(raw) > (2<<20)-4096 {
				if count == 0 {
					return nil, fmt.Errorf("message exceeds the mailbox page limit")
				}
				break
			}
			pageBytes += len(raw) + 1
			count++
		}
		items = items[:count]
		current, e = namedAgentByID(ctx, claims.UserID, a.ID)
		if e != nil || !namedAgentConnectionAllowed(current, claims) {
			return nil, fmt.Errorf("named agent unavailable or access denied")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(items) > 0 || wait == 0 || time.Now().After(deadline) {
			// A waiting connection may have been revoked since HTTP admission.
			if wait > 0 && claims.AccessToken != nil {
				live, e := agentMessageConnectionClaims(ctx, claims.AccessToken.ID)
				if e != nil || live.UserID != claims.UserID || !namedAgentOperationAllowed(live, false) || !namedAgentConnectionAllowed(current, live) {
					return nil, fmt.Errorf("connection is no longer authorized")
				}
				// Rebuild the page with current scopes/target bounds before release.
				if !time.Now().Before(deadline) || len(items) > 0 {
					copyArgs := map[string]any{}
					for k, v := range args {
						copyArgs[k] = v
					}
					copyArgs["wait_seconds"] = 0
					return api.readNamedAgentMessages(ctx, live, copyArgs)
				}
			}
			next := after
			if len(items) > 0 {
				next = items[len(items)-1]["sequence"].(int)
			}
			return map[string]interface{}{"agent_name": a.Name, "messages": items, "next_cursor": next}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

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
)

// A group is an open list of agents. Sharing an agent means joining a group:
// members see each other, message each other directly, or message the whole
// group. Anyone on the server can list groups and members and join one. Access
// is checked wherever a one-person grant is checked, using the group id as the
// conversation's grant id.
const (
	groupsMax           = 500
	groupMembersMax     = 200
	groupsPerAgent      = 50
	groupSenderHourly   = 300 // messages (each fan-out copy counts) one person may send per group per hour
	groupSenderPending  = 300 // undelivered messages one person may leave in one group
	defaultGroupName    = "general"
	defaultGroupPurpose = "Everyone on this server"
)

type agentGroup struct {
	ID            string        `json:"group_id"`
	Name          string        `json:"name"`
	Purpose       string        `json:"purpose"`
	CreatorUserID string        `json:"creator_user_id"`
	CreatedAt     time.Time     `json:"created_at"`
	Members       []groupMember `json:"members"`
}

type groupMember struct {
	AgentID  string    `json:"agent_id"`
	UserID   string    `json:"user_id"`
	JoinedAt time.Time `json:"joined_at"`
}

func groupShareID(id string) string     { return "group:" + id }
func isGroupShareID(id string) bool     { return strings.HasPrefix(id, "group:") }
func groupIDFromShare(id string) string { return strings.TrimPrefix(id, "group:") }
func (g agentGroup) hasMember(agentID, userID string) bool {
	for _, m := range g.Members {
		if m.AgentID == agentID && m.UserID == userID {
			return true
		}
	}
	return false
}

func (s agentMessageStore) groupIndex(name string) int {
	for i := range s.Groups {
		if s.Groups[i].Name == name {
			return i
		}
	}
	return -1
}

func (s agentMessageStore) groupByID(id string) *agentGroup {
	for i := range s.Groups {
		if s.Groups[i].ID == id {
			return &s.Groups[i]
		}
	}
	return nil
}

// commonGroup is a group both agents belong to, or nil.
func (s agentMessageStore) commonGroup(agentA, userA, agentB, userB string) *agentGroup {
	for i := range s.Groups {
		if g := &s.Groups[i]; g.hasMember(agentA, userA) && g.hasMember(agentB, userB) {
			return g
		}
	}
	return nil
}

// groupNamesOf lists the groups an agent belongs to.
func (s agentMessageStore) groupNamesOf(agentID, userID string) []string {
	names := []string{}
	for _, g := range s.Groups {
		if g.hasMember(agentID, userID) {
			names = append(names, g.Name)
		}
	}
	sort.Strings(names)
	return names
}

func (s agentMessageStore) registeredAgent(agentID, userID string) bool {
	for _, a := range s.Agents {
		if a.ID == agentID && a.UserID == userID {
			return true
		}
	}
	return false
}

// groupConversationErr is the fence for a cross-account conversation inside a
// group: both ends are registered agents that are still members, with enabled
// accounts. Leaving the group ends access at the next check.
func (s agentMessageStore) groupConversationErr(c agentConversation) error {
	a, b := c.Endpoints[0], c.Endpoints[1]
	denied := fmt.Errorf("shared agent unavailable or access denied")
	if a.ShareID != b.ShareID || !a.External || !b.External || a.NamedAgentID == "" || b.NamedAgentID == "" || a.UserID == b.UserID {
		return denied
	}
	g := s.groupByID(groupIDFromShare(a.ShareID))
	if g == nil || !g.hasMember(a.NamedAgentID, a.UserID) || !g.hasMember(b.NamedAgentID, b.UserID) ||
		!s.registeredAgent(a.NamedAgentID, a.UserID) || !s.registeredAgent(b.NamedAgentID, b.UserID) ||
		!shareAccountActive(a.UserID) || !shareAccountActive(b.UserID) {
		return denied
	}
	return nil
}

// groupQuotaErr limits what one person sends into one group.
func (s agentMessageStore) groupQuotaErr(src agentMessageEndpoint, now time.Time) error {
	hourly, pending := 0, 0
	for _, o := range s.Conversations {
		for _, m := range o.Messages {
			from := o.Endpoints[m.From]
			if from.ShareID != src.ShareID || from.UserID != src.UserID {
				continue
			}
			if now.Sub(m.SentAt) < time.Hour {
				hourly++
			}
			if m.Delivery == "pending" || m.Delivery == "dispatching" || m.Delivery == "stored" {
				pending++
			}
		}
	}
	if hourly >= groupSenderHourly || pending >= groupSenderPending {
		return fmt.Errorf("group message limit reached; check back later")
	}
	return nil
}

// suppressGroupAgent marks undelivered group mail to or from an agent that left.
func (s *agentMessageStore) suppressGroupAgent(groupID, agentID string) {
	share := groupShareID(groupID)
	for ci := range s.Conversations {
		c := &s.Conversations[ci]
		if c.Endpoints[0].ShareID != share || (c.Endpoints[0].NamedAgentID != agentID && c.Endpoints[1].NamedAgentID != agentID) {
			continue
		}
		for mi := range c.Messages {
			if m := &c.Messages[mi]; m.Delivery == "stored" || m.Delivery == "pending" || m.Delivery == "dispatching" {
				m.Delivery = "suppressed"
			}
		}
	}
}

// groupItems lists the other people's agents in the viewer's groups for list_agents.
func (s agentMessageStore) groupAgentItems(userID string) []map[string]interface{} {
	items := []map[string]interface{}{}
	seen := map[string]int{}
	label := shareOwnerLabeler()
	mine := map[string]bool{}
	for _, g := range s.Groups {
		for _, m := range g.Members {
			if m.UserID == userID {
				mine[g.ID] = true
			}
		}
	}
	for _, g := range s.Groups {
		if !mine[g.ID] {
			continue
		}
		for _, m := range g.Members {
			if m.UserID == userID {
				continue
			}
			for _, a := range s.Agents {
				if a.ID != m.AgentID || a.UserID != m.UserID {
					continue
				}
				owner := label(a.UserID)
				target := "agent:" + owner + "/" + a.Name
				if i, ok := seen[target]; ok {
					items[i]["groups"] = append(items[i]["groups"].([]string), g.Name)
					continue
				}
				seen[target] = len(items)
				items = append(items, map[string]interface{}{"agent_name": owner + "/" + a.Name, "target": target, "owner": owner, "shared": true, "scope": "group", "groups": []string{g.Name}, "purpose": a.Purpose, "online": namedAgentOnline(a)})
			}
		}
	}
	return items
}

// groupsView is the open directory of groups: every signed-in person sees every
// group's name, purpose and members.
func (s agentMessageStore) groupsView(viewerID string, withMembers bool) []map[string]interface{} {
	label := shareOwnerLabeler()
	out := []map[string]interface{}{}
	for _, g := range s.Groups {
		joined := []string{}
		members := []map[string]interface{}{}
		for _, m := range g.Members {
			for _, a := range s.Agents {
				if a.ID != m.AgentID || a.UserID != m.UserID {
					continue
				}
				if m.UserID == viewerID {
					joined = append(joined, a.Name)
				}
				if withMembers {
					members = append(members, map[string]interface{}{"agent_name": label(a.UserID) + "/" + a.Name, "owner": label(a.UserID), "purpose": a.Purpose, "online": namedAgentOnline(a)})
				}
			}
		}
		row := map[string]interface{}{"name": g.Name, "purpose": g.Purpose, "member_count": len(g.Members), "created_by": label(g.CreatorUserID), "joined_with": joined}
		if withMembers {
			sort.Slice(members, func(i, j int) bool { return members[i]["agent_name"].(string) < members[j]["agent_name"].(string) })
			row["members"] = members
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	return out
}

func groupResult(status int, code, msg string) (map[string]interface{}, int, string, string) {
	return nil, status, code, msg
}

// groupJoin puts one of the caller's agents into a group, creating the group
// when it does not exist and a purpose was given. Joining twice is harmless.
func (api *StreamingAPI) groupJoin(ctx context.Context, ownerID, agentName, groupName, purpose string) (map[string]interface{}, int, string, string) {
	agent, err := normalizeMessageAgentName(agentName)
	group, gerr := normalizeMessageAgentName(groupName)
	if err != nil || gerr != nil {
		return groupResult(http.StatusBadRequest, "invalid_arguments", "A group name is 1 to 64 lowercase letters, digits, dots, underscores or hyphens.")
	}
	purpose = cleanAgentPurpose(strings.TrimSpace(purpose))
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return groupResult(http.StatusServiceUnavailable, "unavailable", "Groups are unavailable. Try again.")
	}
	var me *namedMessageAgent
	for i := range s.Agents {
		if s.Agents[i].UserID == ownerID && s.Agents[i].Name == agent {
			me = &s.Agents[i]
		}
	}
	if me == nil {
		return groupResult(http.StatusNotFound, "unknown_agent", "You have no agent with that name.")
	}
	now := time.Now().UTC()
	index := s.groupIndex(group)
	created := false
	if index < 0 {
		if purpose == "" {
			return groupResult(http.StatusNotFound, "group_not_found", "There is no group named "+group+". Give it a purpose to create it.")
		}
		if len(s.Groups) >= groupsMax {
			return groupResult(http.StatusTooManyRequests, "group_limit", "The server has reached its group limit.")
		}
		s.Groups = append(s.Groups, agentGroup{ID: "group-" + uuid.NewString(), Name: group, Purpose: purpose, CreatorUserID: ownerID, CreatedAt: now})
		index, created = len(s.Groups)-1, true
	}
	g := &s.Groups[index]
	if !g.hasMember(me.ID, ownerID) {
		memberships := 0
		for _, other := range s.Groups {
			if other.hasMember(me.ID, ownerID) {
				memberships++
			}
		}
		if memberships >= groupsPerAgent || len(g.Members) >= groupMembersMax {
			return groupResult(http.StatusTooManyRequests, "group_limit", "That group or this agent has reached its member limit.")
		}
		g.Members = append(g.Members, groupMember{AgentID: me.ID, UserID: ownerID, JoinedAt: now})
		s.auditShare("group_joined", ownerID, ownerID, "", groupShareID(g.ID), me.ID)
		if err := saveAgentMessageStore(ctx, s); err != nil {
			return groupResult(http.StatusServiceUnavailable, "unavailable", "Groups are unavailable. Try again.")
		}
	} else if created {
		_ = saveAgentMessageStore(ctx, s)
	}
	return map[string]interface{}{"name": g.Name, "group": g.Name, "purpose": g.Purpose, "agent_name": me.Name, "state": "joined", "created": created, "member_count": len(g.Members)}, 0, "", ""
}

// removeFromAllGroups is an administrator's unshare: the agent leaves every group.
func (s *agentMessageStore) removeFromAllGroups(agentID, userID, actor string) int {
	n := 0
	kept := s.Groups[:0]
	for _, g := range s.Groups {
		if g.hasMember(agentID, userID) {
			members := g.Members[:0]
			for _, m := range g.Members {
				if !(m.AgentID == agentID && m.UserID == userID) {
					members = append(members, m)
				}
			}
			g.Members = members
			s.suppressGroupAgent(g.ID, agentID)
			s.auditShare("group_left", actor, userID, "", groupShareID(g.ID), agentID)
			n++
		}
		if len(g.Members) > 0 {
			kept = append(kept, g)
		}
	}
	s.Groups = kept
	return n
}

// groupLeave takes an agent out of one group, or out of every group when no name
// is given. Mail not yet delivered through the group is suppressed, and a group
// with no members is removed.
func (api *StreamingAPI) groupLeave(ctx context.Context, ownerID, agentName, groupName string) (map[string]interface{}, int, string, string) {
	agent, err := normalizeMessageAgentName(agentName)
	if err != nil {
		return groupResult(http.StatusBadRequest, "invalid_arguments", "Name one of your agents.")
	}
	group := ""
	if strings.TrimSpace(groupName) != "" {
		if group, err = normalizeMessageAgentName(groupName); err != nil {
			return groupResult(http.StatusBadRequest, "invalid_arguments", "That is not a group name.")
		}
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return groupResult(http.StatusServiceUnavailable, "unavailable", "Groups are unavailable. Try again.")
	}
	var me *namedMessageAgent
	for i := range s.Agents {
		if s.Agents[i].UserID == ownerID && s.Agents[i].Name == agent {
			me = &s.Agents[i]
		}
	}
	if me == nil {
		return groupResult(http.StatusNotFound, "unknown_agent", "You have no agent with that name.")
	}
	left := []string{}
	kept := s.Groups[:0]
	for _, g := range s.Groups {
		if (group == "" || g.Name == group) && g.hasMember(me.ID, ownerID) {
			members := g.Members[:0]
			for _, m := range g.Members {
				if !(m.AgentID == me.ID && m.UserID == ownerID) {
					members = append(members, m)
				}
			}
			g.Members = members
			s.suppressGroupAgent(g.ID, me.ID)
			s.auditShare("group_left", ownerID, ownerID, "", groupShareID(g.ID), me.ID)
			left = append(left, g.Name)
		}
		if len(g.Members) > 0 {
			kept = append(kept, g)
		}
	}
	s.Groups = kept
	if len(left) > 0 {
		if err := saveAgentMessageStore(ctx, s); err != nil {
			return groupResult(http.StatusServiceUnavailable, "unavailable", "Groups are unavailable. Try again.")
		}
	}
	return map[string]interface{}{"agent_name": me.Name, "state": "left", "left": left}, 0, "", ""
}

// migrateEveryoneShares moves the earlier share-with-everyone records into one
// default group, so nothing that was shared stops being reachable.
func (api *StreamingAPI) migrateEveryoneShares(ctx context.Context) {
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	changed := false
	for i := range s.Shares {
		sh := &s.Shares[i]
		if !sh.serverScope() || !sh.live(now) {
			continue
		}
		index := s.groupIndex(defaultGroupName)
		if index < 0 {
			s.Groups = append(s.Groups, agentGroup{ID: "group-" + uuid.NewString(), Name: defaultGroupName, Purpose: defaultGroupPurpose, CreatorUserID: sh.OwnerUserID, CreatedAt: now})
			index = len(s.Groups) - 1
		}
		if g := &s.Groups[index]; !g.hasMember(sh.AgentID, sh.OwnerUserID) {
			g.Members = append(g.Members, groupMember{AgentID: sh.AgentID, UserID: sh.OwnerUserID, JoinedAt: now})
		}
		sh.RevokedAt = now
		sh.RevocationVersion++
		s.suppressShare(sh.ID)
		s.auditShare("share_migrated", sh.OwnerUserID, sh.OwnerUserID, sh.SubjectUserID, sh.ID, sh.AgentID)
		changed = true
	}
	if changed {
		_ = saveAgentMessageStore(ctx, s)
	}
}

// ---- person CLI endpoint: group actions for the person's own agents ----

func (api *StreamingAPI) groupAction(ctx context.Context, ownerID, action, agentName, group, purpose string) (map[string]interface{}, int, string, string) {
	switch action {
	case "share", "join":
		return api.groupJoin(ctx, ownerID, agentName, group, purpose)
	case "unshare", "leave":
		return api.groupLeave(ctx, ownerID, agentName, group)
	case "groups", "group", "list":
		agentMessageMu.Lock()
		s, err := readAgentMessageStore(ctx)
		agentMessageMu.Unlock()
		if err != nil {
			return groupResult(http.StatusServiceUnavailable, "unavailable", "Groups are unavailable. Try again.")
		}
		rows := s.groupsView(ownerID, action != "groups")
		if action == "group" {
			name, err := normalizeMessageAgentName(group)
			if err != nil {
				return groupResult(http.StatusBadRequest, "invalid_arguments", "Name a group.")
			}
			for _, row := range rows {
				if row["name"] == name {
					return row, 0, "", ""
				}
			}
			return groupResult(http.StatusNotFound, "group_not_found", "There is no group named "+name+".")
		}
		return map[string]interface{}{"groups": rows}, 0, "", ""
	}
	return groupResult(http.StatusBadRequest, "invalid_arguments", "Unknown group action.")
}

// ---- MCP messages tool ----

func (api *StreamingAPI) externalGroupAction(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	action := externalArg(args, "action")
	if !namedAgentOperationAllowed(claims, action != "list_groups" && action != "group_members") {
		externalError(w, http.StatusForbidden, "insufficient_scope", "Connection lacks messaging access.")
		return
	}
	agentName := externalArg(args, "agent_name")
	if agentName != "" {
		if _, err := namedAgentForClaims(r.Context(), claims, agentName); err != nil {
			externalCommunicationError(w, http.StatusNotFound, "not_found", err)
			return
		}
	}
	mapped := map[string]string{"join_group": "join", "leave_group": "leave", "list_groups": "groups", "group_members": "group"}[action]
	if (mapped == "join" || mapped == "leave") && agentName == "" {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "agent_name is required: the registered agent that joins or leaves.")
		return
	}
	out, status, code, msg := api.groupAction(r.Context(), claims.UserID, mapped, agentName, externalArg(args, "group"), externalArg(args, "purpose"))
	if code != "" {
		externalError(w, status, code, msg)
		return
	}
	externalJSON(w, out)
}

// externalSendGroup sends one message to every other agent in a group the caller's
// agent belongs to. Each member receives its own copy (cross-person copies are
// marked untrusted like any shared-agent message); the reply lists who got it.
func (api *StreamingAPI) externalSendGroup(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !namedAgentOperationAllowed(claims, true) {
		externalError(w, http.StatusForbidden, "insufficient_scope", "Connection lacks messaging access.")
		return
	}
	groupName, err := normalizeMessageAgentName(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(externalArg(args, "target"))), "group:"))
	caller, callerErr := api.externalMessageCaller(r.Context(), claims, args)
	if err != nil || callerErr != nil || caller.NamedAgentID == "" {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Send to group:NAME from one of your registered agents (agent_name).")
		return
	}
	agentMessageMu.Lock()
	s, readErr := readAgentMessageStore(r.Context())
	agentMessageMu.Unlock()
	if readErr != nil {
		externalError(w, http.StatusServiceUnavailable, "unavailable", "Groups are unavailable. Try again.")
		return
	}
	index := s.groupIndex(groupName)
	if index < 0 || !s.Groups[index].hasMember(caller.NamedAgentID, claims.UserID) {
		externalError(w, http.StatusNotFound, "group_not_found", "Your agent is not a member of a group named "+groupName+". Join it first.")
		return
	}
	g := s.Groups[index]
	message, submission := externalArg(args, "message"), externalArg(args, "submission_id")
	ctx := context.WithValue(context.WithoutCancel(r.Context()), groupTagKey{}, g.Name)
	type receipt struct {
		Agent    string `json:"agent_name"`
		Message  string `json:"message_id,omitempty"`
		Inbox    string `json:"inbox_id,omitempty"`
		Delivery string `json:"delivery,omitempty"`
		Error    string `json:"error,omitempty"`
	}
	label := shareOwnerLabeler()
	var sent, failed []receipt
	for _, m := range g.Members {
		if m.AgentID == caller.NamedAgentID {
			continue
		}
		var member *namedMessageAgent
		for i := range s.Agents {
			if s.Agents[i].ID == m.AgentID && s.Agents[i].UserID == m.UserID {
				member = &s.Agents[i]
			}
		}
		if member == nil {
			continue
		}
		display := member.Name
		target := triggerTarget{NamedAgentID: member.ID, Kind: triggerCallerUser, Label: member.Name}
		if m.UserID != claims.UserID {
			display = label(m.UserID) + "/" + member.Name
			target.ShareOwnerUserID, target.ShareID, target.ShareVersion = m.UserID, groupShareID(g.ID), 1
		}
		key := ""
		if submission != "" {
			key = submission + ":" + member.ID
		}
		out, sendErr := api.sendAgentMessage(ctx, claims.UserID, caller, target, message, "", key)
		if sendErr != nil {
			failed = append(failed, receipt{Agent: display, Error: sendErr.Error()})
			continue
		}
		sent = append(sent, receipt{Agent: display, Message: fmt.Sprint(out["message_id"]), Inbox: fmt.Sprint(out["inbox_id"]), Delivery: fmt.Sprint(out["delivery"])})
	}
	raw, _ := json.Marshal(map[string]any{"group": g.Name, "sent_to": sent, "failed": failed, "note": "Accepted means stored for each member; read replies with messages action=read."})
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	externalJSON(w, out)
}

// groupTagKey carries the group name from a fan-out send to the stored message.
type groupTagKey struct{}

// handleBullpenGroups is the browser's read-only group directory.
func (api *StreamingAPI) handleBullpenGroups(w http.ResponseWriter, r *http.Request) {
	claims := bullpenBrowserClaims(w, r, false)
	if claims == nil {
		return
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(r.Context())
	agentMessageMu.Unlock()
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "unavailable", "Groups are unavailable. Try again.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]interface{}{"groups": s.groupsView(claims.UserID, true)})
}

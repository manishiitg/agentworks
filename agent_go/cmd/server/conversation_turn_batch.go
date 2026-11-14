package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Only ordinary ready person/agent deliveries combine. Explicit live steering,
// restart continuations and quota retries keep their own dispatch semantics.
func (api *StreamingAPI) queuedBatchEligible(turn queuedConversationTurn) bool {
	if turn.StartedAt != nil || turn.Steering || turn.DeliveryError != "" || turn.RetryAt != nil || (turn.Request.TriggeredBy == "restart_continue" || turn.Request.TriggeredBy == usageLimitContinueTrigger) {
		return false
	}
	if !personQueuedRequest(turn) && turn.Principal.DeliveredMessage["source"] != "agent_message" {
		return false
	}
	for _, attachment := range turn.Request.PendingAttachments {
		item, err := api.pendingChatUpload(pendingAttachmentKey(turn.UserID, turn.SessionID, turn.Request, attachment.Hash))
		if err != nil {
			return false
		}
		select {
		case <-item.Ready:
			if item.Path == "" || item.Error != "" {
				return false
			}
		default:
			return false
		}

	}
	return true
}

func queuedBatchScope(turn queuedConversationTurn) string {
	principal := turn.Principal
	principal.DeliveredMessage = nil
	principal.DeliveredMessageSession, principal.DeliveredMessageUser = "", ""
	principal.CostAttribution = queuedConversationPrincipal{}.CostAttribution
	request := sanitizedQueuedConversationRequest(context.Background(), turn.Request)
	replyFence := ""
	if desktopAppTurnRestricted(request, turn.UserID) {
		replyFence, _ = turn.Principal.DeliveredMessage["app_agent_reply_to"].(string)
	}
	// Unrestricted owner sessions and trusted owner deliveries have the same
	// authority, although their authenticated transport names differ.
	if !desktopAppTurnRestricted(request, turn.UserID) && principal.AccessTokenID == "" && !principal.SlackTrustedApp && principal.BotRouteGrant == "" && principal.CrewAskInput == nil {
		principal.Provider = ""
	}
	request.Query, request.Message = "", ""
	request.PendingAttachments = nil
	request.AgentMessageInbox, request.TriggeredBy, request.TriggeredByLabel = "", "", ""
	request.IsAutoNotification = false
	request.DeliveryMode = ""
	request.DisableLiveInputDelivery = false
	request.CostSourceID, request.CostSourceLabel, request.CostSourceRunID = "", "", ""
	raw, err := json.Marshal(struct {
		User       string
		Principal  queuedConversationPrincipal
		Request    QueryRequest
		ReplyFence string
	}{turn.UserID, principal, request, replyFence})
	if err != nil {
		return ""
	}
	return string(raw)
}

// The caller holds the owner's durable queue lock. All selected source rows
// are claimed in one write; they remain separate records for receipts/Stop and
// restart recovery. A different principal/runtime is an ordering boundary.
func (api *StreamingAPI) claimQueuedBatch(turns []queuedConversationTurn, head queuedConversationTurn, otherOwnerHead *queuedConversationTurn, now time.Time) (queuedConversationTurn, []queuedConversationTurn) {
	var ordered []int
	for i := range turns {
		if turns[i].UserID == head.UserID && turns[i].SessionID == head.SessionID && turns[i].StartedAt == nil && turns[i].DeliveryError == "" {
			ordered = append(ordered, i)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := turns[ordered[i]], turns[ordered[j]]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	scope := queuedBatchScope(head)
	canCombine := api.queuedBatchEligible(head) && scope != ""
	started := false
	var members []queuedConversationTurn
	for _, i := range ordered {
		candidate := turns[i]
		if !started {
			if candidate.ID != head.ID {
				continue
			}
			started = true
		} else {
			if !canCombine || !api.queuedBatchEligible(candidate) || queuedBatchScope(candidate) != scope {
				break
			}
			if otherOwnerHead != nil && (!candidate.CreatedAt.Before(otherOwnerHead.CreatedAt)) {
				break
			}
		}
		turns[i].StartedAt = &now
		turns[i].RetryAt = nil
		turns[i].BatchID = head.ID
		members = append(members, turns[i])
	}
	if len(members) == 0 {
		return queuedConversationTurn{}, turns
	}
	claimed := members[0]
	if len(members) > 1 {
		claimed.Batch = members
	}
	return claimed, turns
}

func queuedBatchMembers(turn queuedConversationTurn) []queuedConversationTurn {
	if len(turn.Batch) > 0 {
		return turn.Batch
	}
	return []queuedConversationTurn{turn}
}

func queuedMessageAge(age time.Duration) string {
	minutes := int(age / time.Minute)
	if minutes < 1 {
		return "less than 1 min"
	}
	if minutes < 60 {
		return fmt.Sprintf("%d min", minutes)
	}
	if minutes%60 == 0 {
		return fmt.Sprintf("%d h", minutes/60)
	}
	return fmt.Sprintf("%d h %d min", minutes/60, minutes%60)
}

// Resolve each admitted row's attachments and preserve its original reply
// instructions. Agent messaging still replies explicitly, never auto-forwards
// one combined answer to unrelated inboxes.
func (api *StreamingAPI) prepareQueuedBatch(ctx context.Context, turn queuedConversationTurn, now time.Time) (queuedConversationTurn, error) {
	if len(turn.Batch) < 2 {
		return turn, nil
	}
	var text strings.Builder
	text.WriteString("[Messages received while you were working — oldest first]\nRead all of these together before deciding what to do. Later messages may update earlier requests. Each agent message keeps its own reply instructions.\n")
	ids := []string{}
	allAutomatic := true
	hasPerson := false
	inbox := ""
	for _, member := range turn.Batch {
		request := member.Request
		args, err := queryRequestToMap(request)
		if err != nil {
			return turn, err
		}
		principal, err := api.queuedConversationTurnContext(member, args)
		if err != nil {
			return turn, err
		}
		if err := api.validatePendingChatAttachments(member.UserID, member.SessionID, request); err != nil {
			return turn, err
		}
		if err := api.resolvePendingChatAttachments(principal, member.UserID, member.SessionID, &request); err != nil {
			return turn, err
		}
		sender := "you"
		if member.Principal.DeliveredMessage["source"] == "agent_message" {
			if label, ok := member.Principal.DeliveredMessage["sender_label"].(string); ok && label != "" {
				sender = strings.ReplaceAll(strings.ReplaceAll(label, "\n", " "), "\r", " ")
			}
		}
		fmt.Fprintf(&text, "\n%s (%s ago), from %s:\n%s\n", member.CreatedAt.Format("2006-01-02 15:04 MST"), queuedMessageAge(now.Sub(member.CreatedAt)), sender, request.Query)
		if id := queuedTurnDisplayID(member); id != "" {
			ids = append(ids, id)
		}
		if personQueuedRequest(member) {
			hasPerson = true
		}
		if !request.IsAutoNotification {
			allAutomatic = false
		}
		if inbox == "" {
			inbox = request.AgentMessageInbox
		}
	}
	turn.Request.Query, turn.Request.Message = text.String(), ""
	turn.Request.PendingAttachments = nil
	turn.Request.IsAutoNotification = allAutomatic
	turn.Request.AgentMessageInbox = inbox
	if hasPerson {
		turn.Request.AgentMessageInbox = ""
	}
	turn.Principal.DeliveredMessage = map[string]interface{}{"source": "queued_batch", "client_message_id": queuedTurnDisplayID(turn), "combined_client_message_ids": ids, "combined_person_input": hasPerson, "display_content": text.String()}
	if desktopAppTurnRestricted(turn.Request, turn.UserID) {
		turn.Principal.DeliveredMessage["app_agent_reply_to"] = turn.Batch[0].Principal.DeliveredMessage["app_agent_reply_to"]
	}
	turn.Principal.DeliveredMessageUser, turn.Principal.DeliveredMessageSession = turn.UserID, turn.SessionID
	return turn, nil
}

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// An administrator can close one attachment (the owner is told and the change is
// audited), a repeat reports already closed, a live name cannot be removed, a
// stale one can, and a change without a reason or past the rate limit is refused.
func TestBullpenAdminClosesAttachmentAndRemovesStaleName(t *testing.T) {
	newTriggerLinkEnv(t) // a workspace API for the message store
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"root","username":"root","admin":true,"products":[]}]}`)
	api := &StreamingAPI{}
	ctx := context.Background()
	live := namedMessageAgent{ID: "agent-live", UserID: "alice", Name: "helper", Attachment: &namedAgentAttachment{DeliveryContract: 2, ID: "attach-1", Provider: "claude", SessionID: "native-session-secret"}}
	stale := namedMessageAgent{ID: "agent-stale", UserID: "alice", Name: "old"}
	if err := saveAgentMessageStore(ctx, agentMessageStore{Agents: []namedMessageAgent{live, stale}}); err != nil {
		t.Fatal(err)
	}
	listed, _, code, _ := api.bullpenAdminAct(ctx, "root", "list", map[string]any{})
	if code != "" || strings.Contains(strings.ToLower(toJSON(listed)), "native-session-secret") || strings.Contains(toJSON(listed), "attach-1") {
		t.Fatalf("list failed or leaked a session or lease id: %s", toJSON(listed))
	}
	handle := attachmentHandle(live.ID, live.Attachment.ID)
	if _, _, code, _ = api.bullpenAdminAct(ctx, "root", "close_attachment", map[string]any{"handle": handle}); code != "reason_required" {
		t.Fatalf("change without a reason was allowed: %q", code)
	}
	if out, _, code, msg := api.bullpenAdminAct(ctx, "root", "close_attachment", map[string]any{"handle": handle, "reason": "stuck listener"}); code != "" || out["state"] != "closed" {
		t.Fatalf("close failed: %v %s %s", out, code, msg)
	}
	if out, _, _, _ := api.bullpenAdminAct(ctx, "root", "close_attachment", map[string]any{"handle": handle, "reason": "again"}); out["state"] != "already_closed" {
		t.Fatalf("repeat close not idempotent: %v", out)
	}
	agentMessageMu.Lock()
	s, _ := readAgentMessageStore(ctx)
	agentMessageMu.Unlock()
	if s.Agents[0].Attachment != nil || len(s.Notices) != 1 || s.Notices[0].OwnerUserID != "alice" || len(s.AdminAudit) != 2 || s.AdminAudit[0].Reason != "stuck listener" || s.AdminAudit[0].Admin != "root" {
		t.Fatalf("close not fully recorded: %+v", s)
	}
	if out, _, code, _ := api.bullpenAdminAct(ctx, "root", "remove_agent", map[string]any{"agent_id": "agent-stale", "reason": "unused name"}); code != "" || out["state"] != "removed" {
		t.Fatalf("stale name not removed: %v %s", out, code)
	}
	agentLiveEvents.Lock()
	agentLiveEvents.online["agent-live"] = "attach-x"
	agentLiveEvents.Unlock()
	defer func() { agentLiveEvents.Lock(); delete(agentLiveEvents.online, "agent-live"); agentLiveEvents.Unlock() }()
	agentMessageMu.Lock()
	s, _ = readAgentMessageStore(ctx)
	s.Agents[0].Attachment = &namedAgentAttachment{DeliveryContract: 2, ID: "attach-x", Provider: "claude"}
	_ = saveAgentMessageStore(ctx, s)
	agentMessageMu.Unlock()
	if _, _, code, _ := api.bullpenAdminAct(ctx, "root", "remove_agent", map[string]any{"agent_id": "agent-live", "reason": "should refuse"}); code != "agent_live" {
		t.Fatalf("live name was removed: %q", code)
	}
	now := time.Now()
	for i := 0; i < bullpenAdminRateMax; i++ {
		bullpenAdminRateOK("flood", now)
	}
	if _, status, code, _ := api.bullpenAdminAct(ctx, "flood", "remove_agent", map[string]any{"agent_id": "none", "reason": "flooding"}); code != "rate_limited" || status != 429 {
		t.Fatalf("rate limit not applied: %d %q", status, code)
	}
	if bullpenAdminToolAllowed(&UserClaims{UserID: "alice"}) {
		t.Fatal("a non-administrator can see the admin tool")
	}
}

func toJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

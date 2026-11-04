package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func mailboxRowSequences(t *testing.T, inbox string) []int {
	t.Helper()
	mb, err := currentAgentMailbox()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := mb.db.Query(`SELECT sequence FROM messages WHERE inbox_id=? ORDER BY ord`, inbox)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

// PLAT-856-522: concurrent sends into one conversation get consecutive
// sequence numbers in send order; a write whose sequence does not follow the
// stored rows is refused inside the transaction; only the conversation's own
// endpoints get its rows back from the database.
func TestAgentMailboxConcurrentSendsKeepConversationOrder(t *testing.T) {
	newTriggerLinkEnv(t)
	api := &StreamingAPI{}
	ctx := internalBotRequestContext(context.Background(), "owner")
	owner := agentMessageEndpoint{UserID: "owner", Kind: triggerCallerUser, ID: "owner", External: true, Label: "Owner agent"}
	helper := agentMessageEndpoint{UserID: "owner", Kind: triggerCallerUser, ID: "helper", External: true, Label: "Helper"}
	if err := saveAgentMessageStore(ctx, agentMessageStore{Conversations: []agentConversation{{ID: "inbox-order", Endpoints: [2]agentMessageEndpoint{owner, helper}}}}); err != nil {
		t.Fatal(err)
	}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: "owner"}, Label: "Owner agent"}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := api.sendAgentMessage(ctx, "owner", caller, triggerTarget{}, fmt.Sprintf("note %d", i), "inbox-order", fmt.Sprintf("send-%d", i))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got := mailboxRowSequences(t, "inbox-order")
	if len(got) != 20 {
		t.Fatalf("stored %d messages, want 20", len(got))
	}
	for i, n := range got {
		if n != i+1 {
			t.Fatalf("sequence order %v", got)
		}
	}
	// A retried submission returns the original message, not a new row.
	again, err := api.sendAgentMessage(ctx, "owner", caller, triggerTarget{}, "note 3", "inbox-order", "send-3")
	if err != nil || len(mailboxRowSequences(t, "inbox-order")) != 20 {
		t.Fatalf("retry duplicated: %v %v", again, err)
	}

	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err == nil {
		c := &s.Conversations[0]
		c.Messages = append(c.Messages, agentMessage{ID: "msg-stale", Sequence: 5, Text: "stale", Delivery: "stored"})
		err = saveAgentMessageStore(ctx, s)
	}
	agentMessageMu.Unlock()
	if err == nil {
		t.Fatal("a message out of conversation order was stored")
	}

	if _, _, found, err := callerAgentMailboxConversation(ctx, "inbox-order", "other", triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: "other"}}); err != nil || found {
		t.Fatalf("another account's caller got the inbox: %v %v", found, err)
	}
	if c, side, found, err := callerAgentMailboxConversation(ctx, "inbox-order", "owner", caller); err != nil || !found || side != 0 || len(c.Messages) != 20 {
		t.Fatalf("endpoint read: %v %d %d %v", found, side, len(c.Messages), err)
	}
}

// PLAT-856-522: a receipt only moves forward. A later write that would turn a
// delivered message back to pending keeps the delivered state and time.
func TestAgentMailboxReceiptNeverMovesBack(t *testing.T) {
	newTriggerLinkEnv(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	ends := [2]agentMessageEndpoint{{UserID: "owner", Kind: triggerCallerUser, ID: "owner", External: true}, {UserID: "owner", Kind: triggerCallerUser, ID: "helper", External: true}}
	s := agentMessageStore{Conversations: []agentConversation{{ID: "inbox-receipt", Endpoints: ends, Next: 1, Messages: []agentMessage{{ID: "msg-1", Sequence: 1, Text: "hi", Delivery: "delivered", DeliveredAt: at}}}}}
	if err := saveAgentMessageStore(ctx, s); err != nil {
		t.Fatal(err)
	}
	late, _ := readAgentMessageStore(ctx)
	late.Conversations[0].Messages[0].Delivery = "pending"
	late.Conversations[0].Messages[0].DeliveredAt = time.Time{}
	late.Conversations[0].Messages[0].LiveTransport = "mailbox"
	if err := saveAgentMessageStore(ctx, late); err != nil {
		t.Fatal(err)
	}
	mb, _ := currentAgentMailbox()
	var delivery, data string
	if err := mb.db.QueryRow(`SELECT delivery, data FROM messages WHERE message_id='msg-1'`).Scan(&delivery, &data); err != nil {
		t.Fatal(err)
	}
	var stored agentMessage
	_ = json.Unmarshal([]byte(data), &stored)
	if delivery != "delivered" || !stored.DeliveredAt.Equal(at) || stored.LiveTransport != "mailbox" {
		t.Fatalf("receipt moved back: %s %+v", delivery, stored)
	}
	snapshot, _ := readAgentMessageSnapshot(ctx)
	if m := snapshot.Conversations[0].Messages[0]; m.Delivery != "delivered" || !m.DeliveredAt.Equal(at) {
		t.Fatalf("committed image moved back: %+v", m)
	}
}

// PLAT-856-522: the one-off import keeps every message, receipt and cursor of
// the split JSON mailbox, renames (never deletes) the JSON, is safe to rerun,
// and refuses data whose order it would change.
func TestAgentMailboxImportKeepsEveryMessage(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	ends := [2]agentMessageEndpoint{{UserID: "owner", Kind: triggerCallerUser, ID: "owner", External: true, NamedAgentID: "agent-a"}, {UserID: "owner", Kind: triggerCallerUser, ID: "owner", External: true, NamedAgentID: "agent-b"}}
	conversations := []agentConversation{
		{ID: "inbox-1", Endpoints: ends, Next: 7, Messages: []agentMessage{
			{ID: "msg-5", Sequence: 5, AgentSequence: 40, From: 0, Text: "first", SentAt: at, Delivery: "delivered", DeliveredAt: at.Add(time.Minute), Submission: "s-1"},
			{ID: "msg-7", Sequence: 7, AgentSequence: 41, From: 1, Text: "second", SentAt: at.Add(time.Hour), Delivery: "stored", File: &agentMessageFileReference{ID: "file-1", Name: "a.txt", Size: 3}},
		}},
		{ID: "inbox-2", Endpoints: ends, Next: 1, Messages: []agentMessage{{ID: "msg-x", Sequence: 1, AgentSequence: 42, Text: "third", SentAt: at, Delivery: "expired", Error: "cleared"}}},
	}
	history, _ := json.Marshal(conversations)
	meta, _ := json.Marshal(map[string]any{
		"mailbox_storage": "history-v1", "next_agent_sequence": 40,
		"agents":  []namedMessageAgent{{ID: "agent-a", UserID: "owner", Name: "a", CreatedAt: at, LastOnlineAt: at.Add(2 * time.Hour), Attachment: &namedAgentAttachment{ID: "attach-1", Provider: "codex"}}},
		"expired": []expiredAgentConversation{{ID: "inbox-old", Endpoints: ends, ExpiredAt: at}},
		"shares":  []agentShare{{ID: "share-1", OwnerUserID: "owner", AgentID: "agent-a", SubjectUserID: "other", CreatedAt: at}},
	})
	write := func(name string, raw []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(agentMailboxLegacyFile, meta)
	write(agentMailboxLegacyHistory, history)

	dry, err := importAgentMailboxJSON(dir, true)
	if err != nil || dry.Conversations != 2 || dry.Messages != 3 || dry.Delivered != 1 || dry.Agents != 1 || dry.Bytes != int64(len(meta)+len(history)) {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	if _, err := os.Stat(filepath.Join(dir, agentMailboxDBName)); err == nil {
		t.Fatal("dry run wrote the database")
	}
	report, err := importAgentMailboxJSON(dir, false)
	if err != nil || !report.Imported || len(report.Renamed) != 2 {
		t.Fatalf("import: %+v %v", report, err)
	}
	for _, name := range report.Renamed {
		if !strings.Contains(name, ".imported-") {
			t.Fatalf("renamed to %s", name)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal("JSON file was not kept")
		}
	}
	mb, err := openAgentMailboxFile(filepath.Join(dir, agentMailboxDBName), true)
	if err != nil {
		t.Fatal(err)
	}
	defer mb.db.Close()
	got := mb.image.store
	// The agent cursor is the larger of the stored counter and the messages.
	if got.NextAgentSequence != 42 || len(got.Conversations) != 2 || got.Conversations[0].Next != 7 || len(got.Conversations[0].Messages) != 2 {
		t.Fatalf("cursors or counts changed: %+v", got)
	}
	want, _ := json.Marshal(conversations[0].Messages[1])
	sample, _ := json.Marshal(got.Conversations[0].Messages[1])
	if string(want) != string(sample) {
		t.Fatalf("sampled message changed:\n%s\n%s", want, sample)
	}
	if m := got.Conversations[0].Messages[0]; m.Delivery != "delivered" || !m.DeliveredAt.Equal(at.Add(time.Minute)) || m.AgentSequence != 40 {
		t.Fatalf("receipt changed: %+v", m)
	}
	if len(got.Agents) != 1 || !got.Agents[0].LastOnlineAt.Equal(at.Add(2*time.Hour)) || got.Agents[0].Attachment == nil || len(got.Expired) != 1 || len(got.Shares) != 1 {
		t.Fatalf("metadata changed: %+v", got)
	}
	// The deploy's pre-activation dry run passes once the database exists.
	again, err := importAgentMailboxJSON(dir, true)
	if err != nil || again.Imported || again.Reason != "already imported" {
		t.Fatalf("rerun: %+v %v", again, err)
	}

	// A repeated sequence would be renumbered or dropped: refuse the import.
	bad := t.TempDir()
	conversations[1].Messages = append(conversations[1].Messages, agentMessage{ID: "msg-y", Sequence: 1})
	history, _ = json.Marshal(conversations)
	for name, raw := range map[string][]byte{agentMailboxLegacyFile: meta, agentMailboxLegacyHistory: history} {
		if err := os.WriteFile(filepath.Join(bad, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := importAgentMailboxJSON(bad, false); err == nil {
		t.Fatal("import accepted a repeated sequence")
	}
	if _, err := os.Stat(filepath.Join(bad, agentMailboxDBName)); err == nil {
		t.Fatal("a refused import left a database")
	}
	if _, err := os.Stat(filepath.Join(bad, agentMailboxLegacyFile)); err != nil {
		t.Fatal("a refused import moved the JSON")
	}
}

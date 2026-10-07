package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// PLAT-648: only a Code's main chat and side chats are siblings (never its
// isolated trigger/schedule chats or another Code), and an ask between them
// is a function call: it gets a call_id and a saved record, only the target
// chat may answer it, the shared chain guard stops it coming back, and it
// settles with the target's return_function_result.
func TestCodeChatAskIsAFunctionCall(t *testing.T) {
	files := map[string]string{}
	store := productConversationRegistryStore{
		read: func(_ context.Context, path string) (string, bool, error) {
			raw, ok := files[path]
			return raw, ok, nil
		},
		write: func(_ context.Context, path, content string) error { files[path] = content; return nil },
		now:   time.Now, newID: func() string { return "x" },
	}
	entries := map[string]ProductConversationRecord{}
	for _, key := range []string{"p1", "p1:chat:aa", "p1:chat:bb", "p1:trigger:t1", "p1:chat:aa:x", "p2", "p2:chat:cc"} {
		entries["code/"+key] = ProductConversationRecord{ConversationKey: key, SessionID: "s-" + key}
	}
	entries["work/p1:chat:zz"] = ProductConversationRecord{ConversationKey: "p1:chat:zz", SessionID: "s-work"}
	raw, _ := json.Marshal(productConversationRegistryDocument{Version: 1, Entries: entries})
	files[productConversationRegistryPath("u1")] = string(raw)
	chats, err := store.projectChats(context.Background(), "u1", "code", "p1")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, chat := range chats {
		keys = append(keys, chat.ConversationKey)
	}
	sort.Strings(keys)
	if got := strings.Join(keys, ","); got != "p1,p1:chat:aa,p1:chat:bb" {
		t.Fatalf("siblings = %s", got)
	}

	env := newCrewFunctionEnv(t)
	const codePath = "_users/owner/Chats/Code/projects/private-alpha"
	env.mock.mu.Lock()
	env.mock.files[codePath+"/product.json"] = `{"schema_version":1,"product":"code","id":"alpha","title":"Private project","session_id":"code-alpha"}`
	env.mock.mu.Unlock()
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	base, err := crewTriggerLinkCaller(codePath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	main := codeChat{Key: "alpha", ID: "main", Name: "Main chat", SessionID: "s-main"}
	side := codeChat{Key: "alpha:chat:aa", ID: "aa", Name: "Chat 2", SessionID: "s-aa"}
	project := func(self codeChat) codeChatProject {
		return codeChatProject{OwnerID: "owner", ProjectID: "alpha", Workspace: codePath, Chats: []codeChat{main, side}, Self: self}
	}
	if _, err := project(main).find("p2"); err == nil {
		t.Fatal("a chat outside this Code was found")
	}
	ask := func(from, to codeChat, submissionID string) (*crewFunctionCall, error) {
		return env.api.startCrewFunctionCall(ctx, "owner", codeChatCaller(project(from), base), codeChatTarget(project(from), to, codePath),
			defaultAskCrewFunction(), map[string]interface{}{"message": "What changed?", "reply": true}, time.Minute, submissionID)
	}
	if _, err := ask(main, main, ""); err == nil {
		t.Fatal("a chat asked itself")
	}
	// A Code's ordinary call_function caller (no chat) cannot reach a chat.
	if _, err := env.api.startCrewFunctionCall(ctx, "owner", base, codeChatTarget(project(main), side, codePath), defaultAskCrewFunction(), map[string]interface{}{"message": "x"}, time.Minute); err == nil {
		t.Fatal("a non-chat caller reached a chat")
	}

	toolsFor := func(sessionID string) map[string]recordedTool {
		reg := &recordingRegistrar{}
		if err := env.api.registerCrewFunctionTools(reg, "owner", sessionID, QueryRequest{SelectedFolder: codePath}, crewTriggerLinkCaller(codePath), nil); err != nil {
			t.Fatal(err)
		}
		return reg.tools
	}
	mainTools, sideTools := toolsFor(main.SessionID), toolsFor(side.SessionID)
	previous := codeChatTurn
	t.Cleanup(func() { codeChatTurn = previous })
	turnErrs := make(chan error, 1)
	codeChatTurn = func(_ context.Context, call *crewFunctionCall, text string) (internalSessionTurnResult, error) {
		turnErrs <- func() error {
			if !strings.Contains(text, `return_function_result(call_id="`+call.ID+`"`) {
				return fmt.Errorf("callee did not get the call instructions: %s", text)
			}
			if _, err := ask(side, main, ""); err == nil || !strings.Contains(err.Error(), "already took part") {
				return fmt.Errorf("ask back to the sender allowed: %v", err)
			}
			if _, err := mainTools["return_function_result"].exec(ctx, map[string]interface{}{"call_id": call.ID, "result": map[string]interface{}{"answer": "forged"}}); err == nil {
				return fmt.Errorf("the sending chat answered its own call")
			}
			if _, err := sideTools["report_function_progress"].exec(ctx, map[string]interface{}{"call_id": call.ID, "message": "reading"}); err != nil {
				return err
			}
			_, err := sideTools["return_function_result"].exec(ctx, map[string]interface{}{"call_id": call.ID, "result": map[string]interface{}{"answer": "the parser"}})
			return err
		}()
		return internalSessionTurnResult{FinalResponse: "chat text, not the result"}, nil
	}

	call, err := ask(main, side, "sub-1")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-call.done:
	case <-time.After(5 * time.Second):
		t.Fatal("call did not settle")
	}
	if err := <-turnErrs; err != nil {
		t.Fatal(err)
	}
	snapshot := call.snapshot()
	result, _ := snapshot["result"].(map[string]interface{})
	if !strings.HasPrefix(call.ID, "fn-") || snapshot["status"] != "completed" || result["answer"] != "the parser" || len(call.Progress) != 1 {
		t.Fatalf("settled call = %+v", snapshot)
	}
	env.mock.mu.Lock()
	_, indexed := env.mock.files[crewFunctionCallIndexPath(call.ID)]
	env.mock.mu.Unlock()
	if !indexed {
		t.Fatal("call index not saved")
	}
	if out, err := mainTools["get_function_call"].exec(ctx, map[string]interface{}{"call_id": call.ID}); err != nil || !strings.Contains(out, "the parser") {
		t.Fatalf("sender cannot read its call: %s, %v", out, err)
	}
	if again, err := ask(main, side, "sub-1"); err != nil || again.ID != call.ID {
		t.Fatalf("submission_id did not return the original call: %v, %v", again, err)
	}
}

// Owner, 2026-10-07: at most 20 asks an hour between one Code's chats. A
// resubmitted or joined ask (the same call) counts once.
func TestCodeChatAskCapPerHour(t *testing.T) {
	project := codeChatProject{OwnerID: "owner", ProjectID: "cap-test"}
	now := time.Now()
	for i := 0; i < codeChatAsksPerHour; i++ {
		if err := admitCodeChatAsk(project, now); err != nil {
			t.Fatalf("ask %d refused: %v", i+1, err)
		}
		id := "fn-" + strconv.Itoa(i)
		recordCodeChatAsk(project, id, now)
		recordCodeChatAsk(project, id, now) // resubmitted: the same call
	}
	if err := admitCodeChatAsk(project, now); err == nil {
		t.Fatal("ask 21 within the hour was admitted")
	}
	if err := admitCodeChatAsk(codeChatProject{OwnerID: "owner", ProjectID: "other"}, now); err != nil {
		t.Fatalf("another Code shares the cap: %v", err)
	}
	if err := admitCodeChatAsk(project, now.Add(time.Hour+time.Second)); err != nil {
		t.Fatalf("cap did not clear after an hour: %v", err)
	}
}

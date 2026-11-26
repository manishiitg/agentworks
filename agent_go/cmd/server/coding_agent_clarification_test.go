package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func clarificationTestArgs() map[string]interface{} {
	return map[string]interface{}{"questions": []map[string]interface{}{
		{"id": "scope", "question": "Choose scope", "options": []map[string]interface{}{{"label": "Sheets"}, {"label": "Gmail"}}},
		{"id": "purpose", "question": "Choose purpose", "options": []map[string]interface{}{{"label": "Read"}, {"label": "Send"}}},
	}}
}

func TestCodingAgentClarificationCustomAnswersAndBounds(t *testing.T) {
	q := unifiedevents.CodingAgentQuestionPrompt{ID: "one", Question: "Choose", AllowOther: true, Options: []unifiedevents.CodingAgentQuestionOption{{Label: "A"}, {Label: "B"}}}
	custom := []codingAgentQuestionAnswerInput{{ID: "one", OtherText: "  Custom choice  "}}
	answers, err := clarificationAnswers([]unifiedevents.CodingAgentQuestionPrompt{q}, false, custom)
	if err != nil || answers[0].OtherText != "Custom choice" || len(answers[0].SelectedLabels) != 0 {
		t.Fatalf("custom answer not retained: %v %v", answers, err)
	}
	q.AllowOther = false
	if _, err := clarificationAnswers([]unifiedevents.CodingAgentQuestionPrompt{q}, false, custom); err == nil {
		t.Fatal("custom answer accepted by a question without Other")
	}
	q.AllowOther = true
	custom[0].SelectedLabels = []string{"A"}
	if _, err := clarificationAnswers([]unifiedevents.CodingAgentQuestionPrompt{q}, false, custom); err == nil {
		t.Fatal("single choice accepted option and custom answer together")
	}
	q.MultiSelect, q.MaxSelections = true, 2
	if answers, err := clarificationAnswers([]unifiedevents.CodingAgentQuestionPrompt{q}, false, custom); err != nil || answers[0].SelectedLabels[0] != "A" || answers[0].OtherText != "Custom choice" {
		t.Fatalf("multi-select custom answer: %v %v", answers, err)
	}
	q.MaxSelections = 1
	if _, err := clarificationAnswers([]unifiedevents.CodingAgentQuestionPrompt{q}, false, custom); err == nil {
		t.Fatal("custom answer was not counted towards the maximum")
	}
	// PLAT-872-21: native input/editor dialogs have no preset choices.
	textOnly, err := parseCodingAgentClarification(map[string]interface{}{"questions": []map[string]interface{}{
		{"id": "input", "question": "Describe the change", "options": []map[string]interface{}{}, "allow_other": true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clarificationAnswers(textOnly, true, nil); err == nil {
		t.Fatal("first-options action accepted a text-only question")
	}
	answers, err = clarificationAnswers(textOnly, false, []codingAgentQuestionAnswerInput{{ID: "input", OtherText: "line one\nline two"}})
	if err != nil || answers[0].OtherText != "line one\nline two" {
		t.Fatalf("text-only answer: %v %v", answers, err)
	}

}

func pendingClarificationForTest(t *testing.T, api *StreamingAPI, session string) *pendingCodingAgentClarification {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		api.codingAgentClarificationsMu.Lock()
		p := api.codingAgentClarifications[session]
		api.codingAgentClarificationsMu.Unlock()
		if p != nil {
			return p
		}
		select {
		case <-deadline:
			t.Fatal("clarification did not become pending")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestCodingAgentClarificationTwoQuestionsAndSuccessivePrompts(t *testing.T) {
	for _, provider := range []string{"muse-cli", "claude-code", "codex-cli", "cursor-cli", "pi-cli"} {
		t.Run(provider, func(t *testing.T) {
			api := &StreamingAPI{eventStore: events.NewEventStore(100)}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var previous string
			for turn := 0; turn < 2; turn++ {
				result := make(chan string, 1)
				go func() {
					out, err := api.waitForCodingAgentClarification(ctx, "session", provider, clarificationTestArgs())
					if err != nil {
						out = err.Error()
					}
					result <- out
				}()
				p := pendingClarificationForTest(t, api, "session")
				if p.promptID == previous {
					t.Fatal("successive prompts reused an id")
				}
				if err := api.submitCodingAgentClarification("other-session", provider, p.promptID, true, nil); err == nil {
					t.Fatal("another session could answer")
				}
				if err := api.submitCodingAgentClarification("session", "other-provider", p.promptID, true, nil); err == nil {
					t.Fatal("another provider could answer")
				}
				if err := api.submitCodingAgentClarification("session", provider, previous, true, nil); err == nil {
					t.Fatal("an old prompt could answer the new one")
				}
				if err := api.submitCodingAgentClarification("session", provider, p.promptID, false, []codingAgentQuestionAnswerInput{{ID: "scope", SelectedLabels: []string{"Gmail"}}}); err == nil {
					t.Fatal("partial answers accepted")
				}
				input := []codingAgentQuestionAnswerInput{{ID: "scope", SelectedLabels: []string{"Gmail"}}, {ID: "purpose", SelectedLabels: []string{"Read"}}}
				if err := api.submitCodingAgentClarification("session", provider, p.promptID, false, input); err != nil {
					t.Fatal(err)
				}
				if err := api.submitCodingAgentClarification("session", provider, p.promptID, false, input); err == nil {
					t.Fatal("duplicate submission accepted")
				}
				select {
				case out := <-result:
					var data struct {
						Status  string
						Answers []unifiedevents.CodingAgentQuestionAnswer
					}
					if json.Unmarshal([]byte(out), &data) != nil || data.Status != "answered" || len(data.Answers) != 2 || data.Answers[0].SelectedLabels[0] != "Gmail" {
						t.Fatalf("result: %s", out)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				previous = p.promptID
			}
			rows := api.eventStore.GetAllEventsRaw("session")
			if len(rows) != 4 {
				t.Fatalf("expected two request/settlement pairs, got %d", len(rows))
			}
			for i, row := range rows {
				payload := row.Data.Data.(*unifiedevents.CodingAgentQuestionEvent)
				if payload.Provider != provider || (i%2 == 1 && payload.Outcome != "answered") {
					t.Fatalf("event: %+v", payload)
				}
			}
		})
	}
}

func TestCodingAgentClarificationCancellationClosesPendingPrompt(t *testing.T) {
	api := &StreamingAPI{eventStore: events.NewEventStore(100)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := api.waitForCodingAgentClarification(ctx, "session", "codex-cli", clarificationTestArgs())
		done <- err
	}()
	p := pendingClarificationForTest(t, api, "session")
	if _, err := api.waitForCodingAgentClarification(ctx, "session", "codex-cli", clarificationTestArgs()); err == nil {
		t.Fatal("overlapping prompts accepted")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not release tool")
	}
	if err := api.submitCodingAgentClarification("session", "codex-cli", p.promptID, true, nil); err == nil {
		t.Fatal("cancelled prompt accepted an answer")
	}
	rows := api.eventStore.GetAllEventsRaw("session")
	if len(rows) != 2 || rows[1].Data.Data.(*unifiedevents.CodingAgentQuestionEvent).Outcome != "interrupted" {
		t.Fatal("missing durable cancellation")
	}
}

// PLAT-746-184: a question in a turn another agent's message started gets the unattended answer when nobody replies
// in time, and the tab stops showing "Needs you"; a question in a turn the person started keeps waiting.
func TestCodingAgentQuestionUnansweredInAgentMessageTurn(t *testing.T) {
	previous := agentMessageQuestionTimeout
	agentMessageQuestionTimeout = 50 * time.Millisecond
	defer func() { agentMessageQuestionTimeout = previous }()
	api := &StreamingAPI{eventStore: events.NewEventStore(100)}
	ask := func(session string, req *QueryRequest) (chan error, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := api.waitForCodingAgentClarification(withQuestionUnansweredAfter(ctx, codingAgentQuestionUnansweredAfter(req)), session, "claude-code", clarificationTestArgs())
			done <- err
		}()
		pendingClarificationForTest(t, api, session)
		return done, cancel
	}

	agentDone, cancelAgent := ask("agent-tab", &QueryRequest{AgentMessageInbox: "inbox-1"})
	defer cancelAgent()
	select {
	case err := <-agentDone:
		if err != errNoAttendingUser {
			t.Fatalf("agent-delivered question got %v, want the unattended answer", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent-delivered question kept waiting")
	}
	if _, _, waiting := api.pendingCodingAgentQuestion("agent-tab"); waiting {
		t.Fatal("tab still shows Needs you")
	}
	rows := api.eventStore.GetAllEventsRaw("agent-tab")
	if len(rows) != 2 || rows[1].Data.Data.(*unifiedevents.CodingAgentQuestionEvent).Outcome != clarificationOutcomeUnanswered {
		t.Fatal("card not settled as answered automatically")
	}

	personDone, cancelPerson := ask("person-tab", &QueryRequest{})
	defer cancelPerson()
	select {
	case err := <-personDone:
		t.Fatalf("person-started question stopped waiting: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if _, _, waiting := api.pendingCodingAgentQuestion("person-tab"); !waiting {
		t.Fatal("person-started question no longer waiting")
	}
}

func TestCodingAgentClarificationSelectionValidation(t *testing.T) {
	questions, err := parseCodingAgentClarification(clarificationTestArgs())
	if err != nil {
		t.Fatal(err)
	}
	questions[0].MultiSelect, questions[0].MinSelections, questions[0].MaxSelections = true, 2, 2
	answers, err := clarificationAnswers(questions, true, nil)
	if err != nil || len(answers[0].SelectedLabels) != 2 {
		t.Fatalf("first options must meet minimum: %+v %v", answers, err)
	}
	for _, labels := range [][]string{{"Sheets"}, {"Sheets", "Sheets"}, {"Sheets", "Unknown"}} {
		_, err := clarificationAnswers(questions, false, []codingAgentQuestionAnswerInput{{ID: "scope", SelectedLabels: labels}, {ID: "purpose", SelectedLabels: []string{"Read"}}})
		if err == nil {
			t.Fatalf("accepted %v", labels)
		}
	}
	args := clarificationTestArgs()
	qs := args["questions"].([]map[string]interface{})
	qs[1]["id"] = "scope"
	if _, err := parseCodingAgentClarification(args); err == nil {
		t.Fatal("duplicate question IDs accepted")
	}
}

func TestCodingAgentClarificationAnswerEndpointOwnership(t *testing.T) {
	api := &StreamingAPI{eventStore: events.NewEventStore(100), activeSessions: map[string]*ActiveSessionInfo{"session": {SessionID: "session", UserID: "owner"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := api.waitForCodingAgentClarification(ctx, "session", "claude-code", clarificationTestArgs())
		done <- err
	}()
	p := pendingClarificationForTest(t, api, "session")
	for _, tc := range []struct {
		user   string
		status int
	}{{"other", http.StatusNotFound}, {"owner", http.StatusOK}} {
		body := `{"provider":"claude-code","prompt_id":"` + p.promptID + `","auto":true}`
		r := httptest.NewRequest(http.MethodPost, "/api/sessions/session/coding-agent-question/answer", strings.NewReader(body))
		r = r.WithContext(requestWithUserForSessionAccess(tc.user).Context())
		r = mux.SetURLVars(r, map[string]string{"session_id": "session"})
		w := httptest.NewRecorder()
		api.handleCodingAgentQuestionAnswer(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.user, w.Code, w.Body.String())
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// An answer's trace line names the prompt, session and whether the agent's turn was still running; no answer text.
func TestClarificationAnswerTraceHasIdsAndTurnStateOnly(t *testing.T) {
	api := &StreamingAPI{eventStore: events.NewEventStore(100)}
	var out bytes.Buffer
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_, _ = api.waitForCodingAgentClarification(ctx, "session", "codex-cli", clarificationTestArgs())
		close(done)
	}()
	p := pendingClarificationForTest(t, api, "session")
	input := []codingAgentQuestionAnswerInput{{ID: "scope", SelectedLabels: []string{"Gmail"}}, {ID: "purpose", SelectedLabels: []string{"Read"}}}
	if err := api.submitCodingAgentClarification("session", "codex-cli", p.promptID, false, input); err != nil {
		t.Fatal(err)
	}
	<-done
	got := out.String()
	for _, want := range []string{"[HUMAN_INPUT] prompt=" + p.promptID, "answered turn=none", "waiter=consumed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("trace lacks %q: %s", want, got)
		}
	}
	if strings.Contains(got, "Gmail") || strings.Contains(got, "Choose scope") {
		t.Fatalf("answer or question text must not be logged: %s", got)
	}
}

// A prompt starts at most one follow-up, and without a recorded request there is nothing to continue.
func TestClarificationFollowUpStartsOncePerPrompt(t *testing.T) {
	api := &StreamingAPI{lastQueryRequests: map[string]QueryRequest{}}
	var out bytes.Buffer
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	request := httptest.NewRequest(http.MethodPost, "/answer", nil)
	api.startClarificationFollowUp(request, "once-session", "clarification:once", "You chose: A")
	api.startClarificationFollowUp(request, "once-session", "clarification:once", "You chose: A")
	if got := strings.Count(out.String(), "followup=skipped reason=no_prior_request"); got != 1 {
		t.Fatalf("expected exactly one decision line, got %d: %s", got, out.String())
	}
}

// PLAT-746-314: a shared-page answer must settle only its sender's live thread.
func TestSharedAgentQuestionAnswerStaysInSenderThread(t *testing.T) {
	api := &StreamingAPI{eventStore: events.NewEventStore(100)}
	api.setSessionBusy("sender-thread", true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := api.waitForCodingAgentClarification(ctx, "sender-thread", "claude-code", clarificationTestArgs())
		done <- err
	}()
	p := pendingClarificationForTest(t, api, "sender-thread")
	for _, session := range []string{"owner-tab", "another-sender"} {
		if err := api.submitSharedAgentQuestion(session, p.provider, p.promptID, true, nil); err == nil {
			t.Fatalf("answer from %s consumed the sender's question", session)
		}
	}
	if err := api.submitSharedAgentQuestion("sender-thread", p.provider, "wrong-prompt", true, nil); err == nil {
		t.Fatal("a different prompt was accepted")
	}
	if err := api.submitSharedAgentQuestion("sender-thread", p.provider, p.promptID, false, []codingAgentQuestionAnswerInput{{ID: "scope", SelectedLabels: []string{"unknown"}}}); err == nil {
		t.Fatal("invalid answer was accepted")
	}
	if err := api.submitSharedAgentQuestion("sender-thread", p.provider, p.promptID, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := api.submitSharedAgentQuestion("sender-thread", p.provider, p.promptID, true, nil); err == nil {
		t.Fatal("settled question was accepted again")
	}
}

// PLAT-746-764: async native questions survive the asking turn, correlate the
// answer once, and cannot resume an owner's agent through the shared relay.
func TestNativeAsyncQuestionSurvivesTurnAndRefusesSharedContinuation(t *testing.T) {
	api := &StreamingAPI{eventStore: events.NewEventStore(100), lastQueryRequests: map[string]QueryRequest{"session": {}}}
	record := llmtypes.NativeRecord{Provider: "codex-cli", SessionID: "native-session", TurnID: "finished-turn", ID: "call-question", Name: "request_user_input_async", Kind: "question", State: "requested", Timestamp: time.Now(), Text: `{"questions":[{"title":"Which folder?","options":["Existing","New"]}]}`}
	if err := api.observeNativeAsyncQuestion("session", record); err != nil {
		t.Fatal(err)
	}
	var p *pendingCodingAgentClarification
	for _, pending := range api.codingAgentClarifications {
		p = pending
	}
	if p == nil || p.ctx.Err() != nil {
		t.Fatal("question was not retained independently of the turn")
	}
	if err := api.observeNativeAsyncQuestion("session", record); err != nil {
		t.Fatal(err)
	}
	if len(api.eventStore.GetAllEventsRaw("session")) != 1 {
		t.Fatal("native replay duplicated question")
	}
	// PLAT-746-764: another async call must not stop native transcript progress.
	second := record
	second.ID = "second-call"
	if err := api.observeNativeAsyncQuestion("session", second); err != nil {
		t.Fatalf("second async question blocked ingestion: %v", err)
	}
	var other *pendingCodingAgentClarification
	for _, pending := range api.codingAgentClarifications {
		if pending != p {
			other = pending
		}
	}
	if other == nil || len(api.eventStore.GetAllEventsRaw("session")) != 2 {
		t.Fatal("second question did not retain an independent durable card")
	}
	if _, err := api.submitCodingAgentClarificationOutcome("different-session", "codex-cli", other.promptID, true, nil); err == nil {
		t.Fatal("another chat could answer a native async card")
	}
	// The shared map has session and prompt keys. An admitted chat named like
	// another card's prompt must not take that card through the session lookup.
	api.lastQueryRequests[other.promptID] = QueryRequest{}
	if _, err := api.submitCodingAgentClarificationOutcome(other.promptID, "codex-cli", other.promptID, true, nil); err == nil {
		t.Fatal("a colliding chat key could settle another chat's card")
	}
	if _, err := api.submitCodingAgentClarificationOutcome("session", "codex-cli", other.promptID, true, nil); err != nil {
		t.Fatal(err)
	}
	if api.codingAgentClarificationLocked("session", p.promptID) != p {
		t.Fatal("answering the second card removed the first")
	}
	if err := api.submitSharedAgentQuestion("session", "codex-cli", p.promptID, true, nil); err == nil {
		t.Fatal("shared answer could resume owner's native async question")
	}
	outcome, err := api.submitCodingAgentClarificationOutcome("session", "codex-cli", p.promptID, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.askingTurnEnded || !strings.Contains(outcome.message, `questionItemId`) || !strings.Contains(outcome.message, `call-question`) || !strings.Contains(outcome.message, `Existing`) {
		t.Fatalf("missing native continuation correlation: %+v", outcome)
	}
	if _, err := api.submitCodingAgentClarificationOutcome("session", "codex-cli", p.promptID, true, nil); err == nil {
		t.Fatal("duplicate answer could start another turn")
	}
	if err := api.observeNativeAsyncQuestion("session", record); err != nil {
		t.Fatal(err)
	}
	if api.codingAgentClarificationLocked("session", p.promptID) != nil {
		t.Fatal("replay resurrected answered question")
	}
}

// PR1730: restart must close an unanswered native card even after it leaves
// the memory tail; already answered cards stay answered, while unknown live
// callbacks close rather than being misclassified as recoverable questions.
func TestNativeRestartClosesUnrecoverableDurableCards(t *testing.T) {
	journal, err := events.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "questions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := events.NewEventStore(10)
	defer store.Stop()
	store.SetDurableJournal(journal)
	store.SetSessionOwner("chat", "owner")
	if err := store.SetSessionPersistenceClass("chat", events.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{eventStore: store}
	for _, id := range []string{"clarification:async-open", "clarification:async-answered", "clarification:blocking"} {
		p := &pendingCodingAgentClarification{sessionID: "chat", provider: "codex-cli", promptID: id}
		if err := api.emitCodingAgentClarification(p, "requested", "", nil); err != nil {
			t.Fatal(err)
		}
		if id == "clarification:async-answered" {
			if err := api.emitCodingAgentClarification(p, "settled", "answered", nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 0; i < 505; i++ {
		e := events.Event{ID: fmt.Sprintf("later-%d", i), Type: "user_message", SessionID: "chat", Timestamp: time.Now(),
			Data: &unifiedevents.AgentEvent{Type: unifiedevents.EventType("user_message"), Data: events.NewGenericEventData("user_message", map[string]interface{}{"content": "later"})}}
		if err := store.AddEventChecked("chat", e); err != nil {
			t.Fatal(err)
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := api.restoreNativeQuestionsAfterRestart("chat"); err != nil {
			t.Fatal(err)
		}
		page, err := store.ReadDurableChatPage("chat", events.DurableEventPageOptions{Limit: 1000, FromStart: true})
		if err != nil {
			t.Fatal(err)
		}
		settlements := 0
		for _, e := range page.Events {
			if e.ID != "clarification:async-open:settled" && e.ID != "clarification:blocking:settled" {
				continue
			}
			settlements++
			raw, _ := json.Marshal(e.Data.Data)
			var closed unifiedevents.CodingAgentQuestionEvent
			if err := json.Unmarshal(raw, &closed); err != nil {
				t.Fatal(err)
			}
			if closed.Outcome != clarificationOutcomeUnanswered || len(closed.Answers) != 0 || closed.Metadata["message"] == nil {
				t.Fatalf("restart invented an answer or omitted its explanation: %s", raw)
			}
		}
		if settlements != 2 || len(page.Events) != 511 {
			t.Fatalf("restart changed other cards or duplicated settlement: settlements=%d events=%d", settlements, len(page.Events))
		}
	}
}

// #3076: exercise durable journal reload and real owner/request admission helpers,
// not a recreated provider callback. Approval/expired/foreign answers stay refused.
func TestNativeQuestionRestartRestoresOrdinaryAnswersWithoutRevivingApproval(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex-cli"} {
		t.Run(provider, func(t *testing.T) {
			const sid, user = "restored-question-chat", "owner"
			files := map[string]string{}
			path := filepath.Join(t.TempDir(), "questions.sqlite")
			newAPI := func() *StreamingAPI {
				api := newConversationTurnQueueTestAPI(files)
				journal, err := events.OpenSQLiteEventJournal(path)
				if err != nil {
					t.Fatal(err)
				}
				api.eventStore = events.NewEventStore(10)
				api.eventStore.SetDurableJournal(journal)
				api.eventStore.SetSessionOwner(sid, user)
				if err := api.eventStore.SetSessionPersistenceClass(sid, events.SessionPersistenceInteractiveChat); err != nil {
					t.Fatal(err)
				}
				return api
			}
			api := newAPI()
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user, Username: user})
			if err := api.saveRestartContinueRequest(ctx, user, sid, "original-query", QueryRequest{Query: "Do not replay this task", AgentMode: "multi-agent", Provider: provider, ModelID: "example-model", AgentMessageInbox: "original-inbox", TriggeredByLabel: "old trigger", CostSourceID: "old-schedule", BotPlatform: "example-channel"}); err != nil {
				t.Fatal(err)
			}
			questions, err := parseCodingAgentClarification(clarificationTestArgs())
			if err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"question", "command_approval", "plan_approval", "form", "expired"} {
				p := &pendingCodingAgentClarification{sessionID: sid, provider: provider, promptID: "clarification:" + kind, requestKind: kind, questions: questions}
				if kind == "expired" {
					p.requestKind = "question"
					p.expiresAt = time.Now().Add(-time.Minute)
				}
				if err := api.emitCodingAgentClarification(p, "requested", "", nil); err != nil {
					t.Fatal(err)
				}
			}
			api.eventStore.Stop()
			fresh := newAPI()
			defer fresh.eventStore.Stop()
			if err := fresh.restoreNativeQuestionsAfterRestart(sid); err != nil {
				t.Fatal(err)
			}
			if err := fresh.restoreNativeQuestionsAfterRestart(sid); err != nil {
				t.Fatal(err)
			}
			p := fresh.codingAgentClarificationLocked(sid, "clarification:question")
			if p == nil || !p.recovered || p.requestKind != "question" {
				t.Fatal("ordinary question did not survive")
			}
			// #3133: an MCP form's answer belongs to its waiting server request; it is never revived.
			for _, kind := range []string{"command_approval", "plan_approval", "form", "expired"} {
				if fresh.codingAgentClarificationLocked(sid, "clarification:"+kind) != nil {
					t.Fatal("approval/expired callback revived")
				}
			}
			fresh.eventStore.AddEvent(sid, events.Event{ID: "open-turn", Type: "user_message", Timestamp: time.Now(), SessionID: sid})
			turns, err := fresh.prepareRestartContinues(ctx, user, []queuedConversationTurn{{ID: "prior-continue", SessionID: sid, Request: QueryRequest{TriggeredBy: "restart_continue"}}})
			if err != nil || len(turns) != 0 {
				t.Fatal("automatic restart turn ran ahead of the pending question")
			}
			if fresh.eventStore.PersistedTurnOpen(sid) {
				t.Fatal("interrupted execution still appears running while question waits")
			}
			for _, claims := range []*UserClaims{{UserID: "other"}, {UserID: user, Scope: "scoped"}} {
				r := httptest.NewRequest(http.MethodPost, "/answer", nil).WithContext(context.WithValue(context.Background(), UserContextKey, claims))
				if _, err := fresh.recoveredQuestionRequest(r, sid, p.promptID); err == nil {
					t.Fatal("foreign/scoped caller borrowed the owner's saved selectors")
				}
			}
			r := httptest.NewRequest(http.MethodPost, "/answer", nil).WithContext(ctx)
			request, err := fresh.recoveredQuestionRequest(r, sid, p.promptID)
			if err != nil || request == nil || request.Provider != provider || request.Query != "" || request.IsAutoNotification || request.AgentMessageInbox != "" || request.RequestedSkillID != "" || request.TriggeredByLabel != "" || request.CostSourceID != "" || request.BotPlatform != "" {
				t.Fatalf("bad answer admission selectors: %v %v", request, err)
			}
			outcome, err := fresh.submitCodingAgentClarificationOutcome(sid, provider, p.promptID, false, []codingAgentQuestionAnswerInput{{ID: "scope", SelectedLabels: []string{"Sheets"}}, {ID: "purpose", SelectedLabels: []string{"Read"}}})
			if err != nil || !outcome.askingTurnEnded || !strings.Contains(outcome.message, "Choose scope") || !strings.Contains(outcome.message, "Sheets") || strings.Contains(outcome.message, "Do not replay") {
				t.Fatalf("answer was lost or original task replayed: %+v %v", outcome, err)
			}
			if _, err := fresh.submitCodingAgentClarificationOutcome(sid, provider, p.promptID, true, nil); err == nil {
				t.Fatal("answer dispatched twice")
			}
			if err := fresh.restoreNativeQuestionsAfterRestart(sid); err != nil {
				t.Fatal(err)
			}
			if fresh.codingAgentClarificationLocked(sid, p.promptID) != nil {
				t.Fatal("answered question resurrected on replay")
			}
			// #4021: a new message instead of an answer closes a card kept across a restart for good.
			later := &pendingCodingAgentClarification{sessionID: sid, provider: provider, promptID: "clarification:later", requestKind: "question", questions: questions}
			if err := fresh.emitCodingAgentClarification(later, "requested", "", nil); err != nil {
				t.Fatal(err)
			}
			// The chat's history still shows it open while this process holds no question, as when a chat's
			// native session was not reloaded at startup: answering or a new message reloads it first.
			fresh.restoreQuestionsOnce(sid)
			if fresh.codingAgentClarificationLocked(sid, later.promptID) == nil {
				t.Fatal("open card from history not reloaded on demand")
			}
			// A question this process asked and still waits on is not taken for a card from before the restart.
			live := &pendingCodingAgentClarification{sessionID: sid, provider: provider, promptID: "clarification:live", requestKind: "question", questions: questions}
			fresh.codingAgentClarificationsMu.Lock()
			fresh.codingAgentClarifications[sid] = live
			fresh.codingAgentClarificationsMu.Unlock()
			if err := fresh.emitCodingAgentClarification(live, "requested", "", nil); err != nil {
				t.Fatal(err)
			}
			if err := fresh.restoreNativeQuestionsAfterRestart(sid); err != nil || fresh.codingAgentClarificationLocked(sid, live.promptID) != live || live.recovered {
				t.Fatalf("restore took over a question still being asked: %v", err)
			}
			fresh.codingAgentClarificationsMu.Lock()
			delete(fresh.codingAgentClarifications, sid)
			fresh.codingAgentClarificationsMu.Unlock()
			fresh.supersedeRecoveredQuestions(sid)
			if err := fresh.restoreNativeQuestionsAfterRestart(sid); err != nil || fresh.codingAgentClarificationLocked(sid, later.promptID) != nil {
				t.Fatalf("a question the person moved past came back: %v", err)
			}
		})
	}
}

func TestGracefulRestartPreservesOnlyTypedOrdinaryQuestion(t *testing.T) {
	for _, kind := range []string{"question", "command_approval", "plan_approval", "form"} {
		t.Run(kind, func(t *testing.T) {
			api := &StreamingAPI{eventStore: events.NewEventStore(100)}
			defer api.eventStore.Stop()
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			args := clarificationTestArgs()
			args["native_request_kind"] = kind
			done := make(chan error, 1)
			go func() { _, err := api.waitForCodingAgentClarification(ctx, "chat", "claude-code", args); done <- err }()
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				api.codingAgentClarificationsMu.Lock()
				p := api.codingAgentClarifications["chat"]
				api.codingAgentClarificationsMu.Unlock()
				if p != nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
			cancel(errAppRestart)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("lost callback did not close")
			}
			settled := false
			for _, e := range api.eventStore.GetAllEventsRaw("chat") {
				if e.Type == "coding_agent_question" {
					raw, _ := json.Marshal(e.Data.Data)
					var q unifiedevents.CodingAgentQuestionEvent
					_ = json.Unmarshal(raw, &q)
					settled = settled || q.Kind == "settled"
				}
			}
			if settled == (kind == "question") {
				t.Fatalf("question/approval shutdown lifecycle mixed: %s settled=%t", kind, settled)
			}
		})
	}
}

// A new Codex chat learns its native session id only after the first turn,
// so that turn's async question sits before the observer's baseline. It must
// still become a card; other earlier records and older questions stay history.
func TestNativeAsyncQuestionBeforeBaselineOnlyTakesThisChatsQuestions(t *testing.T) {
	observed := time.Now().Add(-time.Minute)
	question := llmtypes.NativeRecord{Provider: "codex-cli", Kind: "question", Name: "request_user_input_async", Timestamp: observed.Add(20 * time.Second)}
	if !nativeAsyncQuestionBeforeBaseline(question, observed) {
		t.Fatal("first-turn async question of a new chat was skipped")
	}
	older := question
	older.Timestamp = observed.Add(-time.Second)
	if nativeAsyncQuestionBeforeBaseline(older, observed) {
		t.Fatal("a question asked before this chat observed its runtime was imported")
	}
	if nativeAsyncQuestionBeforeBaseline(question, time.Time{}) {
		t.Fatal("unknown observation start imported history")
	}
	stale := question
	stale.Timestamp = time.Now().Add(-25 * time.Hour)
	if nativeAsyncQuestionBeforeBaseline(stale, stale.Timestamp.Add(-time.Minute)) {
		t.Fatal("an expired question was revived")
	}
	for _, other := range []llmtypes.NativeRecord{
		{Provider: "codex-cli", Kind: "tool", Name: "Command", Timestamp: question.Timestamp},
		{Provider: "codex-cli", Kind: "question", Name: "request_user_input", Timestamp: question.Timestamp},
		{Provider: "claude-code", Kind: "question", Name: "request_user_input_async", Timestamp: question.Timestamp},
	} {
		if nativeAsyncQuestionBeforeBaseline(other, observed) {
			t.Fatalf("pre-baseline record imported: %+v", other)
		}
	}
}

// request_clarification is registered (and so findable in search_tools) only
// where something calls it: Cursor and Claude's terminal question hook.
func TestClarificationRouteRegisteredOnlyForCursorAndClaudeTerminal(t *testing.T) {
	for _, c := range []struct {
		provider, transport string
		want                bool
	}{
		{"cursor-cli", "", true},
		{"claude-code", "tmux", true},
		{"claude-code", "sdk", false},
		{"codex-cli", "", false},
		{"pi-cli", "", false},
		{"muse-cli", "", false},
		{"agy-cli", "", false},
	} {
		if got := codingAgentNeedsClarificationRoute(c.provider, c.transport); got != c.want {
			t.Fatalf("%s/%s: registered=%t, want %t", c.provider, c.transport, got, c.want)
		}
	}
}

// PLAT-872-153: Cursor cancels an MCP tool call after 60 seconds, so its question must not wait. The tool returns at
// once, the card stays open under its own prompt ID, and the answer continues the chat as a new message.
func TestCursorQuestionReturnsAtOnceAndAnswerContinuesTheChat(t *testing.T) {
	if !codingAgentAsksWithoutWaiting("cursor-cli") || codingAgentAsksWithoutWaiting("claude-code") || codingAgentAsksWithoutWaiting("codex-cli") {
		t.Fatal("only Cursor asks without waiting; Claude's terminal route still waits")
	}
	api := &StreamingAPI{eventStore: events.NewEventStore(100), lastQueryRequests: map[string]QueryRequest{"session": {}}}
	started := time.Now()
	args := clarificationTestArgs()
	args["questions"].([]map[string]interface{})[1]["allow_other"] = true // the person may type their own answer
	out, err := api.askCodingAgentQuestionWithoutWaiting("session", "cursor-cli", args, 0)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("the question waited for an answer")
	}
	var shown struct {
		Status   string `json:"status"`
		PromptID string `json:"prompt_id"`
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil || shown.Status != "shown" || !strings.HasPrefix(shown.PromptID, clarificationPromptPrefix+"async-") {
		t.Fatalf("result: %s %v", out, err)
	}
	api.codingAgentClarificationsMu.Lock()
	p := api.codingAgentClarifications[shown.PromptID]
	_, blocksSession := api.codingAgentClarifications["session"]
	api.codingAgentClarificationsMu.Unlock()
	if p == nil || blocksSession || !p.nativeAsync || p.expiresAt.Sub(p.askedAt) != 24*time.Hour {
		t.Fatalf("pending card: %+v blocks_session=%t", p, blocksSession)
	}
	// A second question opens its own card instead of being refused.
	if _, err := api.askCodingAgentQuestionWithoutWaiting("session", "cursor-cli", clarificationTestArgs(), 0); err != nil {
		t.Fatalf("second question refused: %v", err)
	}
	if err := api.submitCodingAgentClarification("other-session", "cursor-cli", shown.PromptID, true, nil); err == nil {
		t.Fatal("another session could answer")
	}
	input := []codingAgentQuestionAnswerInput{{ID: "scope", SelectedLabels: []string{"Gmail"}}, {ID: "purpose", OtherText: "Only read"}}
	outcome, err := api.submitCodingAgentClarificationOutcome("session", "cursor-cli", shown.PromptID, false, input)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.askingTurnEnded || !strings.Contains(outcome.message, "You chose: ") || !strings.Contains(outcome.message, "Gmail") || !strings.Contains(outcome.message, "Only read") || !strings.HasSuffix(outcome.message, nativeAsyncQuestionContinue) || strings.Contains(outcome.message, "send_user_message_question_reply") {
		t.Fatalf("follow-up: %+v", outcome)
	}
	if _, err := api.submitCodingAgentClarificationOutcome("session", "cursor-cli", shown.PromptID, false, input); err == nil {
		t.Fatal("duplicate answer accepted")
	}
	// An agent-started turn's question closes unanswered after its own limit, never with an assumed choice.
	out, err = api.askCodingAgentQuestionWithoutWaiting("session", "cursor-cli", clarificationTestArgs(), 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(out), &shown)
	deadline := time.After(2 * time.Second)
	for {
		api.codingAgentClarificationsMu.Lock()
		gone := api.codingAgentClarifications[shown.PromptID] == nil
		api.codingAgentClarificationsMu.Unlock()
		if gone {
			break
		}
		select {
		case <-deadline:
			t.Fatal("agent-started question did not expire")
		case <-time.After(5 * time.Millisecond):
		}
	}
	var settled *unifiedevents.CodingAgentQuestionEvent
	for _, row := range api.eventStore.GetAllEventsRaw("session") {
		if row.ID == shown.PromptID+":settled" {
			settled = row.Data.Data.(*unifiedevents.CodingAgentQuestionEvent)
		}
	}
	if settled == nil || settled.Outcome != clarificationOutcomeUnanswered || len(settled.Answers) != 0 {
		t.Fatalf("expired card: %+v", settled)
	}
}

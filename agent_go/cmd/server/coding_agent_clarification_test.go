package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
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

// The follow-up turn is decided by "the turn that asked has ended", not only "nothing is running".
func TestClarificationAskingTurnEnded(t *testing.T) {
	for name, c := range map[string]struct {
		asked, current string
		want           bool
	}{
		"chat idle":                     {"input_lane|t1", "|", true},
		"same retained turn still runs": {"retained_session_turn|t1", "retained_session_turn|t1", false},
		"a new retained turn":           {"retained_session_turn|t1", "retained_session_turn|t2", true},
		"turn without an id, running":   {"input_lane|", "input_lane|", false},
		"busy flag only":                {"busy_flag|", "busy_flag|", false},
	} {
		if got := clarificationAskingTurnEnded(c.asked, c.current); got != c.want {
			t.Errorf("%s: ended=%v, want %v", name, got, c.want)
		}
	}
}

// The follow-up is in the person's words: "You chose: <labels>".
func TestClarificationFollowUpMessage(t *testing.T) {
	one := []unifiedevents.CodingAgentQuestionPrompt{{ID: "run_review_choice", Question: "Run now or review first?"}}
	got := clarificationFollowUpMessage(one, []unifiedevents.CodingAgentQuestionAnswer{{ID: "run_review_choice", SelectedLabels: []string{"Review first"}}})
	if got != "You chose: Review first" {
		t.Fatalf("one question: %q", got)
	}
	two := []unifiedevents.CodingAgentQuestionPrompt{{ID: "a", Header: "Scope", Question: "Choose scope"}, {ID: "b", Question: "Choose purpose"}}
	got = clarificationFollowUpMessage(two, []unifiedevents.CodingAgentQuestionAnswer{
		{ID: "a", SelectedLabels: []string{"Sheets", "Gmail"}}, {ID: "b", OtherText: " custom "},
	})
	if got != "You chose: Scope: Sheets, Gmail; Choose purpose: custom" {
		t.Fatalf("two questions: %q", got)
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

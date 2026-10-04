package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	for _, provider := range []string{"muse-cli", "claude-code", "codex-cli"} {
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

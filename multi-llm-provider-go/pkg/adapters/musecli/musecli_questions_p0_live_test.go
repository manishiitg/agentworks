package musecli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestMuseCLIRealStructuredQuestionChoiceP0(t *testing.T) {
	for _, lane := range []string{"tmux", "msp"} {
		t.Run(lane, func(t *testing.T) {
			t.Setenv(MuseTransportEnv, lane)
			requireMetaMuseCLIE2E(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			owner := "structured-question-p0-" + museRandomHex(t, 3)
			t.Cleanup(func() { KillMusePersistentSession(owner); CloseMuseMSPSession(owner) })
			adapter := museLiveAdapter()
			opts := []llmtypes.CallOption{WithUserChoice(true), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner), WithWorkingDir(t.TempDir()), llmtypes.WithReasoningEffort("low")}
			if _, err := adapter.GenerateContent(ctx, nil, append(opts, llmtypes.WithCodingProviderLaunchOnly())...); err != nil {
				t.Fatal(err)
			}
			token := "STRUCTURED-CHOICE-" + museRandomHex(t, 3)
			prompt := fmt.Sprintf("Integration test. Use your native request_user_input tool to ask ONE question: Choose a color? Provide exactly three options: Red, Blue, Green. Wait for my selection. Then reply with exactly %s and the selected color. Do not use other tools.", token)
			result := make(chan error, 1)
			go func() {
				response, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
				if err == nil && !strings.Contains(response.Choices[0].Content, token) {
					err = fmt.Errorf("reply omitted token: %s", response.Choices[0].Content)
				}
				result <- err
			}()
			reader := NewQuestionReaderForOwner(owner)
			var requested *QuestionEvent
			deadline := time.After(3 * time.Minute)
			for requested == nil {
				rows, err := reader.Poll()
				if err != nil {
					t.Fatal(err)
				}
				for i := range rows {
					if rows[i].Kind == "user_input_prompt_requested" {
						requested = &rows[i]
					}
				}
				if requested != nil {
					break
				}
				select {
				case err := <-result:
					t.Fatalf("turn ended before question: %v", err)
				case <-deadline:
					t.Fatal("structured question did not arrive")
				case <-time.After(250 * time.Millisecond):
				}
			}
			if len(requested.Questions) != 1 || len(requested.Questions[0].Options) != 3 {
				t.Fatalf("question shape: %+v", requested)
			}
			promptID := requested.PromptID
			q := requested.Questions[0]
			if err := SubmitQuestionAnswers(ctx, owner, requested.PromptID, []QuestionAnswer{{ID: q.ID, SelectedLabel: q.Options[2].Label}}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			rows, err := reader.Poll()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows {
				if row.Kind == "user_input_prompt_settled" && row.PromptID == requested.PromptID && row.Outcome == "answered" && len(row.Answers) == 1 && row.Answers[0].SelectedLabel == q.Options[2].Label {
					found = true
				}
			}
			if !found {
				t.Fatalf("selected answer not settled: %+v", rows)
			}
			// A second turn proves ordered questions and the final review screen on
			// the same retained session.
			result = make(chan error, 1)
			go func() {
				prompt := "Integration test of your native request_user_input widget. Ask all THREE questions in a SINGLE request_user_input call. Question 1 Color: Red, Blue (Recommended), Green. Question 2 Drink: Coffee, Tea (Recommended), Water. Question 3 Layout: Compact (Recommended), Spacious. Wait for actual answers and then report the three selected names separated by |. Do not use other tools."
				_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
				result <- err
			}()
			requested = nil
			deadline = time.After(3 * time.Minute)
			for requested == nil {
				rows, err := reader.Poll()
				if err != nil {
					t.Fatal(err)
				}
				for i := range rows {
					if rows[i].Kind == "user_input_prompt_requested" && rows[i].PromptID != promptID {
						requested = &rows[i]
					}
				}
				if requested != nil {
					break
				}
				select {
				case err := <-result:
					t.Fatalf("multi-question turn ended early: %v", err)
				case <-deadline:
					t.Fatal("multi-question prompt did not arrive")
				case <-time.After(250 * time.Millisecond):
				}
			}
			if len(requested.Questions) != 3 {
				t.Fatalf("expected three questions: %+v", requested.Questions)
			}
			chosen := make([]QuestionAnswer, 3)
			for i, question := range requested.Questions {
				chosen[i] = QuestionAnswer{ID: question.ID, SelectedLabel: question.Options[len(question.Options)-1].Label}
			}
			if err := SubmitQuestionAnswers(ctx, owner, requested.PromptID, chosen); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			rows, err = reader.Poll()
			if err != nil {
				t.Fatal(err)
			}
			found = false
			for _, row := range rows {
				if row.Kind == "user_input_prompt_settled" && row.PromptID == requested.PromptID && row.Outcome == "answered" && len(row.Answers) == 3 {
					found = true
				}
			}
			if !found {
				t.Fatalf("multi-question answer not settled: %+v", rows)
			}
			// A third turn proves the multi-select (checkbox) form: two options
			// checked, then Submit.
			previous := requested.PromptID
			result = make(chan error, 1)
			go func() {
				prompt := "Integration test of your native request_user_input widget. Ask ONE question that lets me pick more than one answer: set its selection to {\"mode\": \"multiple\", \"min_selections\": 1, \"max_selections\": 3}. Question: Which fruits? Options: Apple, Banana, Cherry. Wait for actual answers and then report the selected fruit names separated by |. Do not use other tools."
				_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
				result <- err
			}()
			requested = nil
			deadline = time.After(3 * time.Minute)
			for requested == nil {
				rows, err := reader.Poll()
				if err != nil {
					t.Fatal(err)
				}
				for i := range rows {
					if rows[i].Kind == "user_input_prompt_requested" && rows[i].PromptID != promptID && rows[i].PromptID != previous {
						requested = &rows[i]
					}
				}
				if requested != nil {
					break
				}
				select {
				case err := <-result:
					t.Fatalf("multi-select turn ended early: %v", err)
				case <-deadline:
					t.Fatal("multi-select prompt did not arrive")
				case <-time.After(250 * time.Millisecond):
				}
			}
			if len(requested.Questions) != 1 || !requested.Questions[0].Multiple() || len(requested.Questions[0].Options) < 3 {
				t.Fatalf("expected one multi-select question: %+v", requested.Questions)
			}
			q = requested.Questions[0]
			picked := []string{q.Options[1].Label, q.Options[2].Label}
			if err := SubmitQuestionAnswers(ctx, owner, requested.PromptID, []QuestionAnswer{{ID: q.ID, SelectedLabels: picked}}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			rows, err = reader.Poll()
			if err != nil {
				t.Fatal(err)
			}
			found = false
			for _, row := range rows {
				if row.Kind == "user_input_prompt_settled" && row.PromptID == requested.PromptID {
					t.Logf("multi-select settled: %+v", row)
					found = row.Outcome == "answered" && len(row.Answers) == 1
				}
			}
			if !found {
				t.Fatalf("multi-select answer not settled: %+v", rows)
			}
		})
	}
}

// A chat question nobody answers must not hold the turn: after
// museUserChoiceWait Muse takes the first option, as an unattended run does.
func TestMuseCLIRealUserChoiceUnansweredFallsBackP0(t *testing.T) {
	for _, lane := range []string{"tmux", "msp"} {
		t.Run(lane, func(t *testing.T) {
			t.Setenv(MuseTransportEnv, lane)
			requireMetaMuseCLIE2E(t)
			previous := museUserChoiceWait
			museUserChoiceWait = 20 * time.Second
			t.Cleanup(func() { museUserChoiceWait = previous })
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()
			owner := "question-fallback-p0-" + museRandomHex(t, 3)
			t.Cleanup(func() { KillMusePersistentSession(owner); CloseMuseMSPSession(owner) })
			adapter := museLiveAdapter()
			opts := []llmtypes.CallOption{WithUserChoice(true), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner), WithWorkingDir(t.TempDir()), llmtypes.WithReasoningEffort("low")}
			prompt := "Integration test. Use your native request_user_input tool to ask ONE question: Choose a color? Provide exactly three options in this order: Red, Blue, Green. Wait for my selection. Then reply with ONLY the selected color. Do not use other tools."
			started := time.Now()
			resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
			if err != nil {
				t.Fatalf("unanswered chat question did not fall back: %v", err)
			}
			reply := resp.Choices[0].Content
			if !strings.Contains(reply, "Red") {
				t.Fatalf("expected the first option after the wait, got %q", reply)
			}
			if elapsed := time.Since(started); elapsed < museUserChoiceWait {
				t.Fatalf("answered after %s, before the %s wait for the person", elapsed, museUserChoiceWait)
			}
			t.Logf("PASS: fell back to the first option after %s: %q", time.Since(started).Round(time.Second), reply)
		})
	}
}

// The last resort: Esc on an open question ends the turn promptly, and the
// same chat session still takes the next message.
func TestMuseCLIRealInterruptPendingQuestionP0(t *testing.T) {
	for _, lane := range []string{"tmux", "msp"} {
		t.Run(lane, func(t *testing.T) {
			t.Setenv(MuseTransportEnv, lane)
			requireMetaMuseCLIE2E(t)
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()
			owner := "question-interrupt-p0-" + museRandomHex(t, 3)
			t.Cleanup(func() { KillMusePersistentSession(owner); CloseMuseMSPSession(owner) })
			adapter := museLiveAdapter()
			opts := []llmtypes.CallOption{WithUserChoice(true), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner), WithWorkingDir(t.TempDir()), llmtypes.WithReasoningEffort("low")}
			if _, err := adapter.GenerateContent(ctx, nil, append(opts, llmtypes.WithCodingProviderLaunchOnly())...); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				prompt := "Integration test. Use your native request_user_input tool to ask ONE question: Choose a color? Options: Red, Blue, Green. Wait for my selection, then reply with the color. Do not use other tools."
				_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
				result <- err
			}()
			deadline := time.After(3 * time.Minute)
			for {
				if prompt, err := PendingQuestion(owner); err == nil && prompt != nil {
					break
				}
				select {
				case err := <-result:
					t.Fatalf("turn ended before the question: %v", err)
				case <-deadline:
					t.Fatal("question did not arrive")
				case <-time.After(250 * time.Millisecond):
				}
			}
			time.Sleep(time.Second) // let the widget become interactive
			if err := InterruptPendingQuestion(ctx, owner); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				t.Logf("interrupted turn returned: %v", err)
			case <-time.After(90 * time.Second):
				t.Fatal("turn did not end after Esc")
			}
			token := "AFTER-ESC-" + museRandomHex(t, 3)
			resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with exactly " + token + " and nothing else. Do not use tools."}}}}, opts...)
			if err != nil || !strings.Contains(resp.Choices[0].Content, token) {
				t.Fatalf("session unusable after Esc: %v %+v", err, resp)
			}
		})
	}
}

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

func nativeClaudeQuestionTestServer(t *testing.T) (*StreamingAPI, string) {
	t.Helper()
	const session = "native-claude-question-test"
	api := &StreamingAPI{eventStore: events.NewEventStore(100), activeSessions: map[string]*ActiveSessionInfo{session: {SessionID: session, UserID: "owner"}}}
	registrar := &placeMCPToolRegistrar{}
	if err := api.registerCodingAgentClarificationTool(registrar, session, "claude-code"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tools/custom/request_clarification" || r.Header.Get("X-Session-ID") != session || r.Header.Get("Authorization") != "Bearer native-question-test-token" {
			http.Error(w, "unknown session", http.StatusForbidden)
			return
		}
		var args map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, "invalid input", http.StatusBadRequest)
			return
		}
		result, err := registrar.exec(r.Context(), args)
		response := map[string]interface{}{"success": err == nil, "result": result}
		if err != nil {
			response["error"] = err.Error()
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	config, _ := json.Marshal(map[string]interface{}{"mcpServers": map[string]interface{}{"api-bridge": map[string]interface{}{"env": map[string]string{"MCP_API_URL": server.URL, "MCP_API_TOKEN": "native-question-test-token", "MCP_SESSION_ID": session}}}})
	settings, err := mcpagent.BuildClaudeNativeQuestionSettings(string(config), "")
	if err != nil {
		t.Fatal(err)
	}
	return api, settings
}

func nativeClaudeQuestionCommand(t *testing.T, settings string) string {
	t.Helper()
	var config struct {
		Hooks struct {
			Pre []struct {
				Matcher string
				Hooks   []struct{ Command string }
			} `json:"PreToolUse"`
		}
	}
	if err := json.Unmarshal([]byte(settings), &config); err != nil {
		t.Fatal(err)
	}
	for _, match := range config.Hooks.Pre {
		if match.Matcher == "AskUserQuestion" {
			return match.Hooks[0].Command
		}
	}
	t.Fatal("native question hook missing")
	return ""
}

func answerNativeClaudeQuestionForTest(t *testing.T, api *StreamingAPI, p *pendingCodingAgentClarification, answers []codingAgentQuestionAnswerInput) {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"provider": "claude-code", "prompt_id": p.promptID, "answers": answers})
	r := httptest.NewRequest(http.MethodPost, "/api/sessions/"+p.sessionID+"/coding-agent-question/answer", bytes.NewReader(body))
	r = r.WithContext(requestWithUserForSessionAccess("owner").Context())
	r = mux.SetURLVars(r, map[string]string{"session_id": p.sessionID})
	w := httptest.NewRecorder()
	api.handleCodingAgentQuestionAnswer(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("answer submission: %d %s", w.Code, w.Body.String())
	}
}

func TestClaudeNativeQuestionHookThroughUIAnswerEndpoint(t *testing.T) {
	api, settings := nativeClaudeQuestionTestServer(t)
	for turn := 0; turn < 2; turn++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", nativeClaudeQuestionCommand(t, settings))
		cmd.Stdin = strings.NewReader(`{"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Choose scope?","options":[{"label":"Sheets"},{"label":"Gmail"}]},{"question":"Choose features?","multiSelect":true,"options":[{"label":"Read"},{"label":"Send"}]}]}}`)
		var output bytes.Buffer
		cmd.Stdout = &output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		p := pendingClarificationForTest(t, api, "native-claude-question-test")
		if len(p.questions) != 2 || !p.questions[0].AllowOther || !p.questions[1].MultiSelect {
			t.Fatal("native question shape lost")
		}
		answerNativeClaudeQuestionForTest(t, api, p, []codingAgentQuestionAnswerInput{{ID: "question-1", OtherText: "Both"}, {ID: "question-2", SelectedLabels: []string{"Read", "Send"}}})
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
		var reply struct {
			Output struct {
				Decision string                              `json:"permissionDecision"`
				Input    struct{ Answers map[string]string } `json:"updatedInput"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(output.Bytes(), &reply); err != nil || reply.Output.Decision != "allow" || reply.Output.Input.Answers["Choose scope?"] != "Both" || reply.Output.Input.Answers["Choose features?"] != "Read, Send" {
			t.Fatalf("native reply: %s %v", output.String(), err)
		}
	}
	if rows := api.eventStore.GetAllEventsRaw("native-claude-question-test"); len(rows) != 4 {
		t.Fatalf("request/settlement pairs=%d", len(rows))
	}
}

// The hook's HTTP request is cancelled when Claude stops its hook process.
// Verify this records an interruption rather than an answer or a stuck card.
func TestClaudeNativeQuestionHookCancellationClosesCard(t *testing.T) {
	api, settings := nativeClaudeQuestionTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// exec replaces the shell so cancellation closes the hook's own socket.
	cmd := exec.CommandContext(ctx, "sh", "-c", "exec "+nativeClaudeQuestionCommand(t, settings))
	cmd.Stdin = strings.NewReader(`{"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Choose?","options":[{"label":"A"},{"label":"B"}]}]}}`)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pendingClarificationForTest(t, api, "native-claude-question-test")
	cancel()
	_ = cmd.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows := api.eventStore.GetAllEventsRaw("native-claude-question-test")
		if len(rows) == 2 {
			if payload := rows[1].Data.Data.(*unifiedevents.CodingAgentQuestionEvent); payload.Outcome != "interrupted" {
				t.Fatal("cancelled hook recorded an answer")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("hook cancellation did not close the pending card")
}

func TestClaudeNativeQuestionLive(t *testing.T) {
	if os.Getenv("RUN_CLAUDE_NATIVE_QUESTION_LIVE") != "1" {
		t.Skip("set RUN_CLAUDE_NATIVE_QUESTION_LIVE=1 for the native Claude round trip")
	}
	api, settings := nativeClaudeQuestionTestServer(t)
	work := t.TempDir()
	model := os.Getenv("CLAUDE_NATIVE_QUESTION_TEST_MODEL")
	if model == "" {
		model = "claude-sonnet-5-5"
	}
	adapter := claudecode.NewClaudeCodeInteractiveAdapter(model, &e2eMockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	// This test process owns only the one disposable native session below.
	defer claudecode.CleanupClaudeCodeTmuxSessions(context.Background())
	baseOptions := []llmtypes.CallOption{claudecode.WithWorkingDir(work), claudecode.WithInteractiveSessionID("native-claude-question-test"), claudecode.WithPersistentInteractiveSession(true), claudecode.WithEffort("low")}
	// Start an older, warm session without native questions, then enable the
	// hook. The process must change while its conversation history survives.
	seed, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Remember the exact transport test token QUESTION_HISTORY_724 for the rest of this conversation. Reply only ACK. Do not use tools."}}}}, append(baseOptions, claudecode.WithClaudeCodeTools("WebSearch"))...)
	if err != nil || seed == nil || len(seed.Choices) == 0 {
		t.Fatalf("warm session: %v", err)
	}
	seedInfo := seed.Choices[0].GenerationInfo.Additional
	result := make(chan error, 1)
	var nativeInfo map[string]interface{}
	go func() {
		response, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "You are testing native AskUserQuestion. Use only that tool for choices, wait for its answers, and report them exactly. Do not write files or use other tools."}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: `Use native AskUserQuestion to ask these two questions in ONE call: "Pick a palette?" (header "Palette", options "Blue" and "Green", single choice) and "Select delivery channels?" (header "Channels", options "Email" and "Chat", multiSelect true). After the answers arrive, use AskUserQuestion AGAIN to ask "Pick output format?" (header "Format", options "HTML" and "PDF", single choice). After those answers arrive, report all selected answers, including any custom text, and the exact test token I asked you to remember earlier. These choices are for an isolated transport test and have no external effect.`}}},
		}, append(baseOptions, claudecode.WithClaudeCodeTools("AskUserQuestion"), claudecode.WithAllowedTools("AskUserQuestion"), claudecode.WithClaudeCodeSettings(settings))...)
		if err == nil {
			if response == nil || len(response.Choices) == 0 {
				err = fmt.Errorf("missing Claude response")
			} else {
				nativeInfo = response.Choices[0].GenerationInfo.Additional
				for _, answer := range []string{"Amber", "Email", "Chat", "HTML", "QUESTION_HISTORY_724"} {
					if !strings.Contains(response.Choices[0].Content, answer) {
						err = fmt.Errorf("Claude response lost %s: %s", answer, response.Choices[0].Content)
						break
					}
				}
			}
		}
		result <- err
	}()
	var previous string
	for turn := 0; turn < 2; turn++ {
		var p *pendingCodingAgentClarification
		for p == nil {
			api.codingAgentClarificationsMu.Lock()
			p = api.codingAgentClarifications["native-claude-question-test"]
			api.codingAgentClarificationsMu.Unlock()
			if p != nil && p.promptID == previous {
				p = nil
			}
			if p != nil {
				break
			}
			select {
			case err := <-result:
				t.Fatalf("Claude ended before question %d: %v", turn+1, err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
		var answers []codingAgentQuestionAnswerInput
		if turn == 0 {
			if len(p.questions) != 2 || p.questions[0].Question != "Pick a palette?" || !p.questions[1].MultiSelect {
				t.Fatalf("native two-question prompt: %+v", p.questions)
			}
			answers = []codingAgentQuestionAnswerInput{{ID: p.questions[0].ID, OtherText: "Amber"}, {ID: p.questions[1].ID, SelectedLabels: []string{"Email", "Chat"}}}
		} else {
			answers = []codingAgentQuestionAnswerInput{{ID: p.questions[0].ID, SelectedLabels: []string{"HTML"}}}
		}
		answerNativeClaudeQuestionForTest(t, api, p, answers)
		previous = p.promptID
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if rows := api.eventStore.GetAllEventsRaw("native-claude-question-test"); len(rows) != 4 {
		t.Fatalf("native questions did not settle independently: %d", len(rows))
	}
	if seedInfo["claude_code_session"] == nativeInfo["claude_code_session"] || seedInfo["claude_code_session_id"] != nativeInfo["claude_code_session_id"] {
		t.Fatal("enabling native questions did not replace the process and preserve its transcript")
	}
	// Identical settings reuse the process. Removing the hook replaces it and
	// keeps the answers in native history without exposing a terminal menu.
	for _, enabled := range []bool{true, false} {
		options := append([]llmtypes.CallOption{}, baseOptions...)
		if enabled {
			options = append(options, claudecode.WithClaudeCodeTools("AskUserQuestion"), claudecode.WithAllowedTools("AskUserQuestion"), claudecode.WithClaudeCodeSettings(settings))
		} else {
			options = append(options, claudecode.WithClaudeCodeTools("WebSearch"))
		}
		reply, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Without using tools, report the exact token I asked you to remember and my selected palette and output format."}}}}, options...)
		if err != nil || reply == nil || len(reply.Choices) == 0 {
			t.Fatalf("retained question settings turn: %v", err)
		}
		for _, answer := range []string{"QUESTION_HISTORY_724", "Amber", "HTML"} {
			if !strings.Contains(reply.Choices[0].Content, answer) {
				t.Fatalf("retained history lost %s: %s", answer, reply.Choices[0].Content)
			}
		}
		info := reply.Choices[0].GenerationInfo.Additional
		if (info["claude_code_session"] == nativeInfo["claude_code_session"]) != enabled || info["claude_code_session_id"] != nativeInfo["claude_code_session_id"] {
			t.Fatalf("retained native question settings enabled=%t did not preserve/reload correctly", enabled)
		}
	}
}

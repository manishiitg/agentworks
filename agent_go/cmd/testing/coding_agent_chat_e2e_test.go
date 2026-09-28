package testing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	stdtesting "testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"
)

func TestCodingAgentE2EClientObtainsUserJWTSeparatelyFromMCPToken(st *stdtesting.T) {
	st.Setenv("MCP_API_TOKEN", "must-not-be-used-as-user-jwt")
	var loginAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			loginAuthorization = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "live-user-jwt"})
		case "/api/protected":
			if got := r.Header.Get("Authorization"); got != "Bearer live-user-jwt" {
				st.Errorf("protected Authorization = %q, want user JWT", got)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &codingAgentChatE2EClient{baseURL: server.URL, http: server.Client()}
	if err := client.ensureUserAuth(context.Background()); err != nil {
		st.Fatalf("ensureUserAuth: %v", err)
	}
	if loginAuthorization != "" {
		st.Fatalf("login request unexpectedly sent Authorization %q", loginAuthorization)
	}
	if client.token != "live-user-jwt" {
		st.Fatalf("client token = %q, want user JWT", client.token)
	}
	if err := client.doJSON(context.Background(), http.MethodGet, "/api/protected", "session", nil, nil); err != nil {
		st.Fatalf("protected request: %v", err)
	}
}

func TestDefaultCodingAgentE2EModelIncludesCodingCLIProviders(st *stdtesting.T) {
	tests := map[string]string{
		"codex-cli":   "gpt-6-luna",
		"cursor-cli":  "auto",
		"claude-code": "claude-sonnet-5",
		"muse-cli":    "muse-spark-1.3-contributor",
		"agy-cli":     "gemini-3.8-flash-high",
	}

	for provider, want := range tests {
		st.Run(provider, func(st *stdtesting.T) {
			if got := defaultCodingAgentE2EModel(provider); got != want {
				st.Fatalf("defaultCodingAgentE2EModel(%q) = %q, want %q", provider, got, want)
			}
		})
	}
}

func TestProviderSupportsTmuxLossResumeE2EIncludesNativeResumeProviders(st *stdtesting.T) {
	cases := map[string]bool{
		"claude-code": true,
		"codex-cli":   true,
		"cursor-cli":  false,
		"pi-cli":      false,
	}

	for provider, want := range cases {
		st.Run(provider, func(st *stdtesting.T) {
			if got := providerSupportsTmuxLossResumeE2E(provider); got != want {
				st.Fatalf("providerSupportsTmuxLossResumeE2E(%q) = %v, want %v", provider, got, want)
			}
		})
	}
}

func TestEventsProveProviderRequiresTypedProviderEvent(st *stdtesting.T) {
	events := []map[string]interface{}{
		{
			"type": "user_message",
			"data": map[string]interface{}{
				"data": map[string]interface{}{
					"content": `{"provider":"codex-cli"}`,
				},
			},
		},
		{
			"type": "tool_call_start",
			"data": map[string]interface{}{
				"data": map[string]interface{}{
					"args": `{"provider":"codex-cli"}`,
				},
			},
		},
	}
	if eventsProveProvider(events, "codex-cli") {
		st.Fatalf("provider proof accepted echoed request/tool payload")
	}

	events = append(events, map[string]interface{}{
		"type": "agent_start",
		"data": map[string]interface{}{
			"data": map[string]interface{}{
				"provider": "codex-cli",
			},
		},
	})
	if !eventsProveProvider(events, "codex-cli") {
		st.Fatalf("provider proof rejected typed agent_start provider")
	}

	events = []map[string]interface{}{
		{
			"type": "unified_completion",
			"data": map[string]interface{}{
				"data": map[string]interface{}{
					"metadata": map[string]interface{}{
						"provider": "claude-code",
					},
				},
			},
		},
	}
	if !eventsProveProvider(events, "claude-code") {
		st.Fatalf("provider proof rejected typed unified_completion metadata provider")
	}

	events = []map[string]interface{}{
		{
			"type": "llm_generation_end",
			"data": map[string]interface{}{
				"data": map[string]interface{}{
					"metadata": map[string]interface{}{
						"coding_provider_session_handle": map[string]interface{}{
							"provider": "pi-cli",
						},
					},
				},
			},
		},
	}
	if !eventsProveProvider(events, "pi-cli") {
		st.Fatalf("provider proof rejected typed coding provider session handle")
	}
}

func TestExtractUnifiedCompletionFinalUsesDocumentedShape(st *stdtesting.T) {
	events := []map[string]interface{}{
		{
			"type": "tool_call_end",
			"data": map[string]interface{}{
				"data": map[string]interface{}{
					"final_result": "wrong nested tool value",
				},
			},
		},
		{
			"type": "unified_completion",
			"data": map[string]interface{}{
				"data": map[string]interface{}{
					"final_result": "right answer",
				},
			},
		},
	}
	if got := extractUnifiedCompletionFinal(events); got != "right answer" {
		st.Fatalf("extractUnifiedCompletionFinal() = %q, want right answer", got)
	}
}

func TestRetainedWindowAssertionsRequireOneCanonicalToolAndFinal(st *stdtesting.T) {
	const turnID = "turn-retained-p0"
	events := []map[string]interface{}{
		{
			"type": "streaming_chunk",
			"data": map[string]interface{}{"data": map[string]interface{}{
				"content": "RETAINED_PROGRESS_TOKEN",
			}},
		},
		{
			"type":    "tool_call_start",
			"turn_id": turnID,
			"data": map[string]interface{}{"data": map[string]interface{}{
				"tool_name": "execute_shell_command", "tool_call_id": "call-1",
				"tool_params": map[string]interface{}{"arguments": `{"command":"printf RECEIPT_TOKEN"}`},
			}},
		},
		{
			"type":    "tool_call_end",
			"turn_id": turnID,
			"data": map[string]interface{}{"data": map[string]interface{}{
				"tool_name": "execute_shell_command", "tool_call_id": "call-1",
				"result": "RECEIPT_TOKEN", "duration": float64(12000000),
			}},
		},
		{
			"type":    "unified_completion",
			"turn_id": turnID,
			"data": map[string]interface{}{"data": map[string]interface{}{
				"final_result": "FINAL_TOKEN",
				"metadata": map[string]interface{}{
					"turn_id": turnID, "canonical_turn_completion": true,
				},
			}},
		},
	}
	if err := assertOneRetainedToolReceipt(events, "execute_shell_command", "RECEIPT_TOKEN"); err != nil {
		st.Fatalf("tool receipt: %v", err)
	}
	if err := assertOneRetainedCompletion(events, "FINAL_TOKEN"); err != nil {
		st.Fatalf("completion: %v", err)
	}
	if err := assertCanonicalRetainedTurnIdentity(events, "execute_shell_command"); err != nil {
		st.Fatalf("canonical turn identity: %v", err)
	}
	if err := assertRetainedCompletionAfterTool(events, "execute_shell_command"); err != nil {
		st.Fatalf("lifecycle order: %v", err)
	}

	duplicate := append(append([]map[string]interface{}{}, events...), events[3])
	if err := assertOneRetainedCompletion(duplicate, "FINAL_TOKEN"); err == nil {
		st.Fatalf("duplicate completion was accepted")
	}
	missingDuration := append([]map[string]interface{}{}, events...)
	missingDuration[2] = map[string]interface{}{
		"type": "tool_call_end",
		"data": map[string]interface{}{"data": map[string]interface{}{
			"tool_name": "execute_shell_command", "tool_call_id": "call-1", "result": "RECEIPT_TOKEN",
		}},
	}
	if err := assertOneRetainedToolReceipt(missingDuration, "execute_shell_command", "RECEIPT_TOKEN"); err == nil {
		st.Fatalf("tool receipt without duration was accepted")
	}

	earlyCompletion := []map[string]interface{}{events[0], events[3], events[1], events[2]}
	if err := assertRetainedCompletionAfterTool(earlyCompletion, "execute_shell_command"); err == nil {
		st.Fatalf("completion before the tool receipt was accepted")
	}

	missingTurnID := append([]map[string]interface{}{}, events...)
	missingTurnID[1] = map[string]interface{}{
		"type": "tool_call_start",
		"data": events[1]["data"],
	}
	if err := assertCanonicalRetainedTurnIdentity(missingTurnID, "execute_shell_command"); err == nil {
		st.Fatalf("tool event without turn_id was accepted")
	}

	wrongTurnID := append([]map[string]interface{}{}, events...)
	wrongTurnID[2] = map[string]interface{}{
		"type": "tool_call_end", "turn_id": "different-turn",
		"data": events[2]["data"],
	}
	if err := assertCanonicalRetainedTurnIdentity(wrongTurnID, "execute_shell_command"); err == nil {
		st.Fatalf("cross-turn tool event was accepted")
	}

	missingCanonicalMarker := append([]map[string]interface{}{}, events...)
	missingCanonicalMarker[3] = map[string]interface{}{
		"type": "unified_completion", "turn_id": turnID,
		"data": map[string]interface{}{"data": map[string]interface{}{
			"final_result": "FINAL_TOKEN", "metadata": map[string]interface{}{"turn_id": turnID},
		}},
	}
	if err := assertCanonicalRetainedTurnIdentity(missingCanonicalMarker, "execute_shell_command"); err == nil {
		st.Fatalf("completion without canonical marker was accepted")
	}
}

func TestCodingAgentFinalJudgePromptIncludesAppLevelContract(st *stdtesting.T) {
	prompt := buildCodingAgentFinalJudgePrompt(codingAgentFinalJudgeCase{
		Provider:     "codex-cli",
		RawContext:   "raw terminal\nunified_completion noise",
		Extracted:    "DONE_TOKEN",
		UserGoal:     "Reply with DONE_TOKEN.",
		MustContain:  []string{"DONE_TOKEN"},
		Forbidden:    []string{"unified_completion"},
		ExpectedNote: "app-level final result",
	})
	for _, want := range []string{
		"Provider: codex-cli",
		"Reply with DONE_TOKEN.",
		"Must contain these fragments: DONE_TOKEN",
		"Must not contain these noise fragments: unified_completion",
		"EXTRACTED_FINAL:",
		"DONE_TOKEN",
	} {
		if !strings.Contains(prompt, want) {
			st.Fatalf("judge prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestExtractCodingAgentJudgeJSONObjectAllowsWrappedJSON(st *stdtesting.T) {
	input := "judge output\n```json\n{\"pass\":true}\n```\n"
	if got := extractCodingAgentJudgeJSONObject(input); got != `{"pass":true}` {
		st.Fatalf("extractCodingAgentJudgeJSONObject() = %q", got)
	}
}

func TestDefaultCodingAgentFinalJudgeForbiddenIncludesAppNoise(st *stdtesting.T) {
	forbidden := defaultCodingAgentFinalJudgeForbidden()
	for _, want := range []string{"unified_completion", "tool_call_start", "execute_shell_command", "tmux_session"} {
		found := false
		for _, got := range forbidden {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			st.Fatalf("default forbidden list missing %q: %v", want, forbidden)
		}
	}
}

func TestMergeCodingAgentFinalJudgeForbiddenAddsDynamicPriorTurnNoise(st *stdtesting.T) {
	forbidden := mergeCodingAgentFinalJudgeForbidden([]string{"OLD_FINAL_TOKEN", "execute_shell_command", ""})
	counts := map[string]int{}
	for _, got := range forbidden {
		counts[got]++
	}
	if counts["OLD_FINAL_TOKEN"] != 1 {
		st.Fatalf("merged forbidden list missing dynamic prior-turn token: %v", forbidden)
	}
	if counts["execute_shell_command"] != 1 {
		st.Fatalf("merged forbidden list should de-dupe default entries: %v", forbidden)
	}
}

func TestCodingAgentVertexJudgeModelDefaultsToGemini31Pro(st *stdtesting.T) {
	old := codingAgentChatE2EFlags.vertexJudgeModel
	codingAgentChatE2EFlags.vertexJudgeModel = ""
	st.Cleanup(func() { codingAgentChatE2EFlags.vertexJudgeModel = old })
	st.Setenv("VERTEX_FINAL_EXTRACTION_JUDGE_MODEL", "")

	if got := codingAgentVertexJudgeModel(); got != vertex.ModelGemini31ProPreview {
		st.Fatalf("codingAgentVertexJudgeModel() = %q, want %q", got, vertex.ModelGemini31ProPreview)
	}
}

package mcpagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

func TestClaudeNativeQuestionsEmittedOptions(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token")
	for _, enforced := range []bool{false, true} {
		for _, mode := range []string{codingAgentToolsMCPOnly, codingAgentToolsFull} {
			for _, scenario := range []string{"attended", "unattended", "nonpersistent", "structured", "unregistered", "unadvertised", "sdk", "sdk_unregistered"} {
				t.Run(fmt.Sprintf("%s/%s/enforced=%v", mode, scenario, enforced), func(t *testing.T) {
					t.Setenv("MCPAGENT_CLAUDE_ENFORCE_HTTP_TOOL_ROUTING", fmt.Sprint(enforced))
					sdk := strings.HasPrefix(scenario, "sdk")
					if sdk {
						t.Setenv("CLAUDE_CODE_TRANSPORT", "sdk")
					}
					a := bridgeTestAgent()
					a.provider = "claude-code"
					a.sessionID = "claude-question-test"
					a.codingAgentToolsMode, a.cliSecurityPolicy = mode, confinedTestPolicy()
					if !sdk {
						a.claudeCodeTransport = llm.ClaudeCodeTransportTmux
					}
					a.userAnswersNativeQuestions = scenario != "unattended"
					a.claudeCodePersistentInteractiveSession = scenario != "nonpersistent" && !sdk
					if scenario == "structured" {
						a.codingAgentTransport = llm.CodingAgentTransportStructured
					}
					if scenario != "unadvertised" && scenario != "sdk_unregistered" {
						a.additionalBridgeTools = []string{"request_clarification"}
					}
					if scenario != "unregistered" && scenario != "sdk_unregistered" {
						if err := a.registerDirectTool("request_clarification", "Ask for choices", map[string]interface{}{"type": "object"}, func(context.Context, map[string]interface{}) (string, error) { return "", nil }, time.Minute, "human_tools"); err != nil {
							t.Fatal(err)
						}
					}
					opts, err := a.appendClaudeCodeIntegrationOptions(nil, LLMModel{})
					if err != nil {
						t.Fatal(err)
					}
					metadata := metadataFromCallOptions(opts)
					// The tool list no longer carries request_clarification: the terminal hook needs only its
					// registered answer route, and the SDK answers on the turn's question handler.
					want := scenario == "attended" || scenario == "unadvertised" || sdk
					for _, key := range []string{claudecode.MetadataKeyTools, claudecode.MetadataKeyAllowedTools} {
						tools, _ := metadata[key].(string)
						if slices.Contains(strings.Split(tools, ","), "AskUserQuestion") != want {
							t.Fatalf("%s=%s, native question enabled=%v", key, tools, want)
						}
					}
					// #3711: in plan mode Claude said "No such tool available: ExitPlanMode", so a plan could never
					// reach the Approve / Reject card. The SDK offers it with the card; it is never pre-approved.
					tools, _ := metadata[claudecode.MetadataKeyTools].(string)
					allowed, _ := metadata[claudecode.MetadataKeyAllowedTools].(string)
					if slices.Contains(strings.Split(tools, ","), "ExitPlanMode") != (want && sdk) || slices.Contains(strings.Split(allowed, ","), "ExitPlanMode") {
						t.Fatalf("ExitPlanMode tools=%s allowed=%s, want offered=%v", tools, allowed, want && sdk)
					}
					settings, _ := metadata[claudecode.MetadataKeySettings].(string)
					if strings.Contains(settings, `"matcher":"AskUserQuestion"`) != (want && !sdk) {
						t.Fatalf("question hook settings enabled=%v: %s", want, settings)
					}
					if enforced && !strings.Contains(settings, `"matcher":"*"`) {
						t.Fatal("native question hook removed the existing routing policy")
					}
				})
			}
		}
	}
}

func nativeQuestionTestConfig(url, session string) string {
	data, _ := json.Marshal(map[string]interface{}{"mcpServers": map[string]interface{}{"api-bridge": map[string]interface{}{"env": map[string]string{"MCP_API_URL": url, "MCP_API_TOKEN": "question-test-token", "MCP_SESSION_ID": session}}}})
	return string(data)
}

func nativeQuestionTestCommand(t *testing.T, settings string) string {
	t.Helper()
	var config struct {
		Hooks struct {
			Pre []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string
					Timeout int
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(settings), &config); err != nil {
		t.Fatal(err)
	}
	for _, item := range config.Hooks.Pre {
		if item.Matcher == "AskUserQuestion" {
			if item.Hooks[0].Timeout <= 1805 || strings.Contains(item.Hooks[0].Command, "question-test-token") {
				t.Fatal("hook timeout or credential handling is invalid")
			}
			return item.Hooks[0].Command
		}
	}
	t.Fatal("native question hook missing")
	return ""
}

func runNativeQuestionHook(t *testing.T, config, payload string) map[string]interface{} {
	t.Helper()
	settings, err := BuildClaudeNativeQuestionSettings(config, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", nativeQuestionTestCommand(t, settings)) // #nosec G204 -- Execute the production hook command generated from trusted test configuration; payload data is sent only on stdin.
	cmd.Stdin = strings.NewReader(payload)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid hook reply: %s", out.String())
	}
	return result["hookSpecificOutput"].(map[string]interface{})
}

const nativeQuestionTestInput = `{"tool_name":"AskUserQuestion","session_id":"foreign-native-id","tool_use_id":"toolu_one","tool_input":{"questions":[{"header":"Scope","question":"Which scope?","options":[{"label":"Sheets","description":"Keep sheets"},{"label":"Gmail"}],"multiSelect":false},{"question":"Which features?","options":[{"label":"Read"},{"label":"Send"}],"multiSelect":true}],"answers":{"Which scope?":"fabricated answer"}}}`

func TestClaudeNativeQuestionHookMapsSubmittedAnswers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tools/custom/request_clarification" || r.Header.Get("X-Session-ID") != "owner" || r.Header.Get("Authorization") != "Bearer question-test-token" {
			t.Error("hook used a payload-controlled session or lost authentication")
		}
		var args struct {
			Questions []struct {
				ID, Question string
				Multi        bool `json:"multi_select"`
				Other        bool `json:"allow_other"`
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil || len(args.Questions) != 2 || args.Questions[0].ID != "question-1" || !args.Questions[1].Multi || !args.Questions[1].Other {
			t.Error("native questions were not converted to the UI contract")
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": `{"status":"answered","answers":[{"id":"question-1","other_text":"Keep both"},{"id":"question-2","selected_labels":["Read","Send"]}]}`})
	}))
	defer server.Close()
	output := runNativeQuestionHook(t, nativeQuestionTestConfig(server.URL, "owner"), nativeQuestionTestInput)
	if output["permissionDecision"] != "allow" {
		t.Fatalf("submitted choice denied: %v", output)
	}
	updated := output["updatedInput"].(map[string]interface{})
	answers := updated["answers"].(map[string]interface{})
	if answers["Which scope?"] != "Keep both" || answers["Which features?"] != "Read, Send" {
		t.Fatalf("native answer format: %v", answers)
	}
	if len(updated["questions"].([]interface{})) != 2 {
		t.Fatal("original native question input was dropped")
	}
}

func TestClaudeNativeQuestionHookRefusesUnsubmittedOrInvalidAnswers(t *testing.T) {
	for _, response := range []string{
		`{"success":false,"error":"cancelled"}`,
		`{"success":true,"result":{"status":"interrupted","answers":[]}}`,
		`{"success":true,"result":{"status":"answered","answers":[]}}`,
		`{"success":true,"result":{"status":"answered","answers":[{"id":"question-1","selected_labels":["not an option"]},{"id":"question-2","selected_labels":["Read"]}]}}`,
		`{"success":true,"result":{"status":"answered","answers":[{"id":"old-question","selected_labels":["Sheets"]},{"id":"question-2","selected_labels":["Read"]}]}}`,
	} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) }))
			defer server.Close()
			output := runNativeQuestionHook(t, nativeQuestionTestConfig(server.URL, "owner"), nativeQuestionTestInput)
			if output["permissionDecision"] != "deny" || output["updatedInput"] != nil {
				t.Fatalf("invalid answers were accepted: %v", output)
			}
		})
	}
}

func TestClaudeNativeQuestionHookPrivateAndSessionScoped(t *testing.T) {
	a, err := BuildClaudeNativeQuestionSettings(nativeQuestionTestConfig("http://localhost:8080", "one"), "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildClaudeNativeQuestionSettings(nativeQuestionTestConfig("http://localhost:8080", "two"), "")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("different chats share question hook credentials")
	}
	command := nativeQuestionTestCommand(t, a)
	path := strings.TrimSuffix(strings.Split(command, " ")[1], "'")
	path = strings.TrimPrefix(path, "'")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("hook credentials are not private: %v", err)
	}
	if _, err := BuildClaudeNativeQuestionSettings(`{"mcpServers":{}}`, ""); err == nil {
		t.Fatal("sessionless question hook accepted")
	}
}

// Claude may send an option label with surrounding spaces; the clarification server trims labels, so the
// hook must trim them the same way or a valid answer would be denied.
func TestClaudeNativeQuestionHookAcceptsLabelsWithSurroundingSpaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args struct {
			Questions []struct {
				Options []struct{ Label string } `json:"options"`
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil || len(args.Questions) != 1 || args.Questions[0].Options[0].Label != "Alpha" {
			t.Errorf("labels were not trimmed before they reached the server: %+v", args)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": `{"status":"answered","answers":[{"id":"question-1","selected_labels":["Alpha"]}]}`})
	}))
	defer server.Close()
	input := `{"tool_name":"AskUserQuestion","tool_input":{"questions":[{"header":"H","question":"Pick one?","options":[{"label":" Alpha ","description":"a"},{"label":"Beta ","description":"b"}]}]}}`
	output := runNativeQuestionHook(t, nativeQuestionTestConfig(server.URL, "owner"), input)
	if output["permissionDecision"] != "allow" {
		t.Fatalf("an answer for a label with surrounding spaces was denied: %v", output)
	}
	if answers := output["updatedInput"].(map[string]interface{})["answers"].(map[string]interface{}); answers["Pick one?"] != "Alpha" {
		t.Fatalf("answer = %v", answers)
	}
}

// Without python3 the hook cannot run; preparing it must fail cleanly so the caller can turn native questions
// off for the turn, rather than failing the whole turn.
func TestPrepareClaudeNativeQuestionHookFailsCleanlyWithoutPython(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := prepareClaudeNativeQuestionHook(nativeQuestionTestConfig("http://127.0.0.1:1", "owner")); err == nil || !strings.Contains(err.Error(), "python3") {
		t.Fatalf("err = %v, want a python3 error", err)
	}
	if _, err := BuildClaudeNativeQuestionSettings(nativeQuestionTestConfig("http://127.0.0.1:1", "owner"), ""); err == nil {
		t.Fatal("BuildClaudeNativeQuestionSettings succeeded without python3")
	}
}

// Hook scripts hold a session token. Scripts of chats that are long gone are removed; recent ones and
// files of other kinds are left alone.
func TestSweepClaudeNativeQuestionHooksRemovesOnlyOldScripts(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, age time.Duration) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-age)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		return path
	}
	oldScript := write("ask-user-question-aaaa.py", 48*time.Hour)
	freshScript := write("ask-user-question-bbbb.py", time.Hour)
	otherOld := write("routing-hook-cccc.py", 48*time.Hour)
	sweepClaudeNativeQuestionHooks(dir, time.Now().Add(-claudeNativeQuestionHookMaxAge))
	if _, err := os.Stat(oldScript); !os.IsNotExist(err) {
		t.Error("an old question hook script was kept")
	}
	if _, err := os.Stat(freshScript); err != nil {
		t.Error("a recent question hook script was removed")
	}
	if _, err := os.Stat(otherOld); err != nil {
		t.Error("a file that is not a question hook script was removed")
	}
}

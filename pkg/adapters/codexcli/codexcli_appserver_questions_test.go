package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"strings"
	"testing"
	"time"
)

// PLAT-872-21: preserve native question IDs/custom answers; reject cross-turn and secret prompts.
func TestCodexNativeQuestionRoutesAnswersAndRejectsWrongTurn(t *testing.T) {
	ctx := llmtypes.WithCodingAgentQuestionHandler(context.Background(), "codex-cli", "owner", func(_ context.Context, args map[string]any) (string, error) {
		if len(args["questions"].([]any)) != 1 {
			t.Fatal("missing question")
		}
		return `{"status":"answered","answers":[{"id":"choice","other_text":"custom answer"}]}`, nil
	})
	raw := json.RawMessage(`{"threadId":"thread","turnId":"turn","questions":[{"id":"choice","question":"Choose?","isOther":true,"options":[{"label":"First"}]}]}`)
	result, err := codexNativeQuestion(ctx, "item/tool/requestUserInput", raw, "thread", "turn", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(result)
	if string(b) != `{"answers":{"choice":{"answers":["custom answer"]}}}` {
		t.Fatalf("%s", b)
	}
	if _, err = codexNativeQuestion(ctx, "item/tool/requestUserInput", raw, "other", "turn", nil); err == nil {
		t.Fatal("cross-thread request accepted")
	}
	if _, err = codexNativeQuestion(context.Background(), "item/tool/requestUserInput", raw, "thread", "turn", nil); err == nil {
		t.Fatal("unattended request accepted")
	}
	raw = json.RawMessage(`{"threadId":"thread","turnId":"turn","questions":[{"id":"secret","question":"Secret?","isSecret":true}]}`)
	if _, err = codexNativeQuestion(ctx, "item/tool/requestUserInput", raw, "thread", "turn", nil); err == nil {
		t.Fatal("secret entered ordinary transcript")
	}
}

func TestCodexAppServerNativeQuestionLive(t *testing.T) {
	owner, opts := appServerTestOptions(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	asked := 0
	ctx = llmtypes.WithCodingAgentQuestionHandler(ctx, "codex-cli", owner, func(_ context.Context, args map[string]any) (string, error) {
		asked++
		qs := args["questions"].([]any)
		answers := []any{}
		for _, raw := range qs {
			q := raw.(map[string]any)
			answers = append(answers, map[string]any{"id": q["id"], "selected_labels": []string{"Blue"}})
		}
		b, _ := json.Marshal(map[string]any{"status": "answered", "answers": answers})
		return string(b), nil
	})
	resp, _, err := appServerTestTurn(t, ctx, opts, "Use request_user_input for the user's requested question. Do not read files or call any other tool.", "Use your native request_user_input tool to ask Which color? with options Blue and Green. Wait for my answer then reply exactly CHOSEN_<color>.")
	if err != nil {
		t.Fatal(err)
	}
	if asked != 1 || strings.TrimSpace(resp.Choices[0].Content) != "CHOSEN_Blue" {
		t.Fatalf("asked=%d response=%s", asked, resp.Choices[0].Content)
	}
}

// PLAT-872-54: command approvals must return one native decision, not an
// unsupported-request error or a persistent permission grant.
func TestCodexCommandApprovalUsesScopedAnswer(t *testing.T) {
	raw := json.RawMessage(`{"threadId":"thread","turnId":"turn","itemId":"item","command":"printf hello","cwd":"/work","availableDecisions":["accept","acceptForSession","decline","cancel"]}`)
	for _, choice := range []string{"Allow", "Deny", "cancel"} {
		t.Run(choice, func(t *testing.T) {
			asked := 0
			ctx := llmtypes.WithCodingAgentQuestionHandler(context.Background(), "codex-cli", "chat", func(_ context.Context, args map[string]any) (string, error) {
				asked++
				q := args["questions"].([]any)[0].(map[string]any)
				if !strings.Contains(q["question"].(string), "printf hello") {
					t.Fatal("missing command")
				}
				if choice == "cancel" {
					return "", context.Canceled
				}
				return fmt.Sprintf(`{"status":"answered","answers":[{"id":"command_permission","selected_labels":[%q]}]}`, choice), nil
			})
			out, err := codexNativeQuestion(ctx, "item/commandExecution/requestApproval", raw, "thread", "turn", nil)
			if err != nil {
				t.Fatal(err)
			}
			want := "decline"
			if choice == "Allow" {
				want = "accept"
			}
			if asked != 1 || out.(map[string]any)["decision"] != want {
				t.Fatalf("asked=%d response=%v", asked, out)
			}
			if _, err = codexNativeQuestion(ctx, "item/commandExecution/requestApproval", raw, "other", "turn", nil); err == nil || asked != 1 {
				t.Fatal("cross-thread approval reached card")
			}
			out, err = codexNativeQuestion(context.Background(), "item/commandExecution/requestApproval", raw, "thread", "turn", nil)
			if err != nil || out.(map[string]any)["decision"] != "decline" {
				t.Fatal("unattended request not declined")
			}
			empty := json.RawMessage(`{"threadId":"thread","turnId":"turn","itemId":"item","command":null,"networkApprovalContext":null}`)
			out, err = codexNativeQuestion(ctx, "item/commandExecution/requestApproval", empty, "thread", "turn", nil)
			if err != nil || out.(map[string]any)["decision"] != "decline" || asked != 1 {
				t.Fatal("empty nullable command reached approval card")
			}
			limited := json.RawMessage(strings.Replace(string(raw), `"accept",`, "", 1))
			out, err = codexNativeQuestion(ctx, "item/commandExecution/requestApproval", limited, "thread", "turn", nil)
			if err != nil || out.(map[string]any)["decision"] != "decline" || asked != 1 {
				t.Fatal("single approval became a session grant")
			}
		})
	}
}

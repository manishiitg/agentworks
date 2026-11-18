package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3133: a file edit Claude asks permission for shows the file and the change;
// an MCP form goes back as typed content.
func TestClaudeFileChangeApprovalAndFormRequest(t *testing.T) {
	input := json.RawMessage(`{"file_path":"/work/a.go","old_string":"old","new_string":"new"}`)
	for _, choice := range []string{"Allow", "Deny"} {
		ctx := llmtypes.WithCodingAgentQuestionHandler(context.Background(), "claude-code", "chat", func(_ context.Context, args map[string]any) (string, error) {
			q := args["questions"].([]any)[0].(map[string]any)["question"].(string)
			if !strings.Contains(q, "/work/a.go") || !strings.Contains(q, "- old") || !strings.Contains(q, "+ new") {
				return "", fmt.Errorf("card does not show the change: %q", q)
			}
			return fmt.Sprintf(`{"status":"answered","answers":[{"id":"command_permission","selected_labels":[%q]}]}`, choice), nil
		})
		got := claudeSDKPermission(ctx, "Edit", input)["behavior"]
		if want := map[string]string{"Allow": "allow", "Deny": "deny"}[choice]; got != want {
			t.Fatalf("%s: %v", choice, got)
		}
	}
	if claudeSDKPermission(context.Background(), "Write", json.RawMessage(`{"file_path":"/work/b.go","content":"x"}`))["behavior"] != "deny" {
		t.Fatal("unattended file change allowed")
	}

	ctx := llmtypes.WithCodingAgentQuestionHandler(context.Background(), "claude-code", "chat", func(_ context.Context, args map[string]any) (string, error) {
		if args["native_request_kind"] != "form" {
			return "", fmt.Errorf("form shown as %v", args["native_request_kind"])
		}
		return `{"status":"answered","answers":[{"id":"name","other_text":"Ada"}]}`, nil
	})
	out := claudeSDKElicitation(ctx, json.RawMessage(`{"serverName":"example-server","message":"Who?","mode":"form","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}}`))
	b, _ := json.Marshal(out)
	if string(b) != `{"action":"accept","content":{"name":"Ada"}}` {
		t.Fatalf("form answer %s", b)
	}
	if claudeSDKElicitation(context.Background(), json.RawMessage(`{"serverName":"example-server","mode":"url","url":"https://example.com"}`))["action"] != "cancel" {
		t.Fatal("unattended link request accepted")
	}
}

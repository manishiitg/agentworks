package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3133: Codex's file-change approval names the files it will touch and is
// answered once; a form request from an MCP server goes back as typed content.
func TestCodexFileChangeApprovalAndFormRequest(t *testing.T) {
	var files codexFileChanges
	files.observe(json.RawMessage(`{"id":"item","type":"fileChange","status":"inProgress","changes":[{"path":"/work/a.go","kind":{"type":"update"},"diff":"@@ -1 +1 @@\n-old\n+new\n"}]}`))
	raw := json.RawMessage(`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1}`)
	for _, choice := range []string{"Allow", "Deny"} {
		ctx := llmtypes.WithCodingAgentQuestionHandler(context.Background(), "codex-cli", "chat", func(_ context.Context, args map[string]any) (string, error) {
			q := args["questions"].([]any)[0].(map[string]any)["question"].(string)
			if !strings.Contains(q, "edit /work/a.go") || !strings.Contains(q, "+new") || args["native_request_kind"] != "command_approval" {
				return "", fmt.Errorf("card does not show the change: %q", q)
			}
			return fmt.Sprintf(`{"status":"answered","answers":[{"id":"command_permission","selected_labels":[%q]}]}`, choice), nil
		})
		out, err := codexNativeQuestion(ctx, "item/fileChange/requestApproval", raw, "thread", "turn", &files)
		want := map[string]string{"Allow": "accept", "Deny": "decline"}[choice]
		if err != nil || out.(map[string]any)["decision"] != want {
			t.Fatalf("%s: %v %v", choice, out, err)
		}
	}
	// An item whose changes were never seen is declined, never approved blind.
	out, err := codexNativeQuestion(context.Background(), "item/fileChange/requestApproval", json.RawMessage(`{"threadId":"thread","turnId":"turn","itemId":"unknown","startedAtMs":1}`), "thread", "turn", &files)
	if err != nil || out.(map[string]any)["decision"] != "decline" {
		t.Fatalf("blind approval: %v %v", out, err)
	}

	form := json.RawMessage(`{"threadId":"thread","turnId":"turn","serverName":"example-server","mode":"form","message":"Pick a size","requestedSchema":{"type":"object","properties":{"size":{"type":"string","enum":["s","l"]}},"required":["size"]}}`)
	ctx := llmtypes.WithCodingAgentQuestionHandler(context.Background(), "codex-cli", "chat", func(_ context.Context, args map[string]any) (string, error) {
		if args["native_request_kind"] != "form" {
			return "", fmt.Errorf("form shown as %v", args["native_request_kind"])
		}
		return `{"status":"answered","answers":[{"id":"size","selected_labels":["l"]}]}`, nil
	})
	out, err = codexNativeQuestion(ctx, "mcpServer/elicitation/request", form, "thread", "turn", &files)
	b, _ := json.Marshal(out)
	if err != nil || string(b) != `{"action":"accept","content":{"size":"l"}}` {
		t.Fatalf("form answer %s %v", b, err)
	}
	if _, err = codexNativeQuestion(ctx, "mcpServer/elicitation/request", form, "other", "turn", &files); err == nil {
		t.Fatal("form from another thread answered")
	}
}

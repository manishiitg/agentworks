package nativeinput

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"strings"
	"testing"
	"time"
)

// PLAT-872-21: native resolution/turn completion must close pending UI waits.
func TestRequestsCancelOnlyMatchingQuestionAndCloseRemaining(t *testing.T) {
	var r Requests
	first, second := make(chan struct{}), make(chan struct{})
	r.Start(context.Background(), "first", func(ctx context.Context) { <-ctx.Done(); close(first) })
	r.Start(context.Background(), "second", func(ctx context.Context) { <-ctx.Done(); close(second) })
	r.Cancel("first")
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("resolved question stayed pending")
	}
	select {
	case <-second:
		t.Fatal("unrelated question cancelled")
	default:
	}
	r.Close()
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("turn exit left question pending")
	}
}

// PLAT-872-54: permission cards must not expose common inline credentials.
func TestCommandApprovalDisplayMasksInlineCredentials(t *testing.T) {
	for _, command := range []string{
		`API_TOKEN="private-fixture" printf ok`,
		`curl -H "Authorization: Bearer private-fixture" https://example.com`,
		`curl --token private-fixture https://example.com`,
		`tool --password='private-fixture'`,
		`mysql -u user -pprivate-fixture`,
		`curl https://user:private-fixture@example.com`,
	} {
		if got := approvalDisplay(command); strings.Contains(got, "private-fixture") || !strings.Contains(got, "[hidden]") {
			t.Fatalf("credential masking failed for %q", command)
		}
	}
	if got := approvalDisplay("printf hello"); got != "printf hello" {
		t.Fatal("ordinary command changed")
	}
}

// #3076: an approval must never become a recoverable ordinary question just
// because both use the same attended card and answer channel.
func TestNativeRequestKindKeepsApprovalsDistinctFromQuestions(t *testing.T) {
	for _, kind := range []RequestKind{QuestionRequest, CommandApprovalRequest, PlanApprovalRequest} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := llmtypes.WithCodingAgentQuestionHandler(context.Background(), "example-provider", "chat", func(_ context.Context, args map[string]any) (string, error) {
				if args["native_request_kind"] != string(kind) {
					return "", fmt.Errorf("wrong request kind: %v", args)
				}
				return `{"status":"answered","answers":[{"id":"choice","selected_labels":["Keep"]}]}`, nil
			})
			q := []Question{{ID: "choice", Question: "Which one?", Options: []Option{{Label: "Keep"}}}}
			var answers []Answer
			var err error
			if kind == QuestionRequest {
				answers, err = Ask(ctx, q)
			} else {
				answers, err = AskWithKind(ctx, kind, q)
			}
			if err != nil || len(answers) != 1 || answers[0].SelectedLabels[0] != "Keep" {
				t.Fatalf("answer changed: %v %v", answers, err)
			}
		})
	}
	ctx := llmtypes.WithCodingAgentQuestionHandler(context.Background(), "example-provider", "chat", func(_ context.Context, args map[string]any) (string, error) {
		if args["native_request_kind"] != string(CommandApprovalRequest) {
			return "", fmt.Errorf("command approval mislabeled")
		}
		b, _ := json.Marshal(map[string]any{"status": "answered", "answers": []Answer{{ID: "command_permission", SelectedLabels: []string{"Allow"}}}})
		return string(b), nil
	})
	if allowed, err := ApproveCommand(ctx, "printf hello"); err != nil || !allowed {
		t.Fatalf("native approval answer changed: %t %v", allowed, err)
	}
}

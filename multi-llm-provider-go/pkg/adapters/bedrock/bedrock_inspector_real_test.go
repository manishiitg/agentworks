package bedrock

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// requireBedrockRealE2E gates every real-API test in this file on
// RUN_BEDROCK_REAL_E2E=1. AWS credentials are picked up through the
// SDK's default credential chain (env vars, shared config profile,
// SSO, IMDS, etc.), so callers can either export AWS_ACCESS_KEY_ID/
// AWS_SECRET_ACCESS_KEY directly or rely on AWS_PROFILE. Region
// defaults to us-east-1 when AWS_REGION is unset.
//
// Returns the resolved model ID.
func requireBedrockRealE2E(t *testing.T) string {
	t.Helper()
	if os.Getenv("RUN_BEDROCK_REAL_E2E") == "" {
		t.Skip("set RUN_BEDROCK_REAL_E2E=1 to run real Bedrock Converse API tests")
	}
	model := strings.TrimSpace(os.Getenv("BEDROCK_REAL_E2E_MODEL"))
	if model == "" {
		// Uses the direct API integration fixture model
		// suite. Override with BEDROCK_REAL_E2E_MODEL when
		// validating a different model.
		model = "global.anthropic.claude-sonnet-4-5-20250929-v1:0"
	}
	return model
}

func newRealBedrockAdapter(t *testing.T) (*BedrockAdapter, string) {
	t.Helper()
	model := requireBedrockRealE2E(t)
	region := strings.TrimSpace(os.Getenv("AWS_REGION"))
	if region == "" {
		region = "us-east-1"
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(region))
	if err != nil {
		t.Fatalf("aws LoadDefaultConfig: %v", err)
	}
	client := bedrockruntime.NewFromConfig(cfg)
	return NewBedrockAdapter(client, model, &MockLogger{}), model
}

// TestBedrockRealInspectorContract is the per-adapter regression for
// the inspector emission contract. Same shape as the anthropic test:
// makes a real Converse API call with an attached InspectorRecorder
// and asserts the canonical event stream:
//
//  1. exactly one request event (first, monotonic Seq=1)
//  2. zero or more event events (bedrock currently emits only the
//     request+completion bookends via WithObservability, no mid-stream
//     events — that's allowed by the contract)
//  3. exactly one completion event (last)
//  4. no error event
//  5. every event carries Provider="bedrock" and Model
//  6. Seq is monotonically increasing without gaps
//  7. completion metadata includes prompt_tokens, completion_tokens,
//     stop_reason, duration_ms
//
// Gated on RUN_BEDROCK_REAL_E2E=1 + AWS credentials resolvable through
// the default credential chain.
func TestBedrockRealInspectorContract(t *testing.T) {
	adapter, model := newRealBedrockAdapter(t)

	rec := &llmtypes.InspectorRecorder{}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with OK."}}},
		},
		llmtypes.WithMaxTokens(16),
		llmtypes.WithInspectorSink(rec),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("no choices in response")
	}

	events := rec.Events()
	assertBedrockInspectorContract(t, events, "bedrock", model)
}

// assertBedrockInspectorContract mirrors the shared assertion used by
// the per-adapter inspector tests and the cross-adapter matrix test.
// Kept package-local for the same reason anthropic does: each adapter
// package owns its own copy so the contract is enforced symmetrically
// without cross-package test deps.
func assertBedrockInspectorContract(t *testing.T, events []llmtypes.InspectorEvent, wantProvider, wantModel string) {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no inspector events emitted; adapter is not wired up")
	}

	if events[0].Phase != llmtypes.InspectorPhaseRequest {
		t.Fatalf("first event phase = %q, want %q", events[0].Phase, llmtypes.InspectorPhaseRequest)
	}
	last := events[len(events)-1]
	if last.Phase != llmtypes.InspectorPhaseCompletion {
		t.Fatalf("last event phase = %q, want %q (events: %v)", last.Phase, llmtypes.InspectorPhaseCompletion, bedrockPhaseSequence(events))
	}
	for _, ev := range events {
		if ev.Phase == llmtypes.InspectorPhaseError {
			t.Fatalf("unexpected error event: %+v", ev)
		}
	}

	for i, ev := range events {
		if ev.Provider != wantProvider {
			t.Fatalf("events[%d].Provider = %q, want %q", i, ev.Provider, wantProvider)
		}
		if ev.Model != wantModel {
			t.Fatalf("events[%d].Model = %q, want %q", i, ev.Model, wantModel)
		}
		if ev.Timestamp.IsZero() {
			t.Fatalf("events[%d].Timestamp is zero", i)
		}
	}

	for i, ev := range events {
		if ev.Seq != i+1 {
			t.Fatalf("events[%d].Seq = %d, want %d (Seq must be 1-based monotonic)", i, ev.Seq, i+1)
		}
	}

	req := events[0]
	if req.Metadata["message_count"] == nil {
		t.Fatal("request event missing required key 'message_count'")
	}
	if last.Metadata["prompt_tokens"] == nil {
		t.Fatal("completion event missing required key 'prompt_tokens'")
	}
	if last.Metadata["completion_tokens"] == nil {
		t.Fatal("completion event missing required key 'completion_tokens'")
	}
	if last.Metadata["stop_reason"] == nil {
		t.Fatal("completion event missing required key 'stop_reason'")
	}
	if last.Metadata["duration_ms"] == nil {
		t.Fatal("completion event missing required key 'duration_ms'")
	}

	t.Logf("✅ %s inspector contract: %d events (%v)", wantProvider, len(events), bedrockPhaseSequence(events))
}

func bedrockPhaseSequence(events []llmtypes.InspectorEvent) []llmtypes.InspectorPhase {
	out := make([]llmtypes.InspectorPhase, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Phase)
	}
	return out
}

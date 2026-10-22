package costobserver

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

func TestModelRequestLogsKeepUnitsAndExcludeContent(t *testing.T) {
	ledger, err := costledger.NewSQLiteLedger(t.TempDir() + "/costs.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	const private = "private-content-must-not-be-logged"
	observer := New(ledger, "session-request", "owner", "workshop", WithModel("agy-cli", "gemini-test"),
		WithAttribution(ScopeSchedule, "Workflow/example", "run-1", "execution-1"),
		WithSource("workflow-schedule:test", private, "occurrence-1"),
		WithProductAttribution(costledger.Attribution{Activity: "scheduled_run", ProductKind: "workflow", ProductID: "workflow-1", ProductName: private}))
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	now := time.Now().UTC()
	for _, event := range []*unifiedevents.AgentEvent{
		{Type: unifiedevents.LLMGenerationStart, Timestamp: now, SpanID: "success", Data: &unifiedevents.LLMGenerationStartEvent{ModelID: "gemini-test"}},
		{Type: unifiedevents.LLMGenerationStart, Timestamp: now, SpanID: "failure", Data: &unifiedevents.LLMGenerationStartEvent{ModelID: "gemini-test"}},
		{Type: unifiedevents.LLMGenerationError, Timestamp: now, SpanID: "failure", Data: &unifiedevents.LLMGenerationErrorEvent{ModelID: "gemini-test", Error: private}},
	} {
		if err := observer.HandleEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	before, err := ledger.Summarize("", "")
	if err != nil || before.Total.CallCount != 0 {
		t.Fatal("request logging must not charge starts or failed requests")
	}
	completed := &unifiedevents.AgentEvent{Type: unifiedevents.LLMGenerationEnd, Timestamp: now, SpanID: "success", Data: &unifiedevents.LLMGenerationEndEvent{
		Content: private, UsageMetrics: unifiedevents.UsageMetrics{PromptTokens: 10, CompletionTokens: 5},
		BaseEventData: unifiedevents.BaseEventData{Metadata: map[string]interface{}{"llm_call_count": 3, "llm_call_count_source": "native_model_generations", "secret": private}},
	}}
	if err := observer.HandleEvent(context.Background(), completed); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if strings.Contains(text, private) {
		t.Fatal("request logging exposed content, error text or authored labels")
	}
	for _, want := range []string{"stage=started unit=native_turn", "stage=failed unit=native_turn", "stage=completed unit=native_turn", `source_id="workflow-schedule:test"`, `source_run_id="occurrence-1"`, "recorded_generations=3 count_basis=native_model_generations"} {
		if !strings.Contains(text, want) {
			t.Fatalf("request log missing %q: %s", want, text)
		}
	}
	after, err := ledger.Summarize("", "")
	if err != nil || after.Total.CallCount != 3 || after.Total.AccountingEventCount != 1 {
		t.Fatalf("logging changed usage accounting: %+v, %v", after, err)
	}
}

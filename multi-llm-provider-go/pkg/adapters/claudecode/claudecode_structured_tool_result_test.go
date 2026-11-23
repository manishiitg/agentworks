package claudecode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-872-66: separate SDK assistant messages ran together, especially after
// colons; adding whitespace to every delta would corrupt split words and code.
func TestClaudeStructuredTextPreservesMessageBoundaries(t *testing.T) {
	frames := []string{
		`{"type":"stream_event","event":{"type":"message_start"}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Checking dep"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"endencies:"}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Checking dependencies:"},{"type":"tool_use","id":"read-1","name":"Read"}]}}`,
		`{"type":"stream_event","event":{"type":"message_start"}}`,
		// A tool-only message must not consume or duplicate the separator.
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"read-2","name":"Read"}]}}`,
		`{"type":"stream_event","event":{"type":"message_start"}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Ready.\n\n| Name | Value |\n| --- | --- |\n| A | 1 |"}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Ready.\n\n| Name | Value |\n| --- | --- |\n| A | 1 |"}]}}`,
	}
	want := "Checking dependencies:\n\nReady.\n\n| Name | Value |\n| --- | --- |\n| A | 1 |"
	var text claudeStructuredText
	var live strings.Builder
	for _, frame := range frames {
		var event claudeStreamEvent
		if err := json.Unmarshal([]byte(frame), &event); err != nil {
			t.Fatal(err)
		}
		text.observe(event, func(chunk llmtypes.StreamChunk) {
			if chunk.Metadata[llmtypes.ContentDeltaMetadataKey] != true {
				t.Fatal("SDK delta lost its marker")
			}
			live.WriteString(chunk.Content)
		})
	}
	if live.String() != want || text.completed.String() != want {
		t.Fatalf("message boundaries differ: live=%q saved=%q", live.String(), text.completed.String())
	}
	// Structured transport supplies completed blocks instead of token deltas.
	var blocks claudeStructuredText
	var emitted []string
	for _, body := range []string{"Checking dependencies:", "Ready."} {
		blocks.observe(claudeStreamEvent{Type: "assistant", Message: &claudeStreamMessage{Content: []claudeStreamContentBlock{{Type: "text", Text: body}}}}, func(chunk llmtypes.StreamChunk) {
			if chunk.Metadata[llmtypes.ContentDeltaMetadataKey] == true {
				t.Fatal("complete block mislabeled as a delta")
			}
			emitted = append(emitted, chunk.Content)
		})
	}
	if strings.Join(emitted, "") != blocks.completed.String() || blocks.completed.String() != "Checking dependencies:\n\nReady." {
		t.Fatalf("complete blocks changed: %q", blocks.completed.String())
	}
}

func TestClaudeToolResultTextPreservesNonTextBlocks(t *testing.T) {
	raw := json.RawMessage(`[{"type":"tool_reference","tool_name":"Monitor"}]`)
	if got := claudeToolResultText(raw); got != string(raw) {
		t.Fatalf("result = %q, want structured tool reference %q", got, raw)
	}
}

// PLAT-872-47: the earlier streamed answer was overwritten by the result
// answering a steer or a background-tool completion in the same host turn.
func TestClaudeSDKContinuedResultsKeepEarlierAnswer(t *testing.T) {
	frames := []string{
		`{"type":"result","result":"I changed direction.","sdk_continues":true}`,
		// A segment that ended on a steered message: no answer to show.
		`{"type":"result","result":"[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=null","sdk_continues":true}`,
		// The same segment as Claude Code reports it on the SDK: an error
		// result whose only error is the diagnostic line (server B, 2026-10-28).
		`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=null"],"sdk_continues":true}`,
		`{"type":"result","result":"The background tool completed."}`,
	}
	var text string
	var continued bool
	for _, frame := range frames {
		var event claudeStreamEvent
		if err := json.Unmarshal([]byte(frame), &event); err != nil {
			t.Fatal(err)
		}
		text = claudeContinuedResultText(text, event, continued)
		continued = event.SDKContinues
	}
	if text != "I changed direction.\n\nThe background tool completed." {
		t.Fatalf("lost native answer: %q", text)
	}
}

// PLAT-746-839: the SDK decoder discarded completed thinking blocks, leaving
// Show thinking empty even when the provider had supplied a block.
func TestClaudeStructuredThinkingIsSeparateFromReplyAndNotDuplicatedByDeltas(t *testing.T) {
	frames := []string{
		`{"type":"stream_event","event":{"type":"message_start"}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"Provider summary."}}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Provider summary.","signature":"not-displayed"},{"type":"redacted_thinking","data":"not-displayed"},{"type":"tool_use","id":"tool-1","name":"Read"}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"The answer."}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"The answer."}]}}`,
	}
	var text claudeStructuredText
	var reasoning, reply []string
	for _, frame := range frames {
		var event claudeStreamEvent
		if err := json.Unmarshal([]byte(frame), &event); err != nil {
			t.Fatal(err)
		}
		text.observe(event, func(chunk llmtypes.StreamChunk) {
			switch chunk.Type {
			case llmtypes.StreamChunkTypeReasoning:
				reasoning = append(reasoning, chunk.Content)
			case llmtypes.StreamChunkTypeContent:
				reply = append(reply, chunk.Content)
			}
		})
	}
	if len(reasoning) != 1 || reasoning[0] != "Provider summary." {
		t.Fatalf("reasoning blocks=%v", reasoning)
	}
	if strings.Join(reply, "") != "The answer." || text.completed.String() != "The answer." {
		t.Fatal("thinking changed reply text")
	}
}

// A steer can arrive during block A; only the native start of block B may
// start a new UI row. The existing text stream/result must remain byte-exact.
func TestClaudeNativeTextBlocksFrameWithoutRepeatingContent(t *testing.T) {
	frames := []string{
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Before the "}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"steer finishes."}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Before the steer finishes."}]}}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m2"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"After steer."}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		`{"type":"assistant","message":{"id":"m2","content":[{"type":"text","text":"After steer."}]}}`,
	}
	var state claudeStructuredText
	var controls []llmtypes.StreamChunk
	var live strings.Builder
	for _, frame := range frames {
		var ev claudeStreamEvent
		if err := json.Unmarshal([]byte(frame), &ev); err != nil {
			t.Fatal(err)
		}
		state.observe(ev, func(c llmtypes.StreamChunk) {
			if c.Type == llmtypes.StreamChunkTypeAssistantBlock {
				controls = append(controls, c)
			}
			if c.Type == llmtypes.StreamChunkTypeContent {
				live.WriteString(c.Content)
			}
		})
	}
	if len(controls) != 4 || controls[0].Metadata[llmtypes.AssistantBlockIDMetadataKey] != "claude:m1:0" || controls[2].Metadata[llmtypes.AssistantBlockIDMetadataKey] != "claude:m2:0" {
		t.Fatalf("native boundaries = %+v", controls)
	}
	if controls[1].Content != "Before the steer finishes." || controls[3].Content != "After steer." {
		t.Fatal("snapshot cut or repeated a block")
	}
	if live.String() != state.completed.String() || live.String() != "Before the steer finishes.\n\nAfter steer." {
		t.Fatalf("text changed: %q", live.String())
	}
}

func TestClaudeAPIRetryUsesNativeRetryLifecycleWithoutAnswerText(t *testing.T) {
	var text claudeStructuredText
	var phases []string
	var answer strings.Builder
	emit := func(chunk llmtypes.StreamChunk) {
		if chunk.NativeRetry != nil {
			phases = append(phases, chunk.NativeRetry.Phase)
		}
		if chunk.Type == llmtypes.StreamChunkTypeContent {
			answer.WriteString(chunk.Content)
		}
	}
	text.observe(claudeStreamEvent{Type: "system", Subtype: "api_retry", Attempt: 1, MaxRetries: 3, RetryDelayMS: 2000}, emit)
	text.observe(claudeStreamEvent{Type: "result", Result: "done"}, emit)
	if strings.Join(phases, ",") != "start,end" || strings.Contains(answer.String(), "retry") {
		t.Fatalf("phases=%v answer=%q", phases, answer.String())
	}
}

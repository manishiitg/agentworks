package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The SDK's terminal_reason decides how a turn ends: a full context window says to compact or start a new chat, and an
// explicit abort is an interruption even inside a success envelope. Only the external CLI is stubbed.
func TestClaudeStructuredTerminalReasons(t *testing.T) {
	for name, tc := range map[string]struct {
		result string
		check  func(error) bool
	}{
		"prompt too long": {`{"type":"result","is_error":true,"result":"Prompt is too long","terminal_reason":"prompt_too_long"}`, func(err error) bool {
			return err != nil && llmerrors.KindOf(err) == llmerrors.KindContextTooLong && strings.Contains(err.Error(), "Compact it or start a new chat")
		}},
		"blocking limit":                    {`{"type":"result","is_error":true,"result":"","terminal_reason":"blocking_limit"}`, func(err error) bool { return llmerrors.KindOf(err) == llmerrors.KindContextTooLong }},
		"aborted inside a success envelope": {`{"type":"result","is_error":false,"result":"partial","terminal_reason":"aborted_tools"}`, func(err error) bool { return errors.Is(err, context.Canceled) }},
		"abort after the answer was given": {`{"type":"result","is_error":false,"result":"Answer","sdk_continues":true}
{"type":"result","is_error":false,"result":"","terminal_reason":"aborted_tools"}`, func(err error) bool { return err == nil }},
		"aborted streaming": {`{"type":"result","is_error":true,"result":"x","terminal_reason":"aborted_streaming"}`, func(err error) bool { return errors.Is(err, context.Canceled) }},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\ncat <<'RESULT'\n" + tc.result + "\nRESULT\n"
			if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			adapter := NewClaudeCodeInteractiveAdapter("claude-opus-5-5", &MockLogger{})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := adapter.generateContentStructured(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hello"}}}}, &llmtypes.CallOptions{Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{MetadataKeyWorkingDir: dir}}})
			if !tc.check(err) {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}

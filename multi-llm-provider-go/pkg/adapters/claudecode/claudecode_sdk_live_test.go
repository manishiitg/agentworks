package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-872: SDK input must remain open for questions/interrupts, and partial
// tokens plus completed assistant messages must not duplicate the visible reply.
func TestClaudeSDKStreamingQuestionsAndInterrupt(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)
	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	adapter.SetSDKTransport(true)
	t.Run("question_and_stream", func(t *testing.T) {
		owner := "sdk-question-" + randomHex(4)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		asked := 0
		ctx = llmtypes.WithCodingAgentQuestionHandler(ctx, "claude-code", owner, func(_ context.Context, args map[string]any) (string, error) {
			asked++
			raw, _ := json.Marshal(args)
			var request struct {
				Questions []struct {
					ID      string `json:"id"`
					Options []struct {
						Label string `json:"label"`
					} `json:"options"`
				} `json:"questions"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				return "", err
			}
			answers := []any{}
			for _, q := range request.Questions {
				if len(q.Options) < 2 {
					return "", fmt.Errorf("question lacks choices")
				}
				answers = append(answers, map[string]any{"id": q.ID, "selected_labels": []string{q.Options[1].Label}})
			}
			reply, _ := json.Marshal(map[string]any{"status": "answered", "answers": answers})
			return string(reply), nil
		})
		stream := make(chan llmtypes.StreamChunk, 128)
		var text strings.Builder
		deltas := 0
		done := make(chan struct{})
		go func() {
			defer close(done)
			for chunk := range stream {
				if chunk.Type == llmtypes.StreamChunkTypeContent {
					text.WriteString(chunk.Content)
					if chunk.Metadata[llmtypes.ContentDeltaMetadataKey] == true {
						deltas++
					}
				}
			}
		}()
		response, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Use AskUserQuestion now to ask me to pick Alpha or Beta. You must wait for my choice, then reply with only the chosen word. Do not call other tools.")}, WithWorkingDir(t.TempDir()), WithInteractiveSessionID(owner), WithClaudeCodeTools("AskUserQuestion"), WithPermissionMode("default"), llmtypes.WithStreamingChan(stream))
		<-done
		if err != nil {
			t.Fatal(err)
		}
		if asked != 1 {
			t.Fatalf("native question callbacks=%d", asked)
		}
		content := strings.TrimSpace(response.Choices[0].Content)
		if content != "Beta" {
			t.Fatalf("answer=%q", content)
		}
		if deltas == 0 || strings.TrimSpace(text.String()) != content {
			t.Fatalf("stream duplicated or missing: %d deltas, stream=%q final=%q", deltas, text.String(), content)
		}
		if response.Usage == nil || response.Usage.TotalTokens == 0 {
			t.Fatal("native usage absent")
		}
	})
	t.Run("interrupt_slow_tool", func(t *testing.T) {
		owner := "sdk-interrupt-" + randomHex(4)
		dir := t.TempDir()
		marker := filepath.Join(dir, "started")
		server := writeClaudeInteractiveSlowMCPServer(t, marker)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Call api-bridge slow_contract with token TEST and delay_ms 30000. Wait for its result before replying.")}, WithWorkingDir(dir), WithInteractiveSessionID(owner), WithMCPConfig(fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, server)), WithClaudeCodeTools(""), WithAllowedTools("mcp__api-bridge__slow_contract"))
			done <- err
		}()
		deadline := time.NewTimer(40 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
	waiting:
		for {
			select {
			case err := <-done:
				t.Fatalf("completed before slow tool: %v", err)
			case <-deadline.C:
				t.Fatal("slow tool never started")
			case <-ticker.C:
				if _, err := os.Stat(marker); err == nil {
					break waiting
				}
			}
		}
		if err := SendClaudeCodeControlKey(ctx, owner, "Escape"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			t.Fatal("SDK interruption did not finish the active turn")
		}
	})
}

// PLAT-872-47: steering must redirect the active query before a slow tool
// finishes, acknowledge native consumption, and reject input after that query.
func TestClaudeSDKSteersActiveTool(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)
	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	adapter.SetSDKTransport(true)
	owner, dir := "sdk-steer-"+randomHex(4), t.TempDir()
	marker := filepath.Join(dir, "started")
	server := writeClaudeInteractiveSlowMCPServer(t, marker)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	type outcome struct {
		response *llmtypes.ContentResponse
		err      error
	}
	done := make(chan outcome, 1)
	nonce := "REDIRECT_" + randomHex(8)
	stream := make(chan llmtypes.StreamChunk, 128)
	redirected := make(chan struct{})
	go func() {
		var text strings.Builder
		sent := false
		for chunk := range stream {
			if chunk.Type == llmtypes.StreamChunkTypeContent {
				text.WriteString(chunk.Content)
			}
			if !sent && strings.Contains(text.String(), nonce) {
				close(redirected)
				sent = true
			}
		}
	}()
	go func() {
		r, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Call api-bridge slow_contract with token ORIGINAL and delay_ms 30000. Wait for its result before replying ORIGINAL_DONE.")}, WithWorkingDir(dir), WithInteractiveSessionID(owner), WithMCPConfig(fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, server)), WithClaudeCodeTools(""), WithAllowedTools("mcp__api-bridge__slow_contract"), llmtypes.WithStreamingChan(stream))
		done <- outcome{r, err}
	}()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case result := <-done:
			t.Fatalf("completed before tool started: %v", result.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	start := time.Now()
	steerCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	if err := SendClaudeCodeSDKInput(steerCtx, owner, "Change direction now. Do not wait for that tool or call more tools. Reply only with "+nonce); err != nil {
		t.Fatal(err)
	}
	t.Logf("native steering acknowledged after %s", time.Since(start))
	select {
	case <-redirected:
		t.Logf("redirected text streamed after %s", time.Since(start))
	case <-time.After(15 * time.Second):
		t.Fatal("steer waited for slow tool instead of redirecting the query")
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if !strings.Contains(result.response.Choices[0].Content, nonce) {
			t.Fatalf("steer lost: %q", result.response.Choices[0].Content)
		}
		if result.response.Usage == nil || result.response.Usage.TotalTokens == 0 {
			t.Fatal("native usage missing")
		}
		t.Logf("query settled after %s; native usage=%d", time.Since(start), result.response.Usage.TotalTokens)
	case <-time.After(45 * time.Second):
		t.Fatal("background tool never settled")
	}
	if err := SendClaudeCodeSDKInput(ctx, owner, "late input"); !errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
		t.Fatalf("finished query must reject unsent input: %v", err)
	}
}

// PLAT-872-54: command requests used to hit the callback's unconditional deny.
func TestClaudeSDKCommandPermissionUsesScopedAnswer(t *testing.T) {
	for _, choice := range []string{"Allow", "Deny", "cancel"} {
		t.Run(choice, func(t *testing.T) {
			command := `API_TOKEN="private fixture" printf hello`
			input, _ := json.Marshal(map[string]any{"command": command, "description": "fixture"})
			ctx := llmtypes.WithCodingAgentQuestionHandler(context.Background(), "claude-code", "chat", func(ctx context.Context, args map[string]any) (string, error) {
				q := args["questions"].([]any)[0].(map[string]any)
				text := q["question"].(string)
				if strings.Contains(text, "private fixture") || !strings.Contains(text, "printf hello") {
					t.Fatal("command display lost command or exposed credential")
				}
				if choice == "cancel" {
					return "", context.Canceled
				}
				return fmt.Sprintf(`{"status":"answered","answers":[{"id":"command_permission","selected_labels":[%q]}]}`, choice), nil
			})
			result := claudeSDKPermission(ctx, "Bash", input)
			if choice == "Allow" {
				if result["behavior"] != "allow" || result["updatedInput"].(map[string]any)["command"] != command {
					t.Fatalf("approval changed original command: %v", result)
				}
			} else if result["behavior"] != "deny" {
				t.Fatal("non-approval executed command")
			}
			if claudeSDKPermission(context.Background(), "Bash", input)["behavior"] != "deny" {
				t.Fatal("unattended command approved")
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if claudeSDKPermission(cancelled, "Bash", input)["behavior"] != "deny" {
				t.Fatal("cancelled approval executed command")
			}
		})
	}
}

package codexcli

import (
	"context"
	"errors"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// injectCodexHistory gives a fresh Codex thread the earlier conversation the
// host handed over, as real conversation entries (thread/inject_items)
// instead of text in front of the user's message. A resumed thread already
// holds its history; it and any failed injection get the text instead, so the
// history is never lost. Returns the prompt to send.
func (c *CodexCLIAdapter) injectCodexHistory(ctx context.Context, r *codexRPC, owner, thread string, newThread bool, prompt string) string {
	items := llmtypes.ClaimNativeHistoryForSession(ctx, owner)
	if len(items) == 0 {
		return prompt
	}
	text := llmtypes.NativeHistoryText(items)
	withText := func(reason string) string {
		if c.logger != nil {
			c.logger.Infof("[CODEX_HISTORY] owner=%s action=text_fallback reason=%s items=%d bytes=%d", owner, reason, len(items), len(text))
		}
		if strings.TrimSpace(prompt) == "" {
			return text
		}
		return text + "\n\n" + prompt
	}
	if !newThread || r == nil || thread == "" {
		return withText("thread_not_new")
	}
	native := make([]map[string]any, 0, len(items))
	for _, item := range items {
		body := strings.TrimSpace(item.Text)
		if body == "" {
			continue
		}
		role, kind := "user", "input_text"
		if item.Role == "assistant" {
			role, kind = "assistant", "output_text"
		}
		native = append(native, map[string]any{"type": "message", "role": role, "content": []map[string]any{{"type": kind, "text": body}}})
	}
	if err := r.call(ctx, "thread/inject_items", map[string]any{"threadId": thread, "items": native}, nil); err != nil {
		var rpcErr *codexRPCRequestError
		if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
			return withText("unsupported_by_codex")
		}
		return withText("inject_failed")
	}
	if c.logger != nil {
		c.logger.Infof("[CODEX_HISTORY] owner=%s action=injected items=%d bytes=%d", owner, len(native), len(text))
	}
	return prompt
}

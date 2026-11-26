package agycli

import (
	"errors"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// agyQuotaError accepts provider failure carriers, never arbitrary prompt or
// transcript text. AGY has used both HTTP and gRPC quota wording.
func agyQuotaError(model string, values ...string) error {
	for _, value := range values {
		detail := strings.TrimSpace(value)
		lower := strings.ToLower(detail)
		if detail == "" {
			continue
		}
		for _, prefix := range []string{"error:", "failed:", "agy:"} {
			lower = strings.TrimSpace(strings.TrimPrefix(lower, prefix))
		}
		quota := strings.HasPrefix(lower, "resource_exhausted") ||
			strings.HasPrefix(lower, "quota exceeded") ||
			strings.HasPrefix(lower, "quota exhausted") ||
			strings.HasPrefix(lower, "usage limit reached") ||
			strings.HasPrefix(lower, "rate limit exceeded") ||
			(strings.HasPrefix(lower, "429") && (strings.Contains(lower, "quota") || strings.Contains(lower, "rate limit"))) ||
			(strings.HasPrefix(lower, "rpc error:") && (strings.Contains(lower, "resourceexhausted") || strings.Contains(lower, "resource_exhausted")))
		if quota {
			return &llmerrors.Error{Kind: llmerrors.KindQuotaExhausted, Provider: "agy-cli", Model: model, Err: errors.New(detail)}
		}
	}
	return nil
}

// agyQuotaFailureError is used only after AGY has already marked the turn
// failed (or exited nonzero). Failure envelopes can prepend transport text
// before the quota phrase, unlike a successful assistant answer.
func agyQuotaFailureError(model string, values ...string) error {
	if err := agyQuotaError(model, values...); err != nil {
		return err
	}
	for _, value := range values {
		lower := strings.ToLower(value)
		if strings.Contains(lower, "quota") || strings.Contains(lower, "resource_exhausted") ||
			strings.Contains(lower, "resourceexhausted") || strings.Contains(lower, "rate limit") ||
			strings.Contains(lower, "usage limit") || strings.Contains(lower, "too many requests") {
			return &llmerrors.Error{Kind: llmerrors.KindQuotaExhausted, Provider: "agy-cli", Model: model, Err: errors.New(strings.TrimSpace(value))}
		}
	}
	return nil
}

// Stderr can include output from MCP bridge children. Only AGY-style error
// lines can suspend a turn; unrelated tool output mentioning a quota cannot.
func agyQuotaStderrError(model, stderr string) error {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if !strings.HasPrefix(lower, "error:") && !strings.HasPrefix(lower, "failed:") &&
			!strings.HasPrefix(lower, "agy:") && !strings.HasPrefix(lower, "rpc error:") {
			continue
		}
		if err := agyQuotaError(model, line); err != nil {
			return err
		}
	}
	return nil
}

// The TUI has no structured failure envelope. Inspect only its recent status
// rows, and only when the expected durable turn step did not arrive.
func agyQuotaPaneError(model, pane string) error {
	return agyQuotaStderrError(model, agyPaneStatusRows(pane))
}

package llmtypes

import (
	"context"
	"fmt"
)

// CodingAgentQuestionHandler is installed only by the host's attended-chat gate.
// It belongs to one turn, never to a retained provider process or account.
type CodingAgentQuestionHandler func(context.Context, map[string]any) (string, error)
type codingAgentQuestionKey struct{}
type codingAgentQuestionScope struct {
	provider, owner string
	handler         CodingAgentQuestionHandler
}

func WithCodingAgentQuestionHandler(ctx context.Context, provider, owner string, handler CodingAgentQuestionHandler) context.Context {
	return context.WithValue(ctx, codingAgentQuestionKey{}, codingAgentQuestionScope{provider, owner, handler})
}
func AskCodingAgentQuestion(ctx context.Context, questions map[string]any) (string, error) {
	scope, _ := ctx.Value(codingAgentQuestionKey{}).(codingAgentQuestionScope)
	handler := scope.handler
	if handler == nil {
		return "", fmt.Errorf("no attending user; ask in your reply")
	}
	return handler(ctx, questions)
}

// ScopeCodingAgentQuestions prevents inherited child work from asking in a
// different owner/provider's chat. Bounded callers have no attended owner.
func ScopeCodingAgentQuestions(ctx context.Context, provider, owner string) context.Context {
	scope, _ := ctx.Value(codingAgentQuestionKey{}).(codingAgentQuestionScope)
	if scope.provider != provider || scope.owner != owner {
		return context.WithValue(ctx, codingAgentQuestionKey{}, codingAgentQuestionScope{})
	}
	return ctx
}

func CodingAgentQuestionsAvailable(ctx context.Context) bool {
	scope, _ := ctx.Value(codingAgentQuestionKey{}).(codingAgentQuestionScope)
	return scope.handler != nil
}

// NativeAsyncQuestionHandler shows a question a CLI asked without waiting for the answer (Codex
// request_user_input_async). The record matches the one read from the native transcript, so the host shows one card
// for it whichever path sees it first. It is installed with the attended-chat question handler.
type NativeAsyncQuestionHandler func(NativeRecord) bool
type nativeAsyncQuestionKey struct{}

func WithNativeAsyncQuestionHandler(ctx context.Context, handler NativeAsyncQuestionHandler) context.Context {
	return context.WithValue(ctx, nativeAsyncQuestionKey{}, handler)
}

// ShowNativeAsyncQuestion reports whether the host showed the question as a card.
func ShowNativeAsyncQuestion(ctx context.Context, record NativeRecord) bool {
	handler, _ := ctx.Value(nativeAsyncQuestionKey{}).(NativeAsyncQuestionHandler)
	return handler != nil && handler(record)
}

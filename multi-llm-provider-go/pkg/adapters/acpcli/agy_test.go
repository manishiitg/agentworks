package acpcli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// refusalOutcome stands in for agy's conversation-store reading: a refusal
// stop is the account's quota.
type refusalOutcome struct{}

func (refusalOutcome) BeginTurn(string) func(model, stop string) error {
	return func(model, stop string) error {
		if stop == "refusal" {
			return &llmerrors.Error{Provider: "agy-cli", Model: model, Kind: llmerrors.KindQuotaExhausted, Err: errors.New("quota")}
		}
		return nil
	}
}
func (refusalOutcome) RPCFailure(_, _ string, err error) error { return err }

func agyCall(owner, workdir, effort string, extra ...llmtypes.CallOption) *llmtypes.CallOptions {
	opts := &llmtypes.CallOptions{ReasoningEffort: effort}
	for _, opt := range append([]llmtypes.CallOption{WithInteractiveSessionID(owner), WithWorkingDir(workdir)}, extra...) {
		opt(opts)
	}
	return opts
}

func withAgyLaunch(t *testing.T, env ...string) {
	t.Helper()
	SetLaunch("agy", func(LaunchRequest) (*LaunchSpec, error) {
		return &LaunchSpec{Env: env, AutoApprove: true, QuestionIDPrefix: "interaction_", NoSessionMCP: true, Outcome: refusalOutcome{}}, nil
	})
	t.Cleanup(func() { SetLaunch("agy", nil) })
}

// Antigravity runs as its own provider through the connector: the Gemini key
// signs in, the chat's instructions go in session/new _meta (not the prompt),
// "<model>-<level>" picks the model and thought level, the level is set again
// every turn, the outcome hook turns a refusal into a quota error, and the
// session handle keeps agy's provider id, model id and session id.
func TestAgyRunsThroughTheConnector(t *testing.T) {
	if _, ok := LookupAgent("agy"); ok {
		t.Fatal("agy is offered as an acp-cli model")
	}
	withAgyLaunch(t, "GEMINI_API_KEY=test-only")
	workdir, owner := t.TempDir(), "chat-agy"
	peer := installReplay(t, "agy_session", workdir)
	// agy asks the person its own questions as permission requests; auto-approve must not answer them for the
	// person (it picked the first option), the person's choice goes back.
	asked := ""
	ctx := llmtypes.WithCodingAgentQuestionHandler(context.Background(), "agy-cli", owner, func(_ context.Context, args map[string]any) (string, error) {
		raw, _ := json.Marshal(args)
		asked = string(raw)
		return `{"status":"answered","answers":[{"id":"interaction_9960062f","selected_labels":["Needs review"]}]}`, nil
	})

	first, err := Generate(ctx, "agy", "gemini-4-pro-high", human("hello"), agyCall(owner, workdir, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "Which result label should be used?") || !strings.Contains(asked, "Verified") {
		t.Fatalf("the person was not asked agy's question: %s", asked)
	}
	if first.Choices[0].Content != "ok" {
		t.Fatalf("answer = %q", first.Choices[0].Content)
	}
	handle, ok := llmtypes.ExtractCodingProviderSessionHandle(first.Choices[0].GenerationInfo)
	if !ok || handle.Provider != "agy-cli" || handle.Model != "gemini-4-pro-high" || handle.NativeSessionID != "agy_sess" || handle.Transport != llmtypes.CodingProviderTransportACP {
		t.Fatalf("session handle = %+v", handle)
	}
	if got := first.Choices[0].GenerationInfo.Additional["agy_session_id"]; got != "agy_sess" {
		t.Fatalf("agy_session_id = %v", got)
	}
	if status := StatusLine(owner); status == nil || status.Provider != "agy-cli" || status.Metadata[llmtypes.ContextUsedTokensMetaKey] != 900 {
		t.Fatalf("status line = %+v", status)
	}

	second := []llmtypes.MessageContent{human("")[0], {Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "again"}}}}
	_, err = Generate(ctx, "agy", "gemini-4-pro", second, agyCall(owner, workdir, "low"))
	if llmerrors.KindOf(err) != llmerrors.KindQuotaExhausted {
		t.Fatalf("refusal err = %v, want quota", err)
	}
	CloseAgentSession(owner, "agy")
	peer.wait(t)
}

// A chat resumes its own agy conversation by its exact id; when agy cannot
// load it the turn fails rather than starting a fresh conversation.
func TestAgyResumeNeverStartsAFreshConversation(t *testing.T) {
	withAgyLaunch(t)
	workdir, owner := t.TempDir(), "chat-agy-resume"
	peer := installReplay(t, "agy_resume_strict", workdir)
	_, err := Generate(context.Background(), "agy", "gemini-3.8-flash", human("hello"), agyCall(owner, workdir, "", WithResumeSessionID("agy_old")))
	if err == nil {
		t.Fatal("a failed load started a fresh conversation")
	}
	CloseSession(owner)
	peer.wait(t)
}

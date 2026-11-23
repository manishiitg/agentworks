package llmproviders

import (
	"context"
	"errors"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type continuationTestModel struct {
	messages [][]llmtypes.MessageContent
	opts     []llmtypes.CallOptions
	errs     []error
}

func (m *continuationTestModel) GenerateContent(_ context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	var opts llmtypes.CallOptions
	for _, opt := range options {
		opt(&opts)
	}
	m.messages = append(m.messages, messages)
	m.opts = append(m.opts, opts)
	if len(m.errs) > 0 {
		err := m.errs[0]
		m.errs = m.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	return &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{{
			Content:        "ok",
			GenerationInfo: &llmtypes.GenerationInfo{},
		}},
	}, nil
}

func (m *continuationTestModel) GetModelID() string { return "test-model" }

func (m *continuationTestModel) GetModelMetadata(string) (*llmtypes.ModelMetadata, error) {
	return &llmtypes.ModelMetadata{ModelID: "test-model"}, nil
}

func TestContinueCodingAgentSessionSendsOnlyLatestMessageAndResumeOptions(t *testing.T) {
	tests := []struct {
		name          string
		handle        llmtypes.CodingProviderSessionHandle
		resumeKey     string
		workdirKey    string
		projectDirKey string
	}{
		{
			name: "claude code",
			handle: llmtypes.CodingProviderSessionHandle{
				Provider:        string(ProviderClaudeCode),
				Transport:       llmtypes.CodingProviderTransportTmux,
				NativeSessionID: "claude-native",
				WorkingDir:      "/tmp/work",
				Model:           "claude-sonnet-4-6",
			},
			resumeKey:  "claude_code_resume_session_id",
			workdirKey: "claude_code_working_dir",
		},
		{
			name: "codex cli",
			handle: llmtypes.CodingProviderSessionHandle{
				Provider:        string(ProviderCodexCLI),
				Transport:       llmtypes.CodingProviderTransportTmux,
				NativeSessionID: "codex-thread",
				WorkingDir:      "/tmp/work",
				Model:           DefaultCodexCLIModel,
			},
			resumeKey:  "codex_resume_session_id",
			workdirKey: "codex_project_dir_id",
		},
		{
			name: "cursor cli",
			handle: llmtypes.CodingProviderSessionHandle{
				Provider:        string(ProviderCursorCLI),
				Transport:       llmtypes.CodingProviderTransportTmux,
				NativeSessionID: "cursor-native",
				WorkingDir:      "/tmp/work",
				Model:           DefaultCursorCLIModel,
			},
			resumeKey:  "cursor_resume_session_id",
			workdirKey: "cursor_working_dir",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &continuationTestModel{}
			_, err := ContinueCodingAgentSession(context.Background(), model, tt.handle, "what was the codeword?")
			if err != nil {
				t.Fatalf("ContinueCodingAgentSession() error = %v", err)
			}
			if len(model.messages) != 1 || len(model.messages[0]) != 1 {
				t.Fatalf("messages calls/len = %d/%d, want 1/1", len(model.messages), len(model.messages[0]))
			}
			if got := textFromMessage(model.messages[0][0]); got != "what was the codeword?" {
				t.Fatalf("message = %q", got)
			}
			if model.opts[0].Metadata == nil || model.opts[0].Metadata.Custom == nil {
				t.Fatal("metadata custom options not set")
			}
			if got := model.opts[0].Metadata.Custom[tt.resumeKey]; got != tt.handle.NativeSessionID {
				t.Fatalf("%s = %#v, want %q", tt.resumeKey, got, tt.handle.NativeSessionID)
			}
			if got := model.opts[0].CodingProviderTransport; got != tt.handle.Transport {
				t.Fatalf("native transport = %q, want %q", got, tt.handle.Transport)
			}
			if tt.workdirKey != "" {
				if got := model.opts[0].Metadata.Custom[tt.workdirKey]; got != tt.handle.WorkingDir {
					t.Fatalf("%s = %#v, want %q", tt.workdirKey, got, tt.handle.WorkingDir)
				}
			}
			if tt.projectDirKey != "" {
				if got := model.opts[0].Metadata.Custom[tt.projectDirKey]; got != tt.handle.ProjectDirID {
					t.Fatalf("%s = %#v, want %q", tt.projectDirKey, got, tt.handle.ProjectDirID)
				}
			}
		})
	}
}

func TestContinueCodingAgentSessionRejectsNonApplicableAndMissingResume(t *testing.T) {
	model := &continuationTestModel{}
	_, err := ContinueCodingAgentSession(context.Background(), model, llmtypes.CodingProviderSessionHandle{
		Provider:        string(ProviderOpenAI),
		Transport:       llmtypes.CodingProviderTransportAPI,
		NativeSessionID: "api-session",
	}, "hello")
	if !IsCodingAgentContinuationError(err, CodingAgentContinuationErrorNonApplicable) {
		t.Fatalf("err = %v, want non-applicable continuation error", err)
	}

	_, err = ContinueCodingAgentSession(context.Background(), model, llmtypes.CodingProviderSessionHandle{
		Provider:  string(ProviderClaudeCode),
		Transport: llmtypes.CodingProviderTransportTmux,
	}, "hello")
	if !IsCodingAgentContinuationError(err, CodingAgentContinuationErrorNonContinuable) {
		t.Fatalf("err = %v, want non-continuable continuation error", err)
	}
}

func TestContinueCodingAgentSessionRetriesOnceAfterTmuxLoss(t *testing.T) {
	stream := make(chan llmtypes.StreamChunk, 1)
	model := &continuationTestModel{
		errs: []error{llmtypes.WrapCodingAgentTmuxSessionLostError(
			errors.New("failed to capture Codex CLI session: exit status 1: can't find pane: dead-pane"),
			string(ProviderCodexCLI),
			"dead-pane",
			"tmux session lost",
		)},
	}
	resp, err := ContinueCodingAgentSession(context.Background(), model, llmtypes.CodingProviderSessionHandle{
		Provider:        string(ProviderCodexCLI),
		Transport:       llmtypes.CodingProviderTransportTmux,
		NativeSessionID: "codex-thread",
		WorkingDir:      "/tmp/work",
		Model:           "gpt-5.4",
	}, "what was the codeword?", llmtypes.WithStreamingChan(stream))
	if err != nil {
		t.Fatalf("ContinueCodingAgentSession() error = %v", err)
	}
	if resp == nil || len(resp.Choices) != 1 || resp.Choices[0].Content != "ok" {
		t.Fatalf("response = %#v", resp)
	}
	if len(model.messages) != 2 {
		t.Fatalf("GenerateContent calls = %d, want 2", len(model.messages))
	}
	if got := textFromMessage(model.messages[1][0]); got != "what was the codeword?" {
		t.Fatalf("retry message = %q", got)
	}
	if model.opts[0].StreamChan == nil {
		t.Fatal("first call should preserve stream channel")
	}
	if model.opts[1].StreamChan != nil {
		t.Fatal("retry call should clear stream channel after first call owns it")
	}
}

func TestContinueCodingAgentSessionDoesNotRetryStringMatchedTmuxLoss(t *testing.T) {
	model := &continuationTestModel{
		errs: []error{errors.New("failed to capture Codex CLI session: exit status 1: can't find pane: dead-pane")},
	}
	_, err := ContinueCodingAgentSession(context.Background(), model, llmtypes.CodingProviderSessionHandle{
		Provider:        string(ProviderCodexCLI),
		Transport:       llmtypes.CodingProviderTransportTmux,
		NativeSessionID: "codex-thread",
		WorkingDir:      "/tmp/work",
		Model:           "gpt-5.4",
	}, "what was the codeword?")
	if err == nil {
		t.Fatal("ContinueCodingAgentSession() error = nil, want original tmux-text error")
	}
	if len(model.messages) != 1 {
		t.Fatalf("GenerateContent calls = %d, want 1", len(model.messages))
	}
}

func TestStartCodingAgentTransportSessionSetsLaunchOnlyWithoutPrompt(t *testing.T) {
	model := &continuationTestModel{}
	_, err := StartCodingAgentTransportSession(context.Background(), model, llmtypes.CodingProviderSessionHandle{
		Provider:        string(ProviderCodexCLI),
		Transport:       llmtypes.CodingProviderTransportTmux,
		NativeSessionID: "codex-thread",
		WorkingDir:      "/tmp/work",
		Model:           DefaultCodexCLIModel,
	})
	if err != nil {
		t.Fatalf("StartCodingAgentTransportSession() error = %v", err)
	}
	if len(model.messages) != 1 {
		t.Fatalf("GenerateContent calls = %d, want 1", len(model.messages))
	}
	if model.messages[0] != nil {
		t.Fatalf("messages = %#v, want nil launch-only prompt", model.messages[0])
	}
	if model.opts[0].Metadata == nil || model.opts[0].Metadata.Custom == nil {
		t.Fatal("metadata custom options not set")
	}
	if got := model.opts[0].Metadata.Custom["codex_resume_session_id"]; got != "codex-thread" {
		t.Fatalf("codex_resume_session_id = %#v, want codex-thread", got)
	}
	if !llmtypes.CodingProviderLaunchOnlyFromOptions(&model.opts[0]) {
		t.Fatalf("launch-only option not set")
	}
}

func TestStartCodingAgentTransportSessionRejectsStructuredTransport(t *testing.T) {
	model := &continuationTestModel{}
	_, err := StartCodingAgentTransportSession(context.Background(), model, llmtypes.CodingProviderSessionHandle{
		Provider:        string(ProviderOpenAI),
		Transport:       llmtypes.CodingProviderTransportStructured,
		NativeSessionID: "api-session",
		Model:           "gpt-5",
	})
	if !IsCodingAgentContinuationError(err, CodingAgentContinuationErrorNonApplicable) {
		t.Fatalf("err = %v, want non-applicable continuation error", err)
	}
	if len(model.messages) != 0 {
		t.Fatalf("GenerateContent calls = %d, want 0", len(model.messages))
	}
}

func textFromMessage(msg llmtypes.MessageContent) string {
	for _, part := range msg.Parts {
		switch typed := part.(type) {
		case llmtypes.TextContent:
			return typed.Text
		case *llmtypes.TextContent:
			if typed != nil {
				return typed.Text
			}
		}
	}
	return ""
}

package llmtypes

import "testing"

func TestCodingProviderSessionHandleRoundTrip(t *testing.T) {
	gi := &GenerationInfo{}
	want := CodingProviderSessionHandle{
		Provider:        "claude-code",
		Transport:       CodingProviderTransportTmux,
		NativeSessionID: "native-1",
		TmuxSession:     "tmux-1",
		WorkingDir:      "/tmp/work",
		Model:           "sonnet",
		Status:          CodingProviderSessionStatusIdle,
	}

	AttachCodingProviderSessionHandle(gi, want)

	got, ok := ExtractCodingProviderSessionHandle(gi)
	if !ok {
		t.Fatal("expected handle")
	}
	if got != want {
		t.Fatalf("handle = %#v, want %#v", got, want)
	}
	if gi.Additional[CodingProviderSessionHandleAdditionalKey] == nil {
		t.Fatal("expected compatibility handle in Additional")
	}
}

func TestCodingProviderSessionHandleExtractsFromAdditionalMap(t *testing.T) {
	gi := &GenerationInfo{
		Additional: map[string]interface{}{
			CodingProviderSessionHandleAdditionalKey: map[string]interface{}{
				"provider":          "codex-cli",
				"transport":         "structured",
				"native_session_id": "thread-1",
			},
		},
	}

	got, ok := ExtractCodingProviderSessionHandle(gi)
	if !ok {
		t.Fatal("expected handle")
	}
	if got.Provider != "codex-cli" || got.Transport != CodingProviderTransportStructured || got.NativeSessionID != "thread-1" {
		t.Fatalf("unexpected handle: %#v", got)
	}
}

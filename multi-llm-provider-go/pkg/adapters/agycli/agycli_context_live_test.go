package agycli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Live context P0 proofs for agy: working_directory (cwd reaches the
// model), runtime_context (system fold steers), trust_auth_prompts
// (logged-out fails fast with a login-required error; untrusted dirs run).
//
// Print-mode boundaries proven live and pinned here by design:
//   - native tools are denied headless and permissions.allow is ignored,
//     so the workdir proof uses the e2e-only skip-permissions key in a
//     fresh tmpdir (production lanes never set it);
//   - GEMINI.md project files are NOT auto-injected, so runtime_context is
//     the system-header fold only.

func agyTestOnlyOption(key, value string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[key] = value
	}
}

func TestAgyCLIRealWorkingDirectoryContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	// Fresh tmpdir: outside trustedWorkspaces, so this turn also proves
	// print mode is trust-exempt (trust_auth_prompts half).
	workDir := t.TempDir()
	canary := "AGY_WORKDIR_" + agyRandomHex(t, 4)
	if err := os.WriteFile(filepath.Join(workDir, "canary.txt"), []byte(canary+"\n"), 0o644); err != nil {
		t.Fatalf("write canary: %v", err)
	}

	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Do not write or modify anything. Read-only."),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Read the file canary.txt in your working directory and reply with exactly its contents and nothing else."),
	},
		WithWorkingDir(workDir),
		agyTestOnlyOption(agyTestOnlyMetadataKeySkipPermissions, "true"),
	)
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	if content := strings.TrimSpace(resp.Choices[0].Content); !strings.Contains(content, canary) {
		t.Fatalf("content = %q, want workdir canary %s", content, canary)
	}
	handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp)
	if !ok || handle.WorkingDir != workDir {
		t.Fatalf("handle = %#v, want working dir %q", handle, workDir)
	}
}

func TestAgyCLIRealRuntimeContextContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Always begin every reply with the word PICKLEBURST. Do not use tools."),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Say hello."),
	}, WithWorkingDir(t.TempDir()))
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	if content := strings.TrimSpace(resp.Choices[0].Content); !strings.Contains(content, "PICKLEBURST") {
		t.Fatalf("content = %q, want system-steered prefix", content)
	}
}

func TestAgyCLIRealTrustAuthPromptsContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	// Empty HOME: agy sees no stored login, prints the OAuth marker, and
	// would hang forever — the lane must kill it and fail fast.
	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	start := time.Now()
	_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with hi."),
	},
		WithWorkingDir(t.TempDir()),
		agyTestOnlyOption(agyTestOnlyMetadataKeyHomeOverride, t.TempDir()),
	)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "login required") {
		t.Fatalf("error = %v, want login-required failure", err)
	}
	if elapsed > 60*time.Second {
		t.Fatalf("logged-out run took %s, want fail-fast marker detection", elapsed)
	}
	t.Logf("logged-out run failed fast in %s: %v", elapsed, err)
}

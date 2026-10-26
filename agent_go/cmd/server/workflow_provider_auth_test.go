package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/mcpagent/llm"
)

func TestWorkflowSessionIDsAreUserAndWorkflowScoped(t *testing.T) {
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{
		"alice-target": {
			SessionID:     "alice-target",
			UserID:        "alice",
			WorkspacePath: "Workflow/customer-renewals/",
			LastActivity:  time.Now(),
		},
		"bob-same-path": {
			SessionID:     "bob-same-path",
			UserID:        "bob",
			WorkspacePath: "Workflow/customer-renewals",
		},
		"alice-other": {
			SessionID:     "alice-other",
			UserID:        "alice",
			WorkspacePath: "Workflow/support-triage",
		},
	}}

	ids := api.workflowSessionIDs("alice", `Workflow\customer-renewals`)
	if len(ids) != 1 || ids[0] != "alice-target" {
		t.Fatalf("workflowSessionIDs() = %v, want [alice-target]", ids)
	}
	if ids := api.workflowSessionIDs("", "Workflow/customer-renewals"); len(ids) != 0 {
		t.Fatalf("workflowSessionIDs(empty user) = %v, want none", ids)
	}
}

func TestValidateClaudeCodeOAuthTokenUsesIsolatedAuthEnvironment(t *testing.T) {
	fakeBin := t.TempDir()
	capturePath := filepath.Join(fakeBin, "auth-env.txt")
	claudePath := filepath.Join(fakeBin, "claude")
	script := `#!/bin/sh
printf '%s|%s|%s|%s|%s' "$ANTHROPIC_API_KEY" "$ANTHROPIC_AUTH_TOKEN" "$ANTHROPIC_BASE_URL" "$CLAUDE_CODE_OAUTH_TOKEN" "$CLAUDE_CONFIG_DIR" > "$CLAUDE_AUTH_CAPTURE"
printf '{"loggedIn":true,"authMethod":"oauth_token","apiProvider":"firstParty"}\n'
`
	if err := os.WriteFile(claudePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLAUDE_AUTH_CAPTURE", capturePath)
	t.Setenv("ANTHROPIC_API_KEY", "ambient-api-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "ambient-auth-token")
	t.Setenv("ANTHROPIC_BASE_URL", "https://ambient.example")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "ambient-oauth-token")

	const workflowToken = "workflow-oauth-token"
	if err := validateClaudeCodeOAuthToken(context.Background(), workflowToken); err != nil {
		t.Fatalf("validateClaudeCodeOAuthToken() error = %v", err)
	}
	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read captured auth env: %v", err)
	}
	parts := strings.Split(string(captured), "|")
	if len(parts) != 5 {
		t.Fatalf("captured env = %q", string(captured))
	}
	if parts[0] != "" || parts[1] != "" || parts[2] != "" {
		t.Fatalf("ambient Anthropic credentials reached validation: %q", string(captured))
	}
	if parts[3] != workflowToken {
		t.Fatalf("OAuth token = %q, want workflow token", parts[3])
	}
	if strings.TrimSpace(parts[4]) == "" {
		t.Fatal("validation did not isolate Claude config directory")
	}
}

func TestWorkflowProviderAPIKeysAreUserAndWorkflowScoped(t *testing.T) {
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemStore() error = %v", err)
	}
	api := &StreamingAPI{chatStore: store}
	ctx := context.Background()
	const userID = "alice"
	const workflowA = "Workflow/customer-renewals"
	const workflowB = "Workflow/support-triage"
	const workflowToken = "workflow-oauth-token"

	encrypted := encryptProviderCredentialForTest(t, workflowToken, userID)
	if err := store.UpsertWorkflowProviderCredential(ctx, userID, workflowA, claudeCodeProviderID, encrypted); err != nil {
		t.Fatalf("UpsertWorkflowProviderCredential() error = %v", err)
	}
	apiKey := "direct-anthropic-key"
	base := &llm.ProviderAPIKeys{Anthropic: &apiKey}

	keysA, err := api.workflowProviderAPIKeys(ctx, userID, workflowA, base)
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(workflowA) error = %v", err)
	}
	if keysA.ClaudeCodeOAuthToken == nil || *keysA.ClaudeCodeOAuthToken != workflowToken {
		t.Fatalf("workflow A OAuth token = %#v", keysA.ClaudeCodeOAuthToken)
	}
	if keysA.Anthropic == nil || *keysA.Anthropic != apiKey {
		t.Fatalf("direct Anthropic provider key was not preserved: %#v", keysA.Anthropic)
	}

	keysB, err := api.workflowProviderAPIKeys(ctx, userID, workflowB, base)
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(workflowB) error = %v", err)
	}
	if keysB.ClaudeCodeOAuthToken != nil {
		t.Fatalf("workflow B received workflow A token: %#v", keysB.ClaudeCodeOAuthToken)
	}
	keysOtherUser, err := api.workflowProviderAPIKeys(ctx, "bob", workflowA, base)
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(other user) error = %v", err)
	}
	if keysOtherUser.ClaudeCodeOAuthToken != nil {
		t.Fatalf("other user received Alice's token: %#v", keysOtherUser.ClaudeCodeOAuthToken)
	}

	workflowSecrets, err := store.ListWorkflowSecrets(ctx, userID, workflowA)
	if err != nil {
		t.Fatalf("ListWorkflowSecrets() error = %v", err)
	}
	if len(workflowSecrets) != 0 {
		t.Fatalf("provider credential leaked into workflow secrets: %+v", workflowSecrets)
	}
}

func TestWorkflowProviderAPIKeysPreservesDeploymentClaudeTokenWithoutOverride(t *testing.T) {
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemStore() error = %v", err)
	}
	api := &StreamingAPI{chatStore: store}
	globalToken := "deployment-oauth-token"

	keys, err := api.workflowProviderAPIKeys(context.Background(), "alice", "Chats/Example Product/project", &llm.ProviderAPIKeys{
		ClaudeCodeOAuthToken: &globalToken,
	})
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys() error = %v", err)
	}
	if keys.ClaudeCodeOAuthToken == nil || *keys.ClaudeCodeOAuthToken != globalToken {
		t.Fatalf("ClaudeCodeOAuthToken = %#v, want deployment token", keys.ClaudeCodeOAuthToken)
	}
}

func encryptProviderCredentialForTest(t *testing.T, value, userID string) string {
	t.Helper()
	key := deriveSecretsKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher() error = %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM() error = %v", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("rand.Read() error = %v", err)
	}
	ciphertext := aead.Seal(nonce, nonce, []byte(value), []byte(userID))
	return base64.StdEncoding.EncodeToString(ciphertext)
}

// A workflow-scoped Cursor key must behave exactly like the Claude Code token:
// private to one user and one workflow, and never written into the ordinary
// workflow secret store where the agent could read it back.
func TestWorkflowCursorCLIKeyIsUserAndWorkflowScoped(t *testing.T) {
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemStore() error = %v", err)
	}
	api := &StreamingAPI{chatStore: store}
	ctx := context.Background()
	const userID = "alice"
	const workflowA = "Workflow/video-explainer"
	const workflowB = "Workflow/support-triage"
	const scopedKey = "crsr_workflow_scoped_key"

	encrypted := encryptProviderCredentialForTest(t, scopedKey, userID)
	if err := store.UpsertWorkflowProviderCredential(ctx, userID, workflowA, cursorCLIProviderID, encrypted); err != nil {
		t.Fatalf("UpsertWorkflowProviderCredential() error = %v", err)
	}

	keysA, err := api.workflowProviderAPIKeys(ctx, userID, workflowA, &llm.ProviderAPIKeys{})
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(workflowA) error = %v", err)
	}
	if keysA.CursorCLI == nil || *keysA.CursorCLI != scopedKey {
		t.Fatalf("workflow A Cursor key = %#v", keysA.CursorCLI)
	}

	keysB, err := api.workflowProviderAPIKeys(ctx, userID, workflowB, &llm.ProviderAPIKeys{})
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(workflowB) error = %v", err)
	}
	if keysB.CursorCLI != nil {
		t.Fatalf("workflow B received workflow A Cursor key: %#v", keysB.CursorCLI)
	}

	keysOtherUser, err := api.workflowProviderAPIKeys(ctx, "bob", workflowA, &llm.ProviderAPIKeys{})
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(other user) error = %v", err)
	}
	if keysOtherUser.CursorCLI != nil {
		t.Fatalf("other user received Alice's Cursor key: %#v", keysOtherUser.CursorCLI)
	}

	workflowSecrets, err := store.ListWorkflowSecrets(ctx, userID, workflowA)
	if err != nil {
		t.Fatalf("ListWorkflowSecrets() error = %v", err)
	}
	if len(workflowSecrets) != 0 {
		t.Fatalf("Cursor credential leaked into workflow secrets: %+v", workflowSecrets)
	}
}

// Unlike Claude Code, Cursor may already have a server-wide key in the base
// configuration. A workflow without its own key must keep using that shared
// key; clearing it would break every workflow relying on the server default.
func TestWorkflowCursorCLIKeyOverridesSharedKeyOnlyWhenPresent(t *testing.T) {
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemStore() error = %v", err)
	}
	api := &StreamingAPI{chatStore: store}
	ctx := context.Background()
	const userID = "alice"
	const scopedWorkflow = "Workflow/video-explainer"
	const plainWorkflow = "Workflow/no-scoped-key"
	sharedKey := "crsr_server_wide_key"
	const scopedKey = "crsr_workflow_scoped_key"

	encrypted := encryptProviderCredentialForTest(t, scopedKey, userID)
	if err := store.UpsertWorkflowProviderCredential(ctx, userID, scopedWorkflow, cursorCLIProviderID, encrypted); err != nil {
		t.Fatalf("UpsertWorkflowProviderCredential() error = %v", err)
	}
	base := &llm.ProviderAPIKeys{CursorCLI: &sharedKey}

	scoped, err := api.workflowProviderAPIKeys(ctx, userID, scopedWorkflow, base)
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(scoped) error = %v", err)
	}
	if scoped.CursorCLI == nil || *scoped.CursorCLI != scopedKey {
		t.Fatalf("workflow key did not override the shared key: %#v", scoped.CursorCLI)
	}

	plain, err := api.workflowProviderAPIKeys(ctx, userID, plainWorkflow, base)
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(plain) error = %v", err)
	}
	if plain.CursorCLI == nil || *plain.CursorCLI != sharedKey {
		t.Fatalf("a workflow without its own key lost the shared key: %#v", plain.CursorCLI)
	}
	if sharedKey != "crsr_server_wide_key" {
		t.Fatal("the base key was mutated through the clone")
	}
}

func TestWorkflowPiCLIGeminiKeyIsUserAndWorkflowScoped(t *testing.T) {
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemStore() error = %v", err)
	}
	api := &StreamingAPI{chatStore: store}
	ctx := context.Background()
	const userID = "alice"
	const workflowA = "Workflow/video-explainer"
	const workflowB = "Workflow/support-triage"
	const scopedKey = "gemini_workflow_scoped_key"

	encrypted := encryptProviderCredentialForTest(t, scopedKey, userID)
	if err := store.UpsertWorkflowProviderCredential(ctx, userID, workflowA, piCLIProviderID, encrypted); err != nil {
		t.Fatalf("UpsertWorkflowProviderCredential() error = %v", err)
	}

	keysA, err := api.workflowProviderAPIKeys(ctx, userID, workflowA, &llm.ProviderAPIKeys{})
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(workflowA) error = %v", err)
	}
	if keysA.PiProviderKeys["google"] != scopedKey {
		t.Fatalf("workflow A Pi CLI Gemini key = %#v", keysA.PiProviderKeys)
	}

	keysB, err := api.workflowProviderAPIKeys(ctx, userID, workflowB, &llm.ProviderAPIKeys{})
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(workflowB) error = %v", err)
	}
	if _, ok := keysB.PiProviderKeys["google"]; ok {
		t.Fatalf("workflow B received workflow A's Pi CLI Gemini key: %#v", keysB.PiProviderKeys)
	}

	keysOtherUser, err := api.workflowProviderAPIKeys(ctx, "bob", workflowA, &llm.ProviderAPIKeys{})
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(other user) error = %v", err)
	}
	if _, ok := keysOtherUser.PiProviderKeys["google"]; ok {
		t.Fatalf("other user received Alice's Pi CLI Gemini key: %#v", keysOtherUser.PiProviderKeys)
	}
}

// Pi CLI routes by sub-provider, so a workflow-scoped Gemini key must land in
// PiProviderKeys["google"] specifically and must not disturb any other
// sub-provider key (zai, minimax, ...) already present in the shared base --
// those have nothing to do with Example Product's Gemini-pinned Pi CLI option.
func TestWorkflowPiCLIGeminiKeyOverridesSharedKeyOnlyWhenPresent(t *testing.T) {
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemStore() error = %v", err)
	}
	api := &StreamingAPI{chatStore: store}
	ctx := context.Background()
	const userID = "alice"
	const scopedWorkflow = "Workflow/video-explainer"
	const plainWorkflow = "Workflow/no-scoped-key"
	const sharedGeminiKey = "gemini_server_wide_key"
	const sharedZAIKey = "zai_server_wide_key"
	const scopedKey = "gemini_workflow_scoped_key"

	encrypted := encryptProviderCredentialForTest(t, scopedKey, userID)
	if err := store.UpsertWorkflowProviderCredential(ctx, userID, scopedWorkflow, piCLIProviderID, encrypted); err != nil {
		t.Fatalf("UpsertWorkflowProviderCredential() error = %v", err)
	}
	base := &llm.ProviderAPIKeys{PiProviderKeys: map[string]string{"google": sharedGeminiKey, "zai": sharedZAIKey}}

	scoped, err := api.workflowProviderAPIKeys(ctx, userID, scopedWorkflow, base)
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(scoped) error = %v", err)
	}
	if scoped.PiProviderKeys["google"] != scopedKey {
		t.Fatalf("workflow key did not override the shared Gemini key: %#v", scoped.PiProviderKeys)
	}
	if scoped.PiProviderKeys["zai"] != sharedZAIKey {
		t.Fatalf("an unrelated sub-provider key was disturbed: %#v", scoped.PiProviderKeys)
	}

	plain, err := api.workflowProviderAPIKeys(ctx, userID, plainWorkflow, base)
	if err != nil {
		t.Fatalf("workflowProviderAPIKeys(plain) error = %v", err)
	}
	if plain.PiProviderKeys["google"] != sharedGeminiKey {
		t.Fatalf("a workflow without its own key lost the shared Gemini key: %#v", plain.PiProviderKeys)
	}
	if base.PiProviderKeys["google"] != sharedGeminiKey {
		t.Fatal("the base key was mutated through the clone")
	}
}

// resolveEffectiveAPIKeys is the single call site every part of this package
// should use for a turn's effective provider keys — see its doc comment for
// why. This test proves the two things that actually mattered when it was
// missing: base=nil self-computes rather than panicking, and a non-nil base
// is layered rather than discarded.
func TestResolveEffectiveAPIKeysSelfComputesBaseWhenNil(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemStore() error = %v", err)
	}
	api := &StreamingAPI{chatStore: store}
	const userID = "alice"
	const workspacePath = "Chats/Example Product/projects/launch"
	const scopedKey = "crsr_scoped_key"
	encrypted := encryptProviderCredentialForTest(t, scopedKey, userID)
	if err := store.UpsertWorkflowProviderCredential(context.Background(), userID, workspacePath, cursorCLIProviderID, encrypted); err != nil {
		t.Fatalf("UpsertWorkflowProviderCredential() error = %v", err)
	}

	keys, err := api.resolveEffectiveAPIKeys(context.Background(), userID, workspacePath, nil)
	if err != nil {
		t.Fatalf("resolveEffectiveAPIKeys(nil base) error = %v", err)
	}
	if keys == nil || keys.CursorCLI == nil || *keys.CursorCLI != scopedKey {
		t.Fatalf("nil base did not self-compute and layer the scoped credential: %+v", keys)
	}
}

func TestResolveEffectiveAPIKeysLayersOntoProvidedBase(t *testing.T) {
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemStore() error = %v", err)
	}
	api := &StreamingAPI{chatStore: store}
	const userID = "alice"
	const workspacePath = "Chats/Example Product/projects/launch"
	const scopedKey = "crsr_scoped_key"
	encrypted := encryptProviderCredentialForTest(t, scopedKey, userID)
	if err := store.UpsertWorkflowProviderCredential(context.Background(), userID, workspacePath, cursorCLIProviderID, encrypted); err != nil {
		t.Fatalf("UpsertWorkflowProviderCredential() error = %v", err)
	}
	directKey := "direct-anthropic-key"
	base := &llm.ProviderAPIKeys{Anthropic: &directKey}

	keys, err := api.resolveEffectiveAPIKeys(context.Background(), userID, workspacePath, base)
	if err != nil {
		t.Fatalf("resolveEffectiveAPIKeys(base) error = %v", err)
	}
	if keys.Anthropic == nil || *keys.Anthropic != directKey {
		t.Fatalf("provided base was not preserved: %+v", keys)
	}
	if keys.CursorCLI == nil || *keys.CursorCLI != scopedKey {
		t.Fatalf("scoped credential was not layered onto the provided base: %+v", keys)
	}
	if base.Anthropic == nil || *base.Anthropic != directKey {
		t.Fatal("the caller's base was mutated in place")
	}
}

// This is the actual consolidation guarantee: every place in this package
// that wants a turn's effective provider keys goes through
// resolveEffectiveAPIKeys, not a direct call to the lower-level
// workflowProviderAPIKeys. Before this, five call sites independently
// reimplemented the same two-line pattern; two of them gated it incorrectly,
// and Example Product's Cursor turns silently ran under the server's shared
// login for two commits before either gate was caught and fixed. A future
// edit that adds a sixth direct call — instead of routing through the shared
// function — reintroduces exactly that risk, and this test catches it at
// compile-test time rather than in a live "I saved a key and it still
// failed" report.
func TestWorkflowProviderAPIKeysHasExactlyOneCallSite(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read cmd/server: %v", err)
	}
	fset := token.NewFileSet()
	var callSites []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "workflowProviderAPIKeys" {
				return true
			}
			pos := fset.Position(sel.Pos())
			callSites = append(callSites, fmt.Sprintf("%s:%d", name, pos.Line))
			return true
		})
	}
	if len(callSites) != 1 {
		t.Fatalf("workflowProviderAPIKeys called from %d site(s), want exactly 1 (inside resolveEffectiveAPIKeys): %v.\n"+
			"If you added a new caller, route it through resolveEffectiveAPIKeys instead — that is the whole point of consolidating this.",
			len(callSites), callSites)
	}
}

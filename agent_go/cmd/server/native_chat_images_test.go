package server

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-1000: only a writer-admitted attachment in this exact caller's chat
// becomes vision input; typed paths, another tab/person, current access loss
// and a replaced symlink cannot grant a native file read. Queue JSON carries
// references only and a fresh delivery rechecks access.
func TestNativeChatImagesUseAdmittedReferencesAndCurrentCrewAccess(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	filePath := filepath.Join(docs, linkBetaPath, "uploads", "image.png")
	if err := os.MkdirAll(filepath.Dir(filePath), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(file, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	file.Close()
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	req := QueryRequest{AgentProfileID: crewProfileID, AgentProfileConversationKey: "beta:chat:side", Query: "Inspect" + filesInContextMarker + " " + linkBetaPath + "/uploads/image.png"}
	// A path typed in text has no attachment capability.
	if inputs := llmtypes.NativeImageInputsForSession(env.api.withNativeChatImages(ctx, "owner", "chat", "codex-cli", req), "chat"); len(inputs) != 0 {
		t.Fatal("typed path became vision input")
	}
	hash := strings.Repeat("a", 64)
	req.PendingAttachments = []pendingChatAttachment{{Hash: hash, Name: "ignored-client-path"}}
	key := pendingAttachmentKey("owner", "chat", req, hash)
	env.api.planPendingChatUpload(key)
	stat, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	env.api.finishPendingChatUpload(key, linkBetaPath+"/uploads/image.png", stat.Size())
	if err = env.api.validatePendingChatAttachments("owner", "chat", req); err != nil {
		t.Fatal(err)
	}
	// More than16 admitted files must not reject the text before the native
	// payload limiter can skip excess images. Every reference is still validated.
	many := req
	many.PendingAttachments = make([]pendingChatAttachment, 17)
	for i := range many.PendingAttachments {
		many.PendingAttachments[i] = req.PendingAttachments[0]
	}
	if err = env.api.validatePendingChatAttachments("owner", "chat", many); err != nil {
		t.Fatal("attachment count swallowed text", err)
	}
	many.PendingAttachments = make([]pendingChatAttachment, 100)
	for i := range many.PendingAttachments {
		many.PendingAttachments[i] = req.PendingAttachments[0]
	}
	if err = env.api.validatePendingChatAttachments("owner", "chat", many); err != nil {
		t.Fatal("100 admitted references refused", err)
	}
	many.PendingAttachments = append(many.PendingAttachments, req.PendingAttachments[0])
	if err = env.api.validatePendingChatAttachments("owner", "chat", many); err == nil {
		t.Fatal("unbounded reference count admitted")
	}
	ctx = env.api.bindReadyChatAttachments(ctx, "owner", "chat", &req)
	principal := queuedPrincipalFromContext(ctx)
	raw, err := json.Marshal(principal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "base64") || len(principal.Attachments) != 1 || len(req.PendingAttachments) != 0 {
		t.Fatal("queue did not retain only admitted references")
	}
	// Simulate restart: no pending upload cache is needed for a ready attachment.
	env.api.pendingChatUploads = nil
	turn := queuedConversationTurn{ID: "turn", UserID: "owner", SessionID: "chat", Request: req, Principal: principal}
	replay, err := env.api.queuedConversationTurnContext(turn, nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery := env.api.withNativeChatImages(replay, "owner", "chat", "codex-cli", req)
	inputs := llmtypes.NativeImageInputsForSession(delivery, "chat")
	if len(inputs) != 1 {
		t.Fatal("queue lost admitted image")
	}
	reader, mime, n, err := inputs[0].Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || n == 0 {
		t.Fatal("missing prepared image")
	}
	if _, err = io.Copy(io.Discard, reader); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	// Claude SDK consumes the same admitted factory and access checks. Its
	// terminal transport (and a cold Codex terminal default) stays path-only.
	sdkRequest := req
	sdkRequest.ClaudeCodeTransport = "sdk"
	sdkContext := env.api.withNativeChatImages(replay, "owner", "chat", "claude-code", sdkRequest)
	sdkImages := llmtypes.NativeImageInputsForSession(sdkContext, "chat")
	if len(sdkImages) != 1 {
		t.Fatal("Claude SDK lost admitted image")
	}
	sdkReader, _, _, sdkErr := sdkImages[0].Open(context.Background())
	if sdkErr != nil {
		t.Fatal(sdkErr)
	}
	sdkReader.Close()
	piContext := env.api.withNativeChatImages(replay, "owner", "chat", "pi-cli", req)
	piImages := llmtypes.NativeImageInputsForSession(piContext, "chat")
	if len(piImages) != 1 {
		t.Fatal("Pi RPC lost admitted image")
	}
	piReader, _, _, piErr := piImages[0].Open(context.Background())
	if piErr != nil {
		t.Fatal(piErr)
	}
	piReader.Close()
	sdkRequest.ClaudeCodeTransport = "tmux"
	if len(llmtypes.NativeImageInputsForSession(env.api.withNativeChatImages(replay, "owner", "chat", "claude-code", sdkRequest), "chat")) != 0 {
		t.Fatal("image bytes offered to Claude terminal")
	}
	t.Setenv("CODEX_CLI_TRANSPORT", "tmux")
	if len(llmtypes.NativeImageInputsForSession(env.api.withNativeChatImages(replay, "owner", "chat", "codex-cli", req), "chat")) != 0 {
		t.Fatal("image bytes offered to cold Codex terminal")
	}
	t.Setenv("CODEX_CLI_TRANSPORT", "app-server")
	if len(llmtypes.NativeImageInputsForSession(delivery, "another-chat")) != 0 {
		t.Fatal("grant crossed native session")
	}
	wrong := env.api.withNativeChatImages(replay, "owner", "another-chat", "codex-cli", req)
	if len(llmtypes.NativeImageInputsForSession(wrong, "another-chat")) != 0 {
		t.Fatal("queue grant crossed host session")
	}
	// Revoke current principal/crew access after admission.
	denied := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
	if _, err = env.api.openAdmittedChatImage(denied, principal.Attachments[0]); err == nil {
		t.Fatal("another person read admitted image")
	}
	outside := filepath.Join(t.TempDir(), "private.png")
	if err = os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filePath); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filePath); err != nil {
		t.Fatal(err)
	}
	if reader, _, _, err = inputs[0].Open(context.Background()); err == nil {
		reader.Close()
		t.Fatal("replaced symlink reached native input")
	}
}

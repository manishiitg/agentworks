package server

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3189: an attached PDF is given to Claude (SDK) as a document, with the admitted-file checks images use. Codex
// keeps the saved path only, and a file that is not really a PDF is not sent.
func TestNativeChatPDFGoesToClaudeOnly(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	attach := func(name, content, digit string) (QueryRequest, context.Context) {
		path := filepath.Join(docs, linkBetaPath, "uploads", name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
		req := QueryRequest{AgentProfileID: crewProfileID, AgentProfileConversationKey: "beta:chat:side", Query: "Summarise", ClaudeCodeTransport: "sdk"}
		hash := strings.Repeat(digit, 64)
		req.PendingAttachments = []pendingChatAttachment{{Hash: hash, Name: name}}
		key := pendingAttachmentKey("owner", "chat", req, hash)
		env.api.planPendingChatUpload(key)
		env.api.finishPendingChatUpload(key, linkBetaPath+"/uploads/"+name, int64(len(content)))
		ctx = env.api.bindReadyChatAttachments(ctx, "owner", "chat", &req)
		return req, ctx
	}
	pdf := "%PDF-1.7\nexample"
	req, ctx := attach("report.pdf", pdf, "b")
	inputs := llmtypes.NativeImageInputsForSession(env.api.withNativeChatImages(ctx, "owner", "chat", "claude-code", req), "chat")
	if len(inputs) != 1 {
		t.Fatalf("Claude did not get the PDF: %d inputs", len(inputs))
	}
	reader, mime, n, err := inputs[0].Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(reader)
	reader.Close()
	if mime != "application/pdf" || n != int64(len(pdf)) || string(got) != pdf {
		t.Fatalf("mime=%s n=%d got=%q", mime, n, got)
	}
	if !strings.Contains(req.Query, "report.pdf") {
		t.Fatalf("saved path left the message: %q", req.Query)
	}
	if inputs := llmtypes.NativeImageInputsForSession(env.api.withNativeChatImages(ctx, "owner", "chat", "codex-cli", req), "chat"); len(inputs) != 0 {
		t.Fatal("Codex was given a PDF input; it keeps the path")
	}
	fakeReq, fakeCtx := attach("fake.pdf", "not a pdf", "c")
	fake := llmtypes.NativeImageInputsForSession(env.api.withNativeChatImages(fakeCtx, "owner", "chat", "claude-code", fakeReq), "chat")
	if len(fake) != 1 {
		t.Fatal("fake PDF reference missing")
	}
	if r, _, _, err := fake[0].Open(context.Background()); err == nil {
		r.Close()
		t.Fatal("a file without a PDF header was sent")
	}
}

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

func TestCodeChatAttachmentScopeAndRetainedImageReader(t *testing.T) {
	const folder = "Chats/Code/projects/demo"
	image := codeChatAttachmentFolder(folder, "chat-one") + "/screen.png"
	req := QueryRequest{userID: "owner", AgentProfileID: "code", CodeChatMode: "local", SelectedFolder: folder, CodeChatAttachments: []string{image}}
	paths, err := codeChatAttachmentPaths(req, "owner", "chat-one")
	if err != nil || len(paths) != 1 {
		t.Fatalf("scope %v %v", paths, err)
	}
	for _, bad := range []string{codeChatAttachmentFolder(folder, "chat-two") + "/screen.png", folder + "/private.png", image + "/../private.png", "_users/other/" + image} {
		req.CodeChatAttachments = []string{bad}
		if _, err := codeChatAttachmentPaths(req, "owner", "chat-one"); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	req.CodeChatAttachments = paths
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("X-User-ID") != "owner" || !strings.HasPrefix(r.URL.Path, "/api/chat-attachments/") {
			t.Error("missing attachment identity")
		}
		json.NewEncoder(w).Encode(codeChatAttachment{IsImage: true, MimeType: "image/png", Data: "Zml4dHVyZQ=="})
	}))
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	api := &StreamingAPI{lastQueryRequests: map[string]QueryRequest{"chat-one": req}}
	reader := api.codeChatImageReader("chat-one", "owner")
	args := map[string]any{"filepath": filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(paths[0])), "query": "Describe"}
	if _, err := reader(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatal("image not fetched")
	}
	other := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
	if _, err := reader(other, args); err == nil || requests != 1 {
		t.Fatal("another user read retained attachments")
	}
	args["filepath"] = filepath.Join(fsutil.WorkspaceDocsRoot(), "private.png")
	if _, err := reader(context.Background(), args); err == nil || requests != 1 {
		t.Fatal("unattached image read")
	}
	args["filepath"] = filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(paths[0]))
	req.TriggeredBy = "auto_notification"
	api.lastQueryRequests["chat-one"] = req
	if _, err := reader(context.Background(), args); err == nil || requests != 1 {
		t.Fatal("automated turn read attachments")
	}
	req.TriggeredBy = ""
	req.CodeChatAttachments = nil
	api.lastQueryRequests["chat-one"] = req
	if _, err := reader(context.Background(), args); err == nil || requests != 1 {
		t.Fatal("removed attachment retained access")
	}
	definition := codeproduct.BuiltinAgentProfile()
	_, disabled := restrictCodeLocalFeatures(&definition)
	p := &resolvedAgentProfile{Definition: definition, CodeLocalDisabledTools: disabled, CodeChatMode: "local", CodeChatAttachments: paths}
	gate := newProductToolGate(p)
	// The real files feature must admit the scoped reader without a redeclaration.
	if !gate.Allows("read_image") || gate.Allows("read_workspace_file") || gate.Allows("execute_shell_command") {
		t.Fatal("attachment policy widened other tools")
	}
}

func TestPrepareCodeChatAttachmentsBoundedTextAndOwnerScope(t *testing.T) {
	t.Setenv("WORKSPACE_API_TOKEN", "fixture-workspace-token")
	requests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("X-User-ID") != "owner" || r.Header.Get("X-Workspace-Token") != "fixture-workspace-token" {
			t.Error("workspace authentication missing")
		}
		json.NewEncoder(w).Encode(codeChatAttachment{Content: strings.Repeat("x", 80<<10)})
	}))
	defer backend.Close()
	t.Setenv("WORKSPACE_API_URL", backend.URL)
	api := &StreamingAPI{}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	profile := &resolvedAgentProfile{Definition: agentprofiles.Profile{ID: "code"}, CodeChatMode: "local"}
	folder := "Chats/Code/projects/demo"
	uploads := codeChatAttachmentFolder(folder, "chat-one")
	req := QueryRequest{Query: "Compare these", AgentProfileID: "code", CodeChatMode: "local", SelectedFolder: folder, CodeChatAttachments: []string{uploads + "/screen.png"}}
	for i := 0; i < 5; i++ {
		req.CodeChatAttachments = append(req.CodeChatAttachments, uploads+"/notes"+string(rune('a'+i))+".txt")
	}
	if err := api.prepareCodeChatAttachments(ctx, &req, profile, "owner", "chat-one"); err != nil {
		t.Fatal(err)
	}
	if requests != 5 || strings.Count(req.Query, "x") > (256<<10)+10 || !strings.Contains(req.Query, `"truncated":true`) || !strings.Contains(req.Query, "image_filepath") || len(profile.CodeChatAttachments) != 6 {
		t.Fatal("attachments not bounded or propagated")
	}
	req.TriggeredBy = "auto_notification"
	if err := api.prepareCodeChatAttachments(ctx, &req, profile, "owner", "chat-one"); err == nil || requests != 5 {
		t.Fatal("background attachment turn accepted")
	}
}

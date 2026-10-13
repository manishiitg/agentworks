package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// Exercise the seam missed by tests starting with a logical SelectedFolder:
// native registry bindings are physical, but the real upload returns logical.
func TestCodeChatUploadWithPhysicalProductBinding(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("WORKSPACE_API_TOKEN", "attachment-test-token")
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	const logicalRoot = "Chats/Code/projects/demo"
	const physicalRoot = "_users/owner/" + logicalRoot
	if err := os.MkdirAll(filepath.Join(docs, filepath.FromSlash(physicalRoot)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, filepath.FromSlash(physicalRoot), "product.json"), []byte(`{"schema_version":1,"product":"code","id":"demo","title":"Upload example","session_id":"code:project:demo"}`), 0600); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/api/upload", workspacehandlers.UploadFile)
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	fetches := 0
	router.GET("/api/chat-attachments/*filepath", func(c *gin.Context) {
		fetches++
		if c.GetHeader("X-User-ID") != "owner" || c.GetHeader("X-Workspace-Token") != "attachment-test-token" {
			t.Error("attachment fetch lost owner authentication")
		}
		workspacehandlers.GetChatAttachment(c)
	})
	backend := httptest.NewServer(router)
	defer backend.Close()
	t.Setenv("WORKSPACE_API_URL", backend.URL)
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner"})
	definition := codeproduct.BuiltinAgentProfile()
	api := &StreamingAPI{}
	registry := defaultProductConversationRegistryStore()
	for _, key := range []string{"demo", "demo:chat:side-one"} {
		binding, err := resolveProductProjectBinding(ctx, "owner", definition, key)
		if err != nil || binding.WorkspacePath != physicalRoot {
			t.Fatalf("native physical binding: %s %v", binding.WorkspacePath, err)
		}
		conversation, err := registry.resolveOrCreate(ctx, "owner", definition, binding, "")
		if err != nil {
			t.Fatal(err)
		}
		folder := codeChatAttachmentFolder(logicalRoot, conversation.SessionID)
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		if err := form.WriteField("folder_path", folder); err != nil {
			t.Fatal(err)
		}
		file, err := form.CreateFormFile("file", "profiles (1).csv")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("item,qty\ndemo,1\n")); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		upload := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
		upload.Header.Set("Content-Type", form.FormDataContentType())
		upload.Header.Set("X-User-ID", "owner")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, upload)
		var result struct {
			Data struct {
				FilePath string `json:"filepath"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK || result.Data.FilePath != folder+"/profiles (1).csv" {
			t.Fatalf("upload must return logical path: %d %s %v", response.Code, response.Body, err)
		}
		logical := result.Data.FilePath
		physical := "_users/owner/" + logical
		for _, attached := range [][]string{{logical}, {physical}, {logical, physical}} {
			req, err := queryRequestForAgentProfileChat(definition, AgentProfileChatRequest{Message: "Read column names", CodeChatMode: "local", CodeChatAttachments: attached}, conversation)
			if err != nil || req.SelectedFolder != physicalRoot {
				t.Fatalf("product query lost physical binding: %s %v", req.SelectedFolder, err)
			}
			if _, err := cleanAgentProfileWorkspace(req.SelectedFolder, "owner"); err != nil {
				t.Fatal(err)
			}
			profile := &resolvedAgentProfile{Definition: definition, CodeChatMode: "local"}
			if err := api.prepareCodeChatAttachments(ctx, &req, profile, "owner", conversation.SessionID); err != nil {
				t.Fatalf("%s uploaded attachment refused: %v", key, err)
			}
			if len(req.CodeChatAttachments) != 1 || req.CodeChatAttachments[0] != physical || !strings.Contains(req.Query, `item,qty\ndemo,1\n`) {
				t.Fatalf("attachment not normalized/fetched once: %+v", req)
			}
		}
		before := fetches
		for _, bad := range []string{"_users/other/" + logical, codeChatAttachmentFolder(logicalRoot, "another-chat") + "/profiles (1).csv", folder + "/../profiles (1).csv", path.Join(folder, "nested", "profiles (1).csv")} {
			req := QueryRequest{AgentProfileID: "code", CodeChatMode: "local", SelectedFolder: physicalRoot, CodeChatAttachments: []string{bad}}
			if err := api.prepareCodeChatAttachments(ctx, &req, &resolvedAgentProfile{Definition: definition}, "owner", conversation.SessionID); err == nil || fetches != before {
				t.Fatalf("invalid upload reached fetch: %s %v", bad, err)
			}
		}
	}
	if fetches != 6 {
		t.Fatalf("expected one real fetch per accepted request, got %d", fetches)
	}
}

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

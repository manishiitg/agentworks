//go:build linux

package workspace

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// Exercise the real HTTP shell and file readers using an ordinary Crew chat's
// existing project/history grants, then the actual confined native shell.
func TestCrewShellSpillReadableWithExistingChatGrants(t *testing.T) {
	if os.Getenv("AGENTWORKS_LANDLOCK_RUNNER") == "" {
		if os.Getenv("VERIFY_SLOT") != "" {
			t.Fatal("remote verification must provide the actual Landlock launcher")
		}
		t.Skip("requires the real Landlock launcher (remote verifier supplies it)")
	}
	docs, err := os.MkdirTemp(".", "crew-shell-log-*")
	if err != nil {
		t.Fatal(err)
	}
	docs, _ = filepath.Abs(docs)
	defer os.RemoveAll(docs)
	for _, dir := range []string{"Crew/project", "_users/person/chat_history", "_users/other/chat_history"} {
		if err := os.MkdirAll(filepath.Join(docs, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(docs, "_users/other/chat_history/private.log")
	if err := os.WriteFile(other, []byte("another person's private fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_SLOTS", "off") // disposable fixture, no server account or model
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	previous := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	defer viper.Set("docs-dir", previous)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/execute", handlers.ExecuteShellCommand)
	router.GET("/api/documents/*filepath", handlers.HandleDocumentRequest)
	server := httptest.NewServer(router)
	defer server.Close()
	const session = "crew-spill-readable-fixture"
	defer ClearSessionShellConfig(session)
	SetSessionWorkingDir(session, "Crew/project")
	SetSessionFolderGuard(session, []string{"Crew/project", "_users/person/chat_history"}, []string{"Crew/project", "_users/person/chat_history"})
	SetSessionFolderGuardBlockedPaths(session, []string{"_users/other/chat_history"})
	ctx := context.WithValue(t.Context(), common.ChatSessionIDKey, session)
	client := NewClient(server.URL)
	client.UserID = "person"
	result, err := client.ExecuteShellCommand(ctx, ExecuteShellCommandParams{Command: `python3 -c 'import sys; sys.stdout.write("Q" * 2097152)'`})
	if err != nil || result.ExitCode != 0 || result.LogFile == "" || result.LogError != "" || !result.LogComplete {
		t.Fatalf("spill failed: err=%v result=%+v", err, result)
	}
	read, err := client.ReadWorkspaceFile(ctx, ReadWorkspaceFileParams{Filepath: result.LogFile})
	if err != nil || strings.Count(read.Content, "Q") != 2097152 {
		t.Fatalf("model file reader cannot read complete log: %v bytes=%d", err, len(read.Content))
	}
	if _, err := client.ReadWorkspaceFile(ctx, ReadWorkspaceFileParams{Filepath: other}); err == nil {
		t.Fatal("file reader gained another person's chat")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	otherRelative, _ := filepath.Rel(filepath.Join(docs, "Crew/project"), other)
	command := fmt.Sprintf("python3 -c %s %s && if cat %s >/dev/null 2>&1; then exit 42; else printf native-deny-ok; fi", quote(`import sys; from pathlib import Path; assert Path(sys.argv[1]).read_text().count("Q") == 2097152; print("native-read-ok")`), quote(result.LogFile), quote(otherRelative))
	native, err := client.ExecuteShellCommand(ctx, ExecuteShellCommandParams{Command: command})
	if err != nil || native.ExitCode != 0 || !strings.Contains(native.Stdout, "native-read-ok") || !strings.Contains(native.Stdout, "native-deny-ok") {
		t.Fatalf("native log read/refusal: %v %+v", err, native)
	}
}

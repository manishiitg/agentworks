//go:build linux

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/spf13/viper"
)

// A Code agent's shell call blocks the project's db/db.sqlite. That used to push the command off Landlock onto the
// mount-namespace backend, which ran it as the service account: it read the platform's .env and wrote the service
// account's home (Excellence 2026-10-03). It must run as the user's slot, under Landlock, with the file hidden.
// Opt-in, same settings as TestInteractiveShellRunsAsTheUsersSlotE2E.
func TestShellWithABlockedFileRunsAsTheUsersSlotE2E(t *testing.T) {
	user := os.Getenv("AGENTWORKS_SHELL_SLOT_E2E_USER")
	docs := os.Getenv("AGENTWORKS_SHELL_SLOT_E2E_DOCS")
	if user == "" || docs == "" {
		t.Skip("set AGENTWORKS_SHELL_SLOT_E2E_USER and AGENTWORKS_SHELL_SLOT_E2E_DOCS to run")
	}
	slot, on, err := slots.For(user)
	if !on || err != nil || slot == "" {
		t.Fatalf("this user has no slot here (on=%v err=%v slot=%q)", on, err, slot)
	}
	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docs)
	project := "_users/" + user + "/Chats/Code/projects/zz-shell-blocked-e2e"
	abs := filepath.Join(docs, project)
	if err := os.MkdirAll(filepath.Join(abs, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(abs, 0o770|os.ModeSetgid)
	defer os.RemoveAll(abs)
	if err := os.WriteFile(filepath.Join(abs, "db", "db.sqlite"), []byte("SECRET-DB"), 0o660); err != nil {
		t.Fatal(err)
	}
	platformEnv := filepath.Join(filepath.Dir(filepath.Dir(docs)), ".env")
	command := "id -un; cat db/db.sqlite 2>&1; echo; echo junk > db/db.sqlite 2>&1 && echo WROTE_DB; head -c 20 " + shellQuote(platformEnv) + " >/dev/null 2>&1 && echo READ_ENV; echo \"HOME=$HOME\"; touch /srv/agents/home/zz_blocked_probe 2>/dev/null && echo WROTE_SERVICE_HOME; echo ok > ok.txt && echo WROTE_PROJECT"
	body, _ := json.Marshal(map[string]any{
		"command": command, "working_directory": project, "timeout": 60, "use_shell": true,
		"folder_guard": map[string]any{"enabled": true, "strict_allowlist": true,
			"read_paths": []string{project + "/"}, "write_paths": []string{project + "/"},
			"blocked_paths":       []string{project + "/db/db.sqlite", project + "/db/db.sqlite-wal"},
			"blocked_write_paths": []string{project + "/workflow.json", project + "/AGENTS.md"}},
	})
	router := gin.New()
	router.POST("/execute", ExecuteShellCommand)
	req := httptest.NewRequest(http.MethodPost, "/execute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", user)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var resp struct {
		Data struct {
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
		} `json:"data"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	out := resp.Data.Stdout
	t.Logf("status=%d stdout=%q stderr=%q msg=%q", w.Code, out, resp.Data.Stderr, resp.Message)
	if w.Code != http.StatusOK {
		t.Fatalf("the command did not run (%d)", w.Code)
	}
	if first := strings.SplitN(out, "\n", 2)[0]; first != slot {
		t.Errorf("ran as %q, want the user's slot %q", first, slot)
	}
	for _, leak := range []string{"SECRET-DB", "WROTE_DB", "READ_ENV", "WROTE_SERVICE_HOME"} {
		if strings.Contains(out, leak) {
			t.Errorf("SECURITY: %s", leak)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(abs, "db", "db.sqlite")); string(raw) != "SECRET-DB" {
		t.Errorf("the real blocked file changed: %q", raw)
	}
	// The command's HOME is a private home inside the project, never the service account's.
	if !strings.Contains(out, "HOME="+abs) && !strings.Contains(out, "HOME="+filepath.Join(docs, project)) {
		t.Errorf("HOME must be inside the project: %q", out)
	}
	if !strings.Contains(out, "WROTE_PROJECT") {
		t.Errorf("the project must stay writable: %q", out)
	}
}

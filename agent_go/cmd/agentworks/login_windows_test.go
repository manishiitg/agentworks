package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

func TestWindowsSavedLoginCommands(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	token := "aw_pat_" + strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("saved credential was not used")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tools":[]}`))
	}))
	defer srv.Close()
	invoke := func(input string, args ...string) (int, string, string) {
		var out, problems bytes.Buffer
		code := run(context.Background(), args, strings.NewReader(input), &out, &problems, func(string) string { return "" })
		if strings.Contains(out.String()+problems.String(), token) {
			t.Fatal("credential printed")
		}
		return code, out.String(), problems.String()
	}
	for _, name := range []string{"default", "work"} {
		args := []string{"--server", srv.URL, "login", "--token-stdin"}
		if name != "default" {
			args = append(args, "--name", name)
		}
		if code, _, problem := invoke(token, args...); code != 0 {
			t.Fatalf("login %s: %s", name, problem)
		}
		path, err := agentworksclient.LoginPath(name)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := agentworksclient.LoadConfig(path)
		if err != nil || cfg.Token != token {
			t.Fatalf("load %s: %v", name, err)
		}
	}
	if code, out, problem := invoke("", "logins"); code != 0 || !strings.Contains(out, "default") || !strings.Contains(out, "work") {
		t.Fatalf("logins: %s %s", out, problem)
	}
	defaultPath, err := agentworksclient.DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if code, _, problem := invoke("", "--config", defaultPath, "status"); code != 0 {
		t.Fatalf("status: %s", problem)
	}
	if code, _, problem := invoke("", "tools", "list"); code != 0 {
		t.Fatalf("tools: %s", problem)
	}
	// One unreadable profile must not disappear behind "No saved logins" or
	// expose malformed secret values. Other valid entries remain visible.
	work, _ := agentworksclient.LoginPath("work")
	if err := os.WriteFile(work, []byte(`{"expires_at":"malformed-secret-value"}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, problem := invoke("", "logins")
	if code == 0 || !strings.Contains(out, "default") || !strings.Contains(problem, `saved login "work"`) || strings.Contains(problem, work) || strings.Contains(problem, "malformed-secret-value") {
		t.Fatalf("invalid profile was hidden or leaked: %s %s", out, problem)
	}
	def, _ := agentworksclient.DefaultConfigPath()
	if err := os.WriteFile(def, []byte(`{"expires_at":"malformed-secret-value"}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, _, problem = invoke("", "--config", def, "status")
	if code == 0 || !strings.Contains(problem, "saved login cannot be loaded") || strings.Contains(problem, def) || strings.Contains(problem, "malformed-secret-value") {
		t.Fatalf("invalid default status: %s", problem)
	}
}

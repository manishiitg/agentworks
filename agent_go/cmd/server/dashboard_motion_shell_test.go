package server

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
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/dashboardmotion"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// Exercises the real workspace request handler, environment filtering and
// Python process. The shell must remove the old custom key while the actual
// fixed exporter still receives its configuration as a process argument.
func TestDashboardMotionConfigThroughWorkspaceShell(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("DASHBOARD_MOTION_ARGS", "")
	previous := viper.Get("docs-dir")
	viper.Set("docs-dir", root)
	defer viper.Set("docs-dir", previous)
	args, err := json.Marshal(map[string]string{"composition_path": "code/motion/config-check'$(touch escaped)", "output_path": "db/assets/motion/check", "cache_path": ".sandbox-cache/dashboard-motion", "renderer_version": dashboardmotion.RendererVersion, "caller": "author"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/api/execute", workspacehandlers.ExecuteShellCommand)
	for _, missing := range []bool{false, true} {
		command := dashboardmotion.Command(args)
		if missing {
			command = strings.TrimSuffix(dashboardmotion.Command(nil), " ''")
		}
		// The original env handoff is intentionally present but invalid. The
		// production handler must filter it, not use a hand-rolled test filter.
		command = "if [ -n \"${DASHBOARD_MOTION_ARGS:-}\" ]; then echo UNFILTERED; exit 99; fi; " + command
		body, _ := json.Marshal(map[string]any{"command": command, "timeout": 15, "use_shell": true, "extra_env": map[string]string{"DASHBOARD_MOTION_ARGS": "invalid-json"}})
		request := httptest.NewRequest(http.MethodPost, "/api/execute", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		var reply struct {
			Data struct {
				Stdout string `json:"stdout"`
				Stderr string `json:"stderr"`
				Exit   int    `json:"exit_code"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
			t.Fatalf("workspace response: %s (%v)", response.Body, err)
		}
		var result struct {
			Success bool   `json:"success"`
			Error   string `json:"error"`
			Details string `json:"details"`
		}
		if response.Code != 200 || reply.Data.Exit != 1 || json.Unmarshal([]byte(reply.Data.Stdout), &result) != nil {
			t.Fatalf("actual shell: status=%d body=%s", response.Code, response.Body)
		}
		if missing {
			if !strings.Contains(result.Error, "The video export could not start") || !strings.Contains(reply.Data.Stderr, "IndexError") {
				t.Fatalf("plain failure lost diagnostics: %+v stderr=%s", result, reply.Data.Stderr)
			}
		} else if !strings.Contains(result.Error, "Missing tools:") && !strings.Contains(reply.Data.Stderr, "config-check") {
			t.Fatalf("configuration did not reach exporter: %+v", result)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "escaped")); !os.IsNotExist(err) {
		t.Fatal("configuration was interpreted as shell code")
	}
}

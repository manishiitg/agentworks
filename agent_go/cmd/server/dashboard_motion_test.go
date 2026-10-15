package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// The external action and app operation share authorization and the real
// workspace HTTP serializer. The renderer itself has a separate real-MP4 check.
func TestDashboardMotionExportBindsCallerAndGrants(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("AUTH_SECRET", "dashboard-motion-test-secret-0123456789")
	const parent = "motion-parent"
	common.SetSessionWorkingDir(parent, "Workflow/invoices")
	common.SetSessionFolderGuard(parent, []string{"Workflow/invoices"}, []string{"Workflow/invoices"})
	common.SetSessionFolderGuardBlockedPaths(parent, []string{"Workflow/invoices/private"})
	defer common.ClearSessionShellConfig(parent)
	upstream, err := url.Parse(os.Getenv("WORKSPACE_API_URL"))
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	var commands atomic.Int64
	finished := make(chan struct{})
	allowFinish := make(chan struct{})
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/execute" {
			proxy.ServeHTTP(w, r)
			return
		}
		commands.Add(1)
		var command workspace.ExecuteShellCommandParams
		if err := json.NewDecoder(r.Body).Decode(&command); err != nil {
			t.Fatal(err)
		}
		if r.Header.Get("X-User-ID") != "owner" || command.WorkingDirectory != "Workflow/invoices" {
			t.Errorf("caller/workspace lost: %s %+v", r.Header.Get("X-User-ID"), command)
		}
		if command.FolderGuard == nil || !command.FolderGuard.Enabled || len(command.FolderGuard.ReadPaths) != 3 || command.FolderGuard.ReadPaths[0] != "Workflow/invoices/code/motion/sales" || len(command.FolderGuard.WritePaths) != 2 {
			t.Fatalf("unbounded export guard: %+v", command.FolderGuard)
		}
		if len(command.FolderGuard.BlockedPaths) != 1 || command.FolderGuard.BlockedPaths[0] != "Workflow/invoices/private" {
			t.Errorf("lost parent denies: %+v", command.FolderGuard)
		}
		<-allowFinish
		defer close(finished)
		var args map[string]string
		parts := strings.Fields(command.Command)
		config, err := base64.StdEncoding.DecodeString(strings.Trim(parts[len(parts)-1], "'"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(config, &args); err != nil {
			t.Fatal(err)
		}
		if args["caller"] != "owner" || !strings.HasPrefix(args["output_path"], "db/assets/motion/") || args["renderer_version"] != "0.8.99" || command.Timeout == nil || *command.Timeout != 600 {
			t.Fatalf("export context: %+v", command)
		}
		out, _ := json.Marshal(map[string]any{"success": true, "path": args["output_path"] + "/animation.mp4", "bytes": 512})
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"stdout": string(out), "exit_code": 0}})
	}))
	defer service.Close()
	t.Setenv("WORKSPACE_API_URL", service.URL)
	owner := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "motion-author", Scopes: []string{"dashboards:read", "workflows:read", "files:read", "runs:execute", "builder:chat"}, WorkflowIDs: []string{"invoices"}}}
	call := func(claims *UserClaims, composition, exportID string) *httptest.ResponseRecorder {
		ctx := context.WithValue(context.Background(), common.UserIDKey, "unrelated-context")
		ctx = context.WithValue(ctx, UserContextKey, claims)
		ctx = context.WithValue(ctx, common.ChatSessionIDKey, parent)
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		r := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", nil).WithContext(ctx)
		w := httptest.NewRecorder()
		f.api.externalDashboardCall(w, r, "export_dashboard_motion", map[string]any{"workflow_id": "invoices", "composition_path": composition, "submission_id": "motion-test-request", "export_id": exportID})
		return w
	}
	first := call(owner, "code/motion/sales", "")
	if first.Code != http.StatusOK {
		t.Fatalf("start: %d %s", first.Code, first.Body)
	}
	var receipt dashboardMotionJob
	if err := json.Unmarshal(first.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.ID == "" || receipt.Status != "running" {
		t.Fatalf("receipt: %+v", receipt)
	}
	retry := call(owner, "code/motion/sales", "")
	if retry.Code != http.StatusOK || !strings.Contains(retry.Body.String(), receipt.ID) {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body)
	}
	close(allowFinish)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("render did not finish after HTTP request cancellation")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		status := call(owner, "", receipt.ID)
		if status.Code != http.StatusOK {
			t.Fatalf("status: %d %s", status.Code, status.Body)
		}
		if strings.Contains(status.Body.String(), "completed") {
			if !strings.Contains(status.Body.String(), "animation.mp4") {
				t.Fatal(status.Body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(status.Body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := call(owner, "code/motion/../../planning", ""); got.Code != 400 {
		t.Fatalf("traversal: %d %s", got.Code, got.Body)
	}
	owner.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"code/motion/sales"}, WritePaths: []string{"docs"}}
	if got := call(owner, "code/motion/sales", ""); got.Code != 403 {
		t.Fatalf("write grant: %d %s", got.Code, got.Body)
	}
	owner.AccessToken.FileGuard = nil
	owner.AccessToken.Scopes = []string{"dashboards:read", "workflows:read", "files:read", "builder:chat"}
	if got := call(owner, "code/motion/sales", ""); got.Code != 403 {
		t.Fatalf("execute consent: %d %s", got.Code, got.Body)
	}
	runPerson := &UserClaims{UserID: "reader", Username: "reader"}
	if got := call(runPerson, "code/motion/sales", ""); got.Code != 403 {
		t.Fatalf("Run person: %d %s", got.Code, got.Body)
	}
	if commands.Load() != 1 {
		t.Fatalf("refused export executed: %d commands", commands.Load())
	}
}

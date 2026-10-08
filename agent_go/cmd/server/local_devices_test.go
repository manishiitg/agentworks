package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func localDeviceTestServer(t *testing.T) (*StreamingAPI, *httptest.Server, *accesstokens.Store, string, string) {
	t.Helper()
	tokenTestSetup(t)
	f := newExternalToolsFixture(t)
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	token, raw, err := store.Issue(context.Background(), accesstokens.Token{Name: "Laptop", UserID: "owner", Username: "owner", Scopes: []string{"devices:connect"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	routes := http.NewServeMux()
	routes.HandleFunc("GET /api/external/v1/devices/connect", f.api.handleLocalDeviceConnect)
	server := httptest.NewServer(AuthMiddleware(routes))
	t.Cleanup(server.Close)
	return f.api, server, store, token.ID, raw
}
func TestLocalDeviceConnectionOwnerToolsRevocationAndOffline(t *testing.T) {
	api, server, store, tokenID, token := localDeviceTestServer(t)
	base := t.TempDir()
	root := filepath.Join(base, "files")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "readme.md"), []byte("laptop content"), 0600)
	executor, err := localfiles.Open("laptop", []localfiles.Grant{{Resource: localfiles.Resource{ID: "project", Writable: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}, ReadOnlyPaths: []string{"locked"}}}, Root: root, State: filepath.Join(base, "state")}})
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close()
	client, err := agentworksclient.New(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connected := make(chan struct{})
	finished := make(chan error, 1)
	go func() { finished <- client.ServeExecutor(ctx, executor, func() { close(connected) }) }()
	select {
	case <-connected:
	case err := <-finished:
		t.Fatalf("connect %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("connect timeout")
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	if err = api.validateCodeLocalFiles(owner, &codeLocalFileTarget{DeviceID: "laptop", ResourceID: "project"}); err != nil {
		t.Fatal(err)
	}
	if _, err = api.localDeviceCall(t.Context(), owner, "laptop", localfiles.Request{ResourceID: "not-shared", Operation: "read", Path: "readme.md"}); wf.StatusCode(err) != 403 {
		t.Fatal("unshared Code folder could read files")
	}
	request := localfiles.Request{ResourceID: "project", Operation: "read", Path: "readme.md"}
	response, err := api.localDeviceCall(t.Context(), owner, "laptop", request)
	if err != nil || response.File.Content != "laptop content" {
		t.Fatalf("read %+v %v", response, err)
	}
	if len(api.localDeviceList(&UserClaims{UserID: "outsider"})) != 0 {
		t.Fatal("device leaked to another user")
	}
	if _, err = api.localDeviceCall(t.Context(), &UserClaims{UserID: "outsider", Username: "outsider"}, "laptop", request); wf.StatusCode(err) != 404 {
		t.Fatalf("cross-user %v", err)
	}
	registrar := &recordingRegistrar{}
	if err = api.registerLocalWorkspaceTools(registrar, newProductToolGate(nil), owner, false, &codeLocalFileTarget{DeviceID: "laptop", ResourceID: "project"}); err != nil || len(registrar.tools) != 2 {
		t.Fatalf("tools %+v %v", registrar.tools, err)
	}
	for _, definition := range append(workspace.GetShellToolDefinitions(), workspace.GetDiffPatchToolDefinitions()...) {
		data, _ := json.Marshal(definition.Function.Parameters)
		var expected map[string]interface{}
		json.Unmarshal(data, &expected)
		if !reflect.DeepEqual(expected, registrar.tools[definition.Function.Name].params) {
			t.Fatalf("local tool changed bridge schema: %s", definition.Function.Name)
		}
	}
	if _, err = registrar.tools["execute_shell_command"].exec(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "outsider"}), map[string]interface{}{"command": "cat readme.md"}); err == nil {
		t.Fatal("conflicting tool owner admitted")
	}
	tokenClaims := writeTestClaims("owner")
	reg := &recordingRegistrar{}
	api.registerLocalWorkspaceTools(reg, newProductToolGate(nil), tokenClaims, false, &codeLocalFileTarget{DeviceID: "laptop", ResourceID: "project"})
	if len(reg.tools) != 0 {
		t.Fatal("public MCP received local device tools")
	}
	request.Operation = "write"
	request.Content = "edited locally"
	request.ExpectedRevision = response.File.Revision
	request.RequestID = "local-edit"
	request.Identity = wf.EditIdentity{UserID: "forged"}
	written, err := api.localDeviceCall(t.Context(), owner, "laptop", request)
	if err != nil || !written.Receipt.Applied {
		t.Fatalf("write %+v %v", written, err)
	}
	if written.Receipt.Identity.UserID != "owner" || written.Receipt.Identity.ConnectionID != tokenID || written.Receipt.Identity.DeviceID != "laptop" {
		t.Fatalf("incorrect authenticated identity %+v", written.Receipt.Identity)
	}
	retried, err := api.localDeviceCall(t.Context(), owner, "laptop", request)
	if err != nil || *retried.Receipt != *written.Receipt {
		t.Fatalf("retry %+v %v", retried, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "readme.md"))
	if string(data) != "edited locally" {
		t.Fatalf("local write %s", data)
	}
	for _, p := range []string{"planning/plan.json", "workflow.json", "locked/a.md", "../escape"} {
		request.Path = p
		if _, err = api.localDeviceCall(t.Context(), owner, "laptop", request); wf.StatusCode(err) != 403 {
			t.Fatalf("protected %s %v", p, err)
		}
	}
	request.Path = "readme.md"
	if err = store.Revoke(t.Context(), tokenID, "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = api.localDeviceCall(t.Context(), owner, "laptop", request); wf.StatusCode(err) != 403 {
		t.Fatalf("revoked %v", err)
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked connection remained open")
	}
	cancel()
}
func TestLocalDeviceDisconnectFailsPendingWithoutRetry(t *testing.T) {
	api, server, _, _, token := localDeviceTestServer(t)
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http://", "ws://", 1)+"/api/external/v1/devices/connect", http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hello := localfiles.Hello{Version: 1, DeviceID: "pending", Resources: []localfiles.Resource{{ID: "project", Writable: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}}}}}
	if err = conn.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	var ack map[string]bool
	if err = conn.ReadJSON(&ack); err != nil || !ack["connected"] {
		t.Fatalf("handshake %v", err)
	}
	returned := make(chan error, 1)
	go func() {
		_, err := api.localDeviceCall(context.Background(), &UserClaims{UserID: "owner", Username: "owner"}, "pending", localfiles.Request{ResourceID: "project", Operation: "write", Path: "one.md", Content: "one", ExpectedRevision: "missing", RequestID: "pending-write"})
		returned <- err
	}()
	var dispatched localfiles.Request
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err = conn.ReadJSON(&dispatched); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	select {
	case err := <-returned:
		var fileErr *wf.FileError
		if !errors.As(err, &fileErr) || fileErr.Status != 503 || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("pending error %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending request hung after disconnect")
	}
}
func TestLocalDeviceBrowserAndUnscopedTokenCannotConnect(t *testing.T) {
	api, _, _, _, _ := localDeviceTestServer(t)
	for _, claims := range []*UserClaims{{UserID: "owner"}, writeTestClaims("owner")} {
		w := httptest.NewRecorder()
		api.handleLocalDeviceConnect(w, adminRequest("GET", "/api/external/v1/devices/connect", "", claims, nil))
		if w.Code != 403 {
			t.Fatalf("connect admitted %d", w.Code)
		}
	}
}

func TestLocalDeviceToolsRejectConnectorPrincipals(t *testing.T) {
	api := &StreamingAPI{}
	for _, claims := range []*UserClaims{{UserID: "owner", Provider: "bot_route"}, {UserID: "owner", Provider: "bot_owner"}, {UserID: "owner", Provider: slackDMProvider}, {UserID: "owner", ExecutionPrincipal: &ExecutionPrincipal{Kind: "bot_route"}}, {UserID: "owner", BotRouteGrant: "run"}} {
		if websiteDeviceClaims(claims) {
			t.Fatalf("connector admitted %+v", claims)
		}
		reg := &recordingRegistrar{}
		if err := api.registerLocalWorkspaceTools(reg, newProductToolGate(nil), claims, false, &codeLocalFileTarget{DeviceID: "laptop", ResourceID: "project"}); err != nil || len(reg.tools) != 0 {
			t.Fatalf("connector tool registration %+v %v", reg.tools, err)
		}
	}
}

func TestLocalDeviceShellUsesSelectedLaptopAndCancelsOnWire(t *testing.T) {
	api, server, _, _, token := localDeviceTestServer(t)
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http://", "ws://", 1)+"/api/external/v1/devices/connect", http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hello := localfiles.Hello{Version: localfiles.Version, DeviceID: "shell-laptop", Resources: []localfiles.Resource{{ID: "project", Writable: true, Shell: true, Patch: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}, BlockedPaths: []string{"blocked"}, ReadOnlyPaths: []string{"locked"}}}}}
	hello.Resources = append(hello.Resources,
		localfiles.Resource{ID: "reference", Shell: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}}},
		localfiles.Resource{ID: "older-cli", Writable: true, Shell: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}}})
	if err = conn.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	var ack map[string]bool
	if err = conn.ReadJSON(&ack); err != nil || !ack["connected"] {
		t.Fatalf("handshake %v", err)
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	target := &codeLocalFileTarget{DeviceID: "shell-laptop", ResourceID: "project"}
	reg := &recordingRegistrar{}
	if err = api.registerLocalWorkspaceTools(reg, newProductToolGate(nil), owner, false, target); err != nil {
		t.Fatal(err)
	}
	for _, resource := range []string{"reference", "older-cli"} {
		inspect := &recordingRegistrar{}
		if err := api.registerLocalWorkspaceTools(inspect, newProductToolGate(nil), owner, false, &codeLocalFileTarget{DeviceID: "shell-laptop", ResourceID: resource}); err != nil {
			t.Fatal(err)
		}
		if len(inspect.tools) != 1 || inspect.tools["execute_shell_command"].exec == nil {
			t.Fatalf("inspection tools for %s: %+v", resource, inspect.tools)
		}
	}
	tool, ok := reg.tools["execute_shell_command"]
	if !ok {
		t.Fatal("shell tool missing")
	}
	readonly := &recordingRegistrar{}
	api.registerLocalWorkspaceTools(readonly, newProductToolGate(nil), owner, true, target)
	if _, ok := readonly.tools["execute_shell_command"]; ok {
		t.Fatal("read-only turn received shell")
	}
	args := map[string]interface{}{"command": "npm test", "timeout": float64(120), "device_id": "forged-device", "resource_id": "forged-folder", "path": "../escape"}
	returned := make(chan error, 1)
	go func() {
		output, err := tool.exec(t.Context(), args)
		if err == nil && !strings.Contains(output, "laptop-result") {
			err = errors.New("local command result lost")
		}
		returned <- err
	}()
	var dispatched localfiles.Request
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err = conn.ReadJSON(&dispatched); err != nil {
		t.Fatal(err)
	}
	if dispatched.Path != "." || dispatched.RequestID == "" || dispatched.Operation != "shell" || dispatched.Command != "npm test" || dispatched.ResourceID != "project" || dispatched.TimeoutSeconds != 120 || dispatched.Identity.UserID != "owner" || dispatched.Identity.Source != "server_local_executor" {
		t.Fatalf("not routed to laptop %+v", dispatched)
	}
	if err = conn.WriteJSON(localfiles.Response{ID: dispatched.ID, Status: 200, Shell: &localfiles.ShellResult{Stdout: "laptop-result", ExitCode: 0}}); err != nil {
		t.Fatal(err)
	}
	if err = <-returned; err != nil {
		t.Fatal(err)
	}
	go func() {
		output, err := reg.tools["diff_patch_workspace_file"].exec(t.Context(), map[string]interface{}{"filepath": "one.txt", "diff": "@@ -1 +1 @@\n-old\n+new\n"})
		if err == nil && !strings.Contains(output, `"applied":true`) {
			err = errors.New("patch result lost")
		}
		returned <- err
	}()
	if err = conn.ReadJSON(&dispatched); err != nil {
		t.Fatal(err)
	}
	if dispatched.Operation != "patch" || dispatched.ResourceID != "project" || dispatched.Path != "one.txt" || dispatched.RequestID == "" || dispatched.Identity.UserID != "owner" {
		t.Fatalf("not routed laptop patch %+v", dispatched)
	}
	if err = conn.WriteJSON(localfiles.Response{ID: dispatched.ID, Status: 200, Patches: []wf.WriteReceipt{{Path: "one.txt", Applied: true}}}); err != nil {
		t.Fatal(err)
	}
	if err = <-returned; err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../escape", "blocked", "locked"} {
		r := localfiles.Request{Operation: "shell", ResourceID: "project", Path: path, Command: "echo no", RequestID: "guard"}
		if _, err := api.localDeviceCall(t.Context(), owner, "shell-laptop", r); wf.StatusCode(err) != 403 {
			t.Fatalf("shell path admitted %s %v", path, err)
		}
	}
	args["timeout"] = float64(1.5)
	if _, err = tool.exec(t.Context(), args); err == nil {
		t.Fatal("fractional timeout admitted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		_, err := api.localDeviceCall(ctx, owner, "shell-laptop", localfiles.Request{Operation: "shell", ResourceID: "project", Path: ".", Command: "sleep 100", RequestID: "cancel-command"})
		returned <- err
	}()
	if err = conn.ReadJSON(&dispatched); err != nil {
		t.Fatal(err)
	}
	cancel()
	var cancellation localfiles.Request
	if err = conn.ReadJSON(&cancellation); err != nil {
		t.Fatal(err)
	}
	if cancellation.Operation != "cancel" || cancellation.ID != dispatched.ID {
		t.Fatalf("no laptop cancellation %+v", cancellation)
	}
	if err = <-returned; wf.StatusCode(err) != 504 || !strings.Contains(err.Error(), "inspect local files") {
		t.Fatalf("cancellation lost uncertain outcome %v", err)
	}
}

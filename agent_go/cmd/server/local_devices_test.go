package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
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
	if err = api.registerLocalDeviceTools(registrar, newProductToolGate(nil), owner, false); err != nil || len(registrar.tools) != 4 {
		t.Fatalf("tools %+v %v", registrar.tools, err)
	}
	if _, err = registrar.tools["read_local_file"].exec(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "outsider"}), map[string]interface{}{"device_id": "laptop", "resource_id": "project", "path": "readme.md"}); err == nil {
		t.Fatal("conflicting tool owner admitted")
	}
	tokenClaims := writeTestClaims("owner")
	reg := &recordingRegistrar{}
	api.registerLocalDeviceTools(reg, newProductToolGate(nil), tokenClaims, false)
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
		if err := api.registerLocalDeviceTools(reg, newProductToolGate(nil), claims, false); err != nil || len(reg.tools) != 0 {
			t.Fatalf("connector tool registration %+v %v", reg.tools, err)
		}
	}
}

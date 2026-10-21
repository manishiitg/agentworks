package server

import (
	"context"
	"encoding/json"
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
	f.write(t, "Crew/local-test/product.json", `{"schema_version":1,"product":"code","id":"local-test","title":"Laptop test"}`)
	f.write(t, "Crew/local-test/workflow.json", `{"capabilities":{}}`)
	registry := withProjectOwnerRegistry(t)
	if err := registry.Register(projectOwnerRecord{Product: "code", Folder: "local-test", ProjectID: "local-test", OwnerID: "owner", CanonicalRoot: "Crew/local-test"}); err != nil {
		t.Fatal(err)
	}
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
	executor, err := localfiles.OpenSettings("laptop", filepath.Join(base, "state"), nil)
	saveTestLaptopSettings(t, "laptop", []localfiles.FolderSelection{{ID: "project", Path: root, Access: "write"}, {ID: "locked", Path: filepath.Join(root, "locked"), Access: "read"}})
	os.Mkdir(filepath.Join(root, "locked"), 0700)
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
	if err = api.validateLaptopConnection(owner, &laptopConnectionTarget{DeviceID: "laptop", ResourceID: "project"}); err != nil {
		t.Fatal(err)
	}
	if _, err = api.localDeviceCall(t.Context(), owner, "laptop", localfiles.Request{CrewID: "Crew/local-test", ResourceID: "not-shared", Operation: "read", Path: "readme.md"}); wf.StatusCode(err) != 403 {
		t.Fatal("unshared Code folder could read files")
	}
	request := localfiles.Request{CrewID: "Crew/local-test", ResourceID: "project", Operation: "read", Path: "readme.md"}
	request.Folders = []localfiles.FolderSelection{{ID: "forged", Path: "/", Access: "write"}} // ignored: saved Crew grants decide
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
	if err = api.registerLaptopTools(registrar, newProductToolGate(nil), owner, false, localWorkspaceTestSession(api, owner, &laptopConnectionTarget{DeviceID: "laptop", ResourceID: "project"}), "Crew/local-test", &laptopConnectionTarget{DeviceID: "laptop", ResourceID: "project"}); err != nil || len(registrar.tools) != 2 {
		t.Fatalf("local tool count=%d: %v", len(registrar.tools), err)
	}

	baseline := api.lastQueryRequests["local-code"]
	for _, trigger := range []string{"code_chat", "auto_notification", "cron", "bot:slack"} {
		changed := baseline
		changed.TriggeredBy = trigger
		api.lastQueryRequests["local-code"] = changed
		if _, err := registrar.tools["execute_local_shell_command"].exec(t.Context(), map[string]interface{}{"command": "touch unauthorized"}); err == nil || !strings.Contains(err.Error(), "interactive Builder") {
			t.Fatalf("retained callback accepted %s: %v", trigger, err)
		}
	}
	api.lastQueryRequests["local-code"] = baseline
	api.lastQueryRequests["local-code"] = QueryRequest{userID: "owner", AgentProfileID: "code"}
	if _, err := registrar.tools["execute_local_shell_command"].exec(t.Context(), map[string]interface{}{"command": "touch unauthorized"}); err == nil {
		t.Fatal("disconnected retained callback dispatched to laptop")
	}
	api.lastQueryRequests["local-code"] = baseline
	if _, err = registrar.tools["execute_local_shell_command"].exec(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "outsider"}), map[string]interface{}{"command": "cat readme.md"}); err == nil {
		t.Fatal("conflicting tool owner admitted")
	}
	tokenClaims := writeTestClaims("owner")
	reg := &recordingRegistrar{}
	api.registerLaptopTools(reg, newProductToolGate(nil), tokenClaims, false, "local-code", "Crew/local-test", &laptopConnectionTarget{DeviceID: "laptop", ResourceID: "project"})
	if len(reg.tools) != 0 {
		t.Fatal("public MCP received local device tools")
	}
	binary := []byte{0, 255, 10, 13, 128}
	if err := os.WriteFile(filepath.Join(root, "sample.bin"), binary, 0600); err != nil {
		t.Fatal(err)
	}
	copied, err := api.localDeviceCall(t.Context(), owner, "laptop", localfiles.Request{CrewID: "Crew/local-test", ResourceID: "project", Operation: "copy_read", Path: "sample.bin"})
	if err != nil || string(copied.Bytes) != string(binary) {
		t.Fatalf("binary transfer: %v", err)
	}
	if _, err := registrar.tools["copy_local_file_to_crew"].exec(t.Context(), map[string]interface{}{"folder": "project", "local_path": "sample.bin", "crew_path": "uploads/import.bin"}); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), "Crew/local-test/uploads/import.bin"))
	if err != nil || string(stored) != string(binary) {
		t.Fatalf("Crew writer changed bytes: %v", err)
	}
	if _, err := registrar.tools["copy_local_file_to_crew"].exec(t.Context(), map[string]interface{}{"folder": "project", "local_path": "sample.bin", "crew_path": "product.json"}); err == nil {
		t.Fatal("copy overwrote Crew manifest")
	}
	for _, name := range []string{"../escape", ".env", "locked/private.key"} {
		if _, err := api.localDeviceCall(t.Context(), owner, "laptop", localfiles.Request{CrewID: "Crew/local-test", ResourceID: "project", Operation: "copy_read", Path: name}); wf.StatusCode(err) != 403 {
			t.Fatalf("private copy %s: %v", name, err)
		}
	}
	saveTestLaptopSettings(t, "laptop", []localfiles.FolderSelection{{ID: "project", Path: root, Access: "read"}})
	if _, err := api.localDeviceCall(t.Context(), owner, "laptop", localfiles.Request{CrewID: "Crew/local-test", ResourceID: "project", Operation: "shell", Path: ".", Command: "touch nope", RequestID: "read-shell"}); wf.StatusCode(err) != 403 {
		t.Fatalf("read allowed shell: %v", err)
	}
	if _, err := api.localDeviceCall(t.Context(), owner, "laptop", localfiles.Request{CrewID: "Crew/other", ResourceID: "project", Operation: "read", Path: "readme.md"}); wf.StatusCode(err) != 403 {
		t.Fatalf("Crew grants leaked: %v", err)
	}
	saveTestLaptopSettings(t, "laptop", []localfiles.FolderSelection{{ID: "project", Path: root, Access: "write"}, {ID: "locked", Path: filepath.Join(root, "locked"), Access: "read"}})
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
	saveTestLaptopSettings(t, "pending", []localfiles.FolderSelection{{ID: "project", Path: "/tmp/project", Access: "write"}})
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http://", "ws://", 1)+"/api/external/v1/devices/connect", http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hello := localfiles.Hello{SettingsManaged: true, Version: 1, DeviceID: "pending", Resources: []localfiles.Resource{{ID: "project", Writable: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}}}}}
	if err = conn.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	var ack map[string]bool
	if err = conn.ReadJSON(&ack); err != nil || !ack["connected"] {
		t.Fatalf("handshake %v", err)
	}
	returned := make(chan error, 1)
	go func() {
		_, err := api.localDeviceCall(context.Background(), &UserClaims{UserID: "owner", Username: "owner"}, "pending", localfiles.Request{CrewID: "Crew/local-test", ResourceID: "project", Operation: "write", Path: "one.md", Content: "one", ExpectedRevision: "missing", RequestID: "pending-write"})
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

func TestLocalDeviceConnectionExplainsRejection(t *testing.T) {
	_, server, store, _, token := localDeviceTestServer(t)
	_, unscoped, err := store.Issue(t.Context(), accesstokens.Token{Name: "Agent", UserID: "owner", Username: "owner", Scopes: []string{"files:read"}, AllWorkflows: true, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token, code, message string
		status                     int
	}{
		{"sign_in", "invalid-test-token", "executor_connection_failed", "sign-in could not be verified", 401},
		{"consent", unscoped, "insufficient_scope", "permission to share local folders", 403},
		{"handshake", token, "invalid_executor_handshake", "local folder connection could not be verified", 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := agentworksclient.New(server.URL, tc.token)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			// The invalid hello must be refused before any folder or command runs.
			err = client.ServeExecutor(ctx, &localfiles.Executor{}, func() { t.Error("rejected executor connected") })
			var apiErr *agentworksclient.APIError
			if !errors.As(err, &apiErr) || apiErr.Status != tc.status || apiErr.Code != tc.code || !strings.Contains(apiErr.Message, tc.message) {
				t.Fatalf("connection rejection = %v", err)
			}
			if strings.Contains(err.Error(), tc.token) {
				t.Fatal("connection error exposed the credential")
			}
		})
	}
}

func TestLocalDeviceToolsRejectConnectorPrincipals(t *testing.T) {
	api := &StreamingAPI{}
	for _, claims := range []*UserClaims{{UserID: "owner", Provider: "bot_route"}, {UserID: "owner", Provider: "bot_owner"}, {UserID: "owner", Provider: slackDMProvider}, {UserID: "owner", ExecutionPrincipal: &ExecutionPrincipal{Kind: "bot_route"}}, {UserID: "owner", BotRouteGrant: "run"}} {
		if websiteDeviceClaims(claims) {
			t.Fatalf("connector admitted %+v", claims)
		}
		reg := &recordingRegistrar{}
		if err := api.registerLaptopTools(reg, newProductToolGate(nil), claims, false, "local-code", "Crew/local-test", &laptopConnectionTarget{DeviceID: "laptop", ResourceID: "project"}); err != nil || len(reg.tools) != 0 {
			t.Fatalf("connector tool registration %+v %v", reg.tools, err)
		}
	}
}

func TestLocalDeviceShellUsesSelectedLaptopAndCancelsOnWire(t *testing.T) {
	api, server, _, _, token := localDeviceTestServer(t)
	saveTestLaptopSettings(t, "shell-laptop", []localfiles.FolderSelection{{ID: "project", Path: "/tmp/project", Access: "write"}})
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http://", "ws://", 1)+"/api/external/v1/devices/connect", http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hello := localfiles.Hello{SettingsManaged: true, Version: localfiles.Version, DeviceID: "shell-laptop", Resources: []localfiles.Resource{{ID: "project", Writable: true, Shell: true, Patch: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}, BlockedPaths: []string{"blocked"}, ReadOnlyPaths: []string{"locked"}}}}}
	hello.Resources = append(hello.Resources,
		localfiles.Resource{ID: "reference-downloads", Downloads: true, Patch: true, Shell: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}}},
		localfiles.Resource{ID: "downloads", Writable: true, Shell: true, Patch: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}}},
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
	target := &laptopConnectionTarget{DeviceID: "shell-laptop", ResourceID: "project"}
	reg := &recordingRegistrar{}
	if err = api.registerLaptopTools(reg, newProductToolGate(nil), owner, false, localWorkspaceTestSession(api, owner, target), "Crew/local-test", target); err != nil {
		t.Fatal(err)
	}
	localWorkspaceTestSession(api, owner, target)
	tool, ok := reg.tools["execute_local_shell_command"]
	if !ok {
		t.Fatal("shell tool missing")
	}
	readonly := &recordingRegistrar{}
	api.registerLaptopTools(readonly, newProductToolGate(nil), owner, true, "local-code", "Crew/local-test", target)
	if _, ok := readonly.tools["execute_local_shell_command"]; ok {
		t.Fatal("read-only turn received shell")
	}
	localWorkspaceTestSession(api, owner, target)
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
	for _, path := range []string{"../escape", ".env"} {
		r := localfiles.Request{CrewID: "Crew/local-test", Operation: "shell", ResourceID: "project", Path: path, Command: "echo no", RequestID: "guard"}
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
		_, err := api.localDeviceCall(ctx, owner, "shell-laptop", localfiles.Request{CrewID: "Crew/local-test", Operation: "shell", ResourceID: "project", Path: ".", Command: "sleep 100", RequestID: "cancel-command"})
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

func localWorkspaceTestSession(api *StreamingAPI, claims *UserClaims, target *laptopConnectionTarget) string {
	api.lastQueryMu.Lock()
	defer api.lastQueryMu.Unlock()
	if api.lastQueryRequests == nil {
		api.lastQueryRequests = map[string]QueryRequest{}
	}
	api.lastQueryRequests["local-code"] = QueryRequest{userID: claims.UserID, AgentProfileID: "code", SelectedFolder: "Crew/local-test", LaptopConnection: target}
	return "local-code"
}

// An OAuth device connection lives as long as its grant, not as long as the hour-long access token it connected with: the CLI never
// renews that token on a live socket, and cutting it at expiry dropped every Local connection once an hour (PLAT-826). Sign-out still ends it.
func TestLocalDeviceOAuthConnectionOutlivesItsAccessTokenButNotSignOut(t *testing.T) {
	tokenTestSetup(t)
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := GetDefaultUserID()
	insert := func(hash, kind string, expires time.Time) {
		t.Helper()
		if _, err := store.db.Exec(`INSERT INTO tokens (hash,kind,family_id,client_id,resource,scopes,user_id,username,email,provider,expires_at,workflow_ids) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			hash, kind, "fam1", cliOAuthClientID, "res", `["devices:connect"]`, owner, "owner", "owner@example.com", "local", expires.Unix(), `[]`); err != nil {
			t.Fatal(err)
		}
	}
	insert("expired-access", "access", time.Now().Add(-time.Minute))
	insert("live-refresh", "refresh", time.Now().Add(24*time.Hour))
	token := accesstokens.Token{ID: "oauth-fam1", UserID: owner, Scopes: []string{"devices:connect"}, ExpiresAt: time.Now().Add(-time.Minute)}
	device := &localDeviceConnection{owner: owner, claims: &UserClaims{UserID: owner, AccessToken: &token}}
	if !device.authorized(context.Background()) {
		t.Fatal("a connection whose access token expired but whose grant is still signed in must stay authorized")
	}
	if _, err := store.db.Exec(`UPDATE tokens SET revoked_at=? WHERE family_id='fam1'`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if device.authorized(context.Background()) {
		t.Fatal("signing the grant out must end the connection")
	}
}

func saveTestLaptopSettings(t *testing.T, device string, folders []localfiles.FolderSelection) {
	t.Helper()
	file, err := laptopSettingsPath("owner", "Crew/local-test")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(crewLaptopSettings{DeviceID: device, Folders: folders, Commands: true})
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
}

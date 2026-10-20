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
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// Real workspace persistence/audit and Streamable MCP, sharing the app's
// account handler. No model or real account is used.
func TestAdminUsersMCPSharesAppGuards(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("WORKSPACE_API_TOKEN", "users-fixture-token")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	if err := os.MkdirAll(filepath.Join(docs, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	initial := `{"users":[{"id":"boss","username":"boss","role":"admin"},{"id":"second","username":"second","role":"admin"},{"id":"member","username":"member","email":"member@example.com","role":"viewer","products":["code"],"password_hash":"private-password-hash-canary","sso":{"provider":"oidc","external_id":"private-login-id-canary"}}]}`
	if err := os.WriteFile(filepath.Join(docs, "config", "users.json"), []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	var failDirectorySave atomic.Bool
	router.Use(func(c *gin.Context) {
		if c.GetHeader("X-Workspace-Token") != "users-fixture-token" {
			c.AbortWithStatus(401)
			return
		}
		if failDirectorySave.Load() && c.Request.Method == http.MethodPut && c.Request.URL.Path == "/api/documents/config/users.json" {
			c.AbortWithStatus(503)
			return
		}
		c.Next()
	})
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	router.POST("/api/audit/crew-admin/append", workspacehandlers.AppendCodeAdminAudit)
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	invalidateUserDirectoryCache()
	t.Cleanup(invalidateUserDirectoryCache)
	api := &StreamingAPI{}
	admin := codeReviewToken("boss", "users:manage")
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	srv := serveExternalMCP(t, api, admin)
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	call := func(args map[string]any, wantError bool) string {
		t.Helper()
		result := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "users", "arguments": args})
		if result.IsError != wantError {
			t.Fatalf("users result error=%v want%v: %s", result.IsError, wantError, marshalContent(t, result))
		}
		if wantError {
			return marshalContent(t, result)
		}
		raw := marshalStructured(t, result)
		for _, secret := range []string{"private-password-hash-canary", "private-login-id-canary", "password_hash", "has_password", "external_id", "account_allowed_models"} {
			if strings.Contains(raw, secret) {
				t.Fatalf("account response leaked %s", secret)
			}
		}
		return raw
	}
	listed := call(map[string]any{"action": "list"}, false)
	if !strings.Contains(listed, `"sign_in_method":"sso"`) || !strings.Contains(listed, `"sign_in_method":"password"`) || strings.Contains(listed, `"sign_in_method":"oidc"`) || !strings.Contains(listed, `"create_products":null`) {
		t.Fatal("safe account metadata incomplete", listed)
	}
	for _, claims := range []*UserClaims{codeReviewToken("member", "users:manage"), codeReviewToken("boss", "crews:review")} {
		for _, action := range []string{"list", "get", "update"} {
			args := map[string]any{"action": action}
			if action != "list" {
				args["user_id"] = "member"
			}
			if action == "update" {
				args["role"], args["request_id"] = "creator", "refused"
			}
			w := codeReviewCall(api, claims, "users", args)
			if w.Code != http.StatusForbidden {
				t.Fatalf("unauthorized %s: %d %s", action, w.Code, w.Body)
			}
		}
		catalog := httptest.NewRecorder()
		api.handleExternalTools(catalog, adminRequest(http.MethodGet, "/api/external/tools", "", claims, nil))
		var data struct {
			Tools []externalTool `json:"tools"`
		}
		if err := json.Unmarshal(catalog.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		for _, tool := range data.Tools {
			if tool.Name == "users" {
				t.Fatal("unauthorized connection discovers users")
			}
		}
	}
	grant := map[string]any{"action": "update", "user_id": "member", "role": "creator", "products": []any{"relays", "code"}, "create_products": []any{"code"}, "request_id": "grant-code"}
	first := call(grant, false)
	if !strings.Contains(first, `"role":"creator"`) || !strings.Contains(first, `"create_products":["code"]`) {
		t.Fatal("app access update not returned", first)
	}
	app := httptest.NewRecorder()
	requireAdmin(api.handleAdminUpdateUser)(app, adminRequest(http.MethodPut, "/api/admin/users/member", `{"products":["work"]}`, admin, map[string]string{"id": "member"}))
	if app.Code != 200 {
		t.Fatal("app update failed", app.Body)
	}
	// Rebuild the API and clear its directory cache: the receipt is on disk,
	// not in an MCP connection, and must not undo the later app update.
	invalidateUserDirectoryCache()
	newSrv := serveExternalMCP(t, &StreamingAPI{}, admin)
	newCLI := dialExternalMCP(t, ctx, newSrv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, newCLI)
	retry := callRemoteTool(t, ctx, newCLI, externalMCPToolCall, map[string]any{"name": "users", "arguments": grant})
	requireRemoteSuccess(t, retry, "users retry")
	if marshalStructured(t, retry) != first {
		t.Fatal("durable retry changed its recorded result")
	}
	if rec := directoryUserFor("member", "", ""); rec == nil || len(rec.Products) != 1 || rec.Products[0] != "work" {
		t.Fatal("retry overwrote a later app update")
	}
	changed := map[string]any{"action": "update", "user_id": "member", "role": "editor", "request_id": "grant-code"}
	if !strings.Contains(call(changed, true), "request_id was already used") {
		t.Fatal("different retry payload accepted")
	}
	// Old receipts expire on the next directory save. Opaque IDs become
	// new updates after the documented seven-day replay window.
	dir, err := readUserDirectoryFile()
	if err != nil {
		t.Fatal(err)
	}
	expiredKey := userAccessHash([]string{"boss", "expired"})
	dir.AccessRequests[expiredKey] = userAccessReceipt{Hash: "old-arguments", CreatedAt: time.Now().Add(-8 * 24 * time.Hour)}
	dir.AccessRequests["unused-expired"] = dir.AccessRequests[expiredKey]
	raw, err := json.Marshal(userDirectoryFile{Users: dir.Users, AccessRequests: dir.AccessRequests})
	if err != nil {
		t.Fatal(err)
	}
	if err := userDirectoryWrite(string(raw)); err != nil {
		t.Fatal(err)
	}
	call(map[string]any{"action": "update", "user_id": "member", "vault_reader": true, "request_id": "expired"}, false)
	dir, err = readUserDirectoryFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, retained := dir.AccessRequests["unused-expired"]; retained || !dir.AccessRequests[expiredKey].CreatedAt.After(time.Now().Add(-time.Hour)) {
		t.Fatal("expired receipts retained or not replaced by a new update")
	}
	if !strings.Contains(call(map[string]any{"action": "update", "user_id": "member", "role": "admin", "request_id": "promote"}, true), "confirm=user_id") {
		t.Fatal("admin promotion lacked confirmation")
	}
	call(map[string]any{"action": "update", "user_id": "member", "role": "admin", "confirm": "member", "request_id": "promote"}, false)
	for _, args := range []map[string]any{
		{"action": "update", "user_id": "boss", "role": "editor", "confirm": "boss", "request_id": "self-demote"},
		{"action": "update", "user_id": "boss", "disabled": true, "confirm": "boss", "request_id": "self-disable"},
	} {
		if !strings.Contains(call(args, true), "your own admin") {
			t.Fatal("self protection missing")
		}
	}
	call(map[string]any{"action": "update", "user_id": "member", "role": "editor", "confirm": "member", "request_id": "demote"}, false)
	app = httptest.NewRecorder()
	requireAdmin(api.handleAdminUpdateUser)(app, adminRequest(http.MethodPut, "/api/admin/users/second", `{"disabled":true}`, admin, map[string]string{"id": "second"}))
	if app.Code != 200 {
		t.Fatal("disable second admin failed", app.Body)
	}
	unknown := &UserClaims{UserID: "outside-directory", Username: "outside-directory"}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		app = httptest.NewRecorder()
		handler := api.handleAdminUpdateUser
		if method == http.MethodDelete {
			handler = api.handleAdminDeleteUser
		}
		requireAdmin(handler)(app, adminRequest(method, "/api/admin/users/boss", `{"role":"editor"}`, unknown, map[string]string{"id": "boss"}))
		if app.Code != http.StatusForbidden || !strings.Contains(app.Body.String(), "workflow permission denied") {
			t.Fatalf("unknown account admin guard %s: %d %s", method, app.Code, app.Body)
		}
	}
	audit, err := os.ReadFile(filepath.Join(docs, filepath.FromSlash(codeAdminAuditPath(time.Now()))))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(audit), `request_id=grant-code `) != 1 || !strings.Contains(string(audit), `"admin_id":"boss"`) || !strings.Contains(string(audit), `"via":"token:tok-boss"`) {
		t.Fatal("caller audit missing or retry duplicated it", string(audit))
	}
	if !strings.Contains(string(audit), `"action":"user_directory_read"`) || !strings.Contains(string(audit), `"action":"update_user_attempt"`) {
		t.Fatal("directory audit actions not labelled", string(audit))
	}
	failDirectorySave.Store(true)
	call(map[string]any{"action": "update", "user_id": "member", "disabled": true, "request_id": "save-failure"}, true)
	failDirectorySave.Store(false)
	dir, err = readUserDirectoryFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, found := dir.AccessRequests[userAccessHash([]string{"boss", "save-failure"})]; found || dir.byID("member").Disabled {
		t.Fatal("failed save changed the account or stored a success receipt")
	}
	audit, err = os.ReadFile(filepath.Join(docs, filepath.FromSlash(codeAdminAuditPath(time.Now()))))
	if err != nil || !strings.Contains(string(audit), `"action":"update_user_failed"`) {
		t.Fatal("failed save lacks its failure audit", err)
	}
	previousAppend := codeAdminAuditAppend
	codeAdminAuditAppend = func(context.Context, string, string) error { return errors.New("audit unavailable") }
	t.Cleanup(func() { codeAdminAuditAppend = previousAppend })
	if !strings.Contains(call(map[string]any{"action": "update", "user_id": "member", "disabled": true, "request_id": "audit-failure"}, true), "could not be audited") {
		t.Fatal("unaudited change accepted")
	}
	if rec := directoryUserFor("member", "", ""); rec == nil || rec.Disabled {
		t.Fatal("failed audit changed account")
	}
}

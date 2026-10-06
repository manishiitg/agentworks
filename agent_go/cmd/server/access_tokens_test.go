package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

func tokenTestSetup(t *testing.T) *StreamingAPI {
	t.Helper()
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "token-test-signing-secret")
	return &StreamingAPI{}
}

func TestKnowledgebaseTokenToolAdmission(t *testing.T) {
	tokenTestSetup(t)
	t.Setenv("AGENT_PRODUCTS", "knowledgebase")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","admin":true},{"id":"priya","username":"priya","role":"viewer","products":["knowledgebase"]}]}`)
	read := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"knowledgebase:read"}}}
	write := &UserClaims{UserID: "priya", AccessToken: &accesstokens.Token{Scopes: []string{"knowledgebase:read", "knowledgebase:write"}}}
	workflow := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"workflows:read", "files:read"}, AllWorkflows: true}}
	for _, definition := range knowledgebase.ToolDefinitions() {
		tool := externalTool{Name: definition.Name, mutates: definition.Mutates}
		if externalTokenAllows(workflow, tool) {
			t.Errorf("ordinary workflow token admitted %s", definition.Name)
		}
		wantRead := definition.Name != "brain_update"
		if got := externalTokenAllows(read, tool); got != wantRead {
			t.Errorf("reader admission for %s = %v, want %v", definition.Name, got, wantRead)
		}
		if !externalTokenAllows(write, tool) {
			t.Errorf("KB writer with workflow viewer account role denied %s", definition.Name)
		}
	}
	// Brain is core: no product allowlist switches it off for a connection.
	t.Setenv("AGENT_PRODUCTS", "work")
	if !externalTokenAllows(write, externalTool{Name: "brain_read"}) || !externalTokenAllows(write, externalTool{Name: "brain_update", mutates: true}) {
		t.Fatal("Brain must stay available whatever AGENT_PRODUCTS lists")
	}
}

func TestKnowledgebaseTokenHTTPScopesServiceIdentityAndRevocation(t *testing.T) {
	api := tokenTestSetup(t)
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "knowledgebase")
	t.Setenv("AGENTWORKS_KNOWLEDGEBASE_ROOT", filepath.Join(t.TempDir(), "knowledgebase"))
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","admin":true},{"id":"priya","username":"priya","role":"viewer","products":["knowledgebase"]}]}`)
	service, err := knowledgebaseService()
	if err != nil {
		t.Fatal(err)
	}
	if err := knowledgebaseSyncIdentities(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	owner := knowledgebase.Principal{IdentityID: "owner", IsAdmin: true}
	call := func(name string, args map[string]any) any {
		t.Helper()
		if name == "manage_knowledgebase_access" && (args["action"] == "grant" || args["action"] == "revoke") {
			access, err := service.Call(context.Background(), owner, "get_knowledgebase_access", map[string]any{"folder_path": args["folder_path"]})
			if err != nil {
				t.Fatal("inspect current ACL", err)
			}
			encoded, err := json.Marshal(access)
			if err != nil {
				t.Fatal(err)
			}
			var current struct {
				Version string `json:"acl_version"`
			}
			if err := json.Unmarshal(encoded, &current); err != nil || current.Version == "" {
				t.Fatalf("missing ACL version: %s %v", encoded, err)
			}
			args["expected_acl_version"] = current.Version
		}
		result, err := service.Call(context.Background(), owner, name, args)
		if err != nil {
			t.Fatal(name, err)
		}
		return result
	}
	call("create_knowledgebase_folder", map[string]any{"folder_path": "", "name": "Engineering", "request_id": "folder"})
	call("manage_knowledgebase_access", map[string]any{"action": "grant", "folder_path": "Engineering", "identity_id": "priya", "role": "Editor", "request_id": "grant-priya"})
	created := call("create_knowledgebase", map[string]any{"folder_path": "Engineering", "filename": "checkout.md", "title": "Checkout", "type": "note", "content": "# Checkout\nShared knowledge.\n", "request_id": "entry"})
	var entry struct {
		EntryID string `json:"entry_id"`
	}
	encoded, _ := json.Marshal(created)
	if err := json.Unmarshal(encoded, &entry); err != nil || entry.EntryID == "" {
		t.Fatalf("missing entry ID: %s %v", encoded, err)
	}
	createdService := call("manage_knowledgebase_access", map[string]any{"action": "create_service_account", "name": "Deployment", "request_id": "create-service"})
	var identity struct {
		IdentityID string `json:"identity_id"`
	}
	encoded, _ = json.Marshal(createdService)
	if err := json.Unmarshal(encoded, &identity); err != nil || identity.IdentityID == "" {
		t.Fatalf("missing service ID: %s %v", encoded, err)
	}
	call("manage_knowledgebase_access", map[string]any{"action": "grant", "folder_path": "Engineering", "identity_id": identity.IdentityID, "role": "Editor", "request_id": "grant-service"})
	ownerJWT, err := GenerateJWT("owner", "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	priyaJWT, err := GenerateJWT("priya", "priya", "")
	if err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	router.HandleFunc("/api/auth/access-tokens", api.handleAccessTokens).Methods("GET", "POST")
	router.HandleFunc("/api/auth/access-tokens/{id}", api.handleAccessTokens).Methods("DELETE")
	router.HandleFunc("/api/external/v1/tools", api.handleExternalTools)
	router.HandleFunc("/api/external/v1/call", api.handleExternalCall)
	handler := AuthMiddleware(router)
	request := func(method, path, body, credential string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+credential)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	type issuedToken struct {
		Token    string             `json:"token"`
		Metadata accesstokens.Token `json:"access_token"`
	}
	issue := func(body, credential string) issuedToken {
		t.Helper()
		w := request("POST", "/api/auth/access-tokens", body, credential)
		var issued issuedToken
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &issued) != nil || issued.Token == "" {
			t.Fatalf("token issue: %d %s", w.Code, w.Body)
		}
		return issued
	}
	unrestricted := issue(`{"name":"Local reader","scopes":["knowledgebase:read"],"knowledgebase_folders":null,"expires_in_days":7}`, priyaJWT)
	denied := issue(`{"name":"No content","scopes":["knowledgebase:read"],"knowledgebase_folders":[],"expires_in_days":7}`, priyaJWT)
	if unrestricted.Metadata.KnowledgebaseFolders != nil || denied.Metadata.KnowledgebaseFolders == nil || len(*denied.Metadata.KnowledgebaseFolders) != 0 {
		t.Fatalf("null and empty caps confused: %+v %+v", unrestricted.Metadata, denied.Metadata)
	}
	readRequest := `{"name":"brain_read","arguments":{"action":"read","entry_id":"` + entry.EntryID + `"}}`
	if w := request("POST", "/api/external/v1/call", readRequest, unrestricted.Token); w.Code != 200 {
		t.Fatalf("live inherited reader denied: %d %s", w.Code, w.Body)
	}
	if w := request("POST", "/api/external/v1/call", readRequest, denied.Token); w.Code != 404 {
		t.Fatalf("empty caps exposed content: %d %s", w.Code, w.Body)
	}
	serviceBody := `{"name":"Workflow","scopes":["knowledgebase:read","knowledgebase:write"],"knowledgebase_identity_id":"` + identity.IdentityID + `","knowledgebase_folders":[{"folder_path":"Engineering","role":"editor"}],"expires_in_days":7}`
	if w := request("POST", "/api/auth/access-tokens", serviceBody, priyaJWT); w.Code != 403 {
		t.Fatalf("non-admin minted service token: %d %s", w.Code, w.Body)
	}
	if w := request("POST", "/api/auth/access-tokens", `{"scopes":["knowledgebase:read"],"knowledgebase_identity_id":"priya","expires_in_days":7}`, ownerJWT); w.Code != 403 {
		t.Fatalf("admin token impersonated a human identity: %d %s", w.Code, w.Body)
	}
	bound := issue(serviceBody, ownerJWT)
	if bound.Metadata.KnowledgebaseIdentityID != identity.IdentityID {
		t.Fatal("service binding not persisted")
	}
	if w := request("POST", "/api/external/v1/call", readRequest, bound.Token); w.Code != 200 {
		t.Fatalf("service reader denied: %d %s", w.Code, w.Body)
	}
	call("manage_knowledgebase_access", map[string]any{"action": "disable_service_account", "identity_id": identity.IdentityID, "request_id": "disable-service"})
	if w := request("GET", "/api/external/v1/tools", "", bound.Token); w.Code != 401 {
		t.Fatalf("disabled service identity retained connection: %d %s", w.Code, w.Body)
	}
	if w := request("DELETE", "/api/auth/access-tokens/"+unrestricted.Metadata.ID, "", priyaJWT); w.Code != 204 {
		t.Fatalf("explicit revoke: %d %s", w.Code, w.Body)
	}
	if w := request("GET", "/api/external/v1/tools", "", unrestricted.Token); w.Code != 401 {
		t.Fatalf("revoked KB token retained connection: %d %s", w.Code, w.Body)
	}
}

func TestKnowledgebasePrincipalRecheckRejectsRevokedToken(t *testing.T) {
	tokenTestSetup(t)
	t.Setenv("AGENT_PRODUCTS", "knowledgebase")
	withMemoryUserDirectory(t, `{"users":[]}`)
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	identityID := GetDefaultUserID()
	token, _, err := store.Issue(context.Background(), accesstokens.Token{Name: "MCP writer", UserID: identityID, Scopes: []string{"knowledgebase:read", "knowledgebase:write"}, ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: identityID, AccessToken: &token}
	r := httptest.NewRequest("POST", "/api/external/v1/call", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
	principal := knowledgebasePrincipal(r, claims)
	if principal.Recheck == nil {
		t.Fatal("publication principal does not revalidate connection authority")
	}
	if err := principal.Recheck(context.Background()); err != nil {
		t.Fatal("live connection rejected:", err)
	}
	if err := store.Revoke(context.Background(), token.ID, identityID, now); err != nil {
		t.Fatal(err)
	}
	err = principal.Recheck(context.Background())
	domain, ok := err.(*knowledgebase.Error)
	if !ok || domain.Code != "FORBIDDEN" {
		t.Fatalf("revoked principal permitted publication: %v", err)
	}
}
func TestAccessTokenHTTPManagementAndRestrictions(t *testing.T) {
	api := tokenTestSetup(t)
	jwt, err := GenerateJWT(GetDefaultUserID(), "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	router.HandleFunc("/api/auth/access-tokens", api.handleAccessTokens).Methods("GET", "POST")
	router.HandleFunc("/api/auth/access-tokens/{id}", api.handleAccessTokens).Methods("DELETE")
	router.HandleFunc("/api/external/v1/tools", api.handleExternalTools)
	router.HandleFunc("/api/external/v1/call", api.handleExternalCall)
	handler := AuthMiddleware(router)
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	issued := request("POST", "/api/auth/access-tokens", `{"name":"Local CLI","scopes":["workflows:read","files:read"],"all_workflows":true,"expires_in_days":30}`, jwt)
	if issued.Code != 201 {
		t.Fatal(issued.Code, issued.Body)
	}
	if issued.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("token response may be cached")
	}
	var created struct {
		Token    string             `json:"token"`
		Metadata accesstokens.Token `json:"access_token"`
	}
	json.Unmarshal(issued.Body.Bytes(), &created)
	if created.Token == "" {
		t.Fatal("missing token")
	}
	list := request("GET", "/api/auth/access-tokens", "", jwt)
	if list.Code != 200 || strings.Contains(list.Body.String(), created.Token) {
		t.Fatal("token disclosed in list")
	}
	catalog := request("GET", "/api/external/v1/tools", "", created.Token)
	var catalogBody struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if catalog.Code != 200 || json.Unmarshal(catalog.Body.Bytes(), &catalogBody) != nil {
		t.Fatal(catalog.Code, catalog.Body)
	}
	visible := map[string]bool{}
	for _, tool := range catalogBody.Tools {
		visible[tool.Name] = true
	}
	if !visible["read_file"] || !visible["list_step_code"] || visible["write_file"] || visible["builder_chat"] {
		t.Fatal(catalog.Code, catalog.Body)
	}
	for _, path := range []string{"/api/auth/access-tokens", "/api/auth/password", "/api/wp/api/documents", "/api/query"} {
		w := request("POST", path, `{}`, created.Token)
		if w.Code != 403 {
			t.Fatal("PAT escaped external API", path, w.Code)
		}
	}
	// Direct writes are available only with explicitly granted files:write.
	denied := request("POST", "/api/external/v1/call", `{"name":"write_file","arguments":{}}`, created.Token)
	if denied.Code != 403 || !strings.Contains(denied.Body.String(), "insufficient_scope") {
		t.Fatal(denied.Code, denied.Body)
	}
	query := httptest.NewRequest("GET", "/api/external/v1/tools?token="+created.Token, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, query)
	if w.Code != 401 {
		t.Fatal("PAT accepted from URL")
	}
	revoked := request("DELETE", "/api/auth/access-tokens/"+created.Metadata.ID, "", jwt)
	if revoked.Code != 204 {
		t.Fatal(revoked.Code, revoked.Body)
	}
	if w = request("GET", "/api/external/v1/tools", "", created.Token); w.Code != 401 {
		t.Fatal("revocation not enforced", w.Code)
	}
}
func TestAccessTokenIssuingReplacesPreviousToken(t *testing.T) {
	api := tokenTestSetup(t)
	jwt, err := GenerateJWT(GetDefaultUserID(), "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	router.HandleFunc("/api/auth/access-tokens", api.handleAccessTokens).Methods("GET", "POST")
	router.HandleFunc("/api/external/v1/tools", api.handleExternalTools)
	handler := AuthMiddleware(router)
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	issue := func(name string) (string, string) {
		t.Helper()
		w := request("POST", "/api/auth/access-tokens", `{"name":"`+name+`","scopes":["workflows:read","files:read"],"all_workflows":true,"expires_in_days":30}`, jwt)
		if w.Code != 201 {
			t.Fatalf("issue %s: %d %s", name, w.Code, w.Body.String())
		}
		var created struct {
			Token    string             `json:"token"`
			Metadata accesstokens.Token `json:"access_token"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		return created.Token, created.Metadata.ID
	}
	// Empty names default server-side now that the dialog asks for none.
	unnamed := request("POST", "/api/auth/access-tokens", `{"scopes":["workflows:read"],"all_workflows":true,"expires_in_days":7}`, jwt)
	if unnamed.Code != 201 {
		t.Fatalf("unnamed issue: %d %s", unnamed.Code, unnamed.Body.String())
	}
	first, _ := issue("First")
	if w := request("GET", "/api/external/v1/tools", "", first); w.Code != 200 {
		t.Fatalf("first token rejected before replacement: %d", w.Code)
	}
	second, _ := issue("Second")
	if w := request("GET", "/api/external/v1/tools", "", first); w.Code != 401 || !strings.Contains(w.Body.String(), "invalid_token") {
		t.Fatalf("replaced token still accepted: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/external/v1/tools", "", second); w.Code != 200 {
		t.Fatalf("replacement token rejected: %d", w.Code)
	}
	list := request("GET", "/api/auth/access-tokens", "", jwt)
	var listed struct {
		Tokens []accesstokens.Token `json:"tokens"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	live := 0
	for _, token := range listed.Tokens {
		if token.RevokedAt == nil && token.ExpiresAt.After(time.Now()) {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("%d live tokens, want exactly 1", live)
	}
}

func TestTokenSessionWorkflowReadRoot(t *testing.T) {
	tokenClaims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{ID: "t"}}
	appClaims := &UserClaims{UserID: "owner"}
	cases := []struct {
		name   string
		claims *UserClaims
		folder string
		want   string
	}{
		{"token session scoped to its workflow", tokenClaims, "Workflow/Invoices", "Workflow/Invoices/"},
		{"token session trims trailing slash", tokenClaims, "Workflow/Invoices/", "Workflow/Invoices/"},
		{"app session scoped to its workflow too", appClaims, "Workflow/Invoices", "Workflow/Invoices/"},
		{"nil claims scoped to the workflow", nil, "Workflow/Invoices", "Workflow/Invoices/"},
		{"unresolved folder grants nothing", tokenClaims, "", ""},
		{"blank folder grants nothing", appClaims, "   ", ""},
	}
	for _, tc := range cases {
		if got := tokenSessionWorkflowReadRoot(tc.claims, tc.folder); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestAccessTokenIdentityFailsClosed(t *testing.T) {
	tokenTestSetup(t)
	t.Setenv("MULTI_USER_MODE", "true")
	content := withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true}]}`)
	token := accesstokens.Token{UserID: "owner", Username: "owner"}
	if _, err := accessTokenClaims(token); err != nil {
		t.Fatal(err)
	}
	*content = `{"users":[{"id":"owner","username":"owner","disabled":true}]}`
	if _, err := accessTokenClaims(token); err == nil {
		t.Fatal("disabled account accepted")
	}
	*content = `{"users":[]}`
	if _, err := accessTokenClaims(token); err == nil {
		t.Fatal("removed account accepted")
	}
	*content = `broken`
	if _, err := accessTokenClaims(token); err == nil {
		t.Fatal("directory failure accepted")
	}
}
func TestAccessTokenCannotInheritOtherBuilderSession(t *testing.T) {
	api := tokenTestSetup(t)
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{ID: "one", AllWorkflows: true, Scopes: accesstokens.Scopes}}
	for _, sid := range []string{"browser-session", "pat-two-session"} {
		r := httptest.NewRequest("POST", "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		api.externalBuilderCall(w, r, "builder_status", map[string]interface{}{"session_id": sid}, DiscoveredWorkflow{WorkspacePath: "Workflow/test"})
		if w.Code != 404 {
			t.Fatal("PAT inherited a session", w.Code)
		}
	}
}
func TestAccessTokenStateOutsideWorkspace(t *testing.T) {
	tokenTestSetup(t)
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(root, "state"))
	if store, err := openAccessTokens(); err == nil {
		store.Close()
		t.Fatal("token store exposed in workspace")
	}
}
func TestAccessTokenScopesNeverTrustJWTJSON(t *testing.T) {
	var claims UserClaims
	if err := json.Unmarshal([]byte(`{"user_id":"owner","AccessToken":{"AllWorkflows":true},"access_token":{}}`), &claims); err != nil {
		t.Fatal(err)
	}
	if claims.AccessToken != nil {
		t.Fatal("claims deserialized PAT authority")
	}
}
func TestAccessTokenExpiry(t *testing.T) {
	api := tokenTestSetup(t)
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	old := time.Now().Add(-2 * time.Hour)
	_, raw, err := store.Issue(context.Background(), accesstokens.Token{Name: "expired", UserID: GetDefaultUserID(), AllWorkflows: true, Scopes: []string{"workflows:read"}, ExpiresAt: old.Add(time.Hour)}, old)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/external/v1/tools", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	AuthMiddleware(http.HandlerFunc(api.handleExternalTools)).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("expired PAT accepted", w.Code)
	}
}

func TestAccessTokenWorkflowFilterAndAccountIntersection(t *testing.T) {
	tokenTestSetup(t)
	f := newExternalToolsFixture(t)
	// Give owner ordinary access to both workflows; the PAT narrows this to one.
	f.write(t, "Workflow/secret/workflow.json", `{"id":"secret","label":"Private research","access":{"owners":["owner"]}}`)
	claims := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"workflows:read"}, WorkflowIDs: []string{"invoices"}}}
	call := func(name string, args map[string]any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		w := httptest.NewRecorder()
		f.api.handleExternalCall(w, adminRequest("POST", "/api/external/v1/call", string(body), claims, nil))
		return w
	}
	w := call("list_workflows", map[string]any{})
	if w.Code != 200 || strings.Contains(w.Body.String(), `"secret"`) {
		t.Fatal("workflow scope ignored", w.Code, w.Body)
	}
	w = call("get_workflow", map[string]any{"workflow_id": "secret"})
	if w.Code != 404 {
		t.Fatal("out-of-scope workflow accessible", w.Code)
	}
	claims.UserID = "reader"
	claims.Username = "reader"
	w = call("read_file", map[string]any{"workflow_id": "invoices", "path": "docs/process.md"})
	if w.Code != 403 {
		t.Fatal("token scope not enforced", w.Code, w.Body)
	}
	w = call("list_step_code", map[string]any{"workflow_id": "invoices"})
	if w.Code != 403 {
		t.Fatal("code inventory scope not enforced", w.Code, w.Body)
	}
}

func TestAccessTokenRuntimeLeaseAndCancellation(t *testing.T) {
	tokenTestSetup(t)
	f := newExternalToolsFixture(t)
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	token, _, err := store.Issue(context.Background(), accesstokens.Token{Name: "Reader", UserID: "owner", Username: "owner", AllWorkflows: true, Scopes: []string{"workflows:read", "files:read"}, ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	entry := accessTokenSession{token.ID, "pat-test", "Workflow/invoices", WorkflowAccessOwner}
	if !accessTokenSessionAllowed(context.Background(), entry) {
		t.Fatal("valid runtime lease denied")
	}
	if err = store.Revoke(context.Background(), token.ID, "owner", now); err != nil {
		t.Fatal(err)
	}
	if accessTokenSessionAllowed(context.Background(), entry) {
		t.Fatal("revoked runtime lease accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.api.stoppedSessions = map[string]bool{}
	f.api.sessionBusy = map[string]bool{}
	f.api.agentCancelFuncs = map[string]context.CancelFunc{"pat-test": cancel}
	f.api.accessTokenSessions.sessions = map[string]accessTokenSession{"pat-test": entry}
	f.api.cancelAccessTokenSessions(token.ID)
	if ctx.Err() == nil {
		t.Fatal("revocation did not cancel active turn")
	}
	if !f.api.isSessionMarkedStopped("pat-test") {
		t.Fatal("runtime not stopped against new work")
	}
}

func TestLocalFullAccessTokenIssuance(t *testing.T) {
	api := tokenTestSetup(t)
	withMemoryUserDirectory(t, `{"users":[]}`)
	t.Setenv("AGENT_PRODUCTS", "knowledgebase")
	t.Setenv("AGENTWORKS_KNOWLEDGEBASE_ROOT", filepath.Join(t.TempDir(), "knowledgebase"))
	request := func(claims *UserClaims) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/access-tokens", strings.NewReader(`{"name":"Local agent","local_full_access":true}`))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		api.handleAccessTokens(w, r)
		return w
	}
	claims := &UserClaims{UserID: GetDefaultUserID(), Username: "user"}
	w := request(claims)
	if w.Code != http.StatusCreated {
		t.Fatalf("local full token: %d %s", w.Code, w.Body.String())
	}
	var issued struct {
		AccessToken accesstokens.Token `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if issued.AccessToken.Name != "agentworks-local" {
		t.Fatal("local token name must be fixed")
	}
	if !issued.AccessToken.NonExpiring {
		t.Fatal("local token must not expire")
	}
	for _, scope := range mcpOAuthScopesFor(claims, mcpOAuthScopes) {
		if !issued.AccessToken.Allows(scope) {
			t.Errorf("missing available scope %s", scope)
		}
	}
	if !issued.AccessToken.AllWorkflows || !issued.AccessToken.AllCrews || issued.AccessToken.KnowledgebaseFolders != nil || issued.AccessToken.KnowledgebaseIdentityID != "" {
		t.Fatal("full token must follow the local account's live access")
	}
	if w := request(&UserClaims{UserID: "another-user"}); w.Code != http.StatusForbidden {
		t.Fatalf("foreign local identity admitted: %d", w.Code)
	}
	t.Setenv("MULTI_USER_MODE", "true")
	if _, err := accessTokenClaims(issued.AccessToken); err == nil {
		t.Fatal("non-expiring local token admitted after enabling multi-user mode")
	}
	if w := request(claims); w.Code != http.StatusForbidden {
		t.Fatalf("multi-user full local token admitted: %d", w.Code)
	}
}

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func personalRoute(api *StreamingAPI, handler func(*StreamingAPI, http.ResponseWriter, *http.Request), method, target, body, user string, vars map[string]string) *httptest.ResponseRecorder {
	req := mux.SetURLVars(profileRouteRequest(method, target, []byte(body), user), vars)
	rec := httptest.NewRecorder()
	handler(api, rec, req)
	return rec
}

// A person manages only their own servers and secrets; switching one on
// needs access to the Code; listed URLs drop their query string.
func TestPersonalMCPRoutesActOnTheCallerOnly(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	withPersonalMCPRoot(t)
	project := "c0de0001-0000"

	sealed, err := encryptSecretValueWithAAD("sk-owner", []byte("owner"))
	if err != nil {
		t.Fatal(err)
	}
	if rec := personalRoute(api, (*StreamingAPI).handlePutPersonalSecret, http.MethodPut, "/x", `{"encrypted_value":"`+sealed+`"}`, "owner", map[string]string{"name": "SVC_KEY"}); rec.Code != http.StatusOK {
		t.Fatalf("set secret = %d %s", rec.Code, rec.Body.String())
	}
	// A value sealed for someone else does not decrypt for this caller.
	if rec := personalRoute(api, (*StreamingAPI).handlePutPersonalSecret, http.MethodPut, "/x", `{"encrypted_value":"`+sealed+`"}`, "other", map[string]string{"name": "SVC_KEY"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("other stored owner's sealed value: %d", rec.Code)
	}
	add := `{"name":"svc","url":"https://mcp.example.com/mcp?token=abc","headers":{"Authorization":{"secret":"SVC_KEY","format":"Bearer {}"}}}`
	if rec := personalRoute(api, (*StreamingAPI).handleAddPersonalMCP, http.MethodPost, "/x", add, "owner", nil); rec.Code != http.StatusOK {
		t.Fatalf("add = %d %s", rec.Code, rec.Body.String())
	}
	if rec := personalRoute(api, (*StreamingAPI).handleAddPersonalMCP, http.MethodPost, "/x", `{"name":"bad","url":"https://10.0.0.5/mcp"}`, "owner", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("private address accepted: %d", rec.Code)
	}

	switchOn := `{"enabled":true}`
	if rec := personalRoute(api, (*StreamingAPI).handleSwitchPersonalMCP, http.MethodPut, "/x", switchOn, "owner", map[string]string{"name": "svc", "project_id": project}); rec.Code != http.StatusOK {
		t.Fatalf("switch on = %d %s", rec.Code, rec.Body.String())
	}
	// "other" has no access to the owner's Code, and no server named svc.
	if rec := personalRoute(api, (*StreamingAPI).handleSwitchPersonalMCP, http.MethodPut, "/x", switchOn, "other", map[string]string{"name": "svc", "project_id": project}); rec.Code != http.StatusNotFound {
		t.Fatalf("other switched a server on in owner's Code: %d", rec.Code)
	}

	rec := personalRoute(api, (*StreamingAPI).handleListPersonalMCP, http.MethodGet, "/x?code="+project, "", "owner", nil)
	var listed struct {
		Servers []personalMCPServerView `json:"servers"`
		Secrets []string                `json:"secrets"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Servers) != 1 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	got := listed.Servers[0]
	if !got.Enabled || strings.Contains(got.URL, "token") || strings.Contains(rec.Body.String(), "sk-owner") || len(listed.Secrets) != 1 {
		t.Fatalf("listed = %+v %s", got, rec.Body.String())
	}
	if rec := personalRoute(api, (*StreamingAPI).handleListPersonalMCP, http.MethodGet, "/x", "", "other", nil); !strings.Contains(rec.Body.String(), `"servers":[]`) {
		t.Fatalf("other sees servers: %s", rec.Body.String())
	}
	if rec := personalRoute(api, (*StreamingAPI).handleRemovePersonalMCP, http.MethodDelete, "/x", "", "other", map[string]string{"name": "svc"}); rec.Code != http.StatusNotFound {
		t.Fatalf("other removed owner's server: %d", rec.Code)
	}
	if rec := personalRoute(api, (*StreamingAPI).handleRemovePersonalMCP, http.MethodDelete, "/x", "", "owner", map[string]string{"name": "svc"}); rec.Code != http.StatusOK {
		t.Fatalf("remove = %d", rec.Code)
	}
}

package wsauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTransportStampsTokenOnlyForWorkspaceHost(t *testing.T) {
	var seen string
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.Header.Get(HeaderName) }))
	defer workspace.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.Header.Get(HeaderName) }))
	defer other.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	t.Setenv("WORKSPACE_API_TOKEN", "tok-1")
	client := &http.Client{Transport: Transport(&http.Transport{})}

	if _, err := client.Get(workspace.URL + "/api/documents/x"); err != nil || seen != "tok-1" {
		t.Fatalf("workspace request token=%q err=%v", seen, err)
	}
	if _, err := client.Get(other.URL + "/x"); err != nil || seen != "" {
		t.Fatalf("token leaked to another host: %q err=%v", seen, err)
	}
	t.Setenv("WORKSPACE_API_TOKEN", "")
	if _, err := client.Get(workspace.URL + "/x"); err != nil || seen != "" {
		t.Fatalf("empty token must send nothing: %q", seen)
	}
}

// http.DefaultTransport stays an *http.Transport (libraries such as whatsmeow
// assert it) and default-transport requests to the workspace carry the token.
func TestInstallDefaultKeepsTransportType(t *testing.T) {
	var seen string
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.Header.Get(HeaderName) }))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	t.Setenv("WORKSPACE_API_TOKEN", "tok-2")
	t.Setenv("NO_PROXY", "*")
	InstallDefault()
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("DefaultTransport is %T, want *http.Transport", http.DefaultTransport)
	}
	_ = base.Clone() // what whatsmeow does at NewClient
	if _, err := http.Get(workspace.URL + "/api/documents"); err != nil || seen != "tok-2" {
		t.Fatalf("default client token=%q err=%v", seen, err)
	}
	cloned := &http.Client{Transport: base.Clone()}
	seen = ""
	if _, err := cloned.Get(workspace.URL + "/x"); err != nil || seen != "tok-2" {
		t.Fatalf("cloned default transport token=%q err=%v", seen, err)
	}
}

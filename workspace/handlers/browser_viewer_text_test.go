package handlers

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBrowserViewerTextUsesOnlyExistingSessionIPC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix IPC fixture")
	}
	root, err := os.MkdirTemp("/tmp", "aw-viewer-text-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	t.Setenv("AGENT_BROWSER_SOCKET_DIR", root)
	const session = "aw-viewer-text-fixture"
	os.WriteFile(filepath.Join(root, session+".stream"), []byte("9000"), 0600)
	listener, err := net.Listen("unix", filepath.Join(root, session+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requests := make(chan map[string]any, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request map[string]any
		json.NewDecoder(conn).Decode(&request)
		requests <- request
		json.NewEncoder(conn).Encode(map[string]any{"id": "viewer-paste", "success": true, "data": map[string]bool{"inserted": true}})
	}()
	text := "first line\n日本語🙂 \"quotes\" $(not-a-command)"
	body, _ := json.Marshal(map[string]string{"text": text})
	router := gin.New()
	router.POST("/browser/:session/text", BrowserViewerText)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("POST", "/browser/"+session+"/text", strings.NewReader(string(body))))
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	request := <-requests
	if request["action"] != "keyboard" || request["subaction"] != "insertText" || request["text"] != text || len(request) != 4 {
		t.Fatal(request)
	}
	for _, path := range []string{"missing", "bad_session"} {
		response = httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", "/browser/"+path+"/text", strings.NewReader(string(body))))
		if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), text) {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("POST", "/browser/"+session+"/text", strings.NewReader(`{"text":"`+strings.Repeat("a", 9000)+`"}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatal(response.Code)
	}
}

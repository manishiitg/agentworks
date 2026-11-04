package handlers

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
	"github.com/spf13/viper"
)

func TestChatFolderIsAppendOnlyOverTheDocumentsAPI(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()
	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docsDir)
	router := gin.New()
	router.Any("/api/documents/*filepath", HandleDocumentRequest)
	call := func(method, url string, body interface{}) (int, map[string]interface{}) {
		var reader *bytes.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		} else {
			reader = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, url, reader)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var out map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	history := func(out map[string]interface{}) (string, map[string]interface{}) {
		data, _ := out["data"].(map[string]interface{})
		var record map[string]interface{}
		_ = json.Unmarshal([]byte(data["content"].(string)), &record)
		var texts []string
		for _, row := range record["conversation_history"].([]interface{}) {
			parts := row.(map[string]interface{})["Parts"].([]interface{})
			texts = append(texts, parts[0].(map[string]interface{})["Text"].(string))
		}
		return strings.Join(texts, ","), record
	}
	const url = "/api/documents/Crew/c/builder/conversation/session-s/header.json"
	msg := func(role, text string) map[string]interface{} {
		return map[string]interface{}{"Role": role, "Parts": []map[string]string{{"Text": text}}}
	}
	turn := func(id, at string, messages ...interface{}) map[string]interface{} {
		return map[string]interface{}{"id": id, "at": at, "messages": messages}
	}
	first := map[string]interface{}{"turns": []interface{}{turn("t1", "2026-11-03T10:00:00Z", msg("system", "p"), msg("human", "a"), msg("ai", "b"))}, "patch": map[string]interface{}{"session_id": "s", "user_id": "u"}}
	if code, out := call(http.MethodPost, url+"/append-turn", first); code != http.StatusOK {
		t.Fatalf("append = %d %v", code, out)
	}
	second := map[string]interface{}{"turns": []interface{}{turn("t2", "2026-11-03T10:01:00Z", msg("human", "c"))}}
	if code, out := call(http.MethodPost, url+"/append-turn", second); code != http.StatusOK {
		t.Fatalf("append = %d %v", code, out)
	}
	code, out := call(http.MethodGet, url, nil)
	if got, rec := history(out); code != http.StatusOK || got != "p,a,b,c" || rec["user_id"] != "u" {
		t.Fatalf("GET = %d %s %v", code, got, rec)
	}
	_, out = call(http.MethodGet, url+"?turns=1", nil)
	if got, page := history(out); got != "p,c" || page["history_turns_total"] != float64(2) || page["history_page"] != true {
		t.Fatalf("page = %s %v", got, page)
	}
	// History is append-only: a whole-record write is refused.
	record, _ := json.Marshal(map[string]interface{}{"session_id": "s", "conversation_history": []interface{}{msg("human", "x")}})
	if code, _ := call(http.MethodPut, url, map[string]string{"content": string(record)}); code != http.StatusConflict {
		t.Fatalf("PUT on a chat header must be refused, got %d", code)
	}
	// Deleting the chat removes its whole folder.
	if code, _ := call(http.MethodDelete, url+"?confirm=true", nil); code != http.StatusOK {
		t.Fatalf("DELETE = %d", code)
	}
	if _, err := os.Stat(filepath.Join(docsDir, "Crew/c/builder/conversation/session-s")); !os.IsNotExist(err) {
		t.Fatalf("chat folder not removed: %v", err)
	}
}

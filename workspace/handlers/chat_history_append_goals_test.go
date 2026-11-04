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
	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
	"github.com/spf13/viper"
)

func TestGoalConversationDocumentsUseTheHistoryLog(t *testing.T) {
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
	const url = "/api/documents/Workflow/wf/builder/conversation/session-s-conversation.json"
	msg := func(role, text string) map[string]interface{} {
		return map[string]interface{}{"Role": role, "Parts": []map[string]string{{"Text": text}}}
	}
	record, _ := json.Marshal(map[string]interface{}{"session_id": "s", "revision": 1, "conversation_history": []interface{}{msg("system", "p"), msg("human", "a"), msg("ai", "b")}})

	if code, _ := call(http.MethodPut, url, map[string]string{"content": string(record)}); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	if _, err := os.Stat(chatlog.HistoryLogPath(filepath.Join(docsDir, "Workflow/wf/builder/conversation/session-s-conversation.json"))); err != nil {
		t.Fatalf("history log not written: %v", err)
	}
	code, out := call(http.MethodGet, url, nil)
	if got, _ := history(out); code != http.StatusOK || got != "p,a,b" {
		t.Fatalf("GET = %d %s", code, got)
	}
	append := map[string]interface{}{"messages": []interface{}{msg("human", "c")}, "patch": map[string]interface{}{"revision": 2}}
	if code, out := call(http.MethodPost, url+"/append-history", append); code != http.StatusOK {
		t.Fatalf("append = %d %v", code, out)
	}
	_, out = call(http.MethodGet, url, nil)
	if got, rec := history(out); got != "p,a,b,c" || rec["revision"] != float64(2) {
		t.Fatalf("after append = %s rev %v", got, rec["revision"])
	}
	_, out = call(http.MethodGet, url+"?tail=1", nil)
	got, tail := history(out)
	if got != "p,c" || tail["history_total"] != float64(3) || tail["history_tail"] != true {
		t.Fatalf("tail = %s %v", got, tail)
	}
	tailRaw, _ := json.Marshal(tail)
	if code, _ := call(http.MethodPut, url, map[string]string{"content": string(tailRaw)}); code != http.StatusConflict {
		t.Fatalf("saving a tail must be refused, got %d", code)
	}
	if code, _ := call(http.MethodPost, url+"/move", map[string]string{"destination_path": "Workflow/wf/builder/conversation/moved-conversation.json"}); code == http.StatusOK {
		if _, err := os.Stat(filepath.Join(docsDir, "Workflow/wf/builder/conversation/moved-conversation.history.jsonl")); err != nil {
			t.Fatalf("history log did not move: %v", err)
		}
	}
}

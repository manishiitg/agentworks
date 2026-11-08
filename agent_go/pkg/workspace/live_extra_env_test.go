package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// A secret created by a tool call mid-turn must reach shell commands of the
// same turn: SetExtraEnv on the live client is what carries it, and every
// request reads a consistent snapshot.
func TestSetExtraEnvReachesNextShellRequest(t *testing.T) {
	var mu sync.Mutex
	var seen []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ExtraEnv map[string]string `json:"extra_env"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		seen = append(seen, body.ExtraEnv)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"stdout":"ok","exit_code":0}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithExtraEnv(map[string]string{"SECRET_OLD": "1"}))
	if _, err := c.ExecuteShellCommand(context.Background(), ExecuteShellCommandParams{Command: "true"}); err != nil {
		t.Fatalf("first command: %v", err)
	}
	c.SetExtraEnv("SECRET_NEW", "s3cret")
	if _, err := c.ExecuteShellCommand(context.Background(), ExecuteShellCommandParams{Command: "true"}); err != nil {
		t.Fatalf("second command: %v", err)
	}
	c.DeleteExtraEnv("SECRET_NEW")
	if _, err := c.ExecuteShellCommand(context.Background(), ExecuteShellCommandParams{Command: "true"}); err != nil {
		t.Fatalf("third command: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(seen))
	}
	if _, ok := seen[0]["SECRET_NEW"]; ok {
		t.Fatal("first request must not carry the secret set later")
	}
	if seen[1]["SECRET_NEW"] != "s3cret" || seen[1]["SECRET_OLD"] != "1" {
		t.Fatalf("second request env = %v, want SECRET_NEW alongside SECRET_OLD", seen[1])
	}
	if _, ok := seen[2]["SECRET_NEW"]; ok {
		t.Fatal("third request must not carry the deleted secret")
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Runs the real command over HTTP on Windows as well as Unix. The count is
// displayed before the clear request, using exactly the same selection.
func TestCLIInboxCleanup(t *testing.T) {
	for _, preview := range []string{`{"matched":105}`, `{}`, `{"matched":null}`} {
		t.Run(preview, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			var actions []string
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Name == "get_agent_context" {
					fmt.Fprint(w, `{"guidance_version":"test-guidance"}`)
					return
				}
				action, _ := body.Arguments["action"].(string)
				actions = append(actions, action)
				if body.Name != "messages" || body.Arguments["agent_name"] != "reviewer" || body.Arguments["older_than"] != "7d" || body.Arguments["from_agent"] != "worker" || body.Arguments["delivered_only"] != true {
					t.Error("cleanup selection changed", body)
				}
				if action == "count" {
					fmt.Fprint(w, preview)
					return
				}
				if action != "clear" || !strings.Contains(stderr.String(), "105 matching messages") {
					t.Error("clear was not preceded by the displayed count", action, stderr.String())
				}
				fmt.Fprint(w, `{"removed":105,"remaining":2}`)
			}))
			defer s.Close()
			code := run(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "config.json"), "--server", s.URL, "--json", "messages", "clear", "--agent", "reviewer", "--older-than", "7d", "--from", "worker", "--delivered"}, strings.NewReader(""), &stdout, &stderr, func(key string) string {
				if key == "AGENTWORKS_TOKEN" {
					return "jwt"
				}
				return ""
			})
			if preview == `{"matched":105}` {
				if code != 0 || strings.Join(actions, ",") != "count,clear" || stdout.String() != "{\"removed\":105,\"remaining\":2}\n" {
					t.Fatalf("code=%d actions=%v out=%s err=%s", code, actions, &stdout, &stderr)
				}
			} else if code == 0 || strings.Join(actions, ",") != "count" || stdout.Len() != 0 {
				t.Fatalf("missing count allowed a clear: code=%d actions=%v out=%s err=%s", code, actions, &stdout, &stderr)
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIGuidanceStartupRefresh(t *testing.T) {
	version := "first"
	failCheck := false
	checks, tools := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer example-token" {
			t.Error("startup lost authentication")
		}
		if r.URL.Path == "/api/external/v1/call" {
			var request struct {
				Name      string
				Arguments map[string]any
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.Name != "help" || request.Arguments["action"] != "context" {
				t.Errorf("unexpected startup operation %s", request.Name)
			}
			checks++
			if failCheck {
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"offline"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"guidance_version": version})
			return
		}
		tools++
		_, _ = w.Write([]byte(`{"tools":[]}`))
	}))
	defer srv.Close()
	config := filepath.Join(t.TempDir(), "connection.json")
	invoke := func(wantNotice bool) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"--server", srv.URL, "--config", config, "--json", "tools", "list"}, strings.NewReader(""), &stdout, &stderr, func(name string) string {
			if name == "AGENTWORKS_TOKEN" {
				return "example-token"
			}
			return ""
		})
		if code != 0 || stdout.String() != "{\"tools\":[]}\n" || strings.Contains(stderr.String(), "guidance changed") != wantNotice {
			t.Fatalf("startup output/protocol: %d %s %s", code, &stdout, &stderr)
		}
	}
	invoke(false)
	invoke(false)
	version = "changed"
	invoke(true)
	failCheck = true
	invoke(false)
	if checks != 4 || tools != 4 {
		t.Fatalf("check/business requests: %d/%d", checks, tools)
	}
	cache, err := os.ReadFile(config + ".guidance.json")
	if err != nil || bytes.Contains(cache, []byte("example-token")) || bytes.Contains(cache, []byte("content")) {
		t.Fatalf("cache must contain versions only: %s %v", cache, err)
	}
}

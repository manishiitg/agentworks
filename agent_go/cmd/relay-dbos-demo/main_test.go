package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

func TestBrowserDemoAdmission(t *testing.T) {
	d := &demo{ctx: context.Background(), root: t.TempDir()}
	handler := d.handler()
	for _, tc := range []struct {
		name, path, body, origin, header string
		code                             int
	}{
		{"cross-site", "/api/start", `{"scenario":"normal"}`, "https://example.com", "1", http.StatusForbidden},
		{"missing header", "/api/start", `{"scenario":"normal"}`, "", "", http.StatusForbidden},
		{"unknown scenario", "/api/start", `{"scenario":"unknown"}`, "", "1", http.StatusBadRequest},
		{"unknown field", "/api/start", `{"scenario":"normal","python":"arbitrary"}`, "", "1", http.StatusBadRequest},
		{"trailing body", "/api/start", `{"scenario":"normal"}{}`, "", "1", http.StatusBadRequest},
		{"oversized body", "/api/start", `{"scenario":"` + strings.Repeat("x", 1100) + `"}`, "", "1", http.StatusBadRequest},
		{"recover without run", "/api/recover", "", "", "1", http.StatusConflict},
		{"access without run", "/api/access", "", "", "1", http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18769"+tc.path, strings.NewReader(tc.body))
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Relay-Demo", tc.header)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.code, w.Body.String())
			}
		})
	}
	if d.run != nil {
		t.Fatal("invalid requests created an invocation")
	}
	for _, path := range []string{"/", "/api/state"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET %s: %d, %v", path, w.Code, w.Header())
		}
		if path == "/api/state" {
			var state map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || state["status"] != "idle" {
				t.Fatalf("unexpected initial state: %s", w.Body.String())
			}
		}
	}
}

func TestBrowserDemoRejectsOverlappingRunAndSnapshotsAccess(t *testing.T) {
	d := &demo{ctx: context.Background(), root: t.TempDir(), run: &session{
		id: "test", folder: "test", status: "running", allowed: true, calls: map[string]int{"extract": 1},
	}}
	if err := d.start("normal"); err == nil {
		t.Fatal("overlapping run accepted")
	}
	if err := d.recoverRun(); err == nil {
		t.Fatal("running invocation recovered concurrently")
	}
	d.run.status = "interrupted"
	request := httptest.NewRequest(http.MethodPost, "/api/access", nil)
	request.Header.Set("X-Relay-Demo", "1")
	w := httptest.NewRecorder()
	d.handler().ServeHTTP(w, request)
	if w.Code != http.StatusNoContent || d.run.allowed {
		t.Fatalf("permission toggle failed: %d", w.Code)
	}
	state := d.snapshot()
	d.run.calls["extract"]++
	if state["calls"].(map[string]interface{})["extract"] != float64(1) {
		t.Fatal("snapshot aliases mutable session state")
	}
}

func TestDiskWorkspacePaths(t *testing.T) {
	w := diskWorkspace{t.TempDir()}
	for _, path := range []string{"../outside", "/tmp/outside", "a/../../outside", ""} {
		if _, err := w.file(path); err == nil {
			t.Fatalf("accepted nonlocal path %q", path)
		}
	}
	ctx := context.Background()
	_, err := w.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: "run/relay.py", Content: source})
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: "run/relay.py"})
	if err != nil || got.Content != source {
		t.Fatalf("workspace round trip failed: %v", err)
	}
}

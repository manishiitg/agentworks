package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func setupGmailConfigTestWorkspace(t *testing.T, writeDelay time.Duration) *GmailService {
	t.Helper()
	t.Setenv("GOG_HOME", t.TempDir())
	t.Setenv("GMAIL_OAUTH_TOKEN_DIR", t.TempDir())
	clientsDir := t.TempDir()
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", clientsDir)
	if err := os.MkdirAll(filepath.Join(clientsDir, "primary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clientsDir, "primary", "client_secret.json"), []byte(`{"installed":{"client_id":"id","client_secret":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var saved string
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			if writeDelay > 0 {
				time.Sleep(writeDelay)
			}
			body, _ := io.ReadAll(r.Body)
			var put struct {
				Content string `json:"content"`
			}
			_ = json.Unmarshal(body, &put)
			mu.Lock()
			saved = put.Content
			mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}
		mu.Lock()
		content := saved
		mu.Unlock()
		out, _ := json.Marshal(map[string]any{"success": true, "data": map[string]string{"content": content}})
		_, _ = w.Write(out)
	}))
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	return &GmailService{config: &GmailConfig{}}
}

func TestGmailServiceConcurrentCreateConnectionAssignsUniqueIDs(t *testing.T) {
	g := setupGmailConfigTestWorkspace(t, 5*time.Millisecond)

	const workers = 10
	var wg sync.WaitGroup
	errs := make([]error, workers)
	ids := make([]string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			conn, err := g.CreateConnection(context.Background(), GmailConnectionInput{
				DisplayName: fmt.Sprintf("Account %d", idx),
				ClientName:  "primary",
			})
			if err != nil {
				errs[idx] = err
				return
			}
			ids[idx] = conn.ID
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d failed: %v", i, err)
		}
		if ids[i] == "" {
			t.Fatalf("worker %d got empty ID", i)
		}
		if seen[ids[i]] {
			t.Fatalf("duplicate connection ID generated: %s", ids[i])
		}
		seen[ids[i]] = true
	}

	got := g.ListConnections()
	if len(got) != workers {
		t.Fatalf("saved connections = %d, want %d", len(got), workers)
	}
}

func TestGmailServiceSyncScopesDoesNotClobberConcurrentCreateOrDelete(t *testing.T) {
	g := setupGmailConfigTestWorkspace(t, 15*time.Millisecond)
	keep, err := g.CreateConnection(context.Background(), GmailConnectionInput{
		DisplayName: "Keep",
		ClientName:  "primary",
	})
	if err != nil {
		t.Fatal(err)
	}
	toDelete, err := g.CreateConnection(context.Background(), GmailConnectionInput{
		DisplayName: "Remove",
		ClientName:  "primary",
	})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var createErr, deleteErr error
	var created GmailConnection

	wg.Add(3)
	go func() {
		defer wg.Done()
		g.syncConnectionScopes(context.Background(), keep.ID, []string{"https://www.googleapis.com/auth/gmail.send"})
	}()
	go func() {
		defer wg.Done()
		created, createErr = g.CreateConnection(context.Background(), GmailConnectionInput{
			DisplayName: "Added During Sync",
			ClientName:  "primary",
		})
	}()
	go func() {
		defer wg.Done()
		deleteErr = g.DeleteConnection(context.Background(), toDelete.ID)
	}()
	wg.Wait()

	if createErr != nil {
		t.Fatalf("CreateConnection failed: %v", createErr)
	}
	if deleteErr != nil {
		t.Fatalf("DeleteConnection failed: %v", deleteErr)
	}

	// DeleteConnection may finish before CreateConnection, allowing the new
	// connection to legitimately reuse the freed gmail_002 ID. Verify by
	// DisplayName and total count so we check that "Remove" stayed deleted
	// and "Added During Sync" survived.
	conns := g.ListConnections()
	if len(conns) != 2 {
		t.Fatalf("saved connections = %d (%+v), want 2", len(conns), conns)
	}
	var gotKeep, gotCreated *GmailConnection
	for i := range conns {
		c := conns[i]
		switch c.DisplayName {
		case "Remove":
			t.Fatalf("deleted connection %s (%s) was resurrected by concurrent write", c.ID, c.DisplayName)
		case "Keep":
			gotKeep = &c
		case "Added During Sync":
			gotCreated = &c
		}
	}
	if gotCreated == nil || gotCreated.ID != created.ID {
		t.Fatalf("newly created connection %s was lost (connections=%+v)", created.ID, conns)
	}
	if gotKeep == nil || gotKeep.ID != keep.ID {
		t.Fatalf("kept connection %s missing", keep.ID)
	}
	if len(gotKeep.Scopes) != 1 || gotKeep.Scopes[0] != "https://www.googleapis.com/auth/gmail.send" {
		t.Fatalf("kept connection scopes = %v, want gmail.send", gotKeep.Scopes)
	}
}

func TestGmailServiceDeleteDuringSlowGogCleanupPreservesConcurrentCreate(t *testing.T) {
	g := setupGmailConfigTestWorkspace(t, 0)

	// Fake gog binary that signals when it starts and waits for a gate file before exiting.
	gateDir := t.TempDir()
	startedFile := filepath.Join(gateDir, "started")
	releaseFile := filepath.Join(gateDir, "release")
	gogBinary := filepath.Join(gateDir, "gog")
	script := fmt.Sprintf("#!/bin/sh\ntouch %s\nwhile [ ! -f %s ]; do sleep 0.01; done\nexit 0\n",
		shellQuote(startedFile), shellQuote(releaseFile))
	if err := os.WriteFile(gogBinary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := g.UpdateConfig(context.Background(), func(cfg *GmailConfig) (bool, error) {
		cfg.GogPath = gogBinary
		cfg.Connections = []GmailConnection{{
			ID:          "gmail_001",
			AuthBackend: "gog",
			DisplayName: "To Delete",
			Email:       "old@example.com",
			ClientName:  "primary",
			Enabled:     true,
		}}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}

	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- g.DeleteConnection(context.Background(), "gmail_001")
	}()

	// Wait until gog token deletion is in flight.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for gog subprocess to start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// While gog is running, create a new connection.
	created, err := g.CreateConnection(context.Background(), GmailConnectionInput{
		DisplayName: "Created During Delete",
		ClientName:  "primary",
	})
	if err != nil {
		t.Fatalf("CreateConnection during delete failed: %v", err)
	}

	// Release gog and wait for DeleteConnection to finish.
	if err := os.WriteFile(releaseFile, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatalf("DeleteConnection failed: %v", err)
	}

	if _, ok := g.GetConnection("gmail_001"); ok {
		t.Fatal("gmail_001 should have been deleted")
	}
	if _, ok := g.GetConnection(created.ID); !ok {
		t.Fatalf("concurrently created connection %s was wiped by DeleteConnection", created.ID)
	}
}

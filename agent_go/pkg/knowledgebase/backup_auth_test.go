package knowledgebase

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBackupPATEncryptionRotationAndRemoval(t *testing.T) {
	s, admin, _ := fixture(t, false)
	s.cfg.BackupEncryptionKey = strings.Repeat("k", 32)
	admin.AccessOnly = true
	pat := "kb-test-only-token-value"
	args := map[string]any{"action": "configure_backup", "remote_url": "https://github.com/org/knowledge", "username": "kb-user", "pat": pat, "request_id": "pat-setup"}
	got := mcpCall(t, s, admin, "manage_knowledgebase_access", args)
	if got["pat_configured"] != true || got["pat"] != nil || got["encrypted_pat"] != nil {
		t.Fatal("unsafe setup result", got)
	}
	d, err := s.configuredBackupDestination()
	if err != nil {
		t.Fatal(err)
	}
	if d.EncryptedPAT == "" || d.EncryptedPAT == pat {
		t.Fatal("PAT not encrypted")
	}
	if d.Remote != "https://github.com/org/knowledge.git" {
		t.Fatal("GitHub page URL was not normalized", d.Remote)
	}
	if value, err := s.decryptBackupPAT(d); err != nil || value != pat {
		t.Fatal("PAT cannot be used", err)
	}
	if err := filepath.WalkDir(s.private, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte(pat)) {
			t.Errorf("plaintext PAT in %s", filepath.Base(path))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, err := reopened.configuredBackupDestination()
	if err != nil {
		t.Fatal(err)
	}
	if value, err := reopened.decryptBackupPAT(loaded); err != nil || value != pat {
		t.Fatal("credential lost on restart", err)
	}
	tampered := loaded
	tampered.Username = "other"
	if _, err := reopened.decryptBackupPAT(tampered); err == nil {
		t.Fatal("credential not bound to username")
	}
	// Omitting the field keeps the secret; an explicit empty string removes it.
	without := merge(args, map[string]any{"request_id": "pat-keep"})
	delete(without, "pat")
	mcpCall(t, s, admin, "manage_knowledgebase_access", without)
	mcpCall(t, s, admin, "manage_knowledgebase_access", merge(args, map[string]any{"pat": "replacement-test-only", "request_id": "pat-rotate"}))
	mcpCall(t, s, admin, "manage_knowledgebase_access", merge(args, map[string]any{"pat": "", "request_id": "pat-remove"}))
	d, err = s.configuredBackupDestination()
	if err != nil || d.EncryptedPAT != "" {
		t.Fatal("PAT not removed", err)
	}
	mcpError(t, s, admin, "manage_knowledgebase_access", merge(args, map[string]any{"remote_url": "git@github.com:org/knowledge.git", "request_id": "pat-ssh"}), "INVALID_ARGUMENT")
}

func TestBackupHTTPSUsesPATForReceiptAndFilesGitWithoutPersistingIt(t *testing.T) {
	s, admin, _ := fixture(t, false)
	s.cfg.AllowPrivateBackup = true // Operator opt-in for this local TLS fixture.
	s.cfg.BackupEncryptionKey = strings.Repeat("k", 32)
	root := t.TempDir()
	remote := filepath.Join(root, "backup.git")
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	if out, err := exec.Command("git", "--git-dir="+remote, "config", "http.receivepack", "true").CombinedOutput(); err != nil {
		t.Fatalf("config: %v %s", err, out)
	}
	pat := "kb-http-test-only-token"
	var authenticated atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "kb-user" || password != pat {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(401)
			return
		}
		authenticated.Add(1)
		cmd := exec.CommandContext(r.Context(), "git", "http-backend")
		cmd.Env = append(os.Environ(), "GIT_PROJECT_ROOT="+root, "GIT_HTTP_EXPORT_ALL=1", "PATH_INFO="+r.URL.Path, "QUERY_STRING="+r.URL.RawQuery, "REQUEST_METHOD="+r.Method, "CONTENT_TYPE="+r.Header.Get("Content-Type"), "REMOTE_USER=kb-user")
		cmd.Stdin = r.Body
		raw, err := cmd.Output()
		if err != nil {
			w.WriteHeader(500)
			return
		}
		parts := bytes.SplitN(raw, []byte("\r\n\r\n"), 2)
		if len(parts) != 2 {
			w.WriteHeader(500)
			return
		}
		for _, line := range strings.Split(string(parts[0]), "\r\n") {
			key, value, ok := strings.Cut(line, ":")
			if ok && key != "Status" {
				w.Header().Set(key, strings.TrimSpace(value))
			}
		}
		w.Write(parts[1])
	}))
	defer srv.Close()
	certFile := filepath.Join(root, "ca.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSL_CAINFO", certFile)
	setupAdmin := admin
	setupAdmin.AccessOnly = true
	mcpCall(t, s, setupAdmin, "manage_knowledgebase_access", map[string]any{"action": "configure_backup", "remote_url": srv.URL + "/backup.git", "username": "kb-user", "pat": pat, "request_id": "https-setup"})
	entry := call(t, s, admin, "create_knowledgebase", map[string]any{"folder_path": "", "filename": "hello.md", "type": "note", "title": "Hello", "content": "# Hello\n", "request_id": "https-entry"})
	receipt := mcpCall(t, s, admin, "backup_knowledgebase", map[string]any{"action": "commit", "entries": []any{map[string]any{"entry_id": entry["entry_id"], "expected_version": entry["version"]}}, "message": "HTTPS backup", "request_id": "https-commit"})
	mcpCall(t, s, admin, "backup_knowledgebase", map[string]any{"action": "push", "receipt_id": receipt["receipt_id"], "request_id": "https-push"})
	// Files fetch/pull uses the same encrypted credential, not only receipt pushes.
	if _, err := s.RunGit(t.Context(), admin, map[string]any{"op": "pull", "request_id": "https-files-pull"}, func(_ context.Context, _ string) (any, error) { return map[string]any{"ok": true}, nil }); err != nil {
		t.Fatal(err)
	}
	if authenticated.Load() < 3 {
		t.Fatal("Git did not authenticate over HTTPS", authenticated.Load())
	}
	header := base64.StdEncoding.EncodeToString([]byte("kb-user:" + pat))
	filepath.WalkDir(s.private, func(path string, de os.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err == nil && (bytes.Contains(raw, []byte(pat)) || bytes.Contains(raw, []byte(header))) {
			t.Errorf("Git persisted PAT in %s", filepath.Base(path))
		}
		return err
	})
	// Removing the operator override blocks both transport paths before Git can
	// send the saved PAT, even though the destination was previously configured.
	s.cfg.AllowPrivateBackup = false
	before := authenticated.Load()
	if _, err := s.git(t.Context(), nil, nil, "ls-remote", "origin"); err == nil {
		t.Fatal("receipt transport reached a private destination")
	} else if domain, ok := err.(*Error); !ok || domain.Code != "INVALID_ARGUMENT" {
		t.Fatal("transport did not fail at the network boundary", err)
	}
	ctx, err := s.withBackupCredentials(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitWorkspaceBytes(ctx, root, "ls-remote", "origin"); err == nil {
		t.Fatal("Files transport reached a private destination")
	} else if domain, ok := err.(*Error); !ok || domain.Code != "INVALID_ARGUMENT" {
		t.Fatal("Files transport did not fail at the network boundary", err)
	}
	if authenticated.Load() != before {
		t.Fatal("PAT reached the private server after removing the override")
	}
}

package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-746: a returned absolute shell log must match its existing private
// relative grant. Resolving aliases must preserve all read/write exclusions.
func TestFolderGuardCanonicalPathsKeepPrivateAndDeniedFilesBlocked(t *testing.T) {
	base := t.TempDir()
	docs := filepath.Join(base, "docs")
	own := "_users/person/chat_history"
	other := "_users/other/chat_history"
	for _, dir := range []string{own, other, own + "-sibling", own + "/blocked", own + "/readonly"} {
		if err := os.MkdirAll(filepath.Join(docs, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(docs, own, "output.log"), filepath.Join(docs, other, "private.log"), filepath.Join(outside, "output.log")} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{
		"blocked-alias":  filepath.Join(docs, own, "blocked"),
		"readonly-alias": filepath.Join(docs, own, "readonly"),
		"other-alias":    filepath.Join(docs, other),
		"outside-alias":  outside,
		"dangling":       filepath.Join(outside, "missing"),
	} {
		if err := os.Symlink(target, filepath.Join(docs, own, name)); err != nil {
			t.Fatal(err)
		}
	}
	rootAlias := filepath.Join(base, "root-alias")
	if err := os.Symlink(docs, rootAlias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", rootAlias)
	guard := &FolderGuardConfig{Enabled: true,
		ReadPaths:         []string{"chat_history"},
		WritePaths:        []string{filepath.Join(rootAlias, own)},
		BlockedPaths:      []string{own + "/blocked"},
		BlockedWritePaths: []string{filepath.Join(docs, own, "readonly")},
	}
	client := NewClient("http://unused", WithUserID("person"), WithFolderGuard(guard))
	cases := []struct {
		name, path    string
		write, denied bool
		reason        string
	}{
		{"own public read", "chat_history/output.log", false, false, ""},
		{"own physical read", own + "/output.log", false, false, ""},
		{"own absolute read", filepath.Join(docs, own, "output.log"), false, false, ""},
		{"own root alias read", filepath.Join(rootAlias, own, "output.log"), false, false, ""},
		{"new public write", "chat_history/new/attempt.log", true, false, ""},
		{"new absolute write", filepath.Join(docs, own, "new/attempt.log"), true, false, ""},
		{"other relative", other + "/private.log", false, true, "ACCESS DENIED"},
		{"other absolute", filepath.Join(docs, other, "private.log"), false, true, "ACCESS DENIED"},
		{"prefix sibling", own + "-sibling/output.log", false, true, "ACCESS DENIED"},
		{"relative blocked read", "chat_history/blocked/private.log", false, true, "is blocked"},
		{"absolute blocked write", filepath.Join(docs, own, "blocked/private.log"), true, true, "is blocked"},
		{"alias blocked read", "chat_history/blocked-alias/private.log", false, true, "is blocked"},
		{"readonly read", "chat_history/readonly/settings.json", false, false, ""},
		{"relative readonly write", "chat_history/readonly/settings.json", true, true, "blocked for writes"},
		{"absolute readonly write", filepath.Join(rootAlias, own, "readonly/settings.json"), true, true, "blocked for writes"},
		{"alias readonly write", "chat_history/readonly-alias/settings.json", true, true, "blocked for writes"},
		{"other user alias", "chat_history/other-alias/private.log", false, true, "ACCESS DENIED"},
		{"outside alias", "chat_history/outside-alias/output.log", false, true, "outside the configured workspace"},
		{"dangling alias", "chat_history/dangling", true, true, "cannot be resolved"},
		{"dot dot other person", own + "/../../other/chat_history/private.log", false, true, "ACCESS DENIED"},
		{"dot dot outside docs", "../outside/output.log", false, true, "leaves the configured workspace"},
		{"absolute outside", filepath.Join(outside, "output.log"), false, true, "ACCESS DENIED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Both public entry points apply the same canonical comparison.
			for _, check := range []func() error{
				func() error { return client.ValidatePath(tc.path, tc.write) },
				func() error { return client.ValidatePathWithContext(context.Background(), tc.path, tc.write) },
			} {
				err := check()
				if (err != nil) != tc.denied {
					t.Fatalf("denied=%v, got %v", tc.denied, err)
				}
				if err != nil && !strings.Contains(err.Error(), tc.reason) {
					t.Fatalf("want %q, got %v", tc.reason, err)
				}
			}
		})
	}
	// Explicit external grants remain explicit: no workspace-looking suffix is
	// inferred, and a configured external path retains its previous meaning.
	external := NewClient("http://unused", WithFolderGuard(&FolderGuardConfig{Enabled: true, ReadPaths: []string{outside}}))
	if err := external.ValidatePath(filepath.Join(outside, "output.log"), false); err != nil {
		t.Fatal(err)
	}
	for _, empty := range []string{"", "."} {
		client := NewClient("http://unused", WithFolderGuard(&FolderGuardConfig{Enabled: true, ReadPaths: []string{empty}}))
		if err := client.ValidatePath(filepath.Join(docs, other, "private.log"), false); err == nil {
			t.Fatal("empty grant expanded into access to the whole documents root")
		}
	}
	// Stale unrelated grants must not disable working file tools. Invalid allow
	// entries are dropped individually; invalid deny entries are retained.
	stale := cloneFolderGuard(guard)
	stale.ReadPaths = append(stale.ReadPaths, own+"/dangling", own+"/outside-alias")
	stale.BlockedPaths = append(stale.BlockedPaths, own+"/dangling", own+"/outside-alias")
	staleClient := NewClient("http://unused", WithUserID("person"), WithFolderGuard(stale))
	if err := staleClient.ValidatePath("chat_history/output.log", false); err != nil {
		t.Fatalf("stale grant disabled valid access: %v", err)
	}
	_, normalized, err := staleClient.userScopedFolderGuardPath("chat_history/output.log", stale)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized.ReadPaths) != len(guard.ReadPaths) || len(normalized.BlockedPaths) != len(stale.BlockedPaths) {
		t.Fatalf("invalid allows/denies handled incorrectly: %+v", normalized)
	}
	// Trusted system writes can override a write-only deny, never a hard deny
	// expressed using another spelling of the same physical file.
	managed := WithSystemManagedWritePaths(context.Background(), "chat_history")
	if err := client.ValidatePathWithContext(managed, "chat_history/readonly/settings.json", true); err != nil {
		t.Fatal(err)
	}
	if err := client.ValidatePathWithContext(managed, "chat_history/blocked-alias/private.log", true); err == nil || !strings.Contains(err.Error(), "is blocked") {
		t.Fatalf("hard deny bypassed: %v", err)
	}
}

// Measure the filesystem work on the remote build host. This reports latency;
// it does not make machine speed a correctness assertion.
func TestFolderGuardCanonicalComparisonCost(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	guard := &FolderGuardConfig{Enabled: true}
	for i := 0; i < 50; i++ {
		rel := fmt.Sprintf("_users/person/chat_history/area-%d", i)
		if err := os.MkdirAll(filepath.Join(docs, rel), 0755); err != nil {
			t.Fatal(err)
		}
		guard.ReadPaths = append(guard.ReadPaths, rel)
	}
	client := NewClient("http://unused", WithFolderGuard(guard))
	start := time.Now()
	const samples = 100
	for i := 0; i < samples; i++ {
		if err := client.ValidatePathWithContext(context.Background(), "_users/person/chat_history/area-49/result.log", false); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("canonical guard 50 grants: %s per file operation (%d samples)", time.Since(start)/samples, samples)
}

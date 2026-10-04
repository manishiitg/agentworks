package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestStampProductOwnerNeverOverwritesAndPreservesFields(t *testing.T) {
	raw := `{"schema_version":1,"product":"work","id":"c1","title":"T","identity":{"name":"N"}}`
	stamped, changed, err := stampProductOwner(raw, "alice")
	if err != nil || !changed {
		t.Fatalf("stamp = %v %v", changed, err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stamped), &got); err != nil {
		t.Fatal(err)
	}
	if got["owner_id"] != "alice" || got["title"] != "T" || got["id"] != "c1" || got["identity"].(map[string]interface{})["name"] != "N" {
		t.Fatalf("stamped manifest lost fields: %s", stamped)
	}
	// Idempotent and never an overwrite, even with a different owner.
	again, changed, err := stampProductOwner(stamped, "bob")
	if err != nil || changed || again != stamped {
		t.Fatalf("second stamp changed the manifest: %v %v", changed, err)
	}
	// Not a Crew or Code manifest, or no owner: untouched.
	for _, other := range []string{`{"product":"sparkquill","id":"x"}`, `{"id":"x"}`, `not json`} {
		if out, changed, _ := stampProductOwner(other, "alice"); changed || out != other {
			t.Fatalf("stamped a foreign manifest %q", other)
		}
	}
	if _, changed, _ := stampProductOwner(raw, ""); changed {
		t.Fatal("stamped an empty owner")
	}
}

func TestPickProjectOwnerPrefersManifestAndFlagsDisagreement(t *testing.T) {
	cases := []struct {
		manifest, path, want string
		mismatch             bool
	}{
		{"alice", "alice", "alice", false},
		{"alice", "", "alice", false},
		{"", "alice", "alice", false},
		{"", "", "", false},
		{"alice", "bob", "alice", true},
	}
	for _, tc := range cases {
		got, mismatch := pickProjectOwner(tc.manifest, tc.path)
		if got != tc.want || mismatch != tc.mismatch {
			t.Errorf("pickProjectOwner(%q,%q) = %q,%v", tc.manifest, tc.path, got, mismatch)
		}
	}
}

func writeOwnerFixture(t *testing.T, docs, rel, content string) string {
	t.Helper()
	full := filepath.Join(docs, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestMigrateProductOwnersBackfillsFromPathIdempotently(t *testing.T) {
	docs := t.TempDir()
	crewA := writeOwnerFixture(t, docs, "_users/alice/Chats/Work/projects/crew-a/product.json", `{"schema_version":1,"product":"work","id":"crew-a"}`)
	codeA := writeOwnerFixture(t, docs, "_users/alice/Chats/Code/projects/code-a/product.json", `{"schema_version":1,"product":"code","id":"code-a"}`)
	crewB := writeOwnerFixture(t, docs, "_users/bob/Chats/Work/projects/crew-b/product.json", `{"schema_version":1,"product":"work","id":"crew-b","owner_id":"bob"}`)
	odd := writeOwnerFixture(t, docs, "_users/bob/Chats/Work/projects/odd/product.json", `{"schema_version":1,"product":"work","id":"odd","owner_id":"alice"}`)
	other := writeOwnerFixture(t, docs, "_users/bob/Chats/Work/projects/other/product.json", `{"schema_version":1,"product":"video","id":"other"}`)
	oddBefore, _ := os.ReadFile(odd)
	otherBefore, _ := os.ReadFile(other)

	report := migrateProductOwners(docs)
	if report.Scanned != 5 || report.Stamped != 2 || report.Current != 2 || report.Skipped != 1 || report.Mismatched != 1 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	for path, want := range map[string]string{crewA: "alice", codeA: "alice", crewB: "bob"} {
		raw, _ := os.ReadFile(path)
		if owner, _, ok := productManifestOwnerID(string(raw)); !ok || owner != want {
			t.Errorf("%s owner = %q ok=%v, want %q", path, owner, ok, want)
		}
	}
	// A disagreeing owner_id and a foreign manifest are left exactly as they were.
	if after, _ := os.ReadFile(odd); string(after) != string(oddBefore) {
		t.Errorf("a manifest with an existing owner_id was rewritten: %s", after)
	}
	if after, _ := os.ReadFile(other); string(after) != string(otherBefore) {
		t.Errorf("a non-Crew/Code manifest was rewritten: %s", after)
	}
	// Second run changes nothing.
	again := migrateProductOwners(docs)
	if again.Stamped != 0 || again.Current != 4 {
		t.Fatalf("second run = %+v", again)
	}
}

// The manifest owner and the path owner agree for every physical project root a test fixture
// in this package spells out, so the backfill (path -> owner_id) is the identity everywhere.
func TestProductOwnerFromPathAgreesWithBackfillForEveryFixtureRoot(t *testing.T) {
	roots := regexp.MustCompile(`"(_users/([A-Za-z0-9_-]+)/Chats/(?:Work|Code)/projects/[A-Za-z0-9_-]+)/product\.json"`)
	files, _ := filepath.Glob("*_test.go")
	seen := map[string]string{}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range roots.FindAllStringSubmatch(string(src), -1) {
			seen[m[1]] = m[2]
		}
	}
	if len(seen) < 5 {
		t.Fatalf("found only %d fixture roots; the scan is broken", len(seen))
	}
	for root, segment := range seen {
		pathOwner := productOwnerFromPath(root)
		if pathOwner != sanitizeUserIDForPath(segment) {
			t.Errorf("fixture %s: path owner %q, segment %q", root, pathOwner, segment)
			continue
		}
		stamped, changed, err := stampProductOwner(`{"product":"work","id":"x"}`, pathOwner)
		if err != nil || !changed {
			t.Errorf("fixture %s: stamp %v %v", root, changed, err)
			continue
		}
		manifestOwner, _, _ := productManifestOwnerID(stamped)
		if got, mismatch := pickProjectOwner(manifestOwner, pathOwner); got != pathOwner || mismatch || strings.TrimSpace(got) == "" {
			t.Errorf("fixture %s: manifest %q vs path %q", root, manifestOwner, pathOwner)
		}
	}
}

func TestResolveCrewPathPrefersManifestOwnerForLegacyRoots(t *testing.T) {
	prevRead := crewOwners.read
	crewOwners.mu.Lock()
	prevEntries := crewOwners.entries
	crewOwners.entries = map[string]crewOwnerEntry{}
	crewOwners.mu.Unlock()
	t.Cleanup(func() {
		crewOwners.mu.Lock()
		crewOwners.read, crewOwners.entries = prevRead, prevEntries
		crewOwners.mu.Unlock()
	})
	const root = "_users/alice/Chats/Work/projects/crew-a"
	for _, tc := range []struct{ manifest, want string }{
		{"", "alice"},      // no owner_id yet: the path rules (and nothing breaks)
		{"alice", "alice"}, // agree
		{"carol", "carol"}, // disagree: the manifest wins, logged
	} {
		crewOwners.mu.Lock()
		crewOwners.entries = map[string]crewOwnerEntry{}
		crewOwners.read = func(_ context.Context, _ string) string { return tc.manifest }
		crewOwners.mu.Unlock()
		ref, ok := resolveCrewPath(context.Background(), "alice", root+"/code")
		if !ok || ref.OwnerID != tc.want || ref.Root != root {
			t.Errorf("manifest %q: ref = %+v ok=%v, want owner %q", tc.manifest, ref, ok, tc.want)
		}
	}
}

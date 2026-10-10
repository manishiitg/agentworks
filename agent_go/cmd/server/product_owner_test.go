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

// withProjectOwnerRegistry points the server's state area at a temp folder, so the test has its own owner registry
// (a test binary never touches the real one) and returns it.
func withProjectOwnerRegistry(t *testing.T) *projectOwnerRegistry {
	t.Helper()
	root := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", root)
	return projectOwnersAt(root)
}

// ensureProjectStateRoot gives a test that creates Crews a state area for the owner registry (new Crews are always
// registered at creation) without replacing one a test already set up with withProjectOwnerRegistry.
func ensureProjectStateRoot(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("AGENTWORKS_STATE_ROOT")) == "" {
		t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	}
}

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

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestMigrateProductOwnersRegistersFromThePathAndStampsInformationOnly(t *testing.T) {
	registry := withProjectOwnerRegistry(t)
	docs := t.TempDir()
	crewA := writeOwnerFixture(t, docs, "_users/alice/Chats/Work/projects/crew-a/product.json", `{"schema_version":1,"product":"work","id":"crew-a"}`)
	codeA := writeOwnerFixture(t, docs, "_users/alice/Chats/Code/projects/code-a/product.json", `{"schema_version":1,"product":"code","id":"code-a"}`)
	crewB := writeOwnerFixture(t, docs, "_users/bob/Chats/Work/projects/crew-b/product.json", `{"schema_version":1,"product":"work","id":"crew-b","owner_id":"bob"}`)
	// A manifest that CLAIMS another owner: information only, ignored and never rewritten.
	odd := writeOwnerFixture(t, docs, "_users/bob/Chats/Work/projects/odd/product.json", `{"schema_version":1,"product":"work","id":"odd","owner_id":"alice"}`)
	other := writeOwnerFixture(t, docs, "_users/bob/Chats/Work/projects/other/product.json", `{"schema_version":1,"product":"video","id":"other"}`)
	oddBefore, otherBefore := mustRead(t, odd), mustRead(t, other)

	report := migrateProductOwners(docs)
	if report.Scanned != 5 || report.Registered != 4 || report.Stamped != 2 || report.Current != 2 || report.Skipped != 1 || report.Mismatched != 1 || report.Unsafe != 0 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	for _, want := range []struct{ product, folder, owner string }{
		{"work", "crew-a", "alice"}, {"code", "code-a", "alice"}, {"work", "crew-b", "bob"},
		// The folder's owner is the PATH's (bob's tree), not what its manifest says.
		{"work", "odd", "bob"},
	} {
		rec, ok := registry.Lookup(want.product, want.folder)
		if !ok || rec.OwnerID != want.owner {
			t.Errorf("%s/%s registered to %q ok=%v, want %q", want.product, want.folder, rec.OwnerID, ok, want.owner)
		}
	}
	if _, ok := registry.Lookup("video", "other"); ok {
		t.Error("a non-Crew/Code manifest was registered")
	}
	for path, want := range map[string]string{crewA: "alice", codeA: "alice", crewB: "bob"} {
		if owner, _, ok := productManifestOwnerID(mustRead(t, path)); !ok || owner != want {
			t.Errorf("%s informational owner = %q ok=%v, want %q", path, owner, ok, want)
		}
	}
	if mustRead(t, odd) != oddBefore || mustRead(t, other) != otherBefore {
		t.Error("a manifest with an owner_id or a foreign manifest was rewritten")
	}
	if again := migrateProductOwners(docs); again.Stamped != 0 || again.Registered != 0 || again.Registry != 4 {
		t.Fatalf("second run = %+v", again)
	}
	// No temp file is left behind in a project folder.
	if entries, _ := filepath.Glob(filepath.Join(docs, "_users", "*", "Chats", "*", "projects", "*", ".product.json.*")); len(entries) != 0 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

// PLAT-449: the manifest is user-writable, so editing its owner_id never changes who owns the project.
func TestResolveProjectOwnerIgnoresTheManifest(t *testing.T) {
	registry := withProjectOwnerRegistry(t)
	docs := t.TempDir()
	manifest := writeOwnerFixture(t, docs, "_users/alice/Chats/Work/projects/crew-a/product.json", `{"schema_version":1,"product":"work","id":"crew-a"}`)
	migrateProductOwners(docs)
	const root = "_users/alice/Chats/Work/projects/crew-a"
	ctx := context.Background()
	if got := resolveProjectOwner(ctx, root); got != "alice" {
		t.Fatalf("owner = %q", got)
	}
	// The owner (or anyone with write access to the project) rewrites owner_id to bob.
	if err := os.WriteFile(manifest, []byte(`{"schema_version":1,"product":"work","id":"crew-a","owner_id":"bob"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveProjectOwner(ctx, root); got != "alice" {
		t.Fatalf("an edited manifest changed the owner to %q", got)
	}
	// An unregistered project: the path's owner; the manifest still does not count.
	if got := resolveProjectOwner(ctx, "_users/carol/Chats/Work/projects/unregistered"); got != "carol" {
		t.Fatalf("unregistered project owner = %q", got)
	}
	// A project at the shared root with no registry entry belongs to nobody, whatever its manifest says.
	if got := resolveProjectOwner(ctx, "Crew/ghost-1234"); got != "" {
		t.Fatalf("an unregistered shared Crew has owner %q", got)
	}
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "moved-5678", OwnerID: "dave", Shared: true}); err != nil {
		t.Fatal(err)
	}
	if got := resolveProjectOwner(ctx, "Crew/moved-5678"); got != "dave" {
		t.Fatalf("registered shared Crew owner = %q", got)
	}
	// A copy of a registered project in someone else's tree resolves to the REGISTERED owner (and logs).
	if got := resolveProjectOwner(ctx, "_users/mallory/Chats/Work/projects/crew-a"); got != "alice" {
		t.Fatalf("a copied folder resolved to %q", got)
	}
	// A path that is not a project has no owner.
	if got := resolveProjectOwner(ctx, "_users/alice/Chats/notes"); got != "" {
		t.Fatalf("non-project owner = %q", got)
	}
}

// PLAT-450: the backfill never writes through a symlink and never leaves the owner's real directories.
func TestMigrateProductOwnersNeverFollowsSymlinks(t *testing.T) {
	registry := withProjectOwnerRegistry(t)
	docs := t.TempDir()
	// The victim's manifest lacks owner_id (as during the first backfill).
	victim := writeOwnerFixture(t, docs, "_users/zvictim/Chats/Work/projects/victim/product.json", `{"schema_version":1,"product":"work","id":"victim"}`)
	// 1. a symlinked manifest inside the attacker's own project
	bait := filepath.Join(docs, "_users", "aattacker", "Chats", "Work", "projects", "bait")
	if err := os.MkdirAll(bait, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(bait, "product.json")); err != nil {
		t.Fatal(err)
	}
	// 2. a symlinked project folder
	projects := filepath.Join(docs, "_users", "aattacker", "Chats", "Work", "projects")
	if err := os.Symlink(filepath.Dir(victim), filepath.Join(projects, "linked-project")); err != nil {
		t.Fatal(err)
	}
	// 3. a symlinked "Chats" folder in another attacker account
	if err := os.MkdirAll(filepath.Join(docs, "_users", "battacker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(docs, "_users", "zvictim", "Chats"), filepath.Join(docs, "_users", "battacker", "Chats")); err != nil {
		t.Fatal(err)
	}
	// 4. a symlinked user directory
	if err := os.Symlink(filepath.Join(docs, "_users", "zvictim"), filepath.Join(docs, "_users", "cattacker")); err != nil {
		t.Fatal(err)
	}
	// 5. a manifest that is not a regular file
	dirManifest := filepath.Join(docs, "_users", "dattacker", "Chats", "Work", "projects", "weird", "product.json")
	if err := os.MkdirAll(dirManifest, 0o755); err != nil {
		t.Fatal(err)
	}

	report := migrateProductOwners(docs)
	// The victim's own scan stamps its manifest with the victim's id; no scan of a linked path may stamp anyone
	// else's id into it (the PLAT-450 attack: owner_id became "aattacker").
	if owner, _, _ := productManifestOwnerID(mustRead(t, victim)); owner != "zvictim" {
		t.Fatalf("the victim's manifest was changed through a symlink (owner %q): %s", owner, mustRead(t, victim))
	}
	if report.Unsafe < 4 {
		t.Errorf("unsafe paths not reported: %+v", report)
	}
	// The victim's own project is still registered to the victim, by its own scan, and nothing else got registered.
	if rec, ok := registry.Lookup("work", "victim"); !ok || rec.OwnerID != "zvictim" {
		t.Fatalf("victim registry entry = %+v ok=%v", rec, ok)
	}
	all, _ := registry.All()
	for key, rec := range all {
		if rec.OwnerID != "zvictim" {
			t.Errorf("%s was registered to %q through a link", key, rec.OwnerID)
		}
	}
	if owner, _, _ := productManifestOwnerID(mustRead(t, victim)); owner != "zvictim" {
		t.Errorf("victim informational owner = %q", owner)
	}
}

// The lazy writer (the owner opening a project) is just as safe.
func TestEnsureProjectOwnerIDNeverWritesThroughASymlink(t *testing.T) {
	registry := withProjectOwnerRegistry(t)
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	victim := writeOwnerFixture(t, docs, "_users/zvictim/Chats/Work/projects/victim/product.json", `{"schema_version":1,"product":"work","id":"victim"}`)
	victimBefore := mustRead(t, victim)
	bait := filepath.Join(docs, "_users", "aattacker", "Chats", "Work", "projects", "bait")
	if err := os.MkdirAll(bait, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(bait, "product.json")); err != nil {
		t.Fatal(err)
	}
	ensureProjectOwnerID(context.Background(), "Chats/Work/projects/bait", "aattacker")
	if mustRead(t, victim) != victimBefore {
		t.Fatalf("the victim's manifest was changed through a symlink: %s", mustRead(t, victim))
	}
	// A real project is registered and stamped.
	real := writeOwnerFixture(t, docs, "_users/alice/Chats/Work/projects/real/product.json", `{"schema_version":1,"product":"work","id":"real"}`)
	ensureProjectOwnerID(context.Background(), "Chats/Work/projects/real", "alice")
	if rec, ok := registry.Lookup("work", "real"); !ok || rec.OwnerID != "alice" {
		t.Fatalf("not registered: %+v %v", rec, ok)
	}
	if owner, _, _ := productManifestOwnerID(mustRead(t, real)); owner != "alice" {
		t.Fatalf("not stamped: %s", mustRead(t, real))
	}
	// Opening someone else's registered folder from your own tree registers nothing and changes nothing.
	copyOfReal := writeOwnerFixture(t, docs, "_users/mallory/Chats/Work/projects/real/product.json", `{"schema_version":1,"product":"work","id":"real"}`)
	copyBefore := mustRead(t, copyOfReal)
	ensureProjectOwnerID(context.Background(), "Chats/Work/projects/real", "mallory")
	if rec, _ := registry.Lookup("work", "real"); rec.OwnerID != "alice" {
		t.Fatalf("the copy took over the registration: %+v", rec)
	}
	if mustRead(t, copyOfReal) != copyBefore {
		t.Error("the copy's manifest was stamped")
	}
}

// The manifest owner, the path owner and the registered owner agree for every physical project root a test fixture
// in this package spells out.
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
		if got := resolveProjectOwner(context.Background(), root); got != pathOwner || strings.TrimSpace(got) == "" {
			t.Errorf("fixture %s: resolved owner %q vs path %q", root, got, pathOwner)
		}
	}
}

// resolveCrewPath uses the server's owner, not the manifest, for legacy roots.
func TestResolveCrewPathUsesTheRegistryThenThePathNeverTheManifest(t *testing.T) {
	registry := withProjectOwnerRegistry(t)
	const root = "_users/alice/Chats/Work/projects/crew-a"
	ref, ok := resolveCrewPath(context.Background(), "alice", root+"/code")
	if !ok || ref.OwnerID != "alice" || ref.Root != root {
		t.Fatalf("unregistered: %+v ok=%v", ref, ok)
	}
	// A project registered to carol that sits in alice's tree (a copy): the registry wins.
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "crew-a", OwnerID: "carol"}); err != nil {
		t.Fatal(err)
	}
	ref, ok = resolveCrewPath(context.Background(), "alice", root+"/code")
	if !ok || ref.OwnerID != "carol" {
		t.Fatalf("registered: %+v ok=%v", ref, ok)
	}
}

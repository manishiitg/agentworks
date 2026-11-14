package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	for _, other := range []string{`{"product":"example-app","id":"x"}`, `{"id":"x"}`, `not json`} {
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

// PLAT-449: editable owner_id is information; the server registry decides.
func TestResolveProjectOwnerIgnoresTheManifest(t *testing.T) {
	withProjectOwnerRegistry(t)
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	root := registerTestCrew(t, "crew-a", "alice", false, "crew-a")
	manifest := writeOwnerFixture(t, docs, root+"/product.json", `{"schema_version":1,"product":"work","id":"crew-a","owner_id":"alice"}`)
	ctx := context.Background()
	if got := resolveProjectOwner(ctx, root); got != "alice" {
		t.Fatalf("owner = %q", got)
	}
	if err := os.WriteFile(manifest, []byte(`{"schema_version":1,"product":"work","id":"crew-a","owner_id":"bob"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveProjectOwner(ctx, root); got != "alice" {
		t.Fatalf("an edited manifest changed the owner to %q", got)
	}
	writeOwnerFixture(t, docs, "Crew/ghost/product.json", `{"product":"work","id":"ghost","owner_id":"carol"}`)
	if got := resolveProjectOwner(ctx, "Crew/ghost"); got != "" {
		t.Fatalf("an unregistered Crew acquired owner %q", got)
	}
	for _, retired := range []string{"_users/alice/Chats/Work/projects/crew-a", "_users/mallory/Chats/Work/projects/crew-a", "_users/alice/Chats/notes"} {
		if got := resolveProjectOwner(ctx, retired); got != "" {
			t.Fatalf("a retired or unrelated path acquired owner %q", got)
		}
	}
}

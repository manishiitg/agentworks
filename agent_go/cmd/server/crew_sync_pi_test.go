package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

// The agent can write inside its runtime folder, so a link planted where Pi's session folder goes must not
// redirect an application-side import to another place.
func TestCrewPiStoreRefusesLinkedRuntimeFolder(t *testing.T) {
	runtime, elsewhere := t.TempDir(), t.TempDir()
	const id = "mlp-pi-0123abcd"
	store, err := crewPiStore(runtime, id, true)
	physical, _ := filepath.EvalSymlinks(runtime)
	if err != nil || store != picli.SessionStoreDir(physical, id) {
		t.Fatalf("store %q: %v", store, err)
	}
	other := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(other, ".pi")); err != nil {
		t.Fatal(err)
	}
	if _, err := crewPiStore(other, id, true); err == nil {
		t.Fatal("followed a link out of the runtime folder")
	}
}

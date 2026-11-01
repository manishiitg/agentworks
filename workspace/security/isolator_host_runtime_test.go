package security

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// PLAT-746-265: the Mac Relay interpreter could not resolve its granted host
// path. Ancestor metadata must allow resolution without exposing sibling data.
func TestStrictHostReadGrantResolvesWithoutExposingAncestors(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox-exec regression")
	}
	work := canonicalPath(t.TempDir())
	parent := filepath.Join(canonicalPath(t.TempDir()), "private", "runtimes")
	grant := filepath.Join(parent, "python")
	if err := os.MkdirAll(grant, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{filepath.Join(grant, "known.txt"): "granted", filepath.Join(parent, "private.txt"): "private"} {
		if err := os.WriteFile(name, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	iso := &Isolator{BaseDir: work, WorkDir: work, ReadPaths: []string{grant}, BlockedWritePaths: []string{grant}, StrictAllowlist: true, PrivateScratch: true}
	script := `import os, sys
from pathlib import Path
grant, parent = map(Path, sys.argv[1:])
assert os.path.realpath(grant, strict=True) == str(grant)
assert (grant / "known.txt").read_text() == "granted"
for attempt in [lambda: list(parent.iterdir()), lambda: (parent / "private.txt").read_text(), lambda: (grant / "known.txt").write_text("changed")]:
    try:
        attempt()
    except PermissionError:
        continue
    raise AssertionError("access escaped the read-only grant")
print("READ_ONLY_OK")`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd, cleanup, err := iso.ExecuteIsolated(ctx, "python3 -I -c '"+script+"' "+fmt.Sprintf("%q %q", grant, parent), nil)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "READ_ONLY_OK") {
		t.Fatalf("runtime grant failed: %s (%v)", output, err)
	}
}

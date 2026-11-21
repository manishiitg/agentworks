package noderuntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A private Node is installed only when the download matches the release's
// published SHA-256; a matching one lands first on PATH with its npm/npx links.
func TestInstallChecksPublishedChecksumAndActivates(t *testing.T) {
	plat, err := platform()
	if err != nil {
		t.Skip(err)
	}
	name := "node-v1.2.3-" + plat
	archive := tarball(t, name)
	for _, tc := range []struct {
		name     string
		checksum string
		wantErr  bool
	}{
		{"matching checksum installs", sha(archive), false},
		{"tampered download is refused", sha([]byte("other")), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/index.json":
					_, _ = w.Write([]byte(`[{"version":"v1.3.0","lts":false},{"version":"v1.2.3","lts":"Test"}]`))
				case "/v1.2.3/SHASUMS256.txt":
					_, _ = w.Write([]byte(tc.checksum + "  " + name + ".tar.gz\n"))
				case "/v1.2.3/" + name + ".tar.gz":
					_, _ = w.Write(archive)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			old := distBase
			distBase = srv.URL
			defer func() { distBase = old }()
			t.Setenv("PATH", t.TempDir())

			rt := New(t.TempDir())
			err := rt.install(context.Background())
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "checksum") {
					t.Fatalf("tampered download installed: %v", err)
				}
				if rt.Activate() {
					t.Fatal("a refused download left a usable Node behind")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !rt.Activate() || !strings.HasPrefix(os.Getenv("PATH"), rt.binDir()) {
				t.Fatalf("private Node not first on PATH: %s", os.Getenv("PATH"))
			}
			if target, err := os.Readlink(filepath.Join(rt.binDir(), "npx")); err != nil || target != "../lib/npx-cli.js" {
				t.Fatalf("npx link = %q, %v", target, err)
			}
		})
	}
}

func tarball(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add(&tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: name + "/bin/node", Typeflag: tar.TypeReg, Mode: 0o755}, "#!/bin/sh\n")
	add(&tar.Header{Name: name + "/bin/npm", Typeflag: tar.TypeReg, Mode: 0o755}, "#!/bin/sh\n")
	add(&tar.Header{Name: name + "/lib/npx-cli.js", Typeflag: tar.TypeReg, Mode: 0o644}, "")
	add(&tar.Header{Name: name + "/bin/npx", Typeflag: tar.TypeSymlink, Linkname: "../lib/npx-cli.js"}, "")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

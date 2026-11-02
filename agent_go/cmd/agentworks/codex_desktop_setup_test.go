package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func desktopBundleFixture(t *testing.T, extra string, mode os.FileMode) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, dir := range []string{"codex", "codex-tools"} {
		root := filepath.Join("..", "..", "..", "plugins", dir)
		err := filepath.WalkDir(root, func(file string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(root, file)
			entry, e := w.Create("bundle/plugins/" + dir + "/" + filepath.ToSlash(rel))
			if e != nil {
				return e
			}
			raw, e := os.ReadFile(file)
			if e == nil {
				_, e = entry.Write(raw)
			}
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if extra != "" {
		h := &zip.FileHeader{Name: extra}
		h.SetMode(mode)
		entry, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte("outside"))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// PLAT-856-496: a desktop-only install needs no command line and must preserve
// the person's disabled plugin, hook trust, other MCP servers and project data.
func TestCodexDesktopBundleWithoutCLIPreservesSettings(t *testing.T) {
	profile := t.TempDir()
	personHome := t.TempDir()
	t.Setenv("HOME", personHome)
	t.Setenv("USERPROFILE", personHome)
	t.Setenv("APPDATA", personHome)
	t.Setenv("LOCALAPPDATA", personHome)
	t.Setenv("CODEX_HOME", profile)
	t.Setenv("PATH", "")
	prior := `model = "example-model"
[plugins."agentworks-codex@agentworks"]
enabled = false
[plugins."another@market"]
enabled = true
[mcp_servers.other]
url = "https://example.com/mcp"
[projects."/example/project"]
trust_level = "trusted"
[hooks]
trusted = ["existing-provider-trust"]
`
	if err := os.WriteFile(filepath.Join(profile, "config.toml"), []byte(prior), 0o600); err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	if err := toml.Unmarshal([]byte(prior), &original); err != nil {
		t.Fatal(err)
	}
	bundle, digest := desktopBundleFixture(t, "", 0)
	o := &options{stdout: io.Discard, stderr: io.Discard}
	for _, name := range []string{"agentworks-codex", "agentworks-codex-tools"} {
		if err := installCodexDesktopBundle(o, profile, name, bundle, digest); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := readCodexSettings(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !codexManagedMarketplace(filepath.Dir(profile)) {
		t.Fatal("desktop-only setup must record its managed marketplace for explicit updates")
	}
	for key, want := range original {
		if key == "plugins" {
			for name, plugin := range want.(map[string]any) {
				if !reflect.DeepEqual(doc[key].(map[string]any)[name], plugin) {
					t.Fatalf("changed existing plugin %s", name)
				}
			}
		} else if !reflect.DeepEqual(doc[key], want) {
			t.Fatalf("changed existing %s settings", key)
		}
	}
	for _, name := range []string{"agentworks-codex", "agentworks-codex-tools"} {
		matches, _ := filepath.Glob(filepath.Join(profile, "plugins", "cache", "agentworks", name, "*", ".mcp.json"))
		if len(matches) != 1 {
			t.Fatalf("missing native plugin cache for %s", name)
		}
		var mcp struct {
			MCPServers map[string]struct{ Command string }
		}
		raw, _ := os.ReadFile(matches[0])
		if json.Unmarshal(raw, &mcp) != nil || mcp.MCPServers["agentworks"].Command != "agentworks" {
			t.Fatal("plugin must use the existing AgentWorks transport, with no credential copied")
		}
	}
	backups, _ := filepath.Glob(filepath.Join(profile, "config.toml.agentworks-backup-*"))
	if len(backups) == 0 {
		t.Fatal("the original settings must be backed up")
	}
	got, _ := os.ReadFile(backups[0])
	if string(got) != prior {
		t.Fatal("the first backup must retain the original settings text")
	}
	// The existing automatic CLI updater calls the shared plugin updater too.
	// This new managed source must refresh only on a person's setup/update.
	requests := 0
	transport := http.DefaultTransport
	defer func() { http.DefaultTransport = transport }()
	http.DefaultTransport = desktopSetupTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("unexpected background plugin download")
	})
	updateInstalledPlugins(context.Background(), o, false)
	if requests != 0 {
		t.Fatal("background update tried to refresh the managed plugin")
	}
	o.setupAll = true
	updateInstalledPlugins(context.Background(), o, false)
	if requests == 0 {
		t.Fatal("a person's explicit update did not refresh the installed managed plugin")
	}
	http.DefaultTransport = transport
	// Invalid config is not silently replaced, including invalid section types.
	for _, invalid := range []string{"[broken", "plugins = false\n", "[plugins]\nother = false\n"} {
		_ = os.WriteFile(filepath.Join(profile, "config.toml"), []byte(invalid), 0o600)
		if _, err = editCodexSettings(profile, o, func(doc map[string]any) error { doc["model"] = "changed"; return nil }); err == nil {
			t.Fatal("malformed settings must be left alone")
		}
		got, _ = os.ReadFile(filepath.Join(profile, "config.toml"))
		if string(got) != invalid {
			t.Fatal("invalid settings changed")
		}
	}
}

type desktopSetupTransport func(*http.Request) (*http.Response, error)

func (f desktopSetupTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Only a verified public package may write under the managed plugin tree.
func TestCodexDesktopBundleRejectsUnverifiedAndEscapingFiles(t *testing.T) {
	for _, tc := range []struct {
		name, entry string
		mode        os.FileMode
		badDigest   bool
	}{
		{name: "checksum", badDigest: true},
		{name: "absolute", entry: "/outside", mode: 0o600},
		{name: "parent", entry: "bundle/plugins/codex/../../outside", mode: 0o600},
		{name: "windows", entry: `C:\outside`, mode: 0o600},
		{name: "symlink", entry: "bundle/plugins/codex/link", mode: os.ModeSymlink | 0o777},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := t.TempDir()
			bundle, digest := desktopBundleFixture(t, tc.entry, tc.mode)
			if tc.badDigest {
				digest = strings.Repeat("0", 64)
			}
			if err := installCodexDesktopBundle(&options{stdout: io.Discard}, profile, "agentworks-codex", bundle, digest); err == nil {
				t.Fatal("unsafe or unverified archive accepted")
			}
			if _, err := os.Stat(filepath.Join(profile, "config.toml")); !os.IsNotExist(err) {
				t.Fatal("refused bundle must not install or enable a plugin")
			}
		})
	}
}

// The owner's error ended at the colon because CombinedOutput was empty on an
// exec start failure. Preserve the real OS error, even without provider output.
func TestPluginSetupRetainsEmptyOutputStartError(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), filepath.Join(t.TempDir(), "missing-provider"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("fixture did not fail to start")
	}
	got := pluginSetupError("codex", err, out)
	if !strings.Contains(got.Error(), err.Error()) {
		t.Fatalf("lost start failure: %v", got)
	}
	got = pluginSetupError("claude", err, out)
	if !strings.Contains(got.Error(), "Code tab") || !strings.Contains(got.Error(), err.Error()) {
		t.Fatalf("missing app-only recovery: %v", got)
	}
}

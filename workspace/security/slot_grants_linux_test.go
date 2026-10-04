//go:build linux

package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

// The Landlock policy of a slot command in a Crew project: the project is granted, the project's browser profile and
// socket folder are not (PLAT-478), and the policy is browser scoped, so the launcher adds no shared browser folder.
func TestSlotLandlockPolicyHasNoBrowserFolders(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	project := filepath.Join(docs, "_users", "u1", "Chats", "Work", "projects", "p1")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(browserconfig.ProfileEnv, filepath.Join(root, "state", "browser-profile"))
	session := "project-3bdc30503fa28a4f--browser"
	profile := browserconfig.ProfilePathForSession(session)
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, slot := range []string{"slot01", ""} {
		iso := &Isolator{Slot: slot, BaseDir: docs, WorkDir: project, ReadPaths: []string{project}, WritePaths: []string{project}, BrowserSession: session}
		iso.scopeBrowser()
		policy, err := iso.landlockPolicy()
		if err != nil {
			t.Fatal(err)
		}
		all := strings.Join(append(append([]string{}, policy.ReadPaths...), policy.WritePaths...), "\n")
		if !strings.Contains(all, project) {
			t.Fatalf("slot=%q: the project is not granted: %v", slot, all)
		}
		hasProfile := strings.Contains(all, profile)
		if slot != "" && (hasProfile || strings.Contains(all, browserconfig.SocketRoot)) {
			t.Fatalf("a slot command was granted a browser folder:\n%s", all)
		}
		if slot == "" && !hasProfile {
			t.Fatalf("a service-account command lost its own browser profile:\n%s", all)
		}
		if !policy.BrowserScoped {
			t.Fatalf("slot=%q: the policy must be browser scoped", slot)
		}
		for _, path := range landlockSystemWritePaths(false, policy.BrowserScoped) {
			if strings.HasPrefix(path, filepath.Join(root, "state")) {
				t.Fatalf("slot=%q: the launcher would add the shared browser folder %s", slot, path)
			}
		}
	}
	// A slot command that names no browser still gets no shared browser folder.
	iso := &Isolator{Slot: "slot01", BaseDir: docs, WorkDir: project, ReadPaths: []string{project}, WritePaths: []string{project}}
	policy, err := iso.landlockPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if !policy.BrowserScoped {
		t.Fatal("a slot command without a browser session must still be browser scoped (no shared profile roots)")
	}
}

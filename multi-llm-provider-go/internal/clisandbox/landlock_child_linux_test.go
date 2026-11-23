//go:build linux

package clisandbox

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-872-51: a confined node cannot give the Claude it spawns its own /proc
// entry, so the host stays outside Landlock and every CLI it starts must get
// the launcher and the full policy; nothing is left for an unwrapped spawn.
func TestLandlockChildConfinesTheCLINotTheHost(t *testing.T) {
	dir := t.TempDir()
	runner := filepath.Join(dir, "runner")
	if err := os.WriteFile(runner, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "claude-code", LandlockRunner: runner, PrivateHome: filepath.Join(dir, "home"), CredentialHome: dir}
	host := exec.Command("/usr/bin/node", "bridge.cjs", "sdk.mjs", "/bin/sh", "-p")
	cleanup, err := LandlockChild(policy, host, host.Args[3:], dir, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if host.Path != "/usr/bin/node" || host.Args[0] != "/usr/bin/node" {
		t.Fatalf("host was wrapped: %q %q", host.Path, host.Args)
	}
	var launch ChildLaunch
	for _, entry := range host.Env {
		if value, ok := strings.CutPrefix(entry, ChildLaunchEnv+"="); ok && json.Unmarshal([]byte(value), &launch) != nil {
			t.Fatalf("bad launch %s", value)
		}
	}
	var got landlockPolicy
	if launch.Runner != runner || len(launch.EnvArgs) == 0 || launch.EnvArgs[0] != "/usr/bin/env" || json.Unmarshal(launch.Policy, &got) != nil || got.WorkDir == "" || len(got.WritePaths) == 0 {
		t.Fatalf("launch = %+v", launch)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "home", "agentworks-cli-landlock-*.json")); len(files) != 0 {
		t.Fatalf("policy file left behind: %v", files)
	}
	// PLAT-856-383: repository-local config cannot re-enable automatic code
	// execution when a private non-slot CLI trusts a mixed-UID project.
	repo := filepath.Join(dir, "repo")
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "config", "core.hooksPath", "/untrusted-hooks"}, {"-C", repo, "config", "core.fsmonitor", "/untrusted-monitor"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, out)
		}
	}
	for key, want := range map[string]string{"core.hooksPath": "/dev/null", "core.fsmonitor": "false"} {
		args := append(append([]string{}, launch.EnvArgs[1:]...), "git", "-C", repo, "config", "--get", key)
		cmd := exec.Command(launch.EnvArgs[0], args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != want {
			t.Fatalf("repository overrode %s: %v %s", key, err, out)
		}
	}
}

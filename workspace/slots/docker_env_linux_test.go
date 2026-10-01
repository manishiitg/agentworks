//go:build linux

package slots

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithSlotDockerPointsTheCommandAtItsOwnSocket(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "slotctl.json")
	t.Setenv(EnvConfig, cfg)
	prior := lookupSlotUID
	lookupSlotUID = func(string) (string, error) { return "1042", nil }
	t.Cleanup(func() { lookupSlotUID = prior })
	env := []string{"PATH=/usr/bin", "DOCKER_HOST=unix:///run/user/990/docker.sock", "HOME=/tmp"}

	if err := os.WriteFile(cfg, []byte(`{"slot_docker":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := WithSlotDocker(append([]string(nil), env...), "slot05")
	var hosts []string
	for _, e := range got {
		if strings.HasPrefix(e, "DOCKER_HOST=") {
			hosts = append(hosts, e)
		}
	}
	if len(hosts) != 1 || hosts[0] != "DOCKER_HOST=unix:///run/user/1042/docker.sock" {
		t.Fatalf("DOCKER_HOST = %v: the platform's socket must be replaced by the slot's own", hosts)
	}
	if len(got) != len(env) {
		t.Fatalf("other entries must be kept: %v", got)
	}

	// a host that does not give slots Docker leaves the environment alone
	if err := os.WriteFile(cfg, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := WithSlotDocker(append([]string(nil), env...), "slot05"); strings.Join(got, "|") != strings.Join(env, "|") {
		t.Fatalf("without slot_docker the environment must not change: %v", got)
	}
}

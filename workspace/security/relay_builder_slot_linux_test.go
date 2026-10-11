//go:build linux

package security

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// Opt-in: exercises the production launcher and slot wrapper on a configured
// Linux host. Everything written belongs to a throwaway folder in the slot's
// existing run area; no live workflow, Google connection or service is changed.
func TestSlottedRelayBuilderCreatesPlanWithoutHostGogStore(t *testing.T) {
	slot := os.Getenv("AGENTWORKS_RELAY_TEST_SLOT")
	if slot == "" {
		t.Skip("set AGENTWORKS_RELAY_TEST_SLOT and AGENTWORKS_LANDLOCK_RUNNER on a configured Linux host")
	}
	runRoot, err := slots.RunDirFor(slot)
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.MkdirTemp(runRoot, "relay-builder-check-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	if err := os.Chmod(base, os.ModeSetgid|0770); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(base, "Workflow", "test")
	if err := os.MkdirAll(project, 0770); err != nil {
		t.Fatal(err)
	}
	// The user slot can see this fixture's mode bits, but Landlock must still
	// refuse it because it is outside the granted Relay workspace.
	hostStore := filepath.Join(base, "host-google-credential")
	if err := os.WriteFile(hostStore, []byte("FIXTURE-HOST-CREDENTIAL"), 0644); err != nil {
		t.Fatal(err)
	}
	privateGog := filepath.Join(base, "private-host-home", "gog")
	if err := os.MkdirAll(privateGog, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOG_HOME", privateGog)
	t.Setenv("GOG_KEYRING_PASSWORD", "fixture-host-keyring")
	t.Setenv("GOG_KEYRING_BACKEND", "file")
	t.Setenv("AGENTWORKS_GOG_TERMINAL_ACCESS", "")
	iso := &Isolator{Slot: slot, BaseDir: project, WorkDir: project, ReadPaths: []string{project}, WritePaths: []string{project},
		ExtraEnv: map[string]string{"VAR_INPUT": "invoice-test", "MCP_API_TOKEN": "fixture-session-token"}}
	script := fmt.Sprintf(`set -eu
pwd
id -un
test -z "${GOG_HOME:-}"
test -z "${GOG_KEYRING_PASSWORD:-}"
test -z "${GOG_KEYRING_BACKEND:-}"
test "$VAR_INPUT" = invoice-test
test "$MCP_API_TOKEN" = fixture-session-token
if cat %q >/dev/null 2>&1; then echo HOST_STORE_VISIBLE; exit 1; fi
mkdir -p planning
printf '%%s' '{"kind":"relay"}' > workflow.json
printf '%%s' '{"steps":[{"id":"invoice","type":"agent"}]}' > planning/plan.json
echo RELAY_PLAN_CREATED
`, hostStore)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd, cleanup, err := iso.ExecuteIsolated(ctx, script, nil)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("builder shell failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "\n"+slot+"\n") || !strings.Contains(string(out), "RELAY_PLAN_CREATED") {
		t.Fatalf("wrong identity or missing plan: %s", out)
	}
	data, err := os.ReadFile(filepath.Join(project, "planning", "plan.json"))
	if err != nil || !json.Valid(data) {
		t.Fatalf("invalid saved plan: %s %v", data, err)
	}
	t.Logf("builder ran as %s, created planning/plan.json, and denied the host store", slot)
}

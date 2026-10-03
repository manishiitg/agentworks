//go:build linux

package security

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// A slotted command is run from a request written when the command is wrapped, so the per-call environment
// (session token, secrets, workflow variables) has to be in that request. It used to be appended to the command
// afterwards and never reached the slot's shell (Confida 2026-10-01: the tools gateway answered 401).
func TestSlottedCommandRequestCarriesTheExtraEnvironment(t *testing.T) {
	t.Setenv(slots.EnvSlotctl, "/opt/slotctl")
	t.Setenv("GOG_HOME", "/ungranted-host-gog")
	t.Setenv("GOG_KEYRING_PASSWORD", "fixture-host-keyring")
	t.Setenv("AGENTWORKS_GOG_TERMINAL_ACCESS", "")
	dir := t.TempDir()
	iso := &Isolator{
		Slot:      "slot05",
		BaseDir:   dir,
		WorkDir:   dir,
		ReadPaths: []string{dir},
		ExtraEnv:  map[string]string{"MCP_API_TOKEN": "tok-123", "SECRET_KEY": "s3", "VAR_USER": "u1"},
	}
	policy, err := iso.landlockPolicy()
	if err != nil {
		t.Skipf("no sandbox policy in this environment: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd, cleanup, err := iso.landlockCommand(ctx, policy, "echo ok", nil)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		if strings.Contains(err.Error(), "SANDBOX_UNAVAILABLE") {
			t.Skipf("Landlock launcher is unavailable here: %v", err)
		}
		t.Fatal(err)
	}
	if cmd.Stdin == nil {
		t.Fatal("the command was not wrapped for the slot")
	}
	body, err := io.ReadAll(cmd.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var req slots.ExecRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	env := strings.Join(req.Env, "\n")
	for _, entry := range req.Env {
		if strings.HasPrefix(entry, "GOG_HOME=") || strings.HasPrefix(entry, "GOG_KEYRING_PASSWORD=") {
			t.Errorf("slot request inherited host Google config: %s", strings.SplitN(entry, "=", 2)[0])
		}
	}
	for _, want := range []string{"MCP_API_TOKEN=tok-123", "SECRET_KEY=s3", "VAR_USER=u1"} {
		if !strings.Contains(env, want) {
			t.Errorf("the slot's request lacks %s", strings.SplitN(want, "=", 2)[0])
		}
	}
}

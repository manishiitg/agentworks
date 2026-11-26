package clisandbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-451: the plain-command compatibility launch (no script, no env) is refused too when the declared slot is
// not the host's; a declared app account still gets its command.
func TestPrepareCodexCommandRefusesAnUnconfirmedSlot(t *testing.T) {
	root := t.TempDir()
	table := filepath.Join(root, "slots.json")
	_ = os.WriteFile(table, []byte(`{"slots":{"slot08":"user-a"}}`), 0o600)
	cfg := filepath.Join(root, "slotctl.json")
	_ = os.WriteFile(cfg, []byte(`{"slot_run_root":"`+root+`/run","slot_state_root":"`+root+`/state","docs_root":"`+root+`/docs","slot_table":"`+table+`"}`), 0o644)
	t.Setenv(slotfs.EnvConfig, cfg)
	t.Cleanup(llmtypes.ResetDeclaredRunAsForTest)
	bad := filepath.Join(root, "docs", "Crew", "c")
	llmtypes.DeclareRunAs(bad, llmtypes.RunAs{Declared: true, User: "user-a", Slot: "slot09"})
	app := filepath.Join(root, "docs", "Workflow", "g")
	llmtypes.DeclareRunAs(app, llmtypes.RunAs{Declared: true})

	if cmd, _, err := PrepareCodexCommand(nil, []string{"codex"}, bad, nil); !errors.Is(err, slotfs.ErrLaunchBlocked) || cmd != "" {
		t.Fatalf("mismatch: %q %v", cmd, err)
	}
	if cmd, cleanup, err := PrepareCodexCommand(nil, []string{"codex"}, app, nil); err != nil || cmd == "" {
		t.Fatalf("app account: %q %v", cmd, err)
	} else {
		cleanup()
	}
}

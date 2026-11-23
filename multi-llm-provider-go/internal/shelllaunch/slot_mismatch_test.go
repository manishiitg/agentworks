package shelllaunch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-451 end to end through the launch functions: a launch whose declared slot the host does not confirm is
// refused with slotfs.ErrLaunchBlocked and leaves no launch script behind; a declared app account (what a Crew or
// goal CLI turn declares) still builds its command as before.
func TestLaunchFunctionsRefuseAnUnconfirmedSlot(t *testing.T) {
	root := t.TempDir()
	table := filepath.Join(root, "slots.json")
	if err := os.WriteFile(table, []byte(`{"slots":{"slot08":"user-a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "slotctl.json")
	body := `{"slot_run_root":"` + root + `/run","slot_state_root":"` + root + `/state","docs_root":"` + root + `/docs","slot_table":"` + table + `"}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(slotfs.EnvConfig, cfg)
	t.Cleanup(llmtypes.ResetDeclaredRunAsForTest)

	mismatched := filepath.Join(root, "docs", "Crew", "crew-1")
	llmtypes.DeclareRunAs(mismatched, llmtypes.RunAs{Declared: true, User: "user-a", Slot: "slot09"})
	appAccount := filepath.Join(root, "docs", "Workflow", "goal-1")
	llmtypes.DeclareRunAs(appAccount, llmtypes.RunAs{Declared: true})

	args := []string{"claude", "--print"}
	env := []string{"A=b"}
	launches := map[string]func(dir string) (string, func(), error){
		"CommandWithEnv":               func(d string) (string, func(), error) { return CommandWithEnv(args, d, env) },
		"CommandWithEnv, no env":       func(d string) (string, func(), error) { return CommandWithEnv(args, d, nil) },
		"CommandWithScopedEnv":         func(d string) (string, func(), error) { return CommandWithScopedEnv(args, d, env, nil, nil) },
		"CommandWithScopedEnv, no env": func(d string) (string, func(), error) { return CommandWithScopedEnv(args, d, nil, nil, nil) },
		"CommandWithFinalEnv":          func(d string) (string, func(), error) { return CommandWithFinalEnv(args, d, env, nil) },
		"CommandWithFinalEnv, nothing": func(d string) (string, func(), error) { return CommandWithFinalEnv(args, d, nil, nil) },
	}
	for name, launch := range launches {
		t.Run(name, func(t *testing.T) {
			command, cleanup, err := launch(mismatched)
			if !errors.Is(err, slotfs.ErrLaunchBlocked) || command != "" {
				t.Fatalf("mismatch: command=%q err=%v, want a blocked launch", command, err)
			}
			if cleanup != nil {
				cleanup()
			}
			command, cleanup, err = launch(appAccount)
			if err != nil || command == "" {
				t.Fatalf("app account launch: command=%q err=%v", command, err)
			}
			if strings.Contains(command, root+"/run") {
				t.Fatalf("app account launch was routed to a slot: %q", command)
			}
			cleanup()
		})
	}
}

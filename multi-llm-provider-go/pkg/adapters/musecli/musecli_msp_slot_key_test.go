package musecli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-872-119 / 872-84: shared server logins never become slot-readable files.
func TestMuseSlotKeyStaysInScopedEnvironment(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "slotctl.json")
	if err := os.WriteFile(cfg, []byte(`{"slot_state_root":"`+root+`/state","slot_run_root":"`+root+`/run"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(slotfs.EnvConfig, cfg)
	opts := &llmtypes.CallOptions{CLISecurity: &llmtypes.CLISecurityPolicy{PrivateHome: filepath.Join(root, "state", "slot05", "home")}}
	for _, explicit := range []bool{false, true} {
		key := ""
		expected := "dummy-ambient"
		if explicit {
			key = "dummy-selected"
			expected = key
		}
		fileKey, env := museMSPKeyForLaunch(opts, key, []string{"META_API_KEY=dummy-ambient", "UNRELATED=kept"})
		if fileKey != "" || museEnvValue(env, "META_API_KEY") != expected || museEnvValue(env, "UNRELATED") != "kept" {
			t.Fatal("slot login must use scoped env, not an auth file")
		}
	}
	fileKey, _ := museMSPKeyForLaunch(nil, "dummy-app", nil)
	if runtime.GOOS != "darwin" && fileKey != "dummy-app" {
		t.Fatal("app-account Linux retains private file path")
	}
}

package cursorcli

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// PLAT-872-52: account A must not borrow the host or another account's token.
func TestCursorUsageUsesOnlySelectedAccount(t *testing.T) {
	home := t.TempDir()
	other := t.TempDir()
	t.Setenv("HOME", other)
	t.Setenv("CURSOR_AUTH_TOKEN", "ambient-other-account")
	dir := filepath.Join(home, ".cursor")
	if runtime.GOOS == "linux" {
		dir = filepath.Join(home, ".config", "cursor")
	}
	if runtime.GOOS == "windows" {
		t.Skip("provider runtime currently targets Unix")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"accessToken":"selected-login"}`), 0600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "AGENT_CLI_CREDENTIAL_STORE=file"}
	got, err := cursorUsageToken(env)
	if err != nil || got != "selected-login" {
		t.Fatalf("selected account was not read: %q %v", got, err)
	}
	for _, extra := range []string{"CURSOR_API_KEY=selected-api-key", "AGENT_CLI_CREDENTIAL_STORE=memory"} {
		got, err = cursorUsageToken(append(env, extra))
		if err != nil || got != "" {
			t.Fatalf("fell back to saved login for %s", extra)
		}
	}
	got, err = cursorUsageToken(nil)
	if err != nil || got != "" {
		t.Fatal("borrowed ambient credentials")
	}
}
func TestCursorUsageMissingIsNotZeroAndResetsInMilliseconds(t *testing.T) {
	usage, err := parseCursorAccountUsage([]byte(`{"billingCycleEnd":"1793100000000","planUsage":{"totalPercentUsed":0,"autoPercentUsed":null}}`), llmtypes.AccountUsage{})
	if err != nil || len(usage.Windows) != 1 || usage.Windows[0].UsedPercent != 0 || usage.Windows[0].ResetsAt.Unix() != 1793100000 {
		t.Fatalf("wrong quota: %#v %v", usage, err)
	}
}

package musecli

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A key the account gives only in the environment (a company account's model
// proxy on a Mac) reaches muse serve through the launch's key file, not the
// environment: a dummy one is refused as a saved key, and the run is confined
// as the app runs it.
func TestMuseMSPEnvironmentKeyReachesServe(t *testing.T) {
	skipMuseMSPLive(t)
	t.Setenv("META_API_KEY", "dummy-environment-probe")
	owner := "msp-envkey-" + museCommandID()
	work := t.TempDir()
	t.Cleanup(func() { CloseMuseMSPSession(owner) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := NewMuseCLIAdapter("", "", nil).GenerateContent(ctx, museMSPHuman("Reply with only: ok"), WithWorkingDir(work), WithInteractiveSessionID(owner), liveConfined(t, "muse-cli", work), llmtypes.WithReasoningEffort("low"))
	t.Logf("turn: %v", err)
	want := "saved API key was rejected" // Linux: the launch key file
	if runtime.GOOS == "darwin" {
		want = "API key from META_API_KEY was rejected" // a Mac keeps the key in the environment
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want the dummy key used from the key file, got %v", err)
	}
}

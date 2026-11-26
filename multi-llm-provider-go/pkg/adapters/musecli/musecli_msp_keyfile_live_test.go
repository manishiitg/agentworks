package musecli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Does muse serve keep the stored key in memory once it started, so the
// auth file can be removed before any tool runs? A dummy key: a turn after
// the removal fails with "saved API key was rejected" only if Muse still uses
// the key it loaded.
func TestMuseMSPKeyFileRemovedAfterStart(t *testing.T) {
	skipMuseMSPLive(t)
	owner := "msp-keyfile-" + museCommandID()
	t.Cleanup(func() { CloseMuseMSPSession(owner) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	adapter := NewMuseCLIAdapter("dummy-presence-probe", "", nil)
	options := []llmtypes.CallOption{WithWorkingDir(t.TempDir()), WithInteractiveSessionID(owner), llmtypes.WithReasoningEffort("low")}
	if _, err := adapter.GenerateContent(ctx, nil, append(options, llmtypes.WithCodingProviderLaunchOnly())...); err != nil {
		t.Fatal(err)
	}
	s := museMSPSessionFor(owner)
	env := s.rpc.cmd.Env
	home := ""
	for _, e := range env {
		if strings.HasPrefix(e, "XDG_CONFIG_HOME=") {
			home = strings.TrimPrefix(e, "XDG_CONFIG_HOME=")
		}
	}
	auth := filepath.Join(home, "muse", "auth.json")
	if _, err := os.Stat(auth); err != nil {
		t.Logf("auth file after start: %v", err)
	} else if err := os.Remove(auth); err != nil {
		t.Fatal(err)
	}
	_, err := adapter.GenerateContent(ctx, museMSPHuman("Reply with only: ok"), options...)
	t.Logf("turn after removing the auth file: %v", err)
}

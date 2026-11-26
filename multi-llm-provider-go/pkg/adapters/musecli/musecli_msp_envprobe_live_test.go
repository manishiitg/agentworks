package musecli

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Reviewer check: does a child Muse starts (an MCP server) inherit
// META_API_KEY from muse serve? A dummy value; presence only.
func TestMuseMSPEnvProbe(t *testing.T) {
	skipMuseMSPLive(t)
	museMSPTestCapabilities = []string{"userShell"}
	t.Cleanup(func() { museMSPTestCapabilities = nil })
	out := t.TempDir() + "/presence.txt"
	cfg := `{"mcpServers":{"envprobe":{"command":"python3","args":["/private/tmp/claude-501/-Users-mipl-ai-work/b06e4f25-9579-47b7-90b3-d508c64fb5ca/scratchpad/envprobe/probe_server.py","` + out + `"]}}}`
	owner := "msp-envprobe-" + museCommandID()
	t.Cleanup(func() { CloseMuseMSPSession(owner) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := NewMuseCLIAdapter("dummy-presence-probe", "", nil).GenerateContent(ctx, nil, WithWorkingDir(t.TempDir()), WithInteractiveSessionID(owner), WithMCPConfig(cfg), llmtypes.WithCodingProviderLaunchOnly())
	t.Logf("launch: %v", err)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if raw, _ := os.ReadFile(out); len(raw) > 0 {
			t.Logf("MCP child saw: %s", strings.TrimSpace(string(raw)))
			s := museMSPSessionFor(owner)
			shellOut := t.TempDir() + "/shell.txt"
			cmd := `for k in META_API_KEY MSP_PROBE_CONTROL PATH; do if [ -n "$(printenv $k)" ]; then printf "%s=set " $k; else printf "%s=unset " $k; fi; done > ` + shellOut
			if err := s.rpc.call(ctx, "session/userShell", map[string]any{"commandId": museCommandID(), "sessionId": s.sessionID, "commandText": cmd}, nil); err != nil {
				t.Fatalf("userShell: %v", err)
			}
			for i := 0; i < 30; i++ {
				if raw, _ := os.ReadFile(shellOut); len(raw) > 0 {
					t.Logf("Muse shell child saw: %s", strings.TrimSpace(string(raw)))
					if runtime.GOOS != "darwin" && strings.Contains(string(raw), "META_API_KEY=set") {
						t.Fatal("the selected key reached Muse's shell")
					}
					// The stored key is the one Muse uses: the dummy fails auth.
					_, turnErr := NewMuseCLIAdapter("dummy-presence-probe", "", nil).GenerateContent(ctx, museMSPHuman("Reply with only: ok"), WithWorkingDir(t.TempDir()), WithInteractiveSessionID(owner+"-turn"), llmtypes.WithReasoningEffort("low"))
					t.Cleanup(func() { CloseMuseMSPSession(owner + "-turn") })
					if runtime.GOOS == "darwin" {
						t.Log("Mac: the key stays in the environment (muse auth set writes the Keychain)")
						return
					}
					if turnErr == nil {
						t.Fatal("a turn with the dummy key succeeded: Muse ignored the selected key")
					}
					t.Logf("dummy-key turn refused: %v", turnErr)
					return
				}
				time.Sleep(time.Second)
			}
			t.Fatal("shell probe did not run")
		}
		time.Sleep(time.Second)
	}
	t.Fatal("MCP probe never started")
}

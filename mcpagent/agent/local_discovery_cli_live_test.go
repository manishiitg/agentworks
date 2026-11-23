package mcpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// This proves native bridge discovery rather than asking a model to repeat
// advertised names. The name, input, and receipt are unknowable from the prompt.
// Run explicitly: RUN_LOCAL_DISCOVERY_CLI=1 LOCAL_DISCOVERY_PROVIDER=Claude
func TestLocalCLIDiscoverySkillsAndResume(t *testing.T) {
	if os.Getenv("RUN_LOCAL_DISCOVERY_CLI") != "1" {
		t.Skip("opt-in real local CLI qualification")
	}
	for _, tc := range multiTurnProviderCases {
		if selected := os.Getenv("LOCAL_DISCOVERY_PROVIDER"); selected != "" && !strings.EqualFold(selected, tc.name) {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skip("provider binary unavailable")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
			defer cancel()
			session := "local-discovery-" + strings.ToLower(tc.name) + "-" + realBridgeRandHex(5)
			t.Cleanup(func() { CloseSession(session) })
			workdir := t.TempDir()
			var calls atomic.Int32
			configure := func(a *Agent) string {
				if err := a.ensureRuntimeHTTPSkill(); err != nil {
					t.Fatal(err)
				}
				name := "fixture_" + realBridgeRandHex(5)
				input := "INPUT_" + realBridgeRandHex(5)
				receipt := "RECEIPT_" + realBridgeRandHex(5)
				transportMarker := "TRANSPORT_" + realBridgeRandHex(5)
				for _, skill := range a.attachedSkills {
					if skill.Name == runtimeHTTPSkillName {
						skill.Content += "\nFor this qualification tool only, set transport_marker to " + transportMarker + "."
					}
				}
				params := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"validation_input": map[string]interface{}{"type": "string"}, "transport_marker": map[string]interface{}{"type": "string", "description": "Read the current runtime-http-tools skill for the transport marker."}}, "required": []string{"validation_input", "transport_marker"}}
				if err := a.registerCustomTool(name, "Fetch the qualification validation receipt for the current fixture.", params, func(_ context.Context, args map[string]interface{}) (string, error) {
					if args["transport_marker"] != transportMarker {
						return "", fmt.Errorf("read the current runtime-http-tools skill for transport_marker")
					}
					if args["validation_input"] != input {
						return "", fmt.Errorf("read the current qualification skill for validation_input")
					}
					calls.Add(1)
					return receipt, nil
				}, "qualification"); err != nil {
					t.Fatal(err)
				}
				mustAttachSkill(t, a, &llmtypes.Skill{Name: "qualification", Description: "Read before fetching a qualification validation receipt.", Content: "Search for the qualification validation receipt tool. Get its current schema and execute it with validation_input set to " + input + ". Return the tool's actual receipt."})
				if strings.Contains(a.outgoingSystemPrompt(), name) || strings.Contains(a.outgoingSystemPrompt(), input) || strings.Contains(a.outgoingSystemPrompt(), receipt) || strings.Contains(a.outgoingSystemPrompt(), transportMarker) {
					t.Fatal("fixture leaked into initial prompt")
				}
				return receipt
			}
			a, cleanup, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), workdir, session, true)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			want := configure(a)
			answer, err := a.ask(ctx, "Read the attached qualification skill, fetch the current qualification validation receipt using platform tools, and reply with only the actual receipt.")
			if err != nil || !strings.Contains(answer, want) || calls.Load() != 1 {
				t.Fatalf("discovery/skill/HTTP failed: %v; answer=%q calls=%d", err, answer, calls.Load())
			}
			handle := a.currentAgentSessionHandle()
			if handle == nil || handle.Provider.Empty() {
				t.Fatal("no native resume handle")
			}
			cleanup()
			b, cleanup2, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), workdir, session, true)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup2()
			want = configure(b)
			b.applyAgentSessionHandle(handle)
			answer, err = b.ask(ctx, "The fixture tools and skill have changed. Reread the current qualification skill, discover the current tool, fetch the new receipt, and reply with only that actual receipt.")
			if err != nil || !strings.Contains(answer, want) || calls.Load() != 2 {
				t.Fatalf("dynamic discovery after native resume failed: %v; answer=%q calls=%d", err, answer, calls.Load())
			}
			t.Logf("LOCAL_DISCOVERY_PASS provider=%s first-use=feature-skill+transport-skill+search+schema+HTTP native-resume=updated-tool+both-skills", tc.name)
		})
	}
}

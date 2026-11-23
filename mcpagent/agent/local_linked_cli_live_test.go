package mcpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
)

// This is a local linked-artifact qualification, not a sandbox certificate. Use
// disposable files and actual authenticated CLIs; no application projects are
// touched. Enable explicitly because it consumes real provider requests.
func TestLocalLinkedCLIArtifactsAndResume(t *testing.T) {
	if os.Getenv("RUN_LOCAL_LINKED_CLI") != "1" {
		t.Skip("set RUN_LOCAL_LINKED_CLI=1 for live local CLI qualification")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))
	for _, tc := range multiTurnProviderCases {
		if selected := os.Getenv("LOCAL_LINKED_PROVIDER"); selected != "" && selected != tc.name {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s unavailable", tc.binary)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
			defer cancel()
			root := t.TempDir()
			project := filepath.Join(root, "project-data")
			output := filepath.Join(root, "iteration-0", "group-a", "execution", "step-1")
			for _, dir := range []string{project, output} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			projectToken := "PROJECT_" + realBridgeRandHex(5)
			outputToken := "OUTPUT_" + realBridgeRandHex(5)
			word := "REMEMBER_" + realBridgeRandHex(5)
			for path, contents := range map[string]string{filepath.Join(project, "source-"+realBridgeRandHex(4)+".txt"): projectToken, filepath.Join(output, "stage-"+realBridgeRandHex(4)+".txt"): outputToken} {
				if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			session := "local-linked-" + strings.ToLower(tc.name) + "-" + realBridgeRandHex(5)
			t.Cleanup(func() { CloseSession(session) })
			a, cleanup, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), root, session, true)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			listener := &localLinkedToolListener{}
			a.addEventListener(listener)
			a.isolatedSessionWorkspace = true
			a.codingAgentOutputDir = output
			a.codingAgentToolsMode = codingAgentToolsFull
			a.cliSecurityPolicy = confinedTestPolicy()
			if err := a.prepareIsolatedOutputLink(); err != nil {
				t.Fatal(err)
			}
			runtime := a.isolatedWorkspacePath
			if err := os.Symlink(project, filepath.Join(runtime, "project")); err != nil {
				t.Fatal(err)
			}
			a.addInstructions(isolatedOutputInstructions + "\nFor native search/glob, use the exact absolute fixture paths in the request.")
			prompt := fmt.Sprintf("Local fixture check. Discover the unknown .txt filename under %q using an explicit search path and read its exact contents. Discover the unknown .txt filename under %q and read its exact contents. Write %q containing both exact file contents on separate lines and verify it. Use enabled native tools. Do not bypass restrictions or read other folders. Remember the code word %s without writing it into any file. Reply DONE.", project, output, filepath.Join(output, "result.txt"), word)
			if tc.name == "Claude" {
				prompt += " Use native Glob with the exact directory paths above and pattern *.txt, then native Read."
			}
			answer, err := a.ask(ctx, prompt)
			if err != nil {
				t.Fatalf("linked work failed: %v; tools=%v", err, listener.receipts())
			}
			// #nosec G304 -- file is in a test-owned fixture
			contents, err := os.ReadFile(filepath.Join(output, "result.txt"))
			if err != nil || !strings.Contains(string(contents), projectToken) || !strings.Contains(string(contents), outputToken) {
				t.Fatalf("missing authoritative output: %q, %v; answer=%s", contents, err, answer)
			}
			receipts := listener.receipts()
			if len(receipts) == 0 {
				t.Fatal("no observed tool receipts for linked file discovery")
			}
			if tc.name == "Claude" {
				var projectGlob, outputGlob bool
				for _, receipt := range receipts {
					if receipt.name == "Glob" {
						projectGlob = projectGlob || strings.Contains(receipt.arguments, "project")
						outputGlob = outputGlob || strings.Contains(receipt.arguments, output)
					}
				}
				if !projectGlob || !outputGlob {
					t.Fatalf("missing explicit native Glob paths: %v", receipts)
				}
			}
			for _, receipt := range receipts {
				t.Logf("LOCAL_LINKED_TOOL provider=%s tool=%s args=%s result=%s", tc.name, receipt.name, receipt.arguments, receipt.result)
			}
			handle := a.currentAgentSessionHandle()
			if handle == nil || handle.Provider.Empty() || filepath.Clean(handle.Provider.WorkingDir) != filepath.Clean(output) || filepath.Clean(handle.Provider.RuntimeDir) != filepath.Clean(runtime) {
				t.Fatalf("native resume lost its output cwd or private runtime: %#v", handle)
			}
			cleanup()
			b, cleanup2, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), root, session, true)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup2()
			b.isolatedSessionWorkspace = true
			b.codingAgentOutputDir = output
			b.codingAgentToolsMode = codingAgentToolsFull
			b.cliSecurityPolicy = confinedTestPolicy()
			b.addInstructions(isolatedOutputInstructions + "\nFor native search/glob, use the exact absolute fixture paths in the request.")
			if err := b.prepareIsolatedOutputLink(); err != nil {
				t.Fatal(err)
			}
			b.applyAgentSessionHandle(handle)
			answer, err = b.ask(ctx, "What exact code word did I ask you to remember? Reply with only that word. Do not use tools.")
			if err != nil || !strings.Contains(answer, word) {
				t.Fatalf("native resume failed: %v; answer=%s", err, answer)
			}
			cleanup2()
			CloseSession(session)
			if _, err := os.Stat(runtime); !os.IsNotExist(err) {
				t.Fatalf("private runtime survived cleanup: %v", err)
			}
			// #nosec G304 -- file is in a test-owned fixture
			contents, err = os.ReadFile(filepath.Join(output, "result.txt"))
			if err != nil || !strings.Contains(string(contents), projectToken) || !strings.Contains(string(contents), outputToken) {
				t.Fatal("session cleanup lost authoritative output")
			}
			t.Logf("LOCAL_LINKED_PASS provider=%s discovery=random-filenames read=both-fixtures write=real-output native-resume=pass cleanup=preserved", tc.name)
		})
	}
}

// Keep live callbacks safe even when native and bridge tool streams overlap.
// Store only tool events from this fixture, never provider auth or history.
type localLinkedToolReceipt struct {
	name      string
	arguments string
	result    string
}

type localLinkedToolListener struct {
	mu    sync.Mutex
	tools []localLinkedToolReceipt
}

func (l *localLinkedToolListener) Name() string { return "local-linked-tools" }

func (l *localLinkedToolListener) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	if start, ok := event.Data.(*events.ToolCallStartEvent); ok {
		l.mu.Lock()
		l.tools = append(l.tools, localLinkedToolReceipt{name: start.ToolName, arguments: start.ToolParams.Arguments})
		l.mu.Unlock()
	}
	if end, ok := event.Data.(*events.ToolCallEndEvent); ok {
		l.mu.Lock()
		result := end.Result
		if len(result) > 1000 {
			result = result[:1000] + "..."
		}
		l.tools = append(l.tools, localLinkedToolReceipt{name: end.ToolName, result: result})
		l.mu.Unlock()
	}
	return nil
}

func (l *localLinkedToolListener) receipts() []localLinkedToolReceipt {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]localLinkedToolReceipt(nil), l.tools...)
}

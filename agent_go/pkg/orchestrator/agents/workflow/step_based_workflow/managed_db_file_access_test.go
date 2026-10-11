package step_based_workflow

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

// Exercise both production guard builders through the actual OS sandbox, not
// just a path-list assertion. In particular a file grant must not expose its
// siblings or accidentally permit writes to the schema documentation.
func TestManagedStepDBFileAccessP0(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("requires OS filesystem sandbox")
	}
	for _, kind := range []string{"agent", "orchestrator"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			workflow := filepath.Join(root, "Workflow", "test")
			hcpo := newAgentClosingTestOrchestrator(t)
			hcpo.BaseOrchestrator.SetWorkspacePath(workflow)
			hcpo.selectedRunFolder = "iteration-0/default"
			config := &AgentConfigs{KnowledgebaseAccess: KBAccessNone, LearningsAccess: LearningsAccessNone}
			var reads, writes []string
			if kind == "agent" {
				reads, writes = hcpo.setupAgentFolderGuard("step-1", "test-step", config, AgentWriteAccess{})
			} else {
				reads, writes = hcpo.setupOrchestratorFolderGuard(&AgentPlanStep{
					Type: StepTypeAgent, CommonStepFields: CommonStepFields{ID: "test-step"}, AgentConfigs: config,
				}, "step-1")
			}
			for _, path := range append(append([]string{}, reads...), writes...) {
				if !strings.HasPrefix(path, root+string(os.PathSeparator)) {
					t.Fatalf("unexpected external test grant: %s", path)
				}
				dir := path
				if strings.HasSuffix(path, ".md") {
					dir = filepath.Dir(path)
				}
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			soul := filepath.Join(workflow, "soul", "soul.md")
			if err := os.MkdirAll(filepath.Dir(soul), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(soul, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			download := filepath.Join(hcpo.getOrchestratorExecutionWorkspacePath(), "Downloads", "download.txt")
			toolOutput := filepath.Join(workflow, "tool_output_folder", "result.txt")
			builderArtifact := filepath.Join(workflow, "builder", "conversation.txt")
			for _, path := range []string{toolOutput, builderArtifact} {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			db := filepath.Join(workflow, "db")
			for _, name := range []string{"README.md", "strategy_framework.md", "db.sqlite", "db.sqlite-wal", "db.sqlite-shm"} {
				if err := os.WriteFile(filepath.Join(db, name), []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			isolator := &security.Isolator{ReadPaths: reads, WritePaths: writes, BaseDir: root, WorkDir: filepath.Join(db, "assets")}
			cases := []struct {
				name, command string
				allowed       bool
			}{
				{"read README", "cat ../README.md", true},
				{"read soul", "cat '" + soul + "'", true},
				{"write soul", "printf changed > '" + soul + "'", false},
				{"read spilled tool output", "cat '" + toolOutput + "'", true},
				{"write spilled tool output", "printf changed > '" + toolOutput + "'", false},
				{"read builder artifact", "cat '" + builderArtifact + "'", false},
				{"Downloads read and write", "printf fixture > '" + download + "' && cat '" + download + "'", true},
				{"write README", "printf changed > ../README.md", false},
				{"assets read and write", "printf asset > artifact.txt && cat artifact.txt", true},
				{"read sibling document", "cat ../strategy_framework.md", false},
				{"read raw database", "cat ../db.sqlite", false},
				{"write raw database", "printf changed > ../db.sqlite", false},
				{"read WAL", "cat ../db.sqlite-wal", false},
				{"read SHM", "cat ../db.sqlite-shm", false},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					cmd, cleanup, err := isolator.ExecuteIsolated(ctx, tc.command, nil)
					if cleanup != nil {
						defer cleanup()
					}
					if err != nil {
						t.Fatal(err)
					}
					output, err := cmd.CombinedOutput()
					if (err == nil) != tc.allowed {
						t.Fatalf("allowed=%v, err=%v, output=%s", tc.allowed, err, output)
					}
					if tc.allowed {
						want := "fixture"
						if tc.name == "assets read and write" {
							want = "asset"
						}
						if strings.TrimSpace(string(output)) != want {
							t.Fatalf("read returned %q, want %q", output, want)
						}
					}
				})
			}
			content, err := os.ReadFile(filepath.Join(db, "README.md"))
			if err != nil || string(content) != "fixture" {
				t.Fatalf("README changed: %s (%v)", content, err)
			}
		})
	}
}

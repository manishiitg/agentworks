package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// Owner's Main, 4 Nov (PLAT-856-509): a turn in Run mode wrote the session under the Run runtime folder; the next
// Builder turn resumed it from the Builder folder and `claude --resume` exited 1. Only a runtime of the same Crew counts.
func TestClaudeResumeCopiesTranscriptFromSameCrewRuntimeOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	base := filepath.Join(t.TempDir(), "cli-runtimes", "v1")
	runtime := func(name, crew string) string {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(crew, filepath.Join(dir, cliruntime.ProjectLink)); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	transcript := func(dir, id, body string) string {
		path := filepath.Join(home, ".claude", "projects", sessiontransfer.ClaudeSlug(dir), id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return path
	}
	builder, run := runtime("builder", "/crews/test"), runtime("run", "/crews/test")
	other := runtime("other", "/crews/someone-else")

	original := transcript(run, "main-session", "{\"type\":\"user\"}\n")
	carryClaudeTranscriptIntoRuntime(builder, "main-session")
	if got, err := os.ReadFile(transcript(builder, "main-session", "")); err != nil || string(got) != "{\"type\":\"user\"}\n" {
		t.Fatalf("builder runtime did not get the Run runtime's transcript: %q %v", got, err)
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatalf("the original transcript must stay: %v", err)
	}

	transcript(other, "foreign-session", "{}\n")
	carryClaudeTranscriptIntoRuntime(builder, "foreign-session")
	if _, err := os.Stat(transcript(builder, "foreign-session", "")); !os.IsNotExist(err) {
		t.Fatalf("a transcript from another Crew's runtime must not be copied: %v", err)
	}
}

package llmtypes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractCLIBinaryVersion(t *testing.T) {
	cases := []struct {
		output string
		want   string
	}{
		{"2.1.278 (Claude Code)", "2.1.278"},
		{"codex-cli 0.155.1", "0.155.1"},
		{"2026.09.18-9a7762b", "2026.09.18"},
		{"Muse Code 1.3.0 (1.3.0-R3401.1)", "1.3.0"},
		{"pi 0.86.0", "0.86.0"},
	}
	for _, tc := range cases {
		got, err := ExtractCLIBinaryVersion(tc.output)
		if err != nil {
			t.Errorf("Extract(%q) error = %v", tc.output, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Extract(%q) = %q, want %q", tc.output, got, tc.want)
		}
	}
	if _, err := ExtractCLIBinaryVersion("no digits here"); err == nil {
		t.Error("Extract without a version must fail")
	}
}

func TestProbeCLIBinaryVersion(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'testcli 3.2.1 (extra)'\n"
	if err := os.WriteFile(filepath.Join(dir, "testcli"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := ProbeCLIBinaryVersion(ctx, "testcli", "--version")
	if err != nil {
		t.Fatalf("Probe error = %v", err)
	}
	if got != "3.2.1" {
		t.Fatalf("version = %q, want 3.2.1", got)
	}
	if _, err := ProbeCLIBinaryVersion(ctx, "no-such-binary-xyz", "--version"); err == nil {
		t.Fatal("missing binary must fail")
	}
}

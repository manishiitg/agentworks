package llmproviders

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompareCodingAgentCLIVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.1.278", "2.1.278", 0},
		{"2.1.279", "2.1.278", 1},
		{"2.1.277", "2.1.278", -1},
		{"2.10.0", "2.9.9", 1},
		{"0.155.1", "0.155.1", 0},
		{"2026.09.18", "2026.09.17", 1},
		{"2026.09.18-9a7762b", "2026.09.18", 1},
		{"1.3.0", "1.3", 0},
		{"v1.3.0", "1.3.0", 0},
		{"codex-cli 0.155.1", "0.155.1", 0},
	}
	for _, tc := range cases {
		if got := CompareCodingAgentCLIVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestExtractCodingAgentCLIVersion(t *testing.T) {
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
		got, err := ExtractCodingAgentCLIVersion(tc.output)
		if err != nil {
			t.Errorf("Extract(%q) error = %v", tc.output, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Extract(%q) = %q, want %q", tc.output, got, tc.want)
		}
	}
	if _, err := ExtractCodingAgentCLIVersion("no digits here"); err == nil {
		t.Error("Extract without a version must fail")
	}
}

// writeFakeCLIBinary installs an executable script named binary in dir that
// prints output for --version. PATH-scoped so the probe test is hermetic.
func writeFakeCLIBinary(t *testing.T, dir, binary, output string) {
	t.Helper()
	script := "#!/bin/sh\necho '" + output + "'\n"
	path := filepath.Join(dir, binary)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestCodingAgentCLIVersionProbesContractBinary(t *testing.T) {
	dir := t.TempDir()
	writeFakeCLIBinary(t, dir, "claude", "2.1.300 (Claude Code)")
	t.Setenv("PATH", dir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := CodingAgentCLIVersion(ctx, ProviderClaudeCode)
	if err != nil {
		t.Fatalf("CodingAgentCLIVersion error = %v", err)
	}
	if got != "2.1.300" {
		t.Fatalf("version = %q, want 2.1.300", got)
	}
}

func TestCodingAgentCLIVersionMissingBinaryNamesInstaller(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := CodingAgentCLIVersion(ctx, ProviderClaudeCode)
	if err == nil || !strings.Contains(err.Error(), "npm install -g @anthropic-ai/claude-code@latest") {
		t.Fatalf("error = %v, want the install command", err)
	}
}

func TestCheckCodingAgentCLIVersionEnforcesFloor(t *testing.T) {
	dir := t.TempDir()
	writeFakeCLIBinary(t, dir, "codex", "codex-cli 0.100.0")
	t.Setenv("PATH", dir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := CheckCodingAgentCLIVersion(ctx, ProviderCodexCLI); err == nil {
		t.Fatal("version below the floor must fail with an update message")
	} else if !strings.Contains(err.Error(), "0.155.1") {
		t.Fatalf("error = %v, want the floor version", err)
	}

	writeFakeCLIBinary(t, dir, "codex", "codex-cli 0.200.0")
	if err := CheckCodingAgentCLIVersion(ctx, ProviderCodexCLI); err != nil {
		t.Fatalf("version above the floor err = %v, want nil", err)
	}
}

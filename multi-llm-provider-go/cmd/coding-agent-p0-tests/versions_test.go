package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckInstalledCLIVersions(t *testing.T) {
	certified := &CertifiedCLIVersions{Versions: map[string]string{
		"claude-code": "2.1.278",
		"codex-cli":   "0.155.1",
	}}
	installed := map[string]string{
		"claude-code": "2.1.278",
		"codex-cli":   "0.156.0",
	}
	mismatches := CheckInstalledCLIVersions(installed, certified, []string{"claude-code", "codex-cli"})
	if len(mismatches) != 1 || !strings.Contains(mismatches[0], "codex-cli") {
		t.Fatalf("mismatches = %v, want exactly the codex drift", mismatches)
	}
	if got := CheckInstalledCLIVersions(installed, certified, []string{"claude-code"}); len(got) != 0 {
		t.Fatalf("matching provider mismatches = %v, want none", got)
	}
}

func TestCheckInstalledCLIVersionsRefusesGaps(t *testing.T) {
	certified := &CertifiedCLIVersions{Versions: map[string]string{"codex-cli": "0.155.1"}}
	// Missing binary is a mismatch, never a pass.
	if got := CheckInstalledCLIVersions(
		map[string]string{"codex-cli": "MISSING"}, certified, []string{"codex-cli"},
	); len(got) != 1 {
		t.Fatalf("missing-binary mismatches = %v, want one", got)
	}
	// Uncertified provider is a mismatch, never a pass.
	if got := CheckInstalledCLIVersions(
		map[string]string{"pi-cli": "0.86.0"}, certified, []string{"pi-cli"},
	); len(got) != 1 {
		t.Fatalf("uncertified-provider mismatches = %v, want one", got)
	}
}

func TestCertifiedVersionsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions.json")
	installed := map[string]string{"claude-code": "2.1.278", "codex-cli": "0.155.1"}
	if err := WriteCertifiedCLIVersions(path, installed, []string{"claude-code", "codex-cli"}); err != nil {
		t.Fatalf("write error = %v", err)
	}
	claimed, err := LoadCertifiedCLIVersions(path)
	if err != nil {
		t.Fatalf("load error = %v", err)
	}
	if len(CheckInstalledCLIVersions(installed, claimed, []string{"claude-code", "codex-cli"})) != 0 {
		t.Fatal("round-tripped claim must match")
	}
	if err := WriteCertifiedCLIVersions(path, map[string]string{"pi-cli": "MISSING"}, []string{"pi-cli"}); err == nil {
		t.Fatal("writing a MISSING claim must fail")
	}
	// Merge preserves entries outside the selected set.
	mergeInstalled := map[string]string{"claude-code": "2.1.300", "pi-cli": "MISSING"}
	if err := WriteCertifiedCLIVersions(path, mergeInstalled, []string{"claude-code"}); err != nil {
		t.Fatalf("merge write error = %v", err)
	}
	merged, err := LoadCertifiedCLIVersions(path)
	if err != nil {
		t.Fatalf("load error = %v", err)
	}
	if merged.Versions["claude-code"] != "2.1.300" || merged.Versions["codex-cli"] != "0.155.1" {
		t.Fatalf("merged versions = %v, want updated claude and preserved codex", merged.Versions)
	}
	if _, err := LoadCertifiedCLIVersions(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("loading an absent file must fail")
	}
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCertifiedCLIVersions(path); err == nil {
		t.Fatal("loading malformed JSON must fail")
	}
}

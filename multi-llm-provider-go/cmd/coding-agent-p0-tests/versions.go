package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

// CertifiedCLIVersions is the checked-in claim of which CLI versions P0 has
// certified: provider ID to extracted --version string. The release runner
// fails when the installed CLI differs, so updates are consciously
// re-certified instead of silently inherited.
type CertifiedCLIVersions struct {
	Comment  string            `json:"_comment,omitempty"`
	Versions map[string]string `json:"versions"`
}

// certifiedVersionsComment documents the claim file. WriteCertifiedCLIVersions
// restores it so machine rewrites never drop the human explanation.
const certifiedVersionsComment = "P0-certified CLI versions (extracted --version strings). The release runner fails when an installed CLI differs: re-run P0 with --update-certified-versions to certify the new versions, then commit this file. Every entry must stay at or above the SDK MinCLIVersion floor (see TestCertifiedCLIVersionsMeetFloors)."

// LoadCertifiedCLIVersions reads the checked-in versions claim.
func LoadCertifiedCLIVersions(path string) (*CertifiedCLIVersions, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read certified CLI versions: %w", err)
	}
	var claimed CertifiedCLIVersions
	if err := json.Unmarshal(raw, &claimed); err != nil {
		return nil, fmt.Errorf("parse certified CLI versions: %w", err)
	}
	if claimed.Versions == nil {
		claimed.Versions = map[string]string{}
	}
	return &claimed, nil
}

// CheckInstalledCLIVersions compares installed versions against the certified
// claim for providers. It returns one human-readable mismatch line per
// drifted provider (empty when everything matches). Providers absent from
// either map are mismatches, never silent passes.
func CheckInstalledCLIVersions(installed map[string]string, certified *CertifiedCLIVersions, providers []string) []string {
	var mismatches []string
	for _, provider := range providers {
		got, haveInstalled := installed[provider]
		want, haveCertified := certified.Versions[provider]
		switch {
		case !haveCertified:
			mismatches = append(mismatches, fmt.Sprintf("%s: no certified version recorded", provider))
		case !haveInstalled || strings.TrimSpace(got) == "" || got == "MISSING":
			mismatches = append(mismatches, fmt.Sprintf("%s: CLI not installed (certified %s)", provider, want))
		case got != want:
			mismatches = append(mismatches, fmt.Sprintf("%s: installed %s, certified %s", provider, got, want))
		}
	}
	sort.Strings(mismatches)
	return mismatches
}

// ProbeInstalledCLIVersions returns the installed version per release-matrix
// provider ("MISSING" when a CLI cannot be probed).
func ProbeInstalledCLIVersions(ctx context.Context) (map[string]string, error) {
	matrix, err := llmproviders.CodingAgentP0ReleaseMatrix()
	if err != nil {
		return nil, err
	}
	installed := make(map[string]string, len(matrix))
	for _, entry := range matrix {
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		version, err := llmproviders.CodingAgentCLIVersion(probeCtx, entry.Provider)
		cancel()
		if err != nil {
			installed[string(entry.Provider)] = "MISSING"
			continue
		}
		installed[string(entry.Provider)] = version
	}
	return installed, nil
}

// WriteCertifiedCLIVersions merges installed versions for providers into the
// claim at path, preserving other entries. MISSING entries are refused: a
// claim must name real versions. An absent file starts a new claim.
func WriteCertifiedCLIVersions(path string, installed map[string]string, providers []string) error {
	claimed := &CertifiedCLIVersions{Versions: map[string]string{}}
	if _, err := os.Stat(path); err == nil {
		var loadErr error
		claimed, loadErr = LoadCertifiedCLIVersions(path)
		if loadErr != nil {
			return loadErr
		}
	}
	for _, provider := range providers {
		version, ok := installed[provider]
		if !ok || strings.TrimSpace(version) == "" || version == "MISSING" {
			return fmt.Errorf("cannot certify %s: CLI not installed", provider)
		}
		claimed.Versions[provider] = version
	}
	claimed.Comment = certifiedVersionsComment
	raw, err := json.MarshalIndent(claimed, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// SplitProvidersFlag parses a comma-separated provider list.
func SplitProvidersFlag(flag string) []string {
	var providers []string
	for _, provider := range strings.Split(flag, ",") {
		if trimmed := strings.TrimSpace(provider); trimmed != "" {
			providers = append(providers, trimmed)
		}
	}
	return providers
}

package mcpagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
)

// TestLayer2P0RegistryConsistent is the drift guard on the registry itself:
// every entry must be well-formed and unique. Mirrors Layer 1's contract<->
// registry agreement test — the registry can never silently misrepresent what
// Layer 2 requires.
func TestLayer2P0RegistryConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Layer2P0Certifications {
		if c.ID == "" {
			t.Errorf("cert with empty ID (test %q)", c.TestName)
		}
		if c.TestName == "" {
			t.Errorf("cert %q has no test name", c.ID)
		}
		if len(c.Providers) == 0 {
			t.Errorf("cert %q lists no providers", c.ID)
		}
		if c.Transport != Layer2TransportTmux && c.Transport != Layer2TransportJSON && c.Transport != Layer2TransportAppServer && c.Transport != Layer2TransportRPC && c.Transport != Layer2TransportACP {
			t.Errorf("cert %q has invalid transport %q", c.ID, c.Transport)
		}
		if seen[c.ID] {
			t.Errorf("duplicate cert ID %q", c.ID)
		}
		seen[c.ID] = true
		for _, p := range c.Providers {
			if !layer2KnownShortName(p) {
				t.Errorf("cert %q lists unknown provider %q", c.ID, p)
			}
		}
	}
	for name, reason := range layer2MessageModesTmuxExclusions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("message_modes.tmux exclusion %q has no documented reason", name)
		}
	}
	for name, reason := range layer2SystemPromptJSONExclusions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("system_prompt.json exclusion %q has no documented reason", name)
		}
	}
}

// TestLayer2ProviderSetsDerived is the future-CLI enforcement gate: the
// Layer-2 provider sets derive from the Layer-1 SDK contracts, so a new
// coding CLI is auto-enrolled in every derived row the moment it joins
// layer2ProviderOrder + layer2ShortName — and this test fails until it
// does. No tmux contract may silently lack Layer-2 coverage.
func TestLayer2ProviderSetsDerived(t *testing.T) {
	for _, contract := range llm.CodingAgentProviderContracts() {
		if layer2ShortName(contract.Provider) == "" {
			t.Errorf("provider %q has a Layer-1 coding contract but no layer2ShortName case — add it (and join layer2ProviderOrder) so Layer-2 rows cover the new CLI", contract.Provider)
		}
	}
	inOrder := map[llm.Provider]bool{}
	for _, p := range layer2ProviderOrder {
		inOrder[p] = true
	}
	for _, contract := range llm.CodingAgentProviderContracts() {
		if !inOrder[contract.Provider] {
			t.Errorf("provider %q has a Layer-1 coding contract but is missing from layer2ProviderOrder — append it so Layer-2 rows cover the new CLI", contract.Provider)
		}
	}
	for _, p := range layer2ProviderOrder {
		if _, ok := llm.GetCodingAgentProviderContract(p, ""); !ok {
			t.Errorf("layer2ProviderOrder lists %q but no Layer-1 coding contract resolves for it (empty-model lookup) — remove it or fix the contract", p)
		}
		if layer2ShortName(p) == "" {
			t.Errorf("layer2ProviderOrder lists %q but layer2ShortName has no case for it", p)
		}
	}
	// The derivation must reproduce the curated sets: every tmux contract
	// enrolled in the tmux rows, every coding contract in the json rows.
	var wantTmux, wantJSON []string
	for _, p := range layer2ProviderOrder {
		contract, ok := llm.GetCodingAgentProviderContract(p, "")
		if !ok {
			continue
		}
		wantJSON = append(wantJSON, layer2ShortName(p))
		if contract.Transport == llm.CodingAgentTransportTmux {
			wantTmux = append(wantTmux, layer2ShortName(p))
		}
	}
	if strings.Join(layer2TmuxProviders(), ",") != strings.Join(wantTmux, ",") {
		t.Errorf("layer2TmuxProviders() = %q, want derived %q", layer2TmuxProviders(), wantTmux)
	}
	if strings.Join(layer2JSONProviders(), ",") != strings.Join(wantJSON, ",") {
		t.Errorf("layer2JSONProviders() = %q, want derived %q", layer2JSONProviders(), wantJSON)
	}
	// Every row's providers must come from the derived base (no stale or
	// hand-typed names drifting from the contracts).
	for _, c := range Layer2P0Certifications {
		base := layer2TmuxProviders()
		if c.Transport == Layer2TransportAppServer {
			base = []string{"Codex"}
		}
		if c.Transport == Layer2TransportACP {
			base = []string{"Cursor"}
		}
		if c.Transport == Layer2TransportRPC {
			base = []string{"Pi"}
		}
		if c.Transport == Layer2TransportJSON {
			base = layer2JSONProviders()
		}
		inBase := map[string]bool{}
		for _, p := range base {
			inBase[p] = true
		}
		for _, p := range c.Providers {
			if !inBase[p] {
				t.Errorf("cert %q lists %q, outside its derived transport base %q", c.ID, p, base)
			}
		}
	}
}

// TestLayer2P0AgentReviewedEvidence is the enforcement gate: every agent-reviewed
// P0 capability must keep an APPROVED agentreview record on disk for each of its
// required providers. It reads only committed testdata (no CLI, always runs), so
// CI fails the moment a required Layer-2 capability loses its reviewed evidence
// — the requiredP0CertificationIDs analogue Layer 2 previously lacked.
//
// It deliberately does NOT re-check reviewed_fingerprint against the record's
// current fingerprint: that stronger check (has the reviewed output drifted?)
// belongs to the live e2e via agentreview.RequireReviewed. Here the invariant is
// simply "an approved record exists", which is what regresses when a test or its
// evidence is deleted.
func TestLayer2P0AgentReviewedEvidence(t *testing.T) {
	const dir = "testdata/agent-reviews"
	for _, c := range Layer2P0Certifications {
		if !c.AgentReviewed {
			continue
		}
		for _, p := range c.Providers {
			path := filepath.Join(dir, c.TestName+"_"+p+".json")
			if c.Transport == Layer2TransportACP && c.TestName == "TestRealBridgeStreamingE2E" {
				path = filepath.Join(dir, "TestRealBridgeStreaming_cursor_acp.json")
			}
			// #nosec G304 - path is built from the in-repo registry, not user input.
			data, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("P0 %s (%s): missing agentreview evidence for %s: %v", c.ID, c.Transport, p, err)
				continue
			}
			var rec struct {
				Review struct {
					Verdict             string `json:"verdict"`
					ReviewedFingerprint string `json:"reviewed_fingerprint"`
				} `json:"agent_review"`
			}
			if err := json.Unmarshal(data, &rec); err != nil {
				t.Errorf("P0 %s (%s/%s): unreadable agentreview record %s: %v", c.ID, c.Transport, p, path, err)
				continue
			}
			if rec.Review.Verdict != "good" {
				t.Errorf("P0 %s (%s/%s): agentreview not approved (verdict=%q) at %s", c.ID, c.Transport, p, rec.Review.Verdict, path)
			}
			if rec.Review.ReviewedFingerprint == "" {
				t.Errorf("P0 %s (%s/%s): agentreview record has empty reviewed_fingerprint at %s", c.ID, c.Transport, p, path)
			}
		}
	}
}

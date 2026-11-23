package llmproviders

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodingAgentProviderContractCurrentProviders(t *testing.T) {
	tests := []struct {
		name      string
		provider  Provider
		modelID   string
		wantTmux  bool
		wantFound bool
	}{
		{name: "claude code", provider: ProviderClaudeCode, wantTmux: false, wantFound: true},
		{name: "codex cli", provider: ProviderCodexCLI, wantTmux: false, wantFound: true},
		{name: "cursor cli", provider: ProviderCursorCLI, wantTmux: false, wantFound: true},
		{name: "removed kimi code cli", provider: ProviderKimi, modelID: "kimi-code"},
		{name: "kimi api model", provider: ProviderKimi, modelID: "kimi-k2.6"},
		{name: "openai", provider: ProviderOpenAI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contract, found := GetCodingAgentProviderContract(tt.provider, tt.modelID)
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if !found {
				return
			}
			if got := contract.Transport == CodingAgentTransportTmux; got != tt.wantTmux {
				t.Fatalf("tmux = %v, want %v", got, tt.wantTmux)
			}
			if !contract.RequiresWorkingDir {
				t.Fatal("coding-agent contract must require an explicit working directory")
			}
			if !contract.UsesMCPBridge {
				t.Fatal("coding-agent contract must use the MCP bridge")
			}
			if !contract.RequiresMCPBridgeConfig {
				t.Fatal("coding-agent contract must require MCP bridge config")
			}
			if !contract.UsesNativeSystemPrompt {
				t.Fatal("coding-agent contract must use native system/developer instructions")
			}
			if contract.Transport == CodingAgentTransportTmux {
				for name, ok := range map[string]bool{
					"owner session id":   contract.RequiresOwnerSessionID,
					"persistent session": contract.UsesPersistentSession,
					"live input":         contract.SupportsLiveInput,
					"interrupt":          contract.SupportsInterrupt,
					"terminal stream":    contract.SupportsTerminalStream,
					"final extraction":   contract.SupportsFinalExtraction,
					"login shell launch": contract.LaunchesViaLoginShell,
					"scoped cleanup":     contract.ProcessScopedCleanup,
					"session loss":       contract.HandlesTmuxSessionLoss,
				} {
					if !ok {
						t.Fatalf("tmux coding-agent contract missing %s", name)
					}
				}
			}
		})
	}
}

func TestMuseContractSupportsRetainedFinalExtraction(t *testing.T) {
	contract, ok := GetCodingAgentProviderContract(ProviderMuseCLI, "")
	if !ok {
		t.Fatal("Muse coding-agent contract is missing")
	}
	if !contract.SupportsLiveInput || !contract.SupportsFinalExtraction {
		t.Fatalf("Muse retained-turn contract = live-input:%v final-extraction:%v, want both true",
			contract.SupportsLiveInput, contract.SupportsFinalExtraction)
	}
}

// TestNativeResumeContractMatchesRegistry guards against drift between
// contract.SupportsNativeResume and the actual end-to-end wiring (Agent
// session-id field → adapter populator → public WithXxxResumeSessionID
// re-export → server.go restore switch case).
//
// nativeResumeRegistry in coding_agent_resume_registry.go is the single
// source of truth for "this provider has a public resume option func".
// A contract that claims SupportsNativeResume=true must have a registry
// entry; a registry entry implies the contract must be true. Any mismatch
// fails this test — that's the whole point.
//
// In the past, two contract drifts went undetected for months: Codex was
// fully wired but contract.SupportsNativeResume said false, hiding the
// feature from any caller that gated on the contract value; Cursor's
// contract honestly said false but the adapter quietly accepted --resume
// metadata that nobody plumbed in. Either kind of drift now fails CI.
func TestNativeResumeContractMatchesRegistry(t *testing.T) {
	for _, contract := range CodingAgentProviderContracts() {
		_, hasRegistry := nativeResumeRegistry[contract.Provider]
		if contract.SupportsNativeResume != hasRegistry {
			t.Errorf("native-resume contract drift for %s: contract.SupportsNativeResume=%v but nativeResumeRegistry presence=%v. Update coding_agent_contract.go and coding_agent_resume_registry.go together — see the comment block in coding_agent_resume_registry.go for the 4-layer wiring checklist.",
				contract.Provider, contract.SupportsNativeResume, hasRegistry)
		}
	}
	// Inverse: registry must not list a provider whose contract is missing
	// or already says false. Catches the case where someone adds a registry
	// entry but forgets to flip the contract.
	for provider := range nativeResumeRegistry {
		contract, ok := GetCodingAgentProviderContract(provider, "")
		if !ok {
			t.Errorf("nativeResumeRegistry has %s but no coding-agent contract is registered for it", provider)
			continue
		}
		if !contract.SupportsNativeResume {
			t.Errorf("nativeResumeRegistry has %s but its contract.SupportsNativeResume is false — flip the contract", provider)
		}
	}
}

func TestCodingAgentOptionRegistriesMatchContracts(t *testing.T) {
	for _, contract := range CodingAgentProviderContracts() {
		if contract.Transport == CodingAgentTransportTmux && contract.RequiresOwnerSessionID {
			if opt := CodingAgentInteractiveSessionOption(contract.Provider, "owner-session"); opt == nil {
				t.Errorf("%s requires an owner session id but has no interactive-session option registry entry", contract.Provider)
			}
		}
		if contract.Transport == CodingAgentTransportTmux && contract.UsesPersistentSession {
			if opt := CodingAgentPersistentInteractiveOption(contract.Provider, true); opt == nil {
				t.Errorf("%s uses persistent tmux sessions but has no persistent-session option registry entry", contract.Provider)
			}
		}
		if contract.RequiresWorkingDir {
			if opt := CodingAgentWorkingDirOption(contract.Provider, "/tmp/work"); opt == nil {
				t.Errorf("%s requires a working dir but has no working-dir option registry entry", contract.Provider)
			}
		}
	}

	for provider := range codingAgentInteractiveSessionRegistry {
		contract, ok := GetCodingAgentProviderContract(provider, "")
		if !ok {
			t.Errorf("interactive-session registry has %s but no coding-agent contract", provider)
			continue
		}
		if !contract.RequiresOwnerSessionID {
			t.Errorf("interactive-session registry has %s but contract does not require tmux owner sessions", provider)
		}
	}
	for provider := range codingAgentPersistentInteractiveRegistry {
		contract, ok := GetCodingAgentProviderContract(provider, "")
		if !ok {
			t.Errorf("persistent-session registry has %s but no coding-agent contract", provider)
			continue
		}
		if !contract.UsesPersistentSession && !contract.SupportsNativeResume {
			t.Errorf("persistent-session registry has %s but contract does not claim persistent tmux sessions", provider)
		}
	}
	for provider := range codingAgentWorkingDirRegistry {
		contract, ok := GetCodingAgentProviderContract(provider, "")
		if !ok {
			t.Errorf("working-dir registry has %s but no coding-agent contract", provider)
			continue
		}
		if !contract.RequiresWorkingDir {
			t.Errorf("working-dir registry has %s but contract does not require working dir", provider)
		}
	}
	for provider := range codingAgentProjectDirIDRegistry {
		contract, ok := GetCodingAgentProviderContract(provider, "")
		if !ok {
			t.Errorf("project-dir-id registry has %s but no coding-agent contract", provider)
			continue
		}
		if !contract.SupportsNativeResume {
			t.Errorf("project-dir-id registry has %s but contract does not support native resume", provider)
		}
	}
}

func TestCodingAgentMCPBridgeIsRequiredWhenUsed(t *testing.T) {
	for _, contract := range CodingAgentProviderContracts() {
		if contract.UsesMCPBridge && !contract.RequiresMCPBridgeConfig {
			t.Errorf("%s uses the MCP bridge but does not require bridge config", contract.Provider)
		}
		if contract.RequiresMCPBridgeConfig && !contract.UsesMCPBridge {
			t.Errorf("%s requires bridge config but does not declare UsesMCPBridge", contract.Provider)
		}
		if contract.UsesMCPBridge && !contract.SupportsBridgeOnlyTools && !acceptsBestEffortToolRestrictions(contract) {
			t.Errorf("%s is an active MCP-bridge provider but does not support the platform's default mcp_only containment mode", contract.Provider)
		}
	}
}

func TestToolRestrictionsRequireP0WithExplicitMuseException(t *testing.T) {
	if priority := CodingAgentCertificationPriorityForID(CertBridgeOnlyTools); priority != CodingAgentCertificationPriorityP0 {
		t.Fatalf("%s priority = %q, want P0", CertBridgeOnlyTools, priority)
	}

	for _, contract := range CodingAgentProviderContracts() {
		if contract.Transport != CodingAgentTransportTmux {
			continue
		}
		// Prove the requirement is platform-owned rather than capability-claim
		// driven: even a provider that incorrectly clears its self-declared flag
		// must still hit the P0 containment gate.
		contract.SupportsBridgeOnlyTools = false
		required := RequiredP0CodingAgentCertificationIDs(contract)
		want := CertBridgeOnlyTools
		if acceptsBestEffortToolRestrictions(contract) {
			want = CertBestEffortToolRestrictions
		}
		found := false
		for _, id := range required {
			if id == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s P0 requirements omitted %s when SupportsBridgeOnlyTools=false: %v", contract.Provider, want, required)
		}
	}
}

func TestStopThenNewMessageIsP0ForEveryActiveTmuxProvider(t *testing.T) {
	if priority := CodingAgentCertificationPriorityForID(CertPersistentCancelReuse); priority != CodingAgentCertificationPriorityP0 {
		t.Fatalf("%s priority = %q, want P0", CertPersistentCancelReuse, priority)
	}
	for _, contract := range CodingAgentProviderContracts() {
		if contract.Transport != CodingAgentTransportTmux {
			continue
		}
		found := false
		for _, id := range RequiredP0CodingAgentCertificationIDs(contract) {
			found = found || id == CertPersistentCancelReuse
		}
		if !found {
			t.Errorf("%s P0 requirements omit stop-then-new-message", contract.Provider)
		}
	}
}

func TestProjectInstructionOnlyRegistryIsIntentional(t *testing.T) {
	expected := map[Provider]bool{
		ProviderClaudeCode: true,
		ProviderCodexCLI:   true,
		// Muse has no system-prompt flag, so AGENTS.md is its sole
		// file-only channel (no doubled preamble typed into the TUI).
		ProviderMuseCLI: true,
	}
	for provider := range expected {
		if opt := CodingAgentProjectInstructionOnlyOption(provider, true); opt == nil {
			t.Errorf("%s should have a project-instruction-only option", provider)
		}
	}
	for provider := range codingAgentProjectInstructionOnlyRegistry {
		if !expected[provider] {
			t.Errorf("unexpected project-instruction-only registry entry for %s", provider)
		}
	}
}

// TestTokenUsageContractIsWellFormed catches two classes of drift on the
// SurfacesTokenUsage / TokenUsageSource pair:
//
//  1. A contract that claims SurfacesTokenUsage:true must also name a
//     TokenUsageSource from validTokenUsageSources. Empty or freeform
//     strings fail. This stops "well we surface SOMETHING" claims with
//     no audit trail of where the data comes from.
//  2. A contract that claims SurfacesTokenUsage:false must NOT name a
//     source — if there's no usage, there's no source to declare. Stops
//     the inverse drift where someone fills in the source but forgets
//     to flip the bool.
//
// Add new TokenUsageSource values to validTokenUsageSources first; the
// test will then accept them.
func TestTokenUsageContractIsWellFormed(t *testing.T) {
	for _, c := range CodingAgentProviderContracts() {
		if c.SurfacesTokenUsage {
			if c.TokenUsageSource == "" {
				t.Errorf("%s claims SurfacesTokenUsage but TokenUsageSource is empty — declare one of stream-json|transcript-file|estimated|exec-json", c.Provider)
				continue
			}
			if !IsValidTokenUsageSource(c.TokenUsageSource) {
				t.Errorf("%s declares TokenUsageSource=%q which is not in validTokenUsageSources — add it there first or fix the typo", c.Provider, c.TokenUsageSource)
			}
			if c.InteractiveTokenUsageSource != "" && !IsValidTokenUsageSource(c.InteractiveTokenUsageSource) {
				t.Errorf("%s declares InteractiveTokenUsageSource=%q which is not in validTokenUsageSources — add it there first or fix the typo", c.Provider, c.InteractiveTokenUsageSource)
			}
		} else if c.TokenUsageSource != "" {
			t.Errorf("%s has TokenUsageSource=%q but SurfacesTokenUsage=false — flip the bool or clear the source", c.Provider, c.TokenUsageSource)
		}
	}
}

// TestCodingAgentRuntimeInstallContractIsWellFormed requires every active
// tmux provider to declare how its CLI is detected, installed, and
// version-gated. The providers page, the manifest, and the P0 release runner
// all derive from these declarations; an empty field means a new CLI is
// invisible to install/upgrade lifecycle instead of loudly undeclared.
func TestCodingAgentRuntimeInstallContractIsWellFormed(t *testing.T) {
	for _, c := range CodingAgentProviderContracts() {
		if c.Transport != CodingAgentTransportTmux {
			continue
		}
		if strings.TrimSpace(c.RuntimeBinary) == "" {
			t.Errorf("%s has no RuntimeBinary — declare the PATH binary the terminal probes", c.Provider)
		}
		if strings.TrimSpace(c.InstallCommand) == "" {
			t.Errorf("%s has no InstallCommand — declare the allowlisted install command shown on the providers page", c.Provider)
		}
		if len(c.VersionProbeArgs) == 0 {
			t.Errorf("%s has no VersionProbeArgs — declare how to ask the CLI for its version", c.Provider)
		}
		if strings.TrimSpace(c.MinCLIVersion) == "" {
			t.Errorf("%s has no MinCLIVersion — declare the oldest certified CLI version", c.Provider)
		} else if len(cliVersionComponents(c.MinCLIVersion)) == 0 {
			t.Errorf("%s MinCLIVersion=%q has no numeric components — use the CLI's --version form", c.Provider, c.MinCLIVersion)
		}
	}
}

// TestRuntimeAvailabilityLiveTestsProbeContractBinaries pins the one
// intentional duplication: adapter live tests cannot import the SDK root
// (import cycle), so each names its binary literally. This test requires
// the runtime_availability proof file to mention the contract RuntimeBinary,
// so renaming the binary fails here instead of probing a stale name live.
func TestRuntimeAvailabilityLiveTestsProbeContractBinaries(t *testing.T) {
	for _, c := range CodingAgentProviderContracts() {
		if c.Transport != CodingAgentTransportTmux {
			continue
		}
		var testFile string
		for _, cert := range CodingAgentProviderCertifications(c.Provider) {
			if cert.ID == CertRuntimeAvailability {
				testFile = cert.TestFile
			}
		}
		if testFile == "" {
			t.Errorf("%s has no runtime_availability registration", c.Provider)
			continue
		}
		raw, err := os.ReadFile(filepath.Clean(testFile))
		if err != nil {
			t.Errorf("%s: unreadable proof file %s: %v", c.Provider, testFile, err)
			continue
		}
		if !strings.Contains(string(raw), `"`+c.RuntimeBinary+`"`) {
			t.Errorf("%s proof %s does not probe contract binary %q", c.Provider, testFile, c.RuntimeBinary)
		}
	}
}

// TestTranscriptReaderContractMatchesRegistry mirrors the resume drift
// test for AdapterReadsTranscript. Contract claim must match registry
// membership in both directions, AND the contract's TranscriptPathTemplate
// must equal the registry's PathTemplate when both are set — so docs in
// the contract stay aligned with what the registry advertises.
func TestTranscriptReaderContractMatchesRegistry(t *testing.T) {
	for _, c := range CodingAgentProviderContracts() {
		info, hasReader := TranscriptReaderFor(c.Provider)
		if c.AdapterReadsTranscript != hasReader {
			t.Errorf("transcript-reader contract drift for %s: contract.AdapterReadsTranscript=%v but transcriptReaderRegistry presence=%v. Update coding_agent_contract.go and coding_agent_transcript_registry.go together — see the comment block in coding_agent_transcript_registry.go for the 3-step checklist.",
				c.Provider, c.AdapterReadsTranscript, hasReader)
			continue
		}
		if hasReader && info.PathTemplate != c.TranscriptPathTemplate {
			t.Errorf("transcript path template drift for %s: contract=%q registry=%q. Keep them in sync.",
				c.Provider, c.TranscriptPathTemplate, info.PathTemplate)
		}
		if !hasReader && c.TranscriptPathTemplate != "" {
			t.Errorf("%s has TranscriptPathTemplate=%q but no entry in transcriptReaderRegistry — register the reader or clear the template",
				c.Provider, c.TranscriptPathTemplate)
		}
	}
	// Registry entries may serve an optional transport. The selected transport
	// above must expose only its own reader, while every entry needs a provider.
	for provider := range transcriptReaderRegistry {
		if _, ok := GetCodingAgentProviderContract(provider, ""); !ok {
			t.Errorf("transcript reader registered for unknown provider %s", provider)
		}
	}
}

// TestAPIKeyEnvVarsContractIsWellFormed catches drift in the APIKeyEnvVars
// dimension by enforcing two invariants:
//
//  1. Every coding-agent contract must declare APIKeyEnvVars. nil is
//     treated as "the operator forgot to populate the field" and fails the
//     test. To declare "CLI uses native auth, no env shortcut", use an
//     explicit empty slice ([]string{}). The distinction matters because
//     a nil here is almost always an oversight when adding a new CLI.
//  2. Listed env var names must be SHOUT_SNAKE_CASE letters/digits/_ — no
//     spaces, hyphens, or accidentally-pasted values. Catches typos like
//     "ANTHROPIC API KEY" or "ANTHROPIC-API-KEY".
//
// This intentionally does NOT verify the adapter actually reads each
// listed env var (that needs adapter-level integration tests we may add
// later). It only enforces the contract's own internal consistency.
func TestAPIKeyEnvVarsContractIsWellFormed(t *testing.T) {
	for _, c := range CodingAgentProviderContracts() {
		if c.APIKeyEnvVars == nil {
			t.Errorf("%s has APIKeyEnvVars=nil — declare an explicit []string (use []string{} if the CLI uses native auth only, never nil)", c.Provider)
			continue
		}
		for _, name := range c.APIKeyEnvVars {
			if strings.TrimSpace(name) == "" {
				t.Errorf("%s APIKeyEnvVars contains empty entry: %v", c.Provider, c.APIKeyEnvVars)
				continue
			}
			for _, r := range name {
				ok := (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
				if !ok {
					t.Errorf("%s APIKeyEnvVars entry %q contains invalid character %q — env vars must be SHOUT_SNAKE_CASE", c.Provider, name, string(r))
					break
				}
			}
		}
	}
}

// TestStructuredStreamingContractMatchesRegistry guards the
// SupportsStructuredStreaming dimension the same way the resume/transcript-reader
// drift tests do: the flag must agree, in both directions, with the presence of a
// registered CertStructuredStreaming certification — so "this provider streams
// structured chunks" can never be a bare claim without a real E2E behind it, and
// a registered streaming cert can never sit under a provider that doesn't claim
// the capability. The streaming SOURCE is provider-specific (transcript tail vs
// injected markers), so this does NOT require AdapterReadsTranscript.
func TestStructuredStreamingContractMatchesRegistry(t *testing.T) {
	for _, c := range CodingAgentProviderContracts() {
		hasStreamingCert := false
		for _, cert := range CodingAgentProviderCertifications(c.Provider) {
			if cert.ID == CertStructuredStreaming {
				hasStreamingCert = true
				if !cert.RealE2E {
					t.Errorf("%s CertStructuredStreaming must be a real streaming E2E: %#v", c.Provider, cert)
				}
			}
		}
		if c.SupportsStructuredStreaming != hasStreamingCert {
			t.Errorf("structured-streaming contract drift for %s: contract.SupportsStructuredStreaming=%v but a registered CertStructuredStreaming exists=%v. Register the streaming E2E in codingAgentProviderCertifications and flip the contract flag together.",
				c.Provider, c.SupportsStructuredStreaming, hasStreamingCert)
		}
	}
}

func TestCodingAgentProviderContractsAreSorted(t *testing.T) {
	contracts := CodingAgentProviderContracts()
	if len(contracts) == 0 {
		t.Fatal("expected coding-agent contracts")
	}
	for i := 1; i < len(contracts); i++ {
		prev, cur := contracts[i-1], contracts[i]
		if prev.Provider > cur.Provider || (prev.Provider == cur.Provider && prev.ModelID > cur.ModelID) {
			t.Fatalf("contracts are not sorted at %d: %#v before %#v", i, prev, cur)
		}
	}
}

// knownCertificationGaps tracks certifications a coding-agent contract
// CLAIMS via its capability bools but has not yet provided an e2e test for.
// Listed IDs are TOLERATED — they're a public TODO list, not silently
// ignored. Removing an ID from this map without adding the certification
// entry will fail TestAllCodingAgentCapabilityClaimsHaveRegisteredCertification.
//
// History: every provider's coding-agent capability claims were originally
// enforced only for Claude Code + Codex. Cursor, Gemini
// declared the same capabilities without proof, which let real bugs ship
// (e.g. cursor's "claims UsesMCPBridge=true" while in --print mode it
// returned zero tokens / zero tool calls on a real workflow run). The
// allowance below makes that drift visible to anyone touching the file.
//
// Drain this map by writing the missing e2e tests + registering them in
// codingAgentProviderCertifications.
var knownCertificationGaps = map[Provider][]CodingAgentCertificationID{
	// Muse onboarding (2026-09-10): all release-blocking P0s are registered
	// with live E2E proofs; what remains below is the capability-derived
	// follow-up set (AdapterReadsTranscript satisfies
	// reply_formatting_fidelity, so it is deliberately absent). Remove IDs
	// as proofs land — the staleness guard fails the suite otherwise.
	ProviderMuseCLI: {
		CertBoundedRetention,
		CertCleanup,
		CertLifecyclePolicy,
		CertNativeSystemPrompt,
		CertParallelStartupQueue,
		CertPromptPaste,
		CertResumeCompactionStartup,
		CertSessionLoss,
		CertSessionLossRecovery,
		CertSharedWorkdirMCPIsolation,
		CertSlowToolLiveInput,
		CertStaleDraftCleanup,
		CertStartupTerminalVisibility,
	},
	// Agy onboarding (2026-09-20): all release-blocking P0s are registered
	// with live E2E proofs (bridge_only_tools via the best-effort
	// exception); what remains below is the same capability-derived
	// follow-up set Muse carries. Remove IDs as proofs land — the
	// staleness guard fails the suite otherwise.
	ProviderAgyCLI: {
		CertBoundedRetention,
		CertCleanup,
		CertLifecyclePolicy,
		CertNativeSystemPrompt,
		CertParallelStartupQueue,
		CertPromptPaste,
		CertResumeCompactionStartup,
		CertSessionLoss,
		CertSessionLossRecovery,
		CertSharedWorkdirMCPIsolation,
		CertSlowToolLiveInput,
		CertStaleDraftCleanup,
		CertStartupTerminalVisibility,
	},
}

func TestKnownCertificationGapsCannotWaiveP0(t *testing.T) {
	for provider, gaps := range knownCertificationGaps {
		for _, id := range gaps {
			if CodingAgentCertificationPriorityForID(id) == CodingAgentCertificationPriorityP0 {
				t.Errorf("%s knownCertificationGaps contains release-blocking %s; P0 gaps cannot be waived", provider, id)
			}
		}
	}
}

// TestStreamNoHistoryReplayIsRealE2EOnly enforces that stream_no_history_replay
// can only ever be REGISTERED on real-CLI evidence, for every provider —
// including Claude Code and Codex, whose other certifications may still be
// deterministic.
//
// The reason is specific to this contract: the bug it guards only manifests when
// the transcript PRE-DATES the process, so a deterministic test has to synthesize
// that state (a hand-built transcript plus an empty dedup map). That proves the
// mechanism, and those tests exist and are kept — but a synthetic fixture cannot
// prove the REAL CLI writes its transcript in the shape the fixture assumes.
// Only a live run spanning a process restart does, which is exactly the coverage
// the four live streaming E2Es lacked when this bug shipped.
func TestStreamNoHistoryReplayIsRealE2EOnly(t *testing.T) {
	for _, contract := range CodingAgentProviderContracts() {
		for _, cert := range CodingAgentProviderCertifications(contract.Provider) {
			if cert.ID != CertStreamNoHistoryReplay {
				continue
			}
			if !cert.RealE2E {
				t.Errorf("%s: %s must be backed by a real CLI E2E spanning a process restart, "+
					"not a deterministic fixture (got %#v). Keep the deterministic test as a "+
					"regression guard and list the ID in knownCertificationGaps until the real "+
					"E2E exists.", contract.Provider, cert.ID, cert)
			}
		}
	}
}

// TestAllCodingAgentCapabilityClaimsHaveRegisteredCertification iterates every
// coding-agent contract and asserts that every capability the contract claims
// has a corresponding entry in codingAgentProviderCertifications — proving an
// actual e2e test exists for that claim instead of trusting the bool blindly.
//
// Providers in knownCertificationGaps are allowed to have ALL listed IDs
// missing; any deviation (different missing set, or new gap) fails the test.
// The allowance is intentionally low-friction so adding a new provider
// surfaces its gap immediately rather than silently skipping checks.
func TestAllCodingAgentCapabilityClaimsHaveRegisteredCertification(t *testing.T) {
	for _, contract := range CodingAgentProviderContracts() {
		missing := MissingCodingAgentCertifications(contract)
		if len(missing) == 0 {
			continue
		}
		allowed := knownCertificationGaps[contract.Provider]
		// Build a set of allowed IDs for fast lookup.
		allowedSet := make(map[CodingAgentCertificationID]struct{}, len(allowed))
		for _, id := range allowed {
			allowedSet[id] = struct{}{}
		}
		// missing IDs not in allowedSet → real new failures.
		var unexpected []CodingAgentCertificationID
		for _, id := range missing {
			if _, ok := allowedSet[id]; !ok {
				unexpected = append(unexpected, id)
			}
		}
		if len(unexpected) > 0 {
			t.Errorf("%s claims capabilities without registered certification: %v. If this is intentional, add the IDs to knownCertificationGaps and file a follow-up task to write the e2e tests.",
				contract.Provider, unexpected)
		}
		// Also flag stale allowances — an ID listed in knownCertificationGaps
		// that is no longer missing means somebody added the cert entry but
		// forgot to remove the allowance. Drop it to keep the gap list honest.
		for _, id := range allowed {
			stillMissing := false
			for _, m := range missing {
				if m == id {
					stillMissing = true
					break
				}
			}
			if !stillMissing {
				t.Errorf("%s knownCertificationGaps still lists %s but the certification is now registered — remove the allowance",
					contract.Provider, id)
			}
		}
	}
}

func TestCodingAgentCertificationReferencesExistingTests(t *testing.T) {
	for provider := range codingAgentProviderCertifications {
		certs := CodingAgentProviderCertifications(provider)
		if len(certs) == 0 {
			t.Fatalf("%s has no registered certifications", provider)
		}
		seen := make(map[CodingAgentCertificationID]string, len(certs))
		for _, cert := range certs {
			if cert.ID == "" {
				t.Fatalf("%s has certification with empty id: %#v", provider, cert)
			}
			if previous := seen[cert.ID]; previous != "" {
				t.Fatalf("%s certification %s registered twice: %s and %s", provider, cert.ID, previous, cert.TestName)
			}
			seen[cert.ID] = cert.TestName
			if strings.TrimSpace(cert.TestFile) == "" || strings.TrimSpace(cert.TestName) == "" {
				t.Fatalf("%s certification %s must name a test file and test function: %#v", provider, cert.ID, cert)
			}
			path := filepath.Clean(cert.TestFile)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s certification %s references unreadable test file %s: %v", provider, cert.ID, path, err)
			}
			if !strings.Contains(string(raw), "func "+cert.TestName+"(") {
				t.Fatalf("%s certification %s references missing test %s in %s", provider, cert.ID, cert.TestName, path)
			}
		}
	}
}

func TestActiveCodingAgentProvidersSatisfyP0Contract(t *testing.T) {
	for _, contract := range CodingAgentProviderContracts() {
		if contract.Transport != CodingAgentTransportTmux {
			continue
		}
		if missing := MissingP0CodingAgentCertifications(contract); len(missing) > 0 {
			t.Fatalf("%s is missing release-blocking P0 certifications: %v", contract.Provider, missing)
		}

		byID := make(map[CodingAgentCertificationID]CodingAgentCertification)
		for _, cert := range CodingAgentProviderCertifications(contract.Provider) {
			byID[cert.ID] = cert
		}
		for _, id := range RequiredP0CodingAgentCertificationIDs(contract) {
			cert := byID[id]
			if cert.Priority != CodingAgentCertificationPriorityP0 {
				t.Fatalf("%s certification %s priority = %q, want P0", contract.Provider, id, cert.Priority)
			}
			if !cert.RealE2E {
				t.Fatalf("%s P0 certification %s must be backed by a real CLI E2E: %#v", contract.Provider, id, cert)
			}
			if len(cert.Env) != 1 || cert.Env[0] != "-coding-cli-p0-live" {
				t.Fatalf("%s P0 certification %s must use only the live P0 gate, got %#v", contract.Provider, id, cert.Env)
			}
		}
	}
}

func TestCodingAgentP0ReleaseMatrix(t *testing.T) {
	matrix, err := CodingAgentP0ReleaseMatrix()
	if err != nil {
		t.Fatalf("release matrix error = %v", err)
	}
	if len(matrix) == 0 {
		t.Fatal("release matrix must not be empty")
	}
	// The default release matrix must equal the active release-provider
	// set: every tmux, non-deprecated provider with P0 requirements,
	// computed here via the default-contract lookup rather than the
	// matrix's first-contract scan.
	want := make(map[Provider]bool)
	seen := make(map[Provider]bool)
	for _, contract := range CodingAgentProviderContracts() {
		if seen[contract.Provider] {
			continue
		}
		seen[contract.Provider] = true
		def, ok := GetCodingAgentProviderContract(contract.Provider, "")
		if !ok || (def.Transport != CodingAgentTransportTmux && def.Transport != CodingAgentTransportAppServer && def.Transport != CodingAgentTransportRPC && def.Transport != CodingAgentTransportACP) {
			continue
		}
		if len(RequiredP0CodingAgentCertificationIDs(def)) == 0 {
			continue
		}
		want[def.Provider] = true
	}
	if len(matrix) != len(want) {
		t.Fatalf("release matrix has %d providers, want %d (%v)", len(matrix), len(want), want)
	}
	previous := Provider("")
	for _, entry := range matrix {
		if !want[entry.Provider] {
			t.Fatalf("release matrix has unexpected provider %s", entry.Provider)
		}
		delete(want, entry.Provider)
		if previous != "" && entry.Provider <= previous {
			t.Fatalf("release matrix not sorted: %s after %s", entry.Provider, previous)
		}
		previous = entry.Provider
		// The package must match every registered P0 test's location:
		// a wrong package would run an empty selection and pass vacuously.
		def, _ := GetCodingAgentProviderContract(entry.Provider, "")
		need := make(map[CodingAgentCertificationID]bool)
		for _, id := range RequiredP0CodingAgentCertificationIDs(def) {
			need[id] = true
		}
		checked := 0
		for _, cert := range CodingAgentProviderCertifications(entry.Provider) {
			if !need[cert.ID] {
				continue
			}
			checked++
			if dir := filepath.ToSlash(filepath.Dir(cert.TestFile)); dir != entry.Package {
				t.Fatalf("%s P0 test %s lives in %s, want package %s", entry.Provider, cert.TestName, dir, entry.Package)
			}
		}
		if checked == 0 {
			t.Fatalf("%s release entry has no registered P0 tests", entry.Provider)
		}
	}
	if len(want) != 0 {
		t.Fatalf("release matrix omits active providers %v", want)
	}
	// Deterministic across calls: release tooling resolves this fresh.
	again, err := CodingAgentP0ReleaseMatrix()
	if err != nil {
		t.Fatalf("second release matrix error = %v", err)
	}
	if len(again) != len(matrix) {
		t.Fatalf("second matrix has %d rows, want %d", len(again), len(matrix))
	}
	for i := range matrix {
		if again[i] != matrix[i] {
			t.Fatalf("row %d = %+v on repeat, want %+v", i, again[i], matrix[i])
		}
	}
}

func TestPersistentProviderP0RequiresBothTransportMultiTurnProofs(t *testing.T) {
	for _, contract := range CodingAgentProviderContracts() {
		if !contract.UsesPersistentSession || !contract.SupportsNativeResume {
			continue
		}
		required := make(map[CodingAgentCertificationID]bool)
		for _, id := range RequiredP0CodingAgentCertificationIDs(contract) {
			required[id] = true
		}
		for _, id := range []CodingAgentCertificationID{CertMultiTurn, CertStructuredMultiTurn} {
			if !required[id] {
				t.Errorf("%s P0 must require %s", contract.Provider, id)
			}
		}
	}
}

func TestPiCLICertificationsUseRealE2EOnly(t *testing.T) {
	certs := CodingAgentProviderCertifications(ProviderPiCLI)
	if len(certs) == 0 {
		t.Fatal("pi-cli has no registered certifications")
	}
	for _, cert := range certs {
		if !cert.RealE2E {
			t.Fatalf("pi-cli certification %s must be backed by real Pi E2E, got %#v", cert.ID, cert)
		}
		hasPiEnvGuard := false
		for _, env := range cert.Env {
			if env == "-coding-cli-p0-live" || strings.HasPrefix(env, "RUN_PI_CLI_") || env == "RUN_CODING_AGENT_CONTINUATION_REAL_E2E=1" {
				hasPiEnvGuard = true
				break
			}
		}
		if !hasPiEnvGuard {
			t.Fatalf("pi-cli certification %s must name its real E2E env guard, got %#v", cert.ID, cert.Env)
		}
	}
}

func TestCursorCLICertificationsUseRealE2EOnly(t *testing.T) {
	certs := CodingAgentProviderCertifications(ProviderCursorCLI)
	if len(certs) == 0 {
		t.Fatal("cursor-cli has no registered certifications")
	}
	for _, cert := range certs {
		if !cert.RealE2E {
			t.Fatalf("cursor-cli certification %s must be backed by real Cursor E2E, got %#v", cert.ID, cert)
		}
		hasCursorEnvGuard := false
		for _, env := range cert.Env {
			if env == "-coding-cli-p0-live" || strings.HasPrefix(env, "RUN_CURSOR_CLI_") {
				hasCursorEnvGuard = true
				break
			}
		}
		if !hasCursorEnvGuard {
			t.Fatalf("cursor-cli certification %s must name its real E2E env guard, got %#v", cert.ID, cert.Env)
		}
	}
}

func TestClaudeAndCodexSessionLossRecoveryCertificationUsesRealE2E(t *testing.T) {
	for _, provider := range []Provider{ProviderClaudeCode, ProviderCodexCLI} {
		var found *CodingAgentCertification
		for _, cert := range CodingAgentProviderCertifications(provider) {
			if cert.ID == CertSessionLossRecovery {
				certCopy := cert
				found = &certCopy
				break
			}
		}
		if found == nil {
			t.Fatalf("%s missing %s certification", provider, CertSessionLossRecovery)
		}
		if !found.RealE2E {
			t.Fatalf("%s %s certification must be marked RealE2E: %#v", provider, CertSessionLossRecovery, *found)
		}
		if provider == ProviderCodexCLI {
			if found.TestName != "TestCodexAppServerLifecycleP0" {
				t.Fatal("Codex needs real app-server process-loss recovery proof")
			}
			continue
		}
		if found.TestFile != "coding_agent_continuation_real_test.go" || found.TestName != "TestCodingAgentContinuationRealE2EAfterTmuxLoss" {
			t.Fatalf("%s %s points at %s/%s, want real tmux-loss continuation E2E", provider, CertSessionLossRecovery, found.TestFile, found.TestName)
		}
		wantEnv := "RUN_CODING_AGENT_CONTINUATION_REAL_E2E=1"
		hasEnv := false
		for _, env := range found.Env {
			if env == wantEnv {
				hasEnv = true
				break
			}
		}
		if !hasEnv {
			t.Fatalf("%s %s missing env guard %q: %#v", provider, CertSessionLossRecovery, wantEnv, found.Env)
		}
	}
}

func TestBestEffortToolRestrictionsRequireExplicitMuseGaps(t *testing.T) {
	muse := codingAgentProviderContracts[ProviderMuseCLI]
	if muse.SupportsBridgeOnlyTools || !acceptsBestEffortToolRestrictions(muse) {
		t.Fatal("Muse must explicitly document best-effort gaps without claiming strict containment")
	}
	if CodingAgentCertificationPriorityForID(CertBestEffortToolRestrictions) != CodingAgentCertificationPriorityP0 {
		t.Fatal("best-effort restrictions must still have a P0 proof")
	}
	for _, tc := range []struct {
		name     string
		contract CodingAgentProviderContract
	}{
		{"missing gap documentation", func() CodingAgentProviderContract { c := muse; c.ToolRestrictionGaps = nil; return c }()},
		{"another provider", func() CodingAgentProviderContract { c := muse; c.Provider = ProviderCursorCLI; return c }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if acceptsBestEffortToolRestrictions(tc.contract) {
				t.Fatal("unexpected exception")
			}
			for _, required := range [][]CodingAgentCertificationID{RequiredP0CodingAgentCertificationIDs(tc.contract), RequiredCodingAgentCertificationIDs(tc.contract)} {
				found := false
				for _, id := range required {
					if id == CertBridgeOnlyTools {
						found = true
					}
					if id == CertBestEffortToolRestrictions {
						t.Fatal("unexpected best-effort requirement")
					}
				}
				if !found {
					t.Fatal("strict proof requirement disappeared")
				}
			}
		})
	}
}

// TestPlanUsageContractIsWellFormed forces every provider to decide about plan
// usage (5h/7d windows the chat UI shows and the coding watchdog trusts over
// screen text): either it surfaces them from a named source, or it states why
// it cannot. A new provider cannot silently skip the question.
func TestPlanUsageContractIsWellFormed(t *testing.T) {
	valid := map[string]bool{"app-server": true, "msp": true, "statusline-sidecar": true, "transcript-file": true}
	for _, c := range CodingAgentProviderContracts() {
		if c.SurfacesPlanUsage {
			if !valid[c.PlanUsageSource] {
				t.Errorf("%s claims SurfacesPlanUsage but PlanUsageSource=%q is not one of statusline-sidecar|transcript-file", c.Provider, c.PlanUsageSource)
			}
			if c.PlanUsageUnavailableReason != "" {
				t.Errorf("%s surfaces plan usage but also states PlanUsageUnavailableReason", c.Provider)
			}
			continue
		}
		if c.PlanUsageSource != "" {
			t.Errorf("%s has PlanUsageSource=%q but SurfacesPlanUsage=false", c.Provider, c.PlanUsageSource)
		}
		if strings.TrimSpace(c.PlanUsageUnavailableReason) == "" {
			t.Errorf("%s does not surface plan usage and gives no PlanUsageUnavailableReason — decide: surface it (and certify CertPlanUsage) or say why it cannot", c.Provider)
		}
	}
}

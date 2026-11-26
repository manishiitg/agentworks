package llmproviders

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// CodingAgentSecurityCapability is a human-readable permission which maps to
// provider-specific filesystem details. UIs should show the label/reason and
// keep path templates under optional technical details.
type CodingAgentSecurityCapability struct {
	ID                 string   `json:"id"`
	Label              string   `json:"label"`
	Reason             string   `json:"reason"`
	Risk               string   `json:"risk"`
	ReadPathTemplates  []string `json:"read_path_templates,omitempty"`
	WritePathTemplates []string `json:"write_path_templates,omitempty"`
	Environment        []string `json:"environment,omitempty"`
}

// CodingAgentSecurityProfile is a versioned provider baseline. Certified is
// false until real provider E2E coverage proves the profile is sufficient.
type CodingAgentSecurityProfile struct {
	Provider            Provider                        `json:"provider"`
	DisplayName         string                          `json:"display_name"`
	Version             string                          `json:"version"`
	Executables         []string                        `json:"executables"`
	SupportsPrivateHome bool                            `json:"supports_private_home"`
	Certified           bool                            `json:"certified"`
	Capabilities        []CodingAgentSecurityCapability `json:"capabilities"`
}

var codingAgentSecurityProfiles = map[Provider]CodingAgentSecurityProfile{
	ProviderClaudeCode: providerSecurityProfile(
		ProviderClaudeCode, "Claude Code", "claude", "~/.claude", false,
	),
	ProviderCodexCLI: providerSecurityProfile(
		ProviderCodexCLI, "Codex CLI", "codex", "~/.codex", true,
	),
	ProviderCursorCLI: providerSecurityProfile(
		ProviderCursorCLI, "Cursor CLI", "cursor-agent", "~/.cursor", false,
	),
	ProviderPiCLI: providerSecurityProfile(
		ProviderPiCLI, "Pi", "pi", "~/.pi", false,
	),
}

func providerSecurityProfile(provider Provider, displayName, executable, providerHome string, certified bool) CodingAgentSecurityProfile {
	return CodingAgentSecurityProfile{
		Provider:            provider,
		DisplayName:         displayName,
		Version:             "1",
		Executables:         []string{executable},
		SupportsPrivateHome: true,
		Certified:           certified,
		Capabilities: []CodingAgentSecurityCapability{
			{
				ID:                 "provider_identity",
				Label:              "Use your existing " + displayName + " account and history",
				Reason:             "Provides the CLI login, preferences, cache, and resumable conversation state.",
				Risk:               "account",
				ReadPathTemplates:  []string{providerHome},
				WritePathTemplates: []string{providerHome},
			},
		},
	}
}

// GetCodingAgentSecurityProfile returns a deep copy of the provider baseline.
func GetCodingAgentSecurityProfile(provider Provider) (CodingAgentSecurityProfile, bool) {
	profile, ok := codingAgentSecurityProfiles[normalizeCodingAgentProvider(provider)]
	if !ok {
		return CodingAgentSecurityProfile{}, false
	}
	return cloneCodingAgentSecurityProfile(profile), true
}

// CodingAgentSecurityProfiles lists provider profiles in the same stable order
// as the public coding-agent contract catalog.
func CodingAgentSecurityProfiles() []CodingAgentSecurityProfile {
	contracts := CodingAgentProviderContracts()
	profiles := make([]CodingAgentSecurityProfile, 0, len(contracts))
	for _, contract := range contracts {
		if profile, ok := GetCodingAgentSecurityProfile(contract.Provider); ok {
			profiles = append(profiles, profile)
		}
	}
	return profiles
}

// WithCLISecurityPolicy attaches a deep-copied launch policy to a provider call.
func WithCLISecurityPolicy(policy llmtypes.CLISecurityPolicy) llmtypes.CallOption {
	resolved := policy.Clone()
	return func(opts *llmtypes.CallOptions) {
		copyPolicy := resolved.Clone()
		// The application's run-as decision covers every launch file and folder of this call.
		copyPolicy.DeclareRunAs()
		opts.CLISecurity = &copyPolicy
	}
}

func cloneCodingAgentSecurityProfile(profile CodingAgentSecurityProfile) CodingAgentSecurityProfile {
	copyProfile := profile
	copyProfile.Provider = Provider(strings.ToLower(strings.TrimSpace(string(profile.Provider))))
	copyProfile.Executables = append([]string(nil), profile.Executables...)
	copyProfile.Capabilities = make([]CodingAgentSecurityCapability, len(profile.Capabilities))
	for i, capability := range profile.Capabilities {
		copyCapability := capability
		copyCapability.ReadPathTemplates = append([]string(nil), capability.ReadPathTemplates...)
		copyCapability.WritePathTemplates = append([]string(nil), capability.WritePathTemplates...)
		copyCapability.Environment = append([]string(nil), capability.Environment...)
		copyProfile.Capabilities[i] = copyCapability
	}
	return copyProfile
}

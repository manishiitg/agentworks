package agentworksproduct

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// BuiltinAgentProfile returns the agentworks profile. Brand new, so only
// one version is ever registered -- same reasoning as every other product's
// own BuiltinAgentProfile.
func BuiltinAgentProfile() agentprofiles.Profile {
	manifest := mustAgentWorksManifest()
	profile := manifest.Profile
	profile.SystemPromptTemplate = renderProductPrompt()
	return profile
}

func BuiltinAgentProfiles() []agentprofiles.Profile {
	return []agentprofiles.Profile{BuiltinAgentProfile()}
}

// RegisterProductSkills has no global skills to register. Builder/Run bundles
// are materialized per session with capability and mode filtering.
func RegisterProductSkills() error {
	return nil
}

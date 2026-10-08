package agentworksproduct

import (
	"fmt"
	"path"
	"strings"
)

// PulseTools are the only tools the workflow Pulse agent gets (product.yaml
// pulse.tools): it reads, keeps its own records and talks to the Builder chat.
func PulseTools() []string {
	if def := mustAgentWorksManifest().Pulse; def != nil {
		return append([]string(nil), def.Tools...)
	}
	return nil
}

// PulseSkills are the skills in Pulse's own pack (product.yaml pulse.skills).
func PulseSkills() []string {
	if def := mustAgentWorksManifest().Pulse; def != nil {
		return append([]string(nil), def.Skills...)
	}
	return nil
}

// PulseWritePaths are the workflow-relative folders Pulse may write (product.yaml
// pulse.write_paths); the rest of the workflow is read-only to it.
func PulseWritePaths() []string {
	if def := mustAgentWorksManifest().Pulse; def != nil {
		return append([]string(nil), def.WritePaths...)
	}
	return nil
}

func validatePulse(m ProductManifest) error {
	def := m.Pulse
	if def == nil {
		return fmt.Errorf("AgentWorks product.yaml must declare pulse")
	}
	if len(def.Tools) == 0 {
		return fmt.Errorf("pulse.tools is required")
	}
	seen := map[string]bool{}
	for _, name := range def.Tools {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return fmt.Errorf("pulse.tools: empty or duplicate %q", name)
		}
		seen[name] = true
	}
	if len(def.Skills) == 0 {
		return fmt.Errorf("pulse.skills is required")
	}
	skillSeen := map[string]bool{}
	for _, name := range def.Skills {
		name = strings.TrimSpace(name)
		if name == "" || skillSeen[name] {
			return fmt.Errorf("pulse.skills: empty or duplicate %q", name)
		}
		skillSeen[name] = true
	}
	for _, p := range def.WritePaths {
		clean := path.Clean(strings.TrimSpace(p))
		if clean == "." || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") || !strings.HasSuffix(p, "/") {
			return fmt.Errorf("pulse.write_paths: %q must be a workflow-relative folder ending in /", p)
		}
	}
	return nil
}

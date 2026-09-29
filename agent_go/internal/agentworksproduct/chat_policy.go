package agentworksproduct

import "fmt"

// ChatCapabilities is a fresh set: callers cannot mutate the embedded manifest.
func ChatCapabilities(mode, origin string, readOnly bool) map[string]bool {
	p := mustAgentWorksManifest().ChatPolicy
	result := map[string]bool{}
	if p == nil {
		return result
	}
	contains := func(values []string, key string) bool {
		for _, value := range values {
			if value == key {
				return true
			}
		}
		return false
	}
	originCapabilities := p.Origins[origin]
	if mode == "builder" && origin == "bot" {
		originCapabilities = p.Modes["builder"]
	}
	for _, capability := range p.Modes[mode] {
		if contains(originCapabilities, capability) && (!readOnly || contains(p.ReadOnly, capability)) {
			result[capability] = true
		}
	}
	return result
}

func validateChatPolicy(m ProductManifest) error {
	if m.ChatPolicy == nil {
		return fmt.Errorf("AgentWorks chat_policy is required")
	}
	known := map[string]bool{"user_management": true, "mcp_management": true, "mcp_inspection": true, "plan_authoring": true, "report_authoring": true, "secret_management": true, "knowledgebase_maintenance": true, "improvement_proposals": true, "workspace_ui": true, "bot_management": true, "workflow_suggestions": true}
	validate := func(values []string) error {
		seen := map[string]bool{}
		for _, value := range values {
			if !known[value] || seen[value] {
				return fmt.Errorf("invalid or duplicate chat capability %q", value)
			}
			seen[value] = true
		}
		return nil
	}
	for _, group := range []struct {
		values map[string][]string
		names  []string
	}{
		{m.ChatPolicy.Modes, []string{"builder", "run"}},
		{m.ChatPolicy.Origins, []string{"interactive", "scheduled", "pulse", "child", "bot", "notification", "external_builder"}},
	} {
		if len(group.values) != len(group.names) {
			return fmt.Errorf("unexpected chat policy mode/origin")
		}
		for _, name := range group.names {
			values, ok := group.values[name]
			if !ok {
				return fmt.Errorf("missing chat policy %q", name)
			}
			if err := validate(values); err != nil {
				return err
			}
		}
	}
	return validate(m.ChatPolicy.ReadOnly)
}

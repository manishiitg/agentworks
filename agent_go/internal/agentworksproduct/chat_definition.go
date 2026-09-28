package agentworksproduct

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func validateChatDefinitions(fsys fs.FS, m ProductManifest) error {
	if len(m.Chat) != 2 {
		return fmt.Errorf("AgentWorks chat must declare builder and run")
	}
	for _, mode := range []string{"builder", "run"} {
		def, ok := m.Chat[mode]
		if !ok {
			return fmt.Errorf("missing AgentWorks chat %q", mode)
		}
		if _, err := agentprofiles.LoadChatPrompt(fsys, def.Prompt); err != nil {
			return fmt.Errorf("chat %s: %w", mode, err)
		}
		known := map[string]bool{"system-tools": true, "builder-reference": true, "workflow-ui-control": true, "workflow-commands": mode == "builder", "ui-ux-pro-max": mode == "builder"}
		seen := map[string]bool{}
		for _, name := range def.Skills {
			if !known[name] || seen[name] {
				return fmt.Errorf("chat %s: unknown or duplicate skill %q", mode, name)
			}
			seen[name] = true
		}
		if len(seen) == 0 {
			return fmt.Errorf("chat %s: skills are required", mode)
		}
		toolSeen := map[string]bool{}
		for _, name := range def.Tools {
			name = strings.TrimSpace(name)
			if name == "" || toolSeen[name] {
				return fmt.Errorf("chat %s: empty or duplicate tool %q", mode, name)
			}
			toolSeen[name] = true
		}
		// External Run tools and the four explicit Builder operations have separate admission lists.
		if mode == "run" {
			if len(def.ExternalTools) == 0 {
				return fmt.Errorf("chat run: external_tools are required")
			}
		}
		if mode == "builder" {
			expected := map[string]bool{"builder_chat": true, "builder_status": true, "builder_reply_input": true, "builder_cancel": true, "builder_file_history": true, "builder_restore_file": true}
			for _, name := range def.ExternalTools {
				if !expected[name] {
					return fmt.Errorf("invalid Builder external tool %q", name)
				}
				delete(expected, name)
			}
			if len(expected) != 0 {
				return fmt.Errorf("Builder external operations are incomplete")
			}
		}
		if mode != "run" && len(def.ExternalDenylist) != 0 {
			return fmt.Errorf("chat %s: external_denylist belongs on the run mode only", mode)
		}
		denySeen := map[string]bool{}
		for _, name := range def.ExternalDenylist {
			name = strings.TrimSpace(name)
			if name == "" || denySeen[name] {
				return fmt.Errorf("chat %s: empty or duplicate external_denylist entry %q", mode, name)
			}
			denySeen[name] = true
			if !toolSeen[name] {
				return fmt.Errorf("chat %s: external_denylist entry %q is not a run tool", mode, name)
			}
		}
	}
	return nil
}

// ChatTools returns the tool admission list declared for a chat mode. Tool
// schemas and executors remain implemented in Go; product.yaml decides which
// of those implementations a Builder or Run conversation may receive.
func ChatTools(mode string) []string {
	def, ok := mustAgentWorksManifest().Chat[mode]
	if !ok {
		panic(fmt.Errorf("unknown AgentWorks chat mode %q", mode))
	}
	return append([]string(nil), def.Tools...)
}

func ChatAllowsTool(mode, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	for _, name := range ChatTools(mode) {
		if name == toolName {
			return true
		}
	}
	return false
}

// RunExternalTools returns the external CLI/MCP tool admission list declared
// by the run chat mode. These are the external-native tools: scoped readers
// plus JSON-direct run operations, each with a server implementation.
func RunExternalTools() []string {
	def, ok := mustAgentWorksManifest().Chat["run"]
	if !ok {
		panic(fmt.Errorf("unknown AgentWorks chat mode %q", "run"))
	}
	return append([]string(nil), def.ExternalTools...)
}

// RunTools returns the run-mode chat tool list. This is the single source of
// truth for the run surface: the server proxies every name here except the
// external denylist to a pinned Run-mode session, so a tool added to run
// mode is callable externally under runs:execute unless denylisted.
// Names with an external-native implementation keep that implementation
// (see external_tools.go).
func RunTools() []string {
	return ChatTools("run")
}

// RunExternalDenylist returns run.tools names withheld from the external
// CLI/MCP catalog and token-backed Run sessions. Bot Run channels keep them.
func RunExternalDenylist() []string {
	def, ok := mustAgentWorksManifest().Chat["run"]
	if !ok {
		panic(fmt.Errorf("unknown AgentWorks chat mode %q", "run"))
	}
	return append([]string(nil), def.ExternalDenylist...)
}

// ChatPromptTemplate returns trusted template source; callers inject runtime
// context with their existing strict template renderer.
func ChatPromptTemplate(mode string) string {
	def, ok := mustAgentWorksManifest().Chat[mode]
	if !ok {
		panic(fmt.Errorf("unknown AgentWorks chat mode %q", mode))
	}
	text, err := agentprofiles.LoadChatPrompt(productConfigFiles, def.Prompt)
	if err != nil {
		panic(err)
	}
	return text
}

// ChatSkills returns a copy of the configured core bundles. Workflow-selected
// skills remain additive and use the existing workflow skill loader.
func ChatSkills(mode string) []string {
	def, ok := mustAgentWorksManifest().Chat[mode]
	if !ok {
		panic(fmt.Errorf("unknown AgentWorks chat mode %q", mode))
	}
	return append([]string(nil), def.Skills...)
}

// ChatDefinitionKey refreshes retained coding-CLI sessions when any declared
// chat surface changes across a deployment.
func ChatDefinitionKey(mode string) string {
	sum := sha256.Sum256([]byte(ChatPromptTemplate(mode) + "\x00" + strings.Join(ChatSkills(mode), "\x00") + "\x00" + strings.Join(ChatTools(mode), "\x00")))
	return fmt.Sprintf("%x", sum[:])
}

// BuilderExternalTools exposes only the delegated Builder operation contract.
func BuilderExternalTools() []string {
	return append([]string(nil), mustAgentWorksManifest().Chat["builder"].ExternalTools...)
}

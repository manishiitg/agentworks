package mcpagent

import (
	"context"
	"strings"

	"github.com/manishiitg/mcpagent/agent/prompt"
	llm "github.com/manishiitg/multi-llm-provider-go"
)

const (
	availableToolsOpenTag  = "<available_tools>"
	availableToolsCloseTag = "</available_tools>"
)

// effectiveSystemPrompt composes product and runtime discovery instructions
// at the final read boundary. The stored systemPrompt is not a
// source of tool truth: registration and allow-list state can change after it
// was set, and custom prompts can replace it entirely.
func (a *Agent) effectiveSystemPromptForContext(ctx context.Context) string {
	// systemPrompt is always the caller/product-owned base. Everything
	// mcpagent discovers or contributes (MCP guidance, runtime routing, tools,
	// skills) is an extension and must never replace that identity.
	instructions := composeInstructionExtensions(a.systemPrompt, a.appendedSystemPrompts...)
	return a.composeEffectiveSystemPromptForContext(ctx, instructions)
}

// composeInstructionExtensions preserves the base as the first instruction
// block and appends non-empty extensions in order. Keeping this composition in
// one helper prevents inspection and send paths from quietly disagreeing about
// whether an mcpagent-owned block replaces a product prompt.
func composeInstructionExtensions(base string, extensions ...string) string {
	result := strings.TrimSpace(base)
	for _, extension := range extensions {
		extension = strings.TrimSpace(extension)
		if extension == "" {
			continue
		}
		if result == "" {
			result = extension
			continue
		}
		result = prompt.NormalizeForAppend(result) + "\n\n" + extension
	}
	return result
}

// outgoingSystemPrompt is the exact instruction string placed on outbound
// model requests and mirrored into prompt events/debug output.
func (a *Agent) outgoingSystemPrompt() string {
	return a.outgoingSystemPromptForContext(context.Background())
}

func (a *Agent) outgoingSystemPromptForContext(ctx context.Context) string {
	systemPrompt := a.effectiveSystemPromptForContext(ctx)
	// Most coding CLIs receive attached skills through their native on-disk
	// skill projection. Repeating the catalog inside AGENTS.md/CLAUDE.md makes
	// the CLI discover every skill twice. API models have no native projection,
	// so they still need the prompt listing. Agy currently also needs this fallback.
	if !llm.IsCodingAgentProvider(a.provider, a.modelID) || a.provider == llm.ProviderAgyCLI {
		if listing := renderSkillListing(a.attachedSkills); listing != "" {
			if systemPrompt != "" {
				return systemPrompt + "\n\n" + listing
			}
			return listing
		}
	}
	return systemPrompt
}

// composeEffectiveSystemPrompt is shared by actual send paths, exported prompt
// reads, and prompt-event/log rendering. This keeps what operators inspect
// identical to what the model receives.
func (a *Agent) composeEffectiveSystemPromptForContext(ctx context.Context, base string) string {
	// A single runtime section is composed from current permissions. Caller
	// instructions and arbitrary AddInstructions supplements remain untouched.
	if a.bridgeRoutingPreamble != nil || a.useCodeExecutionMode {
		base = removeTaggedSections(base, "<runtime_tools>", "</runtime_tools>")
		routing := ""
		if a.bridgeRoutingInstructionsOverride != nil {
			routing = *a.bridgeRoutingInstructionsOverride
			if a.useCodeExecutionMode {
				routing = strings.TrimSpace(routing + "\n" + runtimeDiscoveryInstructions)
			}
		} else {
			if a.bridgeRoutingPreamble != nil {
				if llm.IsCodingAgentProvider(a.provider, a.modelID) {
					routing = a.codingAgentProviderRoutingPreambleForContext(ctx)
				} else {
					routing = *a.bridgeRoutingPreamble
				}
			}
			admits := func(name string) bool { return a.admitsCoreBridgeTool(name) && a.isToolAllowedForContext(ctx, name) }
			exposed := make([]string, 0, len(a.additionalBridgeTools))
			for _, name := range a.additionalBridgeTools {
				if name == "read_image" || name == "read_skill" {
					exposed = append(exposed, name)
				} else if _, ok := a.lookupDirectTool(name); ok {
					exposed = append(exposed, name)
				}
			}
			routing = strings.TrimSpace(routing + "\n" + bridgeRoutingInstructions(admits, a.hasRuntimeHTTPSkill(), exposed...))
		}
		if routing != "" {
			base = strings.TrimSpace(base + "\n\n<runtime_tools>\n" + routing + "\n</runtime_tools>")
		}
	}
	// Never reinsert an upfront catalog, even for legacy callers that omitted
	// Discovery or restored prompts containing an older inventory. Search and
	// schema lookup consult the live authorized registry instead. Native API
	// schemas remain supplied through the provider's normal tool-call surface.
	return stripToolInventory(base)
}

// stripToolInventory removes stored catalogs and unresolved inventory markers.
// Prompt composition never generates or inserts a catalog.
func stripToolInventory(base string) string {
	return strings.TrimSpace(removeTaggedSections(strings.ReplaceAll(base, "{{TOOL_STRUCTURE}}", ""), availableToolsOpenTag, availableToolsCloseTag))
}

func firstTaggedRange(input, openTag, closeTag string) (int, int, bool) {
	start := strings.Index(input, openTag)
	if start < 0 {
		return 0, 0, false
	}
	relEnd := strings.Index(input[start+len(openTag):], closeTag)
	if relEnd < 0 {
		return 0, 0, false
	}
	end := start + len(openTag) + relEnd + len(closeTag)
	return start, end, true
}

func removeTaggedSections(input, openTag, closeTag string) string {
	for {
		start, end, ok := firstTaggedRange(input, openTag, closeTag)
		if !ok {
			return input
		}
		input = input[:start] + input[end:]
	}
}

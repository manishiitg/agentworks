package guidance

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// StepExecutionSignals describes what an executing step agent actually holds,
// as opposed to which workshop mode it happens to run under.
//
// PLAT-125: step execution has no mode of its own, so it borrowed the workshop
// chat's — the mode a human uses to *build* a workflow. That handed every step
// agent 41 builder reference docs, among them llm-provider-config, which
// instructs the reader to call list_published_llms. A social-media
// message-sequence sub-agent holding eight tools did exactly that and got
// `tools_unavailable`; with no way left to ask which providers were published,
// it then guessed provider names ("vertex", "minimax-coding-plan", …),
// producing 19 further tool failures.
//
// Selecting by capability makes that class unrepresentable rather than fixing
// the one instance: a doc cannot be attached to a session that lacks the tools
// it describes. It also corrects the mirror case, where a session holds tools
// whose doc its mode excludes.
type StepExecutionSignals struct {
	// ToolNames are the tools actually registered for this agent.
	ToolNames []string

	// CodeExecutionMode reports whether MCP tools are reached over the HTTP
	// bridge, which is what mcp-bridge documents.
	CodeExecutionMode bool

	// ScriptedStep reports a scripted step, the only surface that authors
	// main.py and therefore the only one code-authoring applies to.
	ScriptedStep bool
}

// stepExecutionSignalKinds are the reference kinds selected by a signal other
// than a registered tool name. Keeping them explicit means a kind with no
// Tools entry is never selected by accident.
var stepExecutionSignalKinds = map[string]func(StepExecutionSignals) bool{
	"mcp-bridge":     func(s StepExecutionSignals) bool { return s.CodeExecutionMode },
	"code-authoring": func(s StepExecutionSignals) bool { return s.ScriptedStep },
}

// MaterializeStepExecutionReferenceSkill bundles only the reference docs whose
// subject the step agent can actually act on. Returns nil when nothing
// qualifies, so the caller can skip attachment entirely.
//
// The skill keeps the stable "builder-reference" identity that every other
// surface uses, so prompts and read_skill paths do not need to know which
// surface materialized the bundle.
func MaterializeStepExecutionReferenceSkill(signals StepExecutionSignals) *llmtypes.Skill {
	held := make(map[string]struct{}, len(signals.ToolNames))
	for _, name := range signals.ToolNames {
		if name = strings.TrimSpace(name); name != "" {
			held[name] = struct{}{}
		}
	}

	return buildMegaSkill(buildMegaSkillSpec{
		Registry:         referenceKinds,
		Name:             "builder-reference",
		DescriptionIntro: "Workflow execution reference docs — the contracts behind the tools this step actually holds.",
		Intro: "This skill bundles reference documentation for the tools available to this step. It deliberately contains " +
			"only topics you can act on here — workflow design, Pulse review, and platform administration are not part of " +
			"this step's job and are not included. Read the single matching file under `references/`.",
		Render: renderReferenceKind,
		Select: func(kind string, meta kindMeta) bool {
			// Saved Python harnesses also need the compatibility/lifecycle contract,
			// even when the agent invokes them through the scripted runner.
			if kind == "playwright-scripted" && signals.ScriptedStep {
				return true
			}
			if qualifies, ok := stepExecutionSignalKinds[kind]; ok {
				return qualifies(signals)
			}
			for _, tool := range meta.Tools {
				if _, ok := held[tool]; ok {
					return true
				}
			}
			return false
		},
	})
}

package server

import (
	"log"
	"strings"
)

// The system prompt is assembled from independent sections, each with its own
// condition. Those conditions used to live in ~14 consecutive inline `if`s in
// handleQuery, every one discarding AddInstructions' error with `_ =`, and no
// two conditions readable against each other.
//
// That produced a real defect: the "CLI Tool Environment" section asserts "Your
// native tools (Bash, Read, Write, etc.) are disabled" and was gated only on
// "is this a CLI provider" — never on the profile — although the profile was in
// scope 118 lines above. Injected into a hybrid profile it contradicted the
// product prompt, and the contradiction won: Codex concluded it had no shell
// and reported the product broken. Diagnosing it meant reconstructing the
// assembled prompt from a coding agent's session transcript, because nothing
// recorded which sections had been applied.
//
// So each section is named, its condition is a field rather than an `if`, and
// the assembler logs what it included and skipped. See
// docs/bugs/hybrid_profile_told_it_has_no_shell.md.
//
// Deliberately NOT modeled here: applying the profile prompt (it calls
// ResetInstructions and fails the turn), and attaching skills or reference
// surfaces (AttachSkill, not instructions). This registry owns instruction
// text; it does not own agent lifecycle.
type promptSection struct {
	// Name is a stable identifier. It appears in the assembly log and in tests,
	// and is the handle for a section that otherwise can only be found by
	// recognizing its wording.
	Name string
	// Applies decides whether this session gets the section. Keeping it beside
	// every other section's condition is the point: the contradiction above was
	// invisible while the two conditions sat 118 lines apart.
	Applies func(promptContext) bool
	// Build returns the section text. An empty result is skipped and recorded,
	// so "had nothing to say" and "was not applicable" stay distinguishable.
	Build func(promptContext) string
}

// promptContext is everything the conditions and builders may read. It is
// assembled once from handleQuery's locals so a section cannot reach for a
// value the caller did not intend to expose, and so `Applies` stays a pure
// function of stated inputs rather than of whatever happened to be in scope.
type promptContext struct {
	Provider        string
	ProfileID       string
	HasProfile      bool
	IsWorkflowPhase bool
	// HasTriggerAutoNotifyTool is set only after the tool is registered for
	// this chat. Keep its guidance paired with the actual tool surface.
	HasTriggerAutoNotifyTool bool
	// NativeCodingTools is true for agent_tools.mode=hybrid: the coding CLI
	// keeps its provider-specific native subset. Sections that describe a
	// bridge-only world must not apply when this is set.
	NativeCodingTools bool

	ShellRoot           string
	PerUserChatsFolder  string
	WorkflowPhaseFolder string
	ProfileWorkspace    string

	// HasLLMCapabilityTools reports whether this session can actually call the
	// tools the capability snapshot names. A profile allow-list may exclude all
	// of them, in which case the snapshot is instructions for a surface that is
	// not there.
	HasLLMCapabilityTools bool

	// Prebuilt text for sections whose construction needs a request context or
	// other state the registry deliberately does not carry.
	CapabilitySection   string
	WorkflowMode        string
	WorkflowUIAvailable bool
	WorkflowContext     string
	WorkFolders         string
	ChannelFormatting   string
	BrowserPointer      string
	GrantSections       []string
	CLIToolEnvironment  string
	// FeatureExtensions come from the trusted feature catalog resolved from
	// product.yaml. They extend the product prompt; they never replace it.
	FeatureExtensions []string
}

const governedProjectMemoryInstructions = `## Persistent project memory

- Use the project root MEMORY.md as the one durable memory store shared by every chat, schedule, bot, webhook-triggered task, and background task in this project. Keep memory visible in the normal project file browser. Do not create another memory file, memory folder, memory index, or memory skill.
- Provider-native instruction and memory surfaces are not alternate stores. Never create, update, or invoke Claude auto-memory, Cursor Memories, Cursor rules, Codex AGENTS.md, or any provider memory tool to retain learned project information. AgentWorks may temporarily project this system prompt through a provider instruction file; do not edit that file. Persist learned information only in the project-root MEMORY.md.
- Before saying project-specific information is unknown or starting new research, read MEMORY.md when it exists. When remembered information materially affects the answer, mention MEMORY.md so the user can inspect it.
- Save stable, verified information likely to help future work without waiting for the user to repeat a request. Keep the file concise and reverse chronological using this Markdown shape (newest entry first):

  # Project Memory

  ## YYYY-MM-DD — Topic
  - **Summary:** One sentence stating the durable fact, preference, or decision.
  - **Details:** Only the context future work needs.
  - **Source:** Where it was verified and the verification date, when that matters.
  - **Related skill:** ` + "`skills/<skill-name>/SKILL.md`" + ` when an applicable project-local skill exists.

  Omit fields that add no value. Keep bullets short, use one topic per dated heading, and update or merge an existing topic instead of appending a duplicate. Replace or remove stale entries when newer evidence contradicts them. Do not turn MEMORY.md into a raw activity log.
- Never create or update a skill as a side effect of learning something. Skills change only when the user explicitly asks to create, import, install, or change one.
- Do not save guesses, transient status, raw conversation, credentials, secret values, or sensitive personal information unless the user explicitly asks for it to be retained. Never turn unverified research into memory. Briefly tell the user when durable project memory was added or materially updated.
- Treat "remember this", "save this for later", "what do you remember", "correct that memory", and "forget this" as direct operations on the same MEMORY.md file.`

// promptSections is the assembly order. Order is the slice order — previously
// it was "wherever the if happened to sit".
var promptSections = []promptSection{
	{
		// Compact folder listing with absolute paths and access levels. One of
		// three variants; every session gets exactly one.
		Name:    "workspace-map",
		Applies: func(promptContext) bool { return true },
		Build: func(c promptContext) string {
			switch {
			case c.IsWorkflowPhase:
				return getWorkflowPhaseWorkspaceMapForMode(c.ShellRoot, c.WorkflowPhaseFolder, c.WorkflowMode)
			case c.ProfileID == "work":
				chatHistory := newWorkspacePaths(c.ShellRoot, c.PerUserChatsFolder).ChatHistory
				return GetWorkWorkspaceMap(c.ProfileWorkspace, chatHistory)
			case c.HasProfile:
				return GetWorkspaceMap(c.ShellRoot, c.ProfileWorkspace)
			default:
				return GetWorkspaceMap(c.ShellRoot, c.PerUserChatsFolder)
			}
		},
	},
	{
		// Product and workflow sessions share one visible project-level memory
		// contract regardless of provider. Plain unscoped chats have no project
		// root and therefore do not receive a misleading persistence promise.
		Name:    "project-memory",
		Applies: func(c promptContext) bool { return c.HasProfile || c.IsWorkflowPhase },
		Build:   func(promptContext) string { return governedProjectMemoryInstructions },
	},
	{
		Name:    "product-features",
		Applies: func(c promptContext) bool { return c.HasProfile && len(c.FeatureExtensions) > 0 },
		Build:   func(c promptContext) string { return strings.Join(c.FeatureExtensions, "\n\n") },
	},
	{
		Name:    "trigger-auto-notify",
		Applies: func(c promptContext) bool { return c.HasTriggerAutoNotifyTool },
		Build:   func(promptContext) string { return triggerAutoNotifyPrompt },
	},
	{
		// A provider/auth inventory that also instructs the agent to call
		// list_llm_capabilities and set_provider_auth. A profile with an allow-list may have none of them
		// — Video Studio has none — and naming absent tools is how several
		// defects in docs/bugs/ started. It also tells the agent to fetch the
		// same inventory via a tool, so injecting it is redundant where the tool
		// exists and wrong where it does not.
		Name:    "llm-capability",
		Applies: func(c promptContext) bool { return c.HasLLMCapabilityTools },
		Build:   func(c promptContext) string { return c.CapabilitySection },
	},
	{
		Name:    "workflow-context",
		Applies: func(promptContext) bool { return true },
		Build:   func(c promptContext) string { return c.WorkflowContext },
	},
	{
		// Owner-attached external folders for Work sessions (aliases +
		// WORK_FOLDER_<ALIAS> env). Empty for every other surface.
		Name:    "work-folders",
		Applies: func(c promptContext) bool { return c.ProfileID == "work" },
		Build:   func(c promptContext) string { return c.WorkFolders },
	},
	{
		// Which markup subset the bot platform renders, so replies do not arrive
		// with "## Headers" that WhatsApp/Slack display literally. Empty for the
		// chat UI.
		Name:    "channel-formatting",
		Applies: func(promptContext) bool { return true },
		Build:   func(c promptContext) string { return c.ChannelFormatting },
	},
	{
		// A one-line pointer, not the full guide: the browser reference is a
		// ~10KB skill loaded on demand rather than paid for every turn.
		Name:    "browser-pointer",
		Applies: func(promptContext) bool { return true },
		Build:   func(c promptContext) string { return c.BrowserPointer },
	},
	{
		Name:    "grants",
		Applies: func(c promptContext) bool { return len(c.GrantSections) > 0 },
		Build:   func(c promptContext) string { return strings.Join(c.GrantSections, "\n") },
	},
	{
		// Bridge-only by construction: it asserts the CLI's native tools are
		// disabled and names execute_shell_command as their replacement. True
		// for mcp_only, false for hybrid — which is the defect this registry
		// was written after.
		Name:    "cli-tool-environment",
		Applies: func(c promptContext) bool { return c.CLIToolEnvironment != "" && !c.NativeCodingTools },
		Build:   func(c promptContext) string { return c.CLIToolEnvironment },
	},
}

// instructionAppender is the slice of the agent this assembly needs. Narrow so
// the registry can be tested without constructing an agent.
type instructionAppender interface {
	AddInstructions(...string) error
}

// assemblePromptSections appends every applicable section and returns the
// included and skipped names for logging. An AddInstructions failure is
// returned rather than dropped: all 20 previous call sites discarded it.
func assemblePromptSections(agent instructionAppender, ctx promptContext) (included, skipped []string, err error) {
	for _, section := range promptSections {
		if !section.Applies(ctx) {
			skipped = append(skipped, section.Name)
			continue
		}
		text := section.Build(ctx)
		if strings.TrimSpace(text) == "" {
			skipped = append(skipped, section.Name+"(empty)")
			continue
		}
		if addErr := agent.AddInstructions(text); addErr != nil {
			return included, skipped, &promptSectionError{Section: section.Name, Err: addErr}
		}
		included = append(included, section.Name)
	}
	return included, skipped, nil
}

type promptSectionError struct {
	Section string
	Err     error
}

func (e *promptSectionError) Error() string {
	return "prompt section " + e.Section + ": " + e.Err.Error()
}

func (e *promptSectionError) Unwrap() error { return e.Err }

// logPromptAssembly records what the agent was actually told. This is the line
// whose absence made the contradiction above take hours to find, while the
// equivalent tool-surface line ([PRODUCT_TOOL_GATE] registered=… filtered=…)
// made its half take minutes.
func logPromptAssembly(ctx promptContext, included, skipped []string) {
	profile := ctx.ProfileID
	if profile == "" {
		profile = "-"
	}
	log.Printf("[PROMPT_SECTIONS] profile=%s provider=%s hybrid=%t included=%v skipped=%v",
		profile, ctx.Provider, ctx.NativeCodingTools, included, skipped)
}

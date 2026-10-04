package server

import (
	"log"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/instructions"
)

// The system prompt is assembled from independent sections, each with its own
// condition. Those conditions used to live in ~14 consecutive inline `if`s in
// handleQuery, every one discarding AddInstructions' error with `_ =`, and no
// two conditions readable against each other.
//
// That produced a real defect: the "CLI Tool Environment" section asserts "Your
// native tools (Bash, Read, Write, etc.) are disabled" and was gated only on
// "is this a CLI provider" — never on the profile — although the profile was in
// scope 118 lines above. Injected into a native-tools profile it contradicted the
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
	CrewReadOnly    bool
	MemoryReadOnly  bool
	// HasTriggerAutoNotifyTool is set only after the tool is registered for
	// this chat. Keep its guidance paired with the actual tool surface.
	HasTriggerAutoNotifyTool bool
	// NativeCodingTools is true when the chat really starts in Full CLI (its
	// own tools in a sandbox). Sections that describe a
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

// codeHostSafetyInstructions are the rules for a Code project's agent. A Code project is a shared
// server that other people's projects also run on; its agent may write code, install packages and
// run tools for the project, but must keep to the project and never turn the server into a service
// for someone else (Ashutosh's Code chat, excellence, 2026-09-30: a browser IDE opened to the
// internet and used to browse the server's folders).
const codeHostSafetyInstructions = `## Working on a shared server

This project runs on a server that other people's projects share. Keep to your own project.

- Create project files in your working folder. Write to explicitly listed read_write attached folders only through guarded file tools. Never use "~" (the CLI's private hidden folder), "/tmp" or unlisted host folders as project storage; files there are invisible to the user.
- Read only your working folder and additional paths explicitly authorized in this prompt, including the signed-in user's chat history for requested history lookups. Follow each listed access level and use guarded tools where required. Never inspect unlisted server folders or other people's projects. Never read environment variables, ".env" files, credentials or keys that were not given to you for this task.
- Do not install, start or expose remote-access tools: browser IDEs (code-server and similar), SSH or remote-desktop servers, or VPNs. Do not bind any port to all network interfaces or the public internet. A local dev server for the project is fine when it listens on 127.0.0.1 only. A tunnel (cloudflared, ngrok and similar) is allowed when the user asks for one: say once that it makes the app reachable by anyone with the link.
- Do not install or run anything harmful or unrelated to the project: cryptocurrency miners, scanners, botnets, credential or data harvesters, or tools that try to get around this environment's limits or other people's access controls. Do not run other autonomous coding agents or piped remote install scripts ("curl ... | bash") to set up such tools.
- Use ordinary project dependencies (npm, pip, go modules) inside the working folder. If a request needs something outside these rules, say so and ask the user instead of doing it.`

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
			case isProjectProfileID(c.ProfileID):
				chatHistory := newWorkspacePaths(c.ShellRoot, c.PerUserChatsFolder).ChatHistory
				return GetWorkWorkspaceMap(resolveWorkspacePath(c.ShellRoot, c.ProfileWorkspace), chatHistory)
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
		Build: func(c promptContext) string {
			return instructions.ProjectMemoryPrompt(c.MemoryReadOnly || c.CrewReadOnly || (c.IsWorkflowPhase && c.WorkflowMode == "run"))
		},
	},
	{
		// Code only: a private coding workspace whose agent can install and run things on a server
		// shared with other people's projects.
		Name:    "code-host-safety",
		Applies: func(c promptContext) bool { return c.ProfileID == codeproduct.ProfileID },
		Build:   func(promptContext) string { return codeHostSafetyInstructions },
	},
	{
		Name:    "product-features",
		Applies: func(c promptContext) bool { return c.HasProfile && !c.CrewReadOnly && len(c.FeatureExtensions) > 0 },
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
		Applies: func(c promptContext) bool { return isProjectProfileID(c.ProfileID) },
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
	{
		// Full CLI has its own subagents for parallel work. There is no separate
		// background-agent tool: reviews are done by the agent itself.
		Name:    "native-subagents",
		Applies: func(c promptContext) bool { return c.NativeCodingTools },
		Build:   func(promptContext) string { return nativeSubagentsGuidance },
	},
}

const nativeSubagentsGuidance = `## Subagents
Use your own subagents for parallel lookups, analysis and edits within this turn. ` + "`execute_step`" + ` and ` + "`run_full_workflow`" + ` already run in the background; call them directly yourself in this chat, one after another or together, and read their results. There is no separate background-agent tool.
A review, audit or check that must not change anything: do it yourself, or give it to a subagent with an explicit instruction not to write, edit or run anything with side effects, and confirm afterwards that nothing changed.`

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
	log.Printf("[PROMPT_SECTIONS] profile=%s provider=%s native_tools=%t included=%v skipped=%v",
		profile, ctx.Provider, ctx.NativeCodingTools, included, skipped)
}

package agentprofiles

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

type CapabilityRequirement string

const (
	CapabilityRequired  CapabilityRequirement = "required"
	CapabilityPreferred CapabilityRequirement = "preferred"
	CapabilityOptional  CapabilityRequirement = "optional"
	CapabilityDisabled  CapabilityRequirement = "disabled"
)

type RuntimePolicy struct {
	Transport       string           `json:"transport" yaml:"transport"`
	Provider        string           `json:"provider,omitempty" yaml:"provider,omitempty"`
	ModelID         string           `json:"model_id,omitempty" yaml:"model_id,omitempty"`
	ProviderOptions []ProviderOption `json:"provider_options,omitempty" yaml:"provider_options,omitempty"`
	// CredentialScope selects where the coding-agent provider credential comes
	// from. Empty/workspace preserves the historical per-project override;
	// global uses only the server-wide credential and ignores saved project
	// overrides. This does not affect product generation secrets.
	CredentialScope string `json:"credential_scope,omitempty" yaml:"credential_scope,omitempty"`
	// RequireProviderToken forces this profile to refuse a claude-code turn
	// outright when no explicit provider token is resolved, instead of
	// falling back to whatever `claude` CLI login happens to exist on the
	// host. Independent of, and in addition to, the server's own
	// single-product-deployment heuristic (isSingleProductServerDeployment in
	// cmd/server) -- that heuristic is a deployment-topology default any
	// product gets for free on a dedicated server without touching this
	// file; this field is a per-product override for a profile that must
	// never rely on an ambient CLI login, even in a shared or desktop
	// context (for example, a fixed-workspace, single-tenant product with no
	// legitimate desktop use case). False preserves every existing profile's
	// current behavior.
	RequireProviderToken bool                `json:"require_provider_token,omitempty" yaml:"require_provider_token,omitempty"`
	Capabilities         RuntimeCapabilities `json:"capabilities" yaml:"capabilities"`
	// AgentTools selects whether a coding provider receives only AgentWorks MCP
	// tools (mcp_only) or its own tools in a sandbox (full; the retired
	// "hybrid" reads as full). Full keeps the MCP execute_shell_command bridge;
	// only APITransport native_shell is mutually exclusive with that route.
	// Empty preserves mcp_only for existing profiles.
	AgentTools AgentToolsPolicy `json:"agent_tools,omitempty" yaml:"agent_tools,omitempty"`
	// Approvals controls the native-tool approval policy when AgentTools enables
	// native tools. Empty preserves provider_auto.
	Approvals ApprovalsPolicy `json:"approvals,omitempty" yaml:"approvals,omitempty"`
	// APITransport selects how product-specific HTTP APIs are invoked. Native
	// shell keeps the guarded AgentWorks shell tool absent while giving a native
	// coding CLI its scoped, session-bound API endpoint environment.
	APITransport APITransportPolicy `json:"api_transport,omitempty" yaml:"api_transport,omitempty"`
	// Workspace declares where this product's own artifacts belong, in the
	// product's own vocabulary. Write-capable workspace tools append these lines
	// to their description, so a product states its layout once here instead of
	// inheriting AgentWorks' (plan folders, pulse/goals.html) — which a product
	// without those concepts would be told to use anyway, contradicting the
	// placement rules its system prompt had just given.
	Workspace WorkspacePolicy `json:"workspace,omitempty" yaml:"workspace,omitempty"`
	// Conversation declares how a product maps its domain objects to durable
	// chats. The browser supplies only the key (when one is required); the
	// server owns the durable conversation/session identity.
	Conversation ConversationPolicy `json:"conversation,omitempty" yaml:"conversation,omitempty"`

	// Sandbox declares how this profile's shell and file tools are confined.
	// Empty keeps the platform default (the folder guard: the conversation's
	// workspace is writable, a few platform folders are read-only, and the
	// command runs in an allow-by-default kernel sandbox with network).
	Sandbox SandboxPolicy `json:"sandbox,omitempty" yaml:"sandbox,omitempty"`
}

// WorkspacePolicy carries a product's declared artifact placement rules.
// SandboxPolicy confines a profile's shell and file tools. It is enforced by
// the workspace shell isolator (workspace/security), on top of the folder
// guard that already limits paths to the conversation's workspace.
type SandboxPolicy struct {
	// Mode: "" or "folder" is the platform default (allow-by-default kernel
	// sandbox, only the workspace paths are controlled). "strict" is a
	// deny-by-default sandbox: only the workspace folder, the read-only
	// folders below, system binaries and scratch space are visible, and the
	// command's environment carries no secrets. Fully enforced on macOS
	// (seatbelt); on Linux the path rules apply but network is not cut.
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
	// Network: "" or "allowed" keeps outbound network; "disabled" removes it
	// (strict mode only).
	Network string `json:"network,omitempty" yaml:"network,omitempty"`
	// ReadOnly lists workspace-relative folders the profile may read but not
	// write, replacing the platform default of skills/, subagents/ and
	// Downloads/. Use an explicit empty list to grant nothing extra.
	ReadOnly []string `json:"read_only,omitempty" yaml:"read_only,omitempty"`
	// ChatHistory: "" keeps the platform default — the account's chat_history/
	// (every conversation's log, this profile's and others') is readable and
	// writable from the profile's shell, so an assistant can recall earlier
	// chats. "none" hides it: a profile whose user must see one folder only
	// (a child's activity) never reads the rest of the family's conversations.
	// The server keeps writing the profile's own log either way.
	ChatHistory string `json:"chat_history,omitempty" yaml:"chat_history,omitempty"`
}

const (
	SandboxModeFolder      = "folder"
	SandboxModeStrict      = "strict"
	SandboxNetworkAllowed  = "allowed"
	SandboxNetworkOff      = "disabled"
	SandboxChatHistoryNone = "none"
)

// ChatHistoryDenied reports whether the profile's shell must not see the
// account's chat_history/ folder.
func (p SandboxPolicy) ChatHistoryDenied() bool {
	return strings.EqualFold(strings.TrimSpace(p.ChatHistory), SandboxChatHistoryNone)
}

// IsStrict reports whether the policy asks for the deny-by-default sandbox.
func (p SandboxPolicy) IsStrict() bool {
	return strings.EqualFold(strings.TrimSpace(p.Mode), SandboxModeStrict)
}

// NetworkDisabled reports whether outbound network must be cut.
func (p SandboxPolicy) NetworkDisabled() bool {
	return strings.EqualFold(strings.TrimSpace(p.Network), SandboxNetworkOff)
}

// PromptSource says where a profile's system prompt file lives inside the
// product's embedded files, with simple {{NAME}} substitutions applied when
// the manifest is loaded (see RenderPromptTemplate).
type PromptSource struct {
	File      string            `json:"-" yaml:"file"`
	Variables map[string]string `json:"-" yaml:"variables,omitempty"`
}

type WorkspacePolicy struct {
	// Mode is fixed for products with one workspace and project for products
	// whose conversation key selects a project manifest below ProjectsRoot.
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
	// Root is the server-owned default workspace for profile-chat turns. A
	// product with no user-selected project (for example Dominion) declares it
	// here so its chat client never has to send an AgentWorks folder path.
	// Project-based products leave it empty and keep supplying their selected
	// project workspace through their product surface.
	Root string `json:"root,omitempty" yaml:"root,omitempty"`
	// ProjectsRoot is the server-owned parent containing project manifests for
	// a project-bound product. A client sends the manifest's project id, never
	// a path below this root.
	ProjectsRoot string `json:"projects_root,omitempty" yaml:"projects_root,omitempty"`
	// Placement is keyed by tool name: each tool's description gets only the
	// lines declared for it. Per-tool rather than one shared block because the
	// advice that helps differs by tool — where a shell command should write is
	// not what a patch tool needs to be told — and because a product declares
	// rules only for tools it actually exposes. Lines are appended verbatim and
	// read by the model, so phrase each as an instruction naming a concrete path.
	Placement map[string][]string `json:"placement,omitempty" yaml:"placement,omitempty"`
}

// ConversationPolicy controls durable product-conversation identity.
type ConversationPolicy struct {
	// Mode is singleton (one conversation per user/profile) or keyed (one per
	// domain resource, such as a Video Studio project).
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
	// KeyType names the domain key for diagnostics and validation. The first
	// keyed binding is project; future products can add another resolver without
	// widening the public chat request to paths or runtime configuration.
	KeyType string `json:"key_type,omitempty" yaml:"key_type,omitempty"`
}

const (
	ConversationModeSingleton  = "singleton"
	ConversationModeKeyed      = "keyed"
	ConversationKeyTypeProject = "project"
	WorkspaceModeFixed         = "fixed"
	WorkspaceModeProject       = "project"
	CredentialScopeWorkspace   = "workspace"
	CredentialScopeGlobal      = "global"
)

type AgentToolsPolicy struct {
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

type ApprovalsPolicy struct {
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

type APITransportPolicy struct {
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

// ProviderOption is a product-approved coding-agent runtime choice. The
// profile runtime remains authoritative: a request may select only one of
// these YAML-declared bindings, never an arbitrary global AgentWorks model.
type ProviderOption struct {
	ID       string `json:"id" yaml:"id"`
	Label    string `json:"label" yaml:"label"`
	Provider string `json:"provider" yaml:"provider"`
	ModelID  string `json:"model_id" yaml:"model_id"`
	Default  bool   `json:"default,omitempty" yaml:"default,omitempty"`
	// Options are provider-specific runtime options applied to every turn on
	// this binding (the same keys a published LLM entry carries, e.g.
	// reasoning_effort for the coding CLIs). Product-owned, like the model.
	// A declared ReasoningEfforts value lets a turn override the
	// "reasoning_effort" key here specifically; every other key is fixed.
	Options map[string]interface{} `json:"options,omitempty" yaml:"options,omitempty"`
	// Models narrows the composer's model switcher (and the server's accepted
	// model_id) to exactly these ids under this engine, in the order to show
	// them — curation over the platform's full model catalog. Empty offers
	// every model the catalog lists for Provider (the prior, uncurated
	// behavior); this never widens past what the catalog actually has.
	Models []string `json:"models,omitempty" yaml:"models,omitempty"`
	// ReasoningEfforts, when non-empty, offers a reasoning-level control in
	// the composer for this engine and lets a turn's reasoning_effort request
	// override the matching key in Options (every other key in Options stays
	// fixed). Empty means no such control — Options.reasoning_effort, if any,
	// is fixed for every turn on this engine, as before.
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty" yaml:"reasoning_efforts,omitempty"`
}

type RuntimeCapabilities struct {
	LiveInput         CapabilityRequirement `json:"live_input" yaml:"live_input"`
	RawTerminal       CapabilityRequirement `json:"raw_terminal" yaml:"raw_terminal"`
	WarmSession       CapabilityRequirement `json:"warm_session" yaml:"warm_session"`
	WorkflowExecution CapabilityRequirement `json:"workflow_execution,omitempty" yaml:"workflow_execution,omitempty"`
	// Browser controls access to AgentWorks' shared managed agent-browser
	// capability. Products opt in by declaring a requirement; they never carry
	// their own copy of the browser tool or its version-matched skill.
	Browser CapabilityRequirement `json:"browser,omitempty" yaml:"browser,omitempty"`
	// Secrets enables the shared AgentWorks encrypted-secret selection and
	// injection flow for a product agent. Values are never part of the prompt;
	// selected values are supplied only as shell environment variables.
	Secrets CapabilityRequirement `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	// MCPSelection, SkillSelection, and WorkflowReferences opt a
	// general-purpose product into the matching shared AgentWorks composer
	// features. Fixed-purpose products leave these disabled so their profile
	// remains the only source of tools, skills, and external context.
	MCPSelection       CapabilityRequirement `json:"mcp_selection,omitempty" yaml:"mcp_selection,omitempty"`
	SkillSelection     CapabilityRequirement `json:"skill_selection,omitempty" yaml:"skill_selection,omitempty"`
	WorkflowReferences CapabilityRequirement `json:"workflow_references,omitempty" yaml:"workflow_references,omitempty"`
	// Voice enables the shared AgentWorks streaming speech-to-text service
	// (/api/voice/stream). Products opt in by declaring a requirement; they
	// never carry their own STT engine, model, or websocket handling — the
	// same pattern as Browser. A disabled/empty value hides the composer's mic
	// control entirely rather than showing a button that would 404.
	Voice CapabilityRequirement `json:"voice,omitempty" yaml:"voice,omitempty"`
	// NewConversation lets the product's UI offer "start a new chat" for this
	// profile's conversation (POST /api/agent-profiles/{id}/conversation/new).
	// The previous conversation stays in history; only the live one rotates.
	// Disabled/empty hides the control: a product whose conversation is bound
	// to a resource (a video project, a child's activity) manages that
	// lifecycle itself.
	NewConversation CapabilityRequirement `json:"new_conversation,omitempty" yaml:"new_conversation,omitempty"`
	// WhatsApp lets this profile be the account's default WhatsApp destination:
	// a message in the paired chat that names no @<slug> workflow route runs as
	// a turn in this profile's own conversation (the one its app shows),
	// through the platform's shared WhatsApp connector. Products opt in by
	// declaring a requirement; the connector, pairing and routing are the
	// platform's. Disabled/empty means the profile can never be that
	// destination, and its UI hides the pairing controls.
	WhatsApp CapabilityRequirement `json:"whatsapp,omitempty" yaml:"whatsapp,omitempty"`
}

type ToolBinding struct {
	ID     string          `json:"id" yaml:"id"`
	Config json.RawMessage `json:"config,omitempty" yaml:"config,omitempty"`
	// Presentation declares that this tool's execution presents something in
	// the product's UI (a video, a report, a document — one row in the shared
	// ui_presentations table per showing). It is data, not behavior: the tool
	// factory still owns validation and payload shape, but it reads the kind
	// from here rather than hardcoding it, so the declaration is load-bearing
	// instead of decorative. See pkg/presentations and
	// docs/design/agent_tool_surface_single_source.md for why a declared fact
	// beats a fact re-derived per call site.
	Presentation *PresentationBinding `json:"presentation,omitempty" yaml:"presentation,omitempty"`
	// Interaction declares that this tool's execution emits a product
	// interaction the chat renders in place: `kind` names the event the tool
	// emits (read by the factory, like Presentation.Kind), `render` names the
	// shared chat rendering for it. Today: render "chat.suggestions" draws
	// tappable next-step buttons from payload {actions: [{label, message}]}.
	Interaction *InteractionBinding `json:"interaction,omitempty" yaml:"interaction,omitempty"`
}

// InteractionBinding is a tool's declared in-chat rendering.
type InteractionBinding struct {
	Kind    string `json:"kind" yaml:"kind"`
	Render  string `json:"render" yaml:"render"`
	Message string `json:"message,omitempty" yaml:"message,omitempty"`
}

// PresentationBinding names the presentation kind a tool produces and the
// compact activity row that accompanies its update in the transcript. Keeping
// both facts beside the tool declaration means a future presentation cannot
// accidentally fall through to an unrecognized raw event card in the UI.
type PresentationBinding struct {
	Kind     string                       `json:"kind" yaml:"kind"`
	Activity *PresentationActivityBinding `json:"activity" yaml:"activity"`
}

// PresentationActivityBinding is product-owned copy for the shared activity
// row. It deliberately contains display text, not a frontend component name:
// the platform owns the consistent row while product.yaml owns what happened
// and where the user can inspect it.
type PresentationActivityBinding struct {
	Label       string `json:"label" yaml:"label"`
	Destination string `json:"destination" yaml:"destination"`
	Detail      string `json:"detail" yaml:"detail"`
}

// CommandBinding is a slash command a product ships with itself. The platform
// already has two command sources -- hardcoded builtins and per-user markdown
// under commands/custom/ -- but neither lets a product carry its own, so a
// product's opinionated flows had no way to reach the command menu.
//
// Prompt is what the command submits on the user's behalf, and follows the
// same contract as a user command: `{{context}}` is replaced with whatever the
// user typed before the slash, or removed when they typed nothing. Products
// keep the text in their own files and fill this in at load time, so a long
// prompt does not have to live inline in the product manifest.
type CommandBinding struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Icon        string `json:"icon,omitempty" yaml:"icon,omitempty"`
	// Aliases resolve to this command without adding menu entries.
	Aliases []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	// MenuHidden keeps a retained shortcut executable without a menu row.
	MenuHidden bool `json:"menu_hidden,omitempty" yaml:"menu_hidden,omitempty"`
	// File is the product-relative path holding the prompt. It is resolved by
	// the product at load time and never sent to a client.
	File   string `json:"-" yaml:"file,omitempty"`
	Prompt string `json:"prompt" yaml:"prompt,omitempty"`
}

// SecretBinding names a credential a product's skills read from the
// environment, so a workspace can be checked for what it is missing before a
// run starts rather than discovering a missing key mid-generation. Nothing
// before this declared secrets at all -- a product's skills had to explain
// the `SECRET_<NAME>` convention in prose, and there was no way to answer
// "what does this product need" without reading every skill.
//
// This is deliberately declarative only: no value, no default, nothing that
// could leak a credential through a profile response. A client cross-checks
// Name against the workspace/user secrets it already fetches separately (see
// GET /api/secrets/workflow/stored) to compute whether it is configured.
type SecretBinding struct {
	Name string `json:"name" yaml:"name"`
	// Description says what the credential is for and, ideally, where to get
	// one -- this is read by a human deciding whether to go set it up.
	Description string `json:"description" yaml:"description"`
	// Group marks secrets where any ONE member satisfies the requirement --
	// e.g. FAL_KEY and GEMINI_API_KEY both unlock generation, so neither is
	// individually required, but the group as a whole is. Secrets with no
	// group are independently required when Required is true.
	Group    string `json:"group,omitempty" yaml:"group,omitempty"`
	Required bool   `json:"required,omitempty" yaml:"required,omitempty"`
}

type Profile struct {
	ID                   string `json:"id" yaml:"id"`
	Name                 string `json:"name" yaml:"name"`
	Version              int    `json:"version" yaml:"version"`
	SystemPromptTemplate string `json:"system_prompt" yaml:"system_prompt"`
	// Prompt is where the system prompt comes from in product.yaml. It is
	// resolved into SystemPromptTemplate by LoadProductManifest and never
	// travels over the wire.
	Prompt PromptSource `json:"-" yaml:"prompt,omitempty"`
	// Features are reusable platform capability bundles. A feature contributes
	// its tools, default skills, prompt extension, runtime capability flags and
	// UI panels as one unit. Product manifests select bundles here instead of
	// repeating the same capability across five unrelated blocks.
	Features []FeatureBinding `json:"features,omitempty" yaml:"features,omitempty"`
	// ResolvedFeatures is produced by ResolveFeatures. It is returned to clients
	// so product surfaces can render the same bundle declarations the backend
	// enforced. It is never accepted from product.yaml.
	ResolvedFeatures []ResolvedFeature `json:"resolved_features,omitempty" yaml:"-"`
	Skills           []string          `json:"skills,omitempty" yaml:"skills,omitempty"`
	Tools            []ToolBinding     `json:"tools,omitempty" yaml:"tools,omitempty"`
	// Interactions declare product events emitted by feature tools that are
	// registered outside the profile tool factory. This keeps their UI behavior
	// in product.yaml too, rather than hardcoding product copy in React.
	Interactions []InteractionBinding `json:"interactions,omitempty" yaml:"interactions,omitempty"`
	Commands     []CommandBinding     `json:"commands,omitempty" yaml:"commands,omitempty"`
	Secrets      []SecretBinding      `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	ToolPolicy   ToolPolicy           `json:"tool_policy,omitempty" yaml:"tool_policy,omitempty"`
	Runtime      RuntimePolicy        `json:"runtime" yaml:"runtime"`
	BuiltIn      bool                 `json:"built_in" yaml:"built_in"`
	// AdminInspection lets server admins open this product's projects of
	// every user read-only (chats, files, sharing), with every view audited.
	// Off by default: Crew and personal chats stay owner-only even for admins.
	AdminInspection bool   `json:"admin_inspection,omitempty" yaml:"admin_inspection,omitempty"`
	OwnerID         string `json:"owner_id,omitempty" yaml:"owner_id,omitempty"`
	// Product names which product surface this builtin profile belongs to
	// (e.g. "dominion", "video-studio", "sparkquill") -- set by each product's
	// registration call in server.go, never by the product package itself.
	// Empty for non-builtin profiles and for the generic, profile-less
	// AgentWorks chat path. Used only to key per-user product access checks;
	// it has no effect on tool/skill resolution.
	Product string `json:"product,omitempty" yaml:"product,omitempty"`
	// Scope declares whether this profile is bound to one project workspace
	// (the default -- every profile before this field existed, including
	// Video Studio, behaves this way) or operates globally across a user's
	// whole workspace with no single project folder.
	// Empty is equivalent to ProfileScopeProject; always read this through
	// EffectiveScope(), never the raw field, so that equivalence holds
	// everywhere.
	Scope string `json:"scope,omitempty" yaml:"scope,omitempty"`
	// UIPanels declares which optional panels a product's own surface should
	// offer, so a product's frontend does not have to hardcode what it shows
	// -- the same "declare it, don't assume it" contract Commands and Secrets
	// already follow. Unlike each product's own local ui: block (surface,
	// streaming, etc. -- rendering-mode choices the mounted surface component
	// already knows), these panel toggles are read over the wire via
	// GET /api/agent-profiles/{id}, the same response Commands and
	// Runtime.ProviderOptions travel through.
	UIPanels UIPanels `json:"ui_panels,omitempty" yaml:"ui_panels,omitempty"`
	// Schedules are the product's recurring jobs: on a cron or cadence, run
	// this profile for each user who has the product, sending the listed
	// messages one at a time into the user's product conversation. The
	// platform scheduler executes them; a product hosting itself runs the
	// same definition through productschedule.Runner.
	Schedules []productschedule.Schedule `json:"schedules,omitempty" yaml:"schedules,omitempty"`
}

// UIPanels are optional panels a product's surface can offer. Every field
// defaults to off: a product opts in explicitly rather than a panel showing
// up because a field was left unset.
type UIPanels struct {
	// Secrets shows the secrets-management button/dropdown in the product's
	// header, the same control Video Studio already offers.
	Secrets bool `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	// Schedules shows a Schedules panel listing this profile's scheduled
	// runs (enable/disable/trigger/delete), reusing the same list the
	// AgentWorks schedules popup shows.
	Schedules bool `json:"schedules,omitempty" yaml:"schedules,omitempty"`
	// Files shows a Files panel with the same unscoped workspace file
	// browser AgentWorks' own multi-agent files view uses -- unscoped,
	// unlike Video Studio's FilesPanel which is pinned to one project.
	Files bool `json:"files,omitempty" yaml:"files,omitempty"`
}

const (
	// ProfileScopeProject is a profile bound to one project workspace --
	// resolveAgentProfileForQuery requires selected_folder and
	// agent_profile_context.project_title, and the folder guard collapses to
	// that one project root. This is the default.
	ProfileScopeProject = "project"
	// ProfileScopeGlobal is a profile with no single project workspace: it
	// keeps the chat-wide grants a profile-less turn already has (including
	// org-owned pulse/ writes), defaults its workspace to the "Chats" alias,
	// and defaults its prompt project_title to the profile's own Name.
	ProfileScopeGlobal = "global"
)

// EffectiveScope returns the profile's scope, defaulting empty to
// ProfileScopeProject. Every consumer must call this rather than reading
// Scope directly, so that an unset field and an explicit "project" are
// always indistinguishable -- this is what keeps every profile declared
// before this field existed (Video Studio included) byte-for-byte
// unaffected.
func (p Profile) EffectiveScope() string {
	if strings.TrimSpace(p.Scope) == "" {
		return ProfileScopeProject
	}
	return p.Scope
}

// ToolPolicy controls generic AgentWorks capabilities a product receives.
// Product-specific tools are still declared in Tools above.
//
// Mode selects how the policy is read:
//
//	""          legacy deny-list. Every platform pool is registered except the
//	            names in Disabled. New platform tools reach the product agent
//	            automatically, which is how registered-but-invisible drift
//	            started (see docs/design/agent_tool_surface_single_source.md).
//	"allowlist" only the names in Enabled are registered, whichever pool they
//	            come from. Disabled is ignored.
//
// An allowlist fails closed, so ToolPolicyGate records every name it filters:
// a missing capability must be diagnosable from the session log rather than
// from confused agent behavior.
type ToolPolicy struct {
	Mode     string   `json:"mode,omitempty" yaml:"mode,omitempty"`
	Enabled  []string `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Disabled []string `json:"disabled,omitempty" yaml:"disabled,omitempty"`
}

// ToolPolicyModeAllowlist is the opt-in mode: Enabled is the complete set.
const ToolPolicyModeAllowlist = "allowlist"

// IsAllowlist reports whether this policy names its tools explicitly.
func (p ToolPolicy) IsAllowlist() bool {
	return strings.EqualFold(strings.TrimSpace(p.Mode), ToolPolicyModeAllowlist)
}

type PromptContext struct {
	ProjectTitle         string `json:"project_title"`
	LocalDateTime        string `json:"local_date_time,omitempty"`
	WorkspaceDescription string `json:"workspace_description,omitempty"`
	// Product carries per-turn variables a product computes server-side for
	// its own prompt (a child's name, what is still unknown, the current
	// activity folder). Set from the profile's PromptVariablesProvider; a
	// value sent by the client is discarded. Templates read it as
	// {{.Product.NAME}}.
	Product map[string]string `json:"product,omitempty"`
}

// PromptVariablesProvider computes a profile's Product prompt variables for
// one turn from trusted, server-resolved state.
type PromptVariablesProvider func(context.Context, RuntimeContext) (map[string]string, error)

// ChannelProfileRoute is where a product sends a channel (WhatsApp, …)
// message that named one of the product's own @tokens: a profile of the
// product, the conversation key when that profile's conversations are keyed,
// where attachments go (a workspace path the profile can read), and a label
// the channel shows when the route activates.
type ChannelProfileRoute struct {
	ProfileID       string
	ConversationKey string
	UploadFolder    string
	Label           string
}

// ChannelRouter resolves a product's own @token for a user. ok=false means
// the token is not one of the product's; an error is shown to the person who
// typed it (a token the product knows but cannot serve right now).
type ChannelRouter func(ctx context.Context, userID, token string) (route ChannelProfileRoute, ok bool, err error)

type ToolRuntimeContext struct {
	UserID        string
	SessionID     string
	WorkspacePath string
	Emit          func(event any)
	// Presentation is the calling binding's declared PresentationBinding, if
	// any. A tool factory that presents something reads the kind from here
	// instead of hardcoding it — see ToolBinding.Presentation.
	Presentation *PresentationBinding
	// Interaction is the calling binding's declared InteractionBinding, if
	// any; a factory that emits an interaction reads the kind from here.
	Interaction *InteractionBinding
	// Product is the product the calling profile belongs to, so a
	// platform-owned tool can tag what it emits for the right surface.
	Product string
}

// RuntimeContext contains trusted, server-resolved state for a profile turn.
// Profile initializers use it to prepare durable workspace-owned state before
// the agent definition is finalized. Values in this struct are never accepted
// from tool arguments.
type RuntimeContext struct {
	UserID        string
	SessionID     string
	WorkspacePath string
}

type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
	Category    string
	Execute     func(context.Context, map[string]interface{}) (string, error)
}

type ToolFactory func(ToolRuntimeContext, json.RawMessage) (ToolSpec, error)

type RuntimeInitializer func(context.Context, RuntimeContext) error

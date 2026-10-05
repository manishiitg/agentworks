# Workspace UI Design Guidelines

How settings and workspace views in AgentWorks (Builder automations and Crew
projects) are built. New views follow this page; existing views converge to
it. See `setup-consolidation.md` for the consolidation that established it.

## Shared header

Every workspace view renders one `WorkspaceViewHeader`
(`frontend/src/components/workflow/WorkspaceViewHeader.tsx`).
Plan and Report are the only views without one (canvas overlays,
not titled views). See `header-consolidation.md` for the migration
that established this.

- Leading icon in an `h-9 w-9` tile (`h-4` glyph), sized to span the
  title + subtitle block.
- Title `text-sm font-semibold`, subtitle `text-xs
  text-muted-foreground`, both rendered by the header — views never
  override the sizes. Optional inline `context` (counts, badges) sits
  next to the title; a full-width `below` row holds stats or pills.
- Actions on the right: Ask AI left, refresh right, via
  `WorkspaceViewActions`. The order is a product rule. Per-tab extras
  (`tabActions`) render left of the pair; pickers and dropdowns live
  in the content, never in the header row.
- Tabs, when a view has them, belong to the header via its `tabs` prop
  (`{ value, onChange, options, ariaLabel }`), rendered by the shared
  `WorkspaceViewTabs` pinned to the header's bottom edge. Tabs may
  carry icons and counts. Never build a second tab row.
- `bare` variant when the shell owns the row (Costs, Execution Logs),
  `sticky` for scroll views — both visually identical to the standard.

## Nested sections

Use breadcrumb navigation when a section has its own tabs. Render
`WorkspaceViewBreadcrumbs` as the shared header title: its Back action returns
to the parent section picker. Only the current section's tabs appear in
`WorkspaceViewHeader.tabs`; do not stack parent tabs above child tabs.

Integrations uses the shared `IntegrationSectionPicker` for Plugins, Slack,
WhatsApp, Google apps and Connect (subject to product permissions). Inside
Plugins, one header row contains Connected, Available, Secrets, Skills and
Vault. `ProjectPluginsPanel` accepts the controlled header tab and renders
content without another tab row. Crew, Code, workflows and Relay reuse it.

## Chat navigation

Ctrl+K and Global Monitor use `openGlobalTab` / `openGlobalActivitySession`.
Direct chat-tab selection uses the same `activateTab` coordinator, with
workflow tabs committed through `activateWorkflowTab`. A direct schedule/main
tab click invalidates pending asynchronous navigation even within one workflow.

Scheduled-run tab labels use the scheduler-stamped session title, falling back
to the saved job name before the owning workflow's name. Ctrl+K and Global
Monitor must preserve that schedule identity when reopening its tab.

Formatted transcripts own their scrolling. New conversations open at the latest
message; revisiting one preserves deliberate reading position and expanded tools.
Reading state is scoped to both tab and session, so a reused schedule tab does
not inherit its previous run's position. Navigation must not force a bottom
scroll; explicit sends and “Jump to latest” still do.

“Load earlier messages” belongs inside the transcript's virtual scroller, above
its oldest loaded row. It must not be a fixed banner that resizes the viewport
on tab switches. Older pages and their pagination cursors stay together, scoped
to the conversation. The shared event buffer's cursor describes only its own
bounded tail, never another reader's locally loaded older pages.

Revisiting a formatted chat uses the same compact/full history projection as
initial restore. A refresh replaces the overlapping durable tail, preserves
already-loaded rows before that page, and retains their backward cursor. A
smaller refresh page must not replace a complete transcript with a partial one.
If the memory limit trims the retained prefix, paging resumes from the oldest
row actually kept in the shared buffer.

Schedule/trigger restores share concurrent requests, establish the live-event
cursor before SSE, and preserve events received while history was loading.
Opening an already-loaded run uses its cached transcript; background events
continue through the existing live transport.

## Ask AI and chat

One function sends every right-pane message to chat:
`sendWorkspacePaneMessageToChat`
(`frontend/src/utils/workspacePaneChat.ts`). It resolves the
conversation (automation, tab, or Crew project), opens the left chat,
queues behind a running turn, and restores stuck sessions. Every Ask
AI button, report widget, human-decision card, and Pulse action
funnels through it; new senders need only call it. Product surfaces
with their own lane (Crew) route through `AskAIButton`'s `onAsk` prop
and still land in the same function.

- The header button is `AskAIButton` (icon-only, expands on hover,
  two-click arm/confirm, "Sent!" acknowledgement), paired with the
  shared `WorkspaceViewIconButton` refresh.
- Ask AI copy is user-visible plain words: no tool names, skill paths,
  or config keys. Builder messages use the `workspaceAskAI` marker
  blocks (visible summary + hidden builder instructions), one per view
  in `WORKSPACE_ASK_AI_MESSAGE`, exhaustive over registered views so
  a new view cannot ship without one. Views with distinct tabs follow
  the active tab. Crew messages stay plain strings sent to the
  project chat.

## Toolbar

Toolbars use open/close groups (`WorkspaceToolbarGroup`), never dropdown
menus: primary icon buttons stay visible, Ops and Setup collapse into
labeled groups that follow the active view. Group buttons are icon-only
with tooltips (`aria-label` + `title`), in a fixed order — Setup runs
Identity → Integrations → Playbooks.

## Split layout

The chat/workspace split has one decision point per surface: Builder
uses `resolveWorkspaceLayout`
(`frontend/src/components/workflow/workspaceLayoutResolver.ts`), Crew
uses `resolveWorkSurfaceLayout`
(`frontend/src/products/work/workSurfaceLayoutResolver.ts`). Each is a
pure function of its flags (visibility, focus, preview tier, split
ratio) returning every pane class, the grid style, and the mount
branches — call sites render from it and never branch on the flags
themselves. Truth-table unit tests pin every combination, so a new
flag or mode cannot silently break an old one.

- Panes stay mounted; states toggle visibility and width only. Never
  swap display modes or layout engines between states.
- Visibility restores its own display: a flex pane hidden below md
  comes back as `hidden md:flex`, never `md:block`. Responsive
  variants win over base utilities, so `md:block` would override
  `flex`, collapse `flex-1` scroll regions to content height, and
  freeze pane scrolling with no error.
- Scroll invariant: every `overflow-y-auto` keeps a definite-height
  ancestor chain in all modes (see Scroll).
- Narrow viewports stay single-pane: `focusedPane` picks which pane
  owns the content row below md; at md+ both panes show as the
  normal split.

## Cards

Tab content is built from the shared `SettingsCard`
(`frontend/src/components/ui/SettingsCard.tsx`), matching the shared
knowledge-bases design:

- Header row: leading icon (`h-4 w-4 text-primary`), `text-sm font-semibold`
  title, optional muted count pill (`SettingsCount`, e.g. "3 attached"),
  optional right-side action.
- A muted plain-words explainer under the header.
- Content below, separated by the card's own spacing.
- Empty states use the dashed box (`SettingsEmpty`); loading states use a
  muted spinner or status line; errors use the destructive banner
  (`border-destructive/30 bg-destructive/10 text-destructive`).

Cards stack with `space-y-4`. Do not nest cards inside cards, and do not
repeat the tab title as an in-content heading — the shared header already
titles the view.

## Connect tab

Setup → Integrations carries a `Connect` tab (tab value `cli`) on Crew
projects and Builder automations, both rendering one shared
`CliMcpSetupPanel`
(`frontend/src/components/integrations/CliMcpSetupPanel.tsx`). The tab
points at the installation's API origin on both hosted and local installs.

- Lead with AI agent connection over HTTP MCP. Claude Code and Codex get
  direct registration commands; ChatGPT and Cowork get the same URL or a
  Cowork plugin. No CLI binary or pasted token is needed.
- Browser OAuth handles consent. List connected clients with a Revoke action.
- Hosted apps need a public HTTPS URL. A local AI agent may use a configured
  loopback HTTP URL from the same computer.

## Access and users

Users & access is a workspace view (`WorkflowAccessView` via
`WorkspaceViewHost`), not a top-menu entry: it follows the shared
header and cards rules like any other view, and is gated on
multi-user mode with admin/owner checks for management actions.
Personal access tokens are managed from the account menu
(`AccountControl` → `AccessTokensDialog`), never the top bar.

## Forms

Build every form from the kit (`frontend/src/components/ui/`):

- `Input`, `Textarea`, `Label`, `Button` (+ variants), `Checkbox`,
  `Switch`/`ToggleRow`, `SecretField`, `Badge`, `ConfirmationDialog`.
- Surfaces and text use theme tokens only: `border-border`, `bg-card`,
  `bg-muted`, `text-foreground`, `text-muted-foreground`, `text-primary`,
  `hover:bg-muted`. Status states use the semantic tokens — `warning`
  (needs attention: running, missed, partly paused), `info` (waiting),
  `success` (enabled, healthy), `destructive` (errors) — with alpha
  modifiers for tints (e.g. `border-warning/30 bg-warning/10
  text-warning`, paralleling the destructive banner). No hardcoded
  gray/amber/red/blue/green/purple palettes — the one exception is a
  documented semantic state color (e.g. the amber bot-enabled toggle,
  whose meaning is spelled out in the adjacent copy).
- Native elements stay only where the kit has no equivalent and the
  adoption test records the exception: radios and single selects.

## Buttons, badges, counts

- Primary `Button` for the page's main save; `outline`/`ghost` `sm` for
  secondary actions; `ghost` `icon` (`h-7 w-7`) for row actions with
  `title` tooltips.
- Pills are kit `Badge` (`default`/`secondary`/`outline`), not hand-rolled
  spans — except `SettingsCount` for card-header counts.
- Counts read as plain words: "3 attached", "2 saved", "0 attached".

## Delete

Destructive removes use `ConfirmationDialog` with `requireText`: the user
types the object's name to confirm (GitHub-style). Copy states what is
removed and that it cannot be undone.

## Identity icons

All identity badges render through `EntityIdentityIcon` (`WorkflowIcon`
for automations). An icon value is an emoji/short glyph or an uploaded
image stored as a downscaled (~128px) data URL, edited through the shared
`IconUploadField` (emoji field + Upload + Remove). Server validation
(`agent_go/pkg/workflowtypes/icons.go`) accepts both shapes; prompts and
tool results redact image bytes to "(custom uploaded image)".

## Scroll

One scroll surface per pane: the tab container owns `overflow-y-auto` and
embedded panels join it with `manageOwnScroll={false}`. Never nest a
self-scrolling panel inside a scrolling container. Refresh remounts the
active tab (a nonce key), since every tab loads on mount.

## Copy

Plain business words everywhere the user reads: "apps", "passwords and
keys", "folders", "bots". Workflow nouns for automations, project nouns
for Crew. No jargon, no config keys, no emoji as icons.


### Shared sidebar and product actions

- The shared icon rail is 48px wide with 36px tall controls and 6px side padding. Keep product icons and hover/focus flyouts unchanged when adjusting its density.
- The product picker shows the current product icon; hover, click, or Arrow Down opens the permitted product list beside the rail. Escape or leaving the picker closes it.
- The bottom utility section is shared across products: active work, Providers, Connect an AI agent (MCP), Users & access (administrators), and Account.
- Activity belongs to Goals/workflows. Schedules and triggers belong to Goals/workflows and Crew, and sit with product actions above the shared utilities. Code and Vault do not show schedule actions. Relays are API-only and do not support timed schedules (PLAT-433).
- Schedules uses one row, **Schedules · Triggers**, scoped to the current product. Crew uses product schedules; Goals/workflows filter workflow records and exclude Relays.
- Full-page utilities and Activity use `WorkspaceBackButton`; returning closes overlays and restores the product workspace.
- Users & access reuses the Vault directory panel. UI invitations grant Vault only; other product access continues through platform provisioning.

[← platform / browser-automation](index.md)

# PLAT-248 — Managed browser tool can be present without its built-in operating skill

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | browser |
| Area | automation |
| Summary | runtime contract, severity high. |

| Coordination | Value |
|---|---|
| Assigned agent | Unassigned |
| Ticket state | `fixed` |
| Last synchronized | `2026-08-29` |

- **Priority:** runtime contract, severity high.
- **Evidence:** The `jobsearch` workflow has `browser_mode: auto`. Its retained
  `scan-linkedin-feed-for-job-posts` session received `agent_browser`, but its
  attached skill set omitted `agent-browser`; `read_skill` consequently
  returned `attached skill "agent-browser" not found`.

## Problem

Browser capability and browser guidance were configured independently. Enabling
the managed browser registered `agent_browser`, but runtime skill attachment
used only the manually selected workflow/step skill names. A browser-capable
agent could therefore be instructed to load the product-owned browser skill
while the same session made that skill unavailable.

This is a platform defect, not a workflow configuration error. A built-in skill
that defines the safe use of a managed tool is part of that tool's capability;
users should not need to select it again on every Builder or execution step.

## Resolution

- Added one shared capability helper that appends the built-in
  `agent-browser` skill when—and only when—the agent has managed browser
  capability.
- Applied it to direct/Builder identity assembly and workflow execution-agent
  identity assembly.
- Execution uses the registered tool list as the final authority, so an actual
  `agent_browser` grant cannot drift from the attached skill even if older
  server-name metadata is incomplete.
- Explicit selection is deduplicated, and persisted workflow configuration is
  not mutated.

## Verification

- Skill-loader tests cover implicit attachment, explicit deduplication,
  disabled-browser behavior, and input immutability.
- Workflow-agent and server test suites pass.


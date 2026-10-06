[← goals / step-execution](index.md)

# PLAT-087 — message-sequence child advertises MCP servers that have no tools

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | child-session tool registration / agent-spec materialization |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` |
| Last synchronized | `2026-08-11` |

- **Priority:** P1
- **Owner:** child-session tool registration / agent-spec materialization
- **Source workflow:** Instagram, scheduled run
  `schedule-manual--bae435e5_1786432476119732000`
- **Related:** PLAT-003 (managed DB capability) and PLAT-053 (background
  workshop tool inheritance). Those fixes cover narrower session families;
  this is a normal message-sequence child session.

## Confirmed evidence

Instagram's `route-build-video` and `route-generate-illustrations` child
sessions were given a prompt that listed `workspace_advanced`,
`workspace_browser`, and `workflow_db` with their tool rosters. The exact same
sessions then failed calls with:

```text
tools_unavailable: server_unavailable=[workspace_advanced workspace_browser]
... configured for this agent but currently have zero registered tools
```

The missing tools were not guesses: `generate_music`, `read_image`, and the
DB route were listed in the child prompt. `workflow_db` had a narrow HTTP
workaround, but binary tools did not, so the orchestration had to replace the
music-generation action itself. This happened twice in the same producing run
and the concern has `seen_count=2` in
`Workflow/instagram/db/db.sqlite` (`61347449b57c07bd`).

## Problem

One part of child-session creation builds the prompt/tool index; another part
registers the real MCP tools. They can disagree. The agent is told a tool is
available, follows the supplied name, and then receives a server-outage error
because the advertised server was created with zero callable tools.

## Impact

- Agents waste turns retrying names that were already correct.
- Capabilities that have no shell/HTTP fallback (for example image/audio
  operations) cannot be performed at all.
- The prompt misleads both the agent and the operator: it looks like an agent
  mistake, but the platform created an impossible contract.

## Required fix

Use one canonical child `AgentSpec` to derive both:

1. the tool index shown to the child, and
2. the actual MCP/custom-tool registrations for that child session.

Before the agent starts, validate `advertised tools ⊆ registered tools` and
either register the missing capability or remove it from the prompt. A server
with zero tools must be treated as unavailable at construction time, not after
the model has spent a turn trying it.

## Acceptance

- A message-sequence child that advertises `generate_music`, `read_image`, or
  a managed DB tool can invoke it in the same session.
- If a configured provider cannot start, its tools are omitted from the prompt
  and a deterministic startup diagnostic names the unavailable provider.
- Cover normal message-sequence children separately from PLAT-003's workflow
  DB execution steps and PLAT-053's workshop background children.

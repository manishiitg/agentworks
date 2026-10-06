[← browser / browser](index.md)

# PLAT-573: Browser tab groups use the Code, Crew or workflow display name

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | Group titles show only the Code/Crew display name or workflow label, without AgentWorks or deployment branding. |

## What happened

The owner wants tab groups named after their Code project, Crew or workflow,
without the REAL Training Systems prefix. The old extension used branding plus
a physical folder basename, showing storage slugs instead of names from the UI.

## Fix

The authenticated relay decorates already-authorized projects with display-only
names from Code/Crew product.json (identity.name, then title/label) or workflow.json
(label). It preserves the physical workspace path and scope for authorization.
Names are trimmed, control characters removed, and limited to 120 characters;
missing names fall back to the folder basename. Read failures never change access.

The paired envelope and heartbeat project list carry the names. Extension 0.4.3
uses the name alone in groups, the popup heading and project chooser. Heartbeats
rename existing groups when the project name changes without moving or adopting
other tabs. Groups and debugger authority remain separate per project/window.
Legacy servers/codes fall back to the unprefixed folder basename.

The real Chrome E2E verifies exact Code, Crew and workflow group names, adding a
second tab to an existing group, separate Code/Crew groups, and group retention
through debugger recovery. Focused relay/server checks pass.

## Left

Deploy the server build to RTS to deliver friendly names. The installed extension
already supports the new metadata; the old server supplies folder-name fallback.

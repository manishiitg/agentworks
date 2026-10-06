[← crew / learnings-knowledge](index.md)

# PLAT-345 — Crew project memory was durable but invisible as a first-class view

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | crew |
| Area | learnings-knowledge |
| Summary | tracks the first-class Crew Memory view. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented locally; focused validation green` |
| Last synchronized | `2026-09-21` |
| Priority | `P1 product usability / durable context` |

## Problem

Crew already persists canonical project memory in the project-root `MEMORY.md`,
and that memory is shared across interactive chats, schedules, triggers and
bots. The Crew workspace exposed Dashboard as a primary view but made memory
visible only by finding the file manually. Users therefore could not readily
audit what Crew remembered or distinguish durable project facts from reusable
procedures stored in custom skills.

## Fix

- Add Memory as a declarative Crew feature and top-level workspace view beside
  Dashboard.
- Render the canonical project `MEMORY.md`, including an honest empty state for
  new projects.
- Show only skills stored inside this Crew project at
  `<project>/skills/<skill>/SKILL.md` in a compact full-width strip above
  memory. Never source this view from the account-wide `skills/custom/`
  library. The memory document then uses the full pane width.
- Keep Dashboard as the initial default view and preserve each project's last
  selected view.
- Route memory changes through the persistent Crew chat so the agent can keep
  memory concise and split reusable procedures into focused custom skills.

## Acceptance

- Memory appears only when declared by the resolved Crew profile.
- Switching projects restores each project's saved workspace view.
- Missing `MEMORY.md` does not surface as an error.
- Only project-local skills created by this Crew appear above memory.
- The Memory action opens the current Crew conversation, and the skills link
  opens the project-scoped Skills manager.

## Verification

- Crew workspace Vitest suites: 10 tests passed.
- Frontend TypeScript project build passed.
- ESLint passed for all changed Crew components.
- Agent-profile and Work-product Go tests passed.
- `git diff --check` passed.

## Register notes

[PLAT-345](plat-345.md) tracks the first-class
Crew Memory view. It renders the canonical project `MEMORY.md`, distinguishes
durable context from reusable procedures, shows only project-local Crew skills
and opens skill management directly from Memory. Dashboard remains the
initial default view.

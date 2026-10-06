[← goals / plans-contracts](index.md)

# PLAT-049 — shared platform mechanics leak into workflow artifacts

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | plans-contracts |
| Summary | Workflow Builder mutations, version upgrades, and Engineering Review |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented; migration/runtime reverify` |
| Last synchronized | `2026-08-07` |

- **Priority:** P1
- **Owner:** Workflow Builder mutations, version upgrades, and Engineering Review
- **Source:** Upwork learning turn and cross-workflow scan

## Problem

Plan descriptions, Learnings, and KB notes accumulated AgentWorks bridge,
Folder Guard, managed DB-tool, discovery, and coding-session mechanics. Those
platform details duplicate tool prompts, become stale, and are not workflow
behavior or reusable target-system skill.

## Fix

Plan mutation tools reject narrow shared-platform signatures only in changed
description/message fields. Builder guidance states the ownership boundary.
Engineering Review audits Plan/Learnings/KB purity and consolidates confirmed
occurrences into one workflow-level finding. Workflow contract v1.0.21 runs an
idempotent, blocking purification preflight while preserving domain behavior
and refusing ambiguous rewrites.

## Verification

Focused tests cover ordinary domain instructions, nested message-sequence
content, mutation entry points, ordered upgrade execution, and version stamping.
A migrated v1.0.20 producing workflow still needs runtime verification.

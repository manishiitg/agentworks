[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-447 — Remove platform DB, KB and learnings from Relay execution

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | plans-contracts |
| Related | PLAT-441 |

## Owner decision

Relays are authored agentic and scripted chains for external API integration.
They do not need the platform workflow database, KB or learnings. Keep shared
execution infrastructure and explicitly configured user systems.

## Cause

Every execution step resolved DB to read-write; KB and learnings defaulted to
read. The agent factories, per-item overrides, direct script environment and
Builder session setup each applied these workflow defaults independently.
Hiding Builder tools did not remove runtime capability.

## Done

- Product manifest owns `execution.platform_stores: false`; missing Relay policy
  fails closed. Ordinary workflows retain the existing store defaults.
- Every controller's persisted workflow preflight loads the policy along with
  code layout. Draft, workshop single-step/full-run and frozen release execution
  share the same controller and policy; there is no separate Relay executor.
- Product policy overrides legacy store access/objective/contribution fields.
  No DB tools are selected, even through explicit wildcard selections. The
  authored-agent-with-routes factory cannot append them back. User MCP servers,
  saved script tools, variables, secrets and output handoff remain available.
- Sequence/agent/script folder grants exclude the stores. Trusted sessions and
  direct Python guards hard-block db/, knowledgebase/ and learnings/, including
  when a broad workflow root was inherited or supplied as an additional path.
- Direct Python has no platform DB_PATH or managed DB grant. Script bridge
  sessions are set to `none`. DB query, mutation, migration and snapshot tools
  refuse that grant before reaching the workspace API.
- Managed Builder setup/restore uses the same product policy, clears inherited
  DB_PATH and blocks raw stores. No store files or releases are deleted.
- Relay runs do not create DB/KB folders, inject learnings or run reflection
  turns. Builder prompt and skill explain runtime restrictions and user DB tools.
- Regressions cover draft/release policy loading, legacy explicit access,
  sequence write constraints and closing turns, wildcard tool lists, direct
  Python guards/environment, Builder restore, script bridge grants and DB tool
  denial. Existing ordinary workflow and Relay execution regressions pass.

## Verification

Focused Go tests run in owned worktrees with owned dependency checkouts:
step-based workflow Relay/authored/script/message-sequence/factory/DB guards,
Relay product, product manifests, virtual workflow DB tools and server Relay API
surfaces. No production service or local running app is restarted.

## Left

Deploy and verify a real Relay run. Existing Relays that used inherited platform
stores must switch to explicit user integrations. No graph, secrets, user DB or
published release files are rewritten. Crash recovery and PLAT-443/444 are
separate work.

[← crew / security-sandbox](index.md)

# PLAT-353 — Crews get a read-only Run mode: single owner, inspect-and-run for everyone else

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | crew |
| Area | security-sandbox |
| Summary | fixes `BUG_ID_001` (issue #205): every Crew keeps a single owner and is read-only for everyone else, instead of deriving permissions from linked workflows or keeping Crews private. |

| Coordination | Value |
|---|---|
| Assigned agent | Muse Code |
| Ticket state | `implemented and pushed to main; deployment and live acceptance pending` |
| Last synchronized | `2026-09-22` |
| Priority | `P1 authorization` |

## Problem

Crews had no Run mode and no sharing model: a Crew belonged to its owner's
private project tree, invisible to other users, while workflows already
supported owner/reader/runner tiers. QA on the Confida server
([issue #205](https://github.com/manishiitg/coding-agent-loop/issues/205),
`BUG_ID_001`) showed crews need to be usable across users — people reuse one
Crew across multiple workflows — without giving every user full mutation
rights over someone else's Crew.

Deriving Crew permissions from linked workflows was rejected: a Crew can be
linked to several workflows with different ACLs, so the derivation is
ambiguous. Per-crew share lists were rejected as needless machinery.

## Authorized behavior

Every Crew has a single owner and is read-only for everyone else. Any other
signed-in user with the Crew product gets Run:

- chat with a read-only tool surface and a read-only system prompt;
- inspect: read files, briefs, configuration, schedules, triggers, and run
  history through mediated endpoints;
- run: invoke the Crew's attached workflow triggers (each re-checks access
  before executing).

Readers cannot mutate anything: no file, shell, or database writes; no
schedule, trigger, selection, identity, folder, or bot changes. Their
conversation is stored under their own account, separate from the owner's
chats, and the owner's transcripts are never visible to them. Secret names
may be visible; secret values are never printed or exfiltrated.

## Implementation

Backend (`agent_go/cmd/server`):

- `crew_access.go`: cross-owner project binding (`resolveCrewProjectBinding`;
  caller's own tree first, explicit owner segment, then a scan that fails
  closed on ambiguity), ownership tests, reader-turn detection, the reader
  tool deny-list, folder-guard roots, and the reader system prompt.
- `conversationTargetAccess` resolves Crew bindings under whichever owner
  holds the project and returns `WorkflowAccessRead` for non-owners.
- Reader turns receive the deny-list at the product tool gate, read-only
  folder roots with an explicit blocked-write entry, and the read-only
  prompt appended after the Crew's own prompt (`product_tool_gate.go`,
  `tool_setup.go`, `agent_profile_runtime.go`).
- Reader bindings carry no manifest path and no authoritative session, so
  registry operations never touch the owner's manifest and never adopt the
  owner's live session; project-local transcript scans are skipped for
  readers (`agent_profile_conversations.go`, `chat_history_persistence.go`).
- `crew_directory.go`: `GET /api/agent-profiles/{id}/shared-projects` lists
  other owners' Crews (secret values, trigger endpoint material, and LLM
  connection IDs never serialized; workflow references filtered to
  workflows the caller may open), plus mediated `files` and `file`
  endpoints confined to the verified crew root with `builder/` (owner
  transcripts) and `db/` (run databases) excluded.
- `workspace_proxy.go`: the browser proxy refuses raw cross-user workspace
  traffic, making the mediated endpoints the only path to another owner's
  crew tree.

Frontend:

- Shared Crews list under "Shared by others · read-only" with owner badges
  (`workSessions.ts`, `WorkSurface.tsx`); owned rows win id collisions and
  shared-listing failures degrade to owned-only.
- Shared Crews open with chat plus Memory and a mediated read-only file
  browser (`SharedCrewFilesPanel.tsx`); identity, integrations, automation,
  dashboard, database, browser, and costs stay owner-only
  (`WorkWorkspacePane.tsx`).
- All UI-initiated manifest writes (model, selections, identity, delete)
  throw for shared rows; the chat guide and AskAI prompts switch to
  read-only copy.

## Regression and acceptance

Committed coverage:

- `crew_run_mode_test.go`: cross-owner binding, ambiguity fail-closed,
  reader deny-list, prompt contents, endpoint confinement and exclusions.
- `workSessions.test.ts` / `sharedCrewFiles.test.ts`: shared-row mapping,
  merge order, collision preference, owned-only degradation, write guards,
  path relativization/re-prefixing, and the 404 contract.

`go build`, `go vet`, focused Crew tests, `tsc -b`, eslint on touched
files, and the work-area vitest files pass. The full `cmd/server` suite and
the full frontend suite show only failures also present on the unmodified
baseline (verified via an isolated worktree run).

After deployment, live acceptance on the Confida server should confirm: a
second user sees the first user's Crew as read-only with the owner's name,
can chat and browse memory/files but cannot change model, selections,
identity, schedules, or triggers; the owner's transcripts and databases
stay unreachable; and each reader's chat history stays private to them.

Related: [PLAT-262](../../sandbox/access/plat-262.md) (workflow Run-mode seams),
[PLAT-339](plat-339.md) (read-only Crew workflow invocation),
[PLAT-330](../../sandbox/access/plat-330.md) (whose "private project permissions" note this
supersedes), and [issue #205](https://github.com/manishiitg/coding-agent-loop/issues/205)
(`BUG_ID_001`).

## Post-review hardening (2026-09-22)

Two P1 findings from pre-acceptance review, both fixed before live
acceptance:

1. Raw manifests excluded from the shared file surface. The mediated
   `files`/`file` endpoints served `product.json` and `workflow.json`
   unchanged, exposing LLM connection IDs, encrypted webhook secrets,
   and unfiltered workflow references and bypassing the listing
   endpoint's filtering. Root-level `product.json`/`workflow.json` now
   404 like the excluded subtrees (nested same-named files are ordinary
   project data and stay servable); the listing summary remains the
   sanitized config view. Regression: endpoint tests assert the 404s
   and sweep every reader file response for the fixture's connection
   ID, ciphertext, and private workflow reference.

2. Fail-closed proxy body inspection. The cross-user body check skipped
   JSON bodies with unknown or over-cap lengths and stopped the
   multipart scan after 4 MiB, so a chunked or file-first request could
   carry an unchecked `_users/` path to the workspace server (which
   resolves explicit paths verbatim). The proxy now spools every body
   in full (memory to 8 MiB, temp file past it, 413 past 256 MiB),
   inspects all JSON path fields and all multipart parts regardless of
   order, and rejects unreadable/malformed bodies with 400 — matching
   the workspace server's own binding behavior (first-value JSON
   decode; file parts required). Allowed bodies replay byte-identical.
   Regression: chunked, oversized (declared and streamed), malformed,
   unreadable, file-first, past-memory-cap spool, and oversized
   multipart cases.

Residual risk (open follow-up, not a launch blocker): a reader's agent
turn can still read the raw manifests through file-read tools, since
the crew root is read-allowed for readers. Prompt-level exfiltration
rules cover it today; a future change should deny-list the two
manifests (or serve redacted views) in reader tool output.

## Register notes

[PLAT-353](plat-353.md) fixes `BUG_ID_001`
([issue #205](https://github.com/manishiitg/coding-agent-loop/issues/205)):
every Crew keeps a single owner and is read-only for everyone else, instead
of deriving permissions from linked workflows or keeping Crews private. Any
other signed-in user with the Crew product gets Run — chat with a read-only
tool deny-list and system prompt, mediated memory/file inspection with owner
transcripts and run databases excluded, and workflow-trigger invocation that
re-checks access. Reader conversations store under the reader's own account
and never touch the owner's manifest or live session; the proxy refuses raw
cross-user workspace traffic. The UI lists shared Crews with owner badges,
restricts them to chat plus Memory and a read-only file browser, and guards
all manifest writes. Implemented and pushed to main; deployment and live
acceptance on the Confida server pending.

[← platform / frontend-chat](index.md)

# PLAT-107 — a later main-agent turn is projected as its own child and steals the terminal pane

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | app |
| Area | ui |
| Summary | Pulse Gate, Review/Fix, and Finalizer are sequential messages sent to the same |

| Field | Value |
|---|---|
| Status | `partially implemented` — projection, self-parent collapse, event-only phantom suppression, and selection repaired 2026-08-15; **runtime verification not yet performed** |
| Priority | P1 |
| Owner | execution-tree terminal projection and selection |
| Reported | 2026-08-15 |
| Related | [PLAT-035](../../coding-agents/bridge/plat-035.md)), [PLAT-095](../../schedules/runs/plat-095.md)), [PLAT-100](../../coding-agents/bridge/plat-100.md)) |

## Problem

Pulse Gate, Review/Fix, and Finalizer are sequential messages sent to the same
scheduled main-agent conversation. During the Build in Public run, the terminal
rail instead showed the Finalizer as:

`PULSE FINALIZER … · Child of PULSE FINALIZER …`

and selected a blank pane. Backend evidence showed the same Schedule session was
still processing the Finalizer and backup work. The workflow was not waiting for
a new child terminal; the UI had invented one.

This hides real progress, creates a nonsensical self-parent relationship, and
makes a healthy run look stalled.

## Root boundary

`frontend/src/utils/terminalExecutionProjection.ts` synthesizes a terminal row
for each live execution-tree node that it considers a visible child. The
selection logic in `frontend/src/components/TerminalCenter.tsx` can then prefer
the latest active synthetic row. A sequential main turn whose execution identity
is not normalized back to the root can therefore become a child placeholder and
take focus before any real terminal exists.

PLAT-095 established the query-rooted execution tree and shared lifecycle
projection. This is a distinct projection regression at that boundary, not a
failure of the Schedule turn-completion contract itself.

## Required repair

1. Normalize execution identity before projection. Nodes with `kind=main_agent`,
   the root session owner, or the same canonical owner as the root are main turns,
   not child terminals.
2. Reject self-parent edges and collapse sequential main turns into the existing
   main conversation timeline.
3. Create placeholder rows only for genuine child/background executions with a
   distinct owner and execution identity.
4. Never auto-select an unpublished placeholder. Keep the current main terminal
   selected until a concrete child terminal is published or the user explicitly
   selects the placeholder.
5. If the user explicitly opens a genuine placeholder, show the existing
   “Waiting for terminal” state; never render an unexplained blank pane.

## Implementation — 2026-08-15

The self-parent mechanism was confirmed exactly as described.
`terminalExecutionProjection.ts` resolved `nodesByID.get(node.parent_execution_id)`
with no guard that the parent differs from the node, then rendered
``display_meta: `Child of ${parent.name}` ``. When `parent_execution_id ===
execution_id` the lookup returned the node itself, so title and meta both read
"PULSE FINALIZER" — reproducing the reported string precisely.

**Correction (same day).** The first attempt only *relabelled* the self-parent
row: it removed the "Child of PULSE FINALIZER" caption but still emitted a
synthetic "Asynchronous child" placeholder, and its test asserted
`toHaveLength(1)` — encoding the incomplete behaviour as correct and
contradicting acceptance 2 ("no `Child of PULSE FINALIZER` row"). A phantom rail
row still hides real progress and makes a healthy run look stalled, which is the
defect this ticket reports. A self-parent node is now **discarded from the rail
entirely** and the test asserts no extra entry exists.

Changes in `frontend/src/utils/terminalExecutionProjection.ts`:

- `isMainConversationExecution()` treats a self-parent edge as proof of a
  misprojected sequential main turn and collapses it into the main conversation,
  so no rail row is produced at all;
- `parentExecutionIDOf()` rejects a self-parent edge and is now the single
  source of the parent ID, used by the placeholder, the enrichment path, and
  the message-sequence terminal bridge;
- `isMainConversationExecution()` replaces the kind-only test. **Repair 1
  needed to be broader than the ticket implied**: `main_agent`, `session_root`,
  and `synthetic_turn` were *already* hidden and `main:` IDs already filtered,
  so the stated fix was largely in place. The real hole was
  `HIDDEN_EXECUTION_KINDS.has((node.kind || '')…)` — a node with an **empty or
  unknown kind** was not hidden, and the placeholder then defaulted it to
  `execution_kind: node.kind || 'background_agent'`. An unclassified node was
  silently promoted to a background child. Ownership is now checked
  independently of kind, and an unclassified node with no distinct parent stays
  in the main timeline.

Change in `frontend/src/utils/terminalIdentity.ts`:

- `preferredTerminalForContext` now skips `execution_tree_placeholder` rows in
  **every** context. The workflow branch already refused to auto-select a child,
  but a Schedule session is not a workflow context, so the non-workflow fallback
  could still take a placeholder. This is also the blank-pane path: a selected
  placeholder that later disappears from the projection leaves the pane with no
  matching terminal to render.

Repair 5 was already satisfied — `TerminalCenter` renders `TerminalWaitingPane`
with an explanatory message for an explicitly opened placeholder.

### Same-day runtime recurrence: event activity was still promoted to a terminal

The Sales Outreach reproduction exposed a second path to the same phantom pane.
The execution tree contained a live `source="event_stream"` child named after
the current command while the runtime reported no live background process and
the terminal API had no corresponding terminal. The rail nevertheless projected
it and could only show `Waiting for terminal` forever.

An event-stream node is not evidence of terminal ownership. Tool calls, turns,
and lifecycle receipts can carry distinct execution and parent IDs while still
running inside an existing terminal. The frontend projection now permits an
event-stream node to enrich a terminal that has already been published, but it
cannot synthesize an unpublished terminal. Temporary child rows remain owned by
the background/tracked execution registries, which are the sources that can
truthfully promise a concrete terminal will follow.

Regression coverage proves both halves: event-only activity produces no phantom
row, while event activity still updates a matching retained terminal.

Regression coverage in `terminalExecutionProjection.test.ts` — a self-parent
node produces **no** rail row, a self-parent node alongside a genuine sibling
suppresses only itself (acceptance 6), a sequential main turn is absent, an
unclassified node invents nothing, and a real child with a distinct parent still
projects — plus `terminalIdentity.test.ts` (no placeholder auto-select; a real
terminal still wins). The self-parent test was verified to fail against the
previous behaviour before the guard was added. Full frontend suite passes
(481 tests).

**Still required before this can be marked implemented:** runtime verification
of the P0 acceptance against a real Gate → Review/Fix → Finalizer Schedule run.
Unit tests prove the projection contract; they do not prove the live rail
renders one main-agent entry for a real sequential Pulse run.

## P0 acceptance

1. Run Gate → Review/Fix → Finalizer as sequential turns in one Schedule session.
2. The rail contains one main-agent entry and no `Child of PULSE FINALIZER` row.
3. Every sequential turn appears in the main formatted transcript.
4. A real background child still receives its own rail entry and terminal.
5. A child placeholder cannot steal selection from the main terminal.
6. Malformed self-parent input is ignored and covered by a regression test.

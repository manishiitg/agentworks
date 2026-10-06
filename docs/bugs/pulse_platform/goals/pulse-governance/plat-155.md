[← goals / pulse-governance](index.md)

# PLAT-155 — Pulse flattens workflow observations into canonical issues and sends the mixed queue to Fixer

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | goals |
| Area | pulse-governance |
| Summary | P0 — a long Pulse pass can spend most of its time investigating |

> **2026-08-28 sequencing update:** PLAT-199 supersedes this ticket's fresh
> independent Fixer conversation. The observation→canonical-issue projection
> remains authoritative. Technical Review still must classify and persist
> before repair, but the same retained child may repair in a later message only
> after the backend observes its completed receipt and unlocks mutation tools.

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — focused automated verification passes; live post-restart convergence proof pending |
| Last synchronized | `2026-08-19` |

- **Priority:** P0 — a long Pulse pass can spend most of its time investigating
  raw step emissions while the confirmed repair backlog barely moves.
- **Owner:** Pulse evidence/issue projection, reviewer promotion boundary,
  Review→Fix sequencing, Gate/backlog tool responses, and Pulse UI typing.

## Live evidence

The Social Media Pulse pass that triggered this ticket ran for roughly 1 hour
38 minutes; its Fixer work occupied roughly 1 hour 26 minutes. It updated 24
identities, attempted 11 repairs, and produced only 2 verified closures. The
terminal SQLite state still appeared to contain 100 open issues.

The apparent 100 split into two different species:

```text
canonical reviewer issues open:       2
workflow observations open:          98
```

The observations were mostly `execution`, `message-sequence`, and
`prevalidation` `CONCERNS:` emissions. They are useful reviewer evidence, but
they had never been accepted as distinct repairable root causes. The backend
`get_pulse_state(view="backlog")` response, Gate `open_concerns`, summary
counts, and Fixer prompt flattened them into the same queue. The frontend had a
partial phase-based visual distinction, so the displayed UI and the agent's
actual worklist also disagreed.

### 2026-08-19 manual Engineering Review — projection cost reproduction

The first manual `/engineering-review` after the observation/issue split was
functionally correct but operationally unacceptable:

- the background reviewer completed in 19m09s after reporting 13,024,922 tokens
  and $9.433499;
- one `get_pulse_state(view="backlog")` response was 2,920,879 bytes because it
  carried every lifecycle's events, attempts, and verification history;
- the response duplicated the complete canonical list under both `findings`
  and `issues`; and
- after the child had already persisted and returned its result, the synthetic
  parent turn downloaded that same response roughly seven more times to decode
  the HTTP envelope and locally filter five PUL IDs.

This is the same projection-boundary defect at a second level: even after raw
observations stopped counting as repair issues, the read contract still made a
small selection task consume the complete audit ledger. The parent completion
path also treated an authoritative child receipt as a request to investigate
again.

## RCA

`run_concerns` intentionally stores observations durably so a Gate cooldown or
context compaction cannot erase them. Later lifecycle tables add reviewer
details, attempts, verification, and events to those same identities. Sharing
the ledger is correct: promotion should keep original evidence and recurrence
history.

The missing boundary was a projection kind. Code treated every ledger row as a
Pulse issue merely because it had a stable `PUL-…` identity. Consequently:

1. recurrence looked like issue creation;
2. backlog counts measured evidence volume rather than root causes;
3. Fixer received observations reviewers had not classified;
4. the same reviewer sequence diagnosed, repaired, and effectively verified
   its own conclusions; and
5. additional Pulse runs repeated discovery rather than converging.

PLAT-010 fixed stable identity/deduplication, PLAT-072 documented telemetry
limits, and PLAT-138 bounded one repair objective. None established the
observation→issue promotion boundary. This ticket is authoritative for that
remaining lifecycle defect.

## Fix and reasoning

### One ledger, two explicit projections

Every lifecycle record now projects one of:

- `observation`: workflow evidence not yet accepted as a repair issue;
- `issue`: reviewer-originated, explicitly promoted, or already carrying Pulse
  repair/disposition history.

The backend derives the kind from durable history rather than adding a second
competing database. Reviewer findings are issues. A step observation becomes
an issue when a reviewer updates it by visible `issue_id`, a Fixer starts work,
or a lifecycle disposition establishes ownership. Promotion appends a
`promoted_to_issue` audit event.

### Tool contract

`get_pulse_state(view="module")` now returns separate
`canonical_issues` and `workflow_observations`; the legacy `open_concerns`
alias means canonical issues only. `view="backlog"` is now a bounded two-stage
read contract:

1. `detail="compact"` (the default) returns active canonical issue cards and
   active observation cards only, plus aggregate terminal counts. It contains
   no lifecycle event arrays and no duplicate compatibility alias.
2. `detail="full"` requires 1–20 exact public `issue_ids` selected from that
   index and returns complete lifecycle evidence only for those records. Broad
   full-history reads are rejected.

Summary counts report issues and observations separately. Internal
fingerprints remain hidden; an observation's public ID can be passed unchanged
as `issue_id` when a reviewer promotes it. Go filters by public identity before
serialization; it does not choose which issues matter.

Guided background reviews/fixes launch with
`completion_mode="present_result"`. The completion notification marks the
child result as authoritative and tells the synthetic parent turn to present it
without tools, state reloads, child-conversation inspection, or independent
revalidation. This prevents the parent from repeating completed review work
merely to write the user-facing summary.

### Independent review and fixing

Pulse now uses ordered Gate → Review → Fix → Finalize parent turns. Reviewer
background agents inspect and classify evidence, continuously link semantic
duplicates, and persist typed review state without modifying implementation
files. Only after they finish does a fresh background Fixer receive at most one
highest-value coherent canonical repair objective. The Fixer records attempts,
dispositions, proof, and Engineering/Operations terminal receipts. Strategic
Review remains non-mutating and owns its own receipt.

This is agentic selection, not a Go-authored bug classifier. Go preserves the
ledger boundary, order, identity, and typed receipts; agents decide whether an
observation is the same root, a new issue, or not a defect and choose the repair
objective from evidence.

The manual commands mirror the same ownership boundary: `/engineering-review`
is review/classification only, and `/pulse-fixer` is the later independent
mutation and verification pass. They intentionally remain two commands so the
reviewer's diagnosis cannot silently become its own proof.

## Acceptance

1. A raw execution/message-sequence/prevalidation concern is projected as an
   observation and does not increase canonical active-issue count.
2. A reviewer can promote an observation by visible ID; the same lifecycle row
   becomes an issue and retains its complete evidence history.
3. Gate receives canonical issue and workflow observation counts separately.
4. Fixer receives only canonical issues and selects a bounded repair batch per
   pass: one primary coherent bundle plus compatible independent low-risk
   bundles with separately clear proof boundaries.
5. Reviewer and Fixer execute as separate background agents; a reviewer never
   repairs or verifies its own newly diagnosed implementation change.
6. The UI prefers explicit `kind` and uses the old phase heuristic only with an
   older backend.
7. A post-restart Social Media Pulse reports the two counts separately and
   materially reduces selected canonical backlog without presenting the 98 raw
   observations as 98 unfixed bugs.
8. Running `/engineering-review` followed by `/pulse-fixer` exercises the same
   review-only then independent-fix boundary without running the full workflow.
9. Compact backlog reads do not expose full text, events, attempts, or
   verification histories and do not duplicate canonical issues under a second
   key.
10. Full backlog detail without explicit public IDs is rejected; targeted reads
    return only requested records and keep internal fingerprints hidden.
11. A guided background completion produces a presentation-only parent turn;
    the parent performs zero Pulse/SQLite/workspace tool calls before surfacing
    the child receipt.


## 2026-09-09 local Pulse audit follow-up

The local audit found repetitive technical attention and sparse strategic
execution. Keep observations separate from accepted issues; a platform-owned
handoff with no available workflow repair must not become a new workflow repair
on every tick. [PLAT-303](plat-303.md) tracks exception-driven technical selection,
protected strategic completion, and remaining schedule independence. Existing
lifecycle/retained-task implementation is not proof that this broader policy is
fully implemented.

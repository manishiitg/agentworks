[← goals / human-decisions](index.md)

# PLAT-021 — proposals appear as decisions the user cannot answer

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | human-decisions |
| Summary | Pulse lifecycle-to-UI projection |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `ui_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** Pulse lifecycle-to-UI projection
- **Source workflow:** RTS Latency
- **Problem:** acknowledged `proposal_recorded` findings were projected into
  the same **Your decisions** queue as `awaiting_user`. The UI could therefore
  claim that approval was required while `report_human_inputs` had no pending
  record and displayed no controls.
- **Implementation (2026-08-04):** proposals have a separate **Proposed
  improvements** queue and explicitly say no answer is required. An
  `awaiting_user` finding reaches **Your decisions** only when an event carries
  a non-empty linked `human_input_id`; otherwise it becomes Pulse-owned
  **Decision request missing** work. The former ambiguous Pulse-level **Needs
  attention** summary is split into **Pulse to fix**, **Your decisions**,
  **Waiting on run**, and **Platform team**. Proposals do not make workflow
  health look blocked and are counted separately as **Ideas**.
- **Verification:** presentation tests cover a linked decision, an unlinked
  decision, and a proposal; TypeScript compilation passes.
- **Regression test:** `pulseFindingPresentation.test.ts`. The full frontend
  suite passes on 2026-08-04 (63 files, 386 tests).
- **Acceptance:** every count in **Your decisions** has a visible answerable
  input, and answering it removes it from pending UI without hiding the durable
  decision history. Proposals never inflate the approval count.

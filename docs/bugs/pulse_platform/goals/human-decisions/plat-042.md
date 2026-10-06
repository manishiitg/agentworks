[← goals / human-decisions](index.md)

# PLAT-042 — human answers lack provable actor attribution

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | human-decisions |
| Summary | report/Pulse human-input persistence and HTTP attribution |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-07` |

- **Priority:** P1
- **Owner:** report/Pulse human-input persistence and HTTP attribution
- **Source finding:** `answer_path_captures_no_operator_identity`
- **Source workflow:** Confida QA
- **Problem:** `answered_by='default'` is a valid local user ID, but it does not
  prove whether an answer entered through the report UI, an agent-mediated chat,
  or legacy data. The older `report_human_inputs` path also had no event ledger.
- **Implementation:** answers now persist server-derived `answered_by_kind`,
  `answered_via`, and `answered_session_id`; browser payloads cannot forge the
  actor ID. `report_human_input_events` records create, refresh, answer, claim,
  release, consume, and dismiss transitions. Agent-mediated explicit answers
  are marked `human_via_chat`; unattributed legacy/direct writes remain
  `legacy_unattributed` rather than being falsely upgraded. The event ledger
  stores attribution and answer shape, not a second copy of sensitive free
  text; the current input row remains the answer source of truth.
- **Verification:** focused tests prove attribution and event persistence and
  prove a forged HTTP `answered_by` value is ignored.
- **Runtime acceptance:** answer one report question in the Electron UI and one
  explicit question through chat; both must retain the same authenticated user
  while exposing distinct actor kinds/channels and append-only events.

[← crew / plans-contracts](index.md)

# PLAT-358 — Builder-created Crews are born identity-complete: purpose and role, no description

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | crew |
| Area | plans-contracts |
| Summary | fixes issue #205 `BUG_ID_003`: `create_crew` took optional `description` plus optional `purpose` and no `role`, so Builder-created Crews opened with the "needs a role and purpose" banner. |

| Coordination | Value |
|---|---|
| Assigned agent | Muse Code |
| Ticket state | `implemented and pushed to main; deployment and live acceptance pending` |
| Last synchronized | `2026-09-24` |
| Priority | `P1 correctness` |

## Problem

`create_crew` took two overlapping identity inputs — optional `description`
("short Crew summary") and optional `purpose` ("what the Crew owns, seeded
into its starter brief") — and no `role` at all. Identity completeness
needs role AND purpose (`isWorkIdentityComplete`), and the Identity panel
reads purpose from the top-level project description, so Builder-created
Crews were born incomplete in two ways. QA on the Confida server
([issue #205](https://github.com/manishiitg/coding-agent-loop/issues/205),
`BUG_ID_003`) showed a Crew whose proposal carried a purpose still opening
with "This Crew needs a role and purpose before it can help at its best":
role was never written, and a purpose-only proposal left the description
slot empty.

## Authorized behavior

Crew creation has one identity statement, not two: required `purpose` plus
required `role`. Purpose fills the stored description slot the Identity
panel reads and is seeded into the starter brief; role fills the identity
block. A Builder-created Crew opens with complete identity and no setup
banner. The `product.json` storage field keeps its `description` name (the
slot the Identity panel already reads), so existing Crews need no
migration; pre-fix Crews without a role keep showing the banner until a
role is set once in Identity.

## Implementation

Backend (`agent_go/cmd/server`):

- `crew_builder_tools.go`: `create_crew` schema drops `description`;
  `purpose` and `role` (≤120 chars, matching the Identity limit) join the
  required list; arg mapping passes both through.
- `crew_creation.go`: `CreateCrewRequest.Description` removed, `Role`
  added; validation requires non-empty purpose (≤2000) and role (≤120);
  `writeCrewCreationManifests` writes purpose into the `description` slot
  and role into the identity block; the idempotency fingerprint swaps
  description for role.

Regression: `crew_creation_test.go` asserts the description slot carries
the purpose and the identity block carries the role; validation cases
cover missing/overlong role and purpose; the obsolete empty-starter test
now asserts the brief is seeded (an empty brief is unreachable once
purpose is required).

## Regression and acceptance

- [ ] Deploy to Confida; Builder-create a Crew with purpose + role.
- [ ] Crew opens with no identity banner; Identity → General shows both.
- [ ] Re-run issue #205 `BUG_ID_003` repro steps.

Related: [issue #205](https://github.com/manishiitg/coding-agent-loop/issues/205)
(`BUG_ID_003`), [PLAT-353](../security-sandbox/plat-353.md) (Crew Run mode, `BUG_ID_001`).

## Register notes

[PLAT-358](plat-358.md) fixes issue #205
`BUG_ID_003`: `create_crew` took optional `description` plus optional
`purpose` and no `role`, so Builder-created Crews opened with the "needs a
role and purpose" banner. The tool now takes required `purpose` (stored in
the purpose slot, seeded into the starter brief) and required `role`
(stored in the identity block); `description` is removed. Pushed to main;
deployment and live acceptance pending.

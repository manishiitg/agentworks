[← goals / human-decisions](index.md)

## SQLite backlog reconciled — 2026-09-05

Resolved PUL-D7D173FB in the corresponding workflow databases after checking
the existing implementation and passing focused regression tests. Full evidence
and the complete remaining inventory: [reconciliation audit](../../../../audits/platform-backlog-reconciliation-2026-09-05.md).
This is internal tracking closure, not a claim of a new deployed end-to-end run.
Previous SQLite records are retained in audit events; unrelated findings remain
open. No business data or historical schedule outcome was rewritten.

# PLAT-218 — `create_human_input_request` hid invalid nested HTTP fields behind a false “apply_contract must be an object” error

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | goals |
| Area | human-decisions |
| Summary | P1 — Pulse could not persist an approval-gated repair and then |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` |
| Last synchronized | `2026-08-29` |

- **Priority:** P1 — Pulse could not persist an approval-gated repair and then
  misdiagnosed the correctly transported outer JSON object as damaged by the
  HTTP bridge.
- **Owner:** `agent_go/cmd/server/report_human_inputs.go` and the Pulse
  Review+Fix guidance that constructs the custom HTTP request.
- **Related:** Upwork `PUL-D7D173FB`; the failed decision request
  `technical-decision-derive-route-scripted-migration-2026-08-28`.

## Reproduction and corrected diagnosis

The retained coding-agent session contains the exact shell and HTTP payload.
It built the request with `jq -n` and posted it through
`$MCP_CUSTOM/create_human_input_request`. The HTTP bridge preserved
`apply_contract` as a JSON object. Two nested fields violated the published
schema instead:

- `approved_scope` was an object; the contract requires one string.
- `post_run_proof` was an array; the contract requires one string.

The handler marshalled the outer value and unmarshalled it directly into the
typed Go struct. Any nested type error was collapsed into
`apply_contract must be an object`, falsely implicating the HTTP boundary and
giving the reviewer no actionable correction.

## Fix

- Kept the HTTP contract strict; stringified JSON is not accepted as an
  alternate transport shape.
- Validate that the outer value is an object, then report the exact nested
  string field whose type is wrong.
- Preserve the underlying decoder error for other malformed contract fields.
- Clarify in both the published tool schema and Pulse Review+Fix guidance that
  `approved_scope` and `post_run_proof` are strings while `pre_run_checks` is
  the string array.

## Verification

`TestReportHumanInputToolReportsExactInvalidApplyContractField` covers the
outer-string, nested-object, and nested-array failures. The existing structured
contract persistence test still passes:

```text
go test ./cmd/server -run 'TestReportHumanInputToolReportsExactInvalidApplyContractField|TestReportHumanInputPersistsStructuredApplyContract' -count=1
```

The full guidance and affected Go package tests are green in the implementation
commit.

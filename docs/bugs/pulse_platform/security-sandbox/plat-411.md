[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-411 — Relay agent tool errors in the invoice run

| Coordination | Value |
|---|---|
| State | open; latest invoice retry completed |
| Date | 2026-10-04 |
| Owner | security-sandbox |

## Observed on Excellence

Read-only inspection of Relay test on release agents-ef5666b3-20261003210541.
The archived iteration-5/default run ended at 2026-10-03T19:16:02Z with
extract_pdf_text complete and extract_invoice failed. Its session entry reports
`final agent response must be valid JSON`. The final response contained JSON
followed by prose explaining failed tool calls, so rejection was correct.

The agent transcript also reports:

- execute_shell_command could not launch: SANDBOX_UNAVAILABLE inspecting
  `/srv/agents/data/docs/Workflow/test/db/README.md` (missing file).
- agent_browser returned `this account has no slot yet`.
- Creating db/README.md through a bridge edit was denied, consistent with its
  output-only write grant. The step should not repair workspace policy itself.

These are distinct from the PLAT-399 script output-directory write issue.
Trace the optional database read grants and step user identity propagation
before claiming these tool paths are fixed. No live files or services changed.

## Latest run verification

Current iteration-0/default completed at 2026-10-03T19:18:14Z in 57.466 seconds.
Both completion receipts and execution summaries are completed. Both script
and authored-agent result.json files exist. Final JSON contains invoice
INV-2026-0042, subtotal 150, tax 15 and total 165 USD, with two line items.
The saved plan now embeds prior PDF text in the authored user message.
Success verifies output handoff, not the failed optional tool paths above.

## Remaining

Reproduce and trace missing-path admission and slot identity in an isolated
agent-step fixture, fix shared paths if needed, and verify their tool calls.

[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-412 — Relay message and validation guidance

| Coordination | Value |
|---|---|
| State | fixed on main; deployment pending |
| Date | 2026-10-04 |
| Owner | coding-agent-bridge |

## Gap and implementation

The manifest-owned Relay builder prompt and skill listed variable syntax but
did not explicitly say context_dependencies does not inject prior output into
authored prompts. A live invoice run assumed implicit context and failed;
the retry passed after embedding the text through a step-output reference.

Both guidance sources now require self-contained message templates, show the
invoice handoff example, explain automatic final JSON persistence and reject
the assumption that writing a file substitutes for a JSON-only response.
Exact user-supplied prompts are preserved.

Clarify existing validation boundaries: Relay graph validation permits only
authored user_message items, not workflow prevalidation/repair loops. Missing
references and invalid final JSON fail runs. Field/schema checks requested by
the user can be explicit Python script nodes. No runtime behavior was changed.

## Verification

Rendered the builder prompt with Go text/template and checked the input and
prior-output variable examples survive literally. Reviewed guidance against
ValidateRelayPlan, authored prompt rendering and final JSON persistence.

## Remaining

Deploy the new embedded prompt/skill through the normal release. PLAT-411's
separate failed tool paths remain open.

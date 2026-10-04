[← Pulse platform index](../../pulse_platform_issue_register.md)

# PLAT-430 — Workflow Costs is too text heavy

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Fixed on main; deployment pending |
| Last synchronized | 2026-10-04 |

## Request and evidence

The user's screenshot shows a workflow/Code Costs inspector with cache
explanations repeated in the header and expanded date, long missing-usage
sentences repeated in its Chat card, an event-ledger description, and daily
accounting prose. The user specifically asked to remove the explanatory text
entirely and emphasize numbers, not move the text into help.

## Changes

- Token summaries show input, fresh input, cached input, output, optional cache
  writes and a numeric cache percentage. Shared token cards no longer include
  explanatory paragraphs, including their Providers/conversation uses.
- The header shows its cost once and uses the compact token card without a
  duplicate input/output sentence. Missing-usage and unpriced counters remain
  short visible markers; zero-cost unpriced totals still say Not priced.
- Daily rows show Date (UTC), Input, Cached, Output and Total. Legacy missing
  input/output/cache data stays a dash; it is not invented as zero.
- Daily expansion keeps tokens and conversations visible; run, model, activity
  and phase details remain available in a secondary disclosure. Each date can
  still expand and collapse independently.
- Activity cards show label and cost, omit empty categories and remove repeated
  token/time descriptions. The lone Code Chat card is omitted because it only
  duplicates the overall total. Workflow activity categories remain available.
- Removed explanatory subtitles from daily, run and legacy builder sections,
  and shortened the shared conversation/prompt-size headings. Chat inspection,
  pagination and latest prompt character counts remain intact.

## Verification

- Focused existing Costs, Providers and conversation tests cover canonical
  input/cache accounting, linked chat access, prompt sizes, daily expansion,
  totals and older data; text expectations follow the shortened labels.
- All 20 focused tests across seven files pass; TypeScript project build passes.
- Visually checked the actual header and daily components in a local fixture
  based on the supplied screenshot, in Code and workflow modes, including two
  dates open at once. This verifies the UI with fixture values, not live RTS
  data. Preview screenshot: local `artifacts/costs-readability-20261004.jpg`.

## Remaining

Deploy the frontend change and verify on the user's workflow. No accounting,
pricing, authorization or production data was changed by this UI work.

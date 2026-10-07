[← relays / frontend-chat](index.md)

# PLAT-637: Show a plain-language Relay overview before implementation code

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | relays |
| Area | frontend-chat |
| Summary | Python Relays opened on raw source despite being usable by people who do not program. Open on a readable overview, retain optional code and actual run results. |

## What happened

Excellence showed Source / Calls with relay.py immediately after opening a Relay.
The landing copy also told users to write Python logic. This exposed implementation
as the product's main interaction, although Builder chat authors the program.

## Fix

- Default to Overview; render Builder-authored relay.md using the shared Markdown
  renderer. Show purpose, input fields, ordered steps/conditions, tools and result.
- Rename Calls to Runs; retain Code as an optional tab with the exact source/editor.
- New starter Relays include a readable companion; existing source without an
  overview gets chat guidance, without inventing a summary or overwriting code.
- Update onboarding, product.yaml command copy, product-owned Builder prompt and
  relay-builder skill to create/maintain the overview beside the implementation.
- Reuse workspace reads, live refresh (including relay.md change notices), Markdown rendering and release snapshots.
  relay.md documents the draft; relay.py remains the execution definition.

## Verification

- Frontend TypeScript build passed.
- Seven focused frontend checks passed, including default Overview, optional Code,
  missing-file chat guidance, versioned results, creation access and shared layout.
- In-app browser preview of the actual component with labelled sample data verified
  Overview, Runs/results and optional Code. Screenshot: /tmp/relay-overview-20261007.jpg.
- Real workspace/Python test covers starter creation, preservation of an authored
  overview and frozen publication alongside source passed with real workspace handlers.

## Left

Deploy to Excellence. Existing Relays without an overview require the Builder to
explain their saved source. No production Relay data edited.

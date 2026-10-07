[← chat / frontend-chat](index.md)

# PLAT-689: Compact agent question cards

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | chat |
| Area | frontend-chat |
| Summary | Agent question cards took too much space and looked bigger than chat text; made compact |

## What happened

## Fix

## Left

## What happened

The owner (2026-10-07): the question card's options "take up too much space and look very big compared to normal
texts". Options used text-sm with py-2.5 padding and 8px gaps, so a two-question card filled the screen.

## Fix

`CodingAgentQuestionCard.tsx`: question 13px, option labels 12px, descriptions 11px, option padding py-1.5 with 4px
gaps, card padding p-3, smaller buttons and radio marks. Behaviour is unchanged.

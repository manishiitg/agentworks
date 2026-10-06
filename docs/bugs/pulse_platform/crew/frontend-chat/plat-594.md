[← crew / frontend-chat](index.md)

# PLAT-594: Show installed template setup progress and exclude optional checks from completion

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | crew |
| Area | frontend-chat |
| Summary | Installed templates looked fully set up while chat showed only Setup pending; optional checks also incorrectly blocked completion. |

## What happened

Ashutosh reported 17 installed templates in Excellence Yami, all still showing
Setup pending in chat after starting their setup conversations. Live inspection
on 2026-10-06 confirmed all 17 installations and valid saved checklists. None
had completed required setup: Finance had identity and skill; 11 others had only
skill; the remaining five had no completed checks. Submission records confirm
that setup actions reached the chat. Installation and verified setup are separate
states; pressing Set up in chat starts a conversation, not a completion event.

There was a separate real completion bug: optional delivery/recurrence/sharing
checks counted toward setup completion, contrary to their template definitions.

## Fix

- Show required-check progress and a collapsed What remains list in each chat card.
- Offer Continue setup when saved progress exists.
- Label right-panel entries Installed and explain that required setup checks are
  completed in chat.
- Optional checks do not block completion. Required/optional definitions come
  from the installed catalog, so a saved progress edit cannot weaken requirements.
- Preserve saved files and progress; do not certify unverified checks automatically.

Verification: existing React checklist regression now uses Yami's Finance state
(two of six required checks), advances through refresh to all required checks
complete with optional extras unfinished, and asserts no progress writes. Added
one regression for edited optional flags. Both template component suites pass;
TypeScript compilation passes.

## Left

Yami's missing setup decisions, access, and
first-result checks still require owner/agent verification.

## Deployment evidence — 2026-10-06

Excellence release `agents-2a169cf4-20261006121601` contains builder revision
`2a169cf4b3`. Frontend build/catalog/bundle checks, public health checks, and
the full slot self-test passed (156 passed, zero failed, 16 skipped).

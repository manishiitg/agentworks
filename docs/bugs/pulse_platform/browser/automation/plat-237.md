[← browser / automation](index.md)

# PLAT-237 — X quote-compose sometimes renders reply mode instead of quote mode: not a platform defect

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | browser |
| Area | automation |
| Summary | harness_issue (self-classified), severity medium. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `investigated — reclassified, no in-repo fix possible or needed` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue (self-classified), severity medium.
- **Findings:** Twitter/social-media `PUL-7074FD09`, `PUL-5A977094` — two
  independent occurrences of the identical mechanism: on a live target,
  Repost→Quote navigates to `x.com/compose/post`, but the composer renders
  in reply mode (no quote embed, disabled `Reply` control, no enabled
  `Post`/`Tweet` control) instead of quote mode.

## Why this is not an in-repo fix, checked first (same boundary as PLAT-224/232)

`agent_browser click` on a menu item has no Go-side handling in
`agent_go/pkg/browser/executor.go` at all — pure passthrough to the
external `agent-browser` binary, which itself just drives a real click on
whatever DOM element X's own frontend rendered. There is no Go-side hook
this repository owns that could influence which compose mode X's own SPA
decides to render after that click. This is an X frontend/UI
characteristic (or a genuine transient race in its own client-side
routing), not anything a click wrapper controls.

## The workflow already correctly detects and handles this

`workspace-docs/Workflow/social-media/learnings/_global/references/quote-tweet-execution.md`
has a dedicated "Compose-mode guard and safe blocker handling" section,
confirmed against this exact failure signature: *"X navigated to
`https://x.com/compose/post` after Repost→Quote but rendered no quote
dialog/content or embedded source and exposed only the underlying disabled
`Reply` control... record the concrete UI evidence, leave `quote_url`
null, emit a skipped/blocked result with a non-null reason."* Both
findings' own evidence confirms the workflow followed exactly this: both
were recorded as `status=skipped`/blocked with the real UI evidence
preserved, not fabricated as successful quotes. This is mature,
already-correct workflow-owned defensive behavior, not a gap.

## Conclusion

This is a third-party UI flake in X's own Repost→Quote flow, already
correctly detected and handled by the workflow's own execution logic and
learnings. There is no platform code to change and no undocumented
mitigation to add — unlike PLAT-232/233, where a genuine gap in shared
guidance was found and fixed, this exact failure mode and its correct
response were already fully documented before these two findings were
filed. Root cause is either an inherent X-side characteristic (occasional
compose-mode misrender) or transient timing on X's end, outside this
platform's control either way.

## Verification

Checked `agent_go/pkg/browser/executor.go` for any Go-side handling of
`click`/menu-item interactions — none exists (passthrough only, same as
prior boundary findings). Checked
`quote-tweet-execution.md`'s "Compose-mode guard" section against both
findings' exact evidence text — the documented failure signature and
handling match verbatim. No code changed; no test suite applicable.

## Reverify

Not applicable — this is a documented, already-correctly-handled
third-party characteristic, not a fix awaiting live confirmation.

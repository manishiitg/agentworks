[← browser / automation](index.md)

# PLAT-233 — `agent_browser wait`'s fixed-delay mode takes a bare number, not `--ms`

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | browser |
| Area | automation |
| Summary | harness_issue, severity medium across findings. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `guidance fixed; independently reproduced against the real binary; live reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue, severity medium across findings.
- **Findings:** Twitter/social-media `PUL-3A639FC5`, `PUL-F7094097`,
  `PUL-0C1112FB`, `PUL-F1E0ACE7` — four independent occurrences during
  like-settle pacing, all with the identical symptom: a short requested wait
  (~1800ms, for human-pacing between actions) fails with
  `Wait timed out after 25000ms`, even though the target tab stays
  responsive and a follow-up DOM read confirms the underlying action already
  landed.

## Root cause, directly reproduced against the installed binary

`agent-browser wait --help` documents the fixed-delay mode as a **bare
positional millisecond argument**: `agent-browser wait <ms>`. There is no
`--ms` flag anywhere in the tool's own help output — its other modes are a
selector, `--url`, `--load`, `--fn`, `--text`, or `--download`.

All four findings used `--ms 1800` (or equivalent). Reproduced directly,
locally installed binary, no CDP target needed to trigger it:

```text
$ agent-browser wait --ms 1800
✗ Wait timed out after 25000ms          (~25 real seconds elapsed)

$ time agent-browser wait 1800
✓ Done
agent-browser wait 1800  1.812 total    (~1.8 real seconds elapsed, matching the request)
```

`--ms` is not rejected as an unrecognized flag; the call silently falls
through to a different, unrelated wait mode and then fails with a
misleading message after a fixed ~25 second internal ceiling, regardless of
what number followed `--ms`. Nothing in `browser-usage.md` documented the
correct bare-number syntax before this fix, so every workflow that needed a
short settle delay had equal odds of guessing the wrong flag form.

## Fix

Added a dedicated section to `browser-usage.md` naming the correct syntax,
the exact wrong form to avoid, the directly-reproduced before/after timing,
and a diagnostic rule: a ~25000ms timeout on a short intended wait almost
always means `--ms` was used by mistake, not that the browser or page is
unresponsive.

## Verification

Directly reproduced against the real, locally installed `agent-browser`
binary — not inferred from finding text alone. `go test
./cmd/server/guidance/...` passes (template renders clean). No Go code
changed; this is a documentation-only fix for a real CLI usage error, same
shape as PLAT-223.

## Reverify

No live step has loaded this corrected guidance through the deployed
server yet. Reverify by observing whether a future like/follow pacing wait
now uses the bare-number form and completes in the requested duration
instead of timing out at ~25000ms.

## Independent review (2026-08-29)

Confirmed against the installed `agent-browser 0.35.1` binary rather than the
ticket narrative alone. `agent-browser wait --help` documents the positional
`<ms>` form; `wait 75` completed successfully, while `wait --ms 75`
reproduced the misleading timeout after approximately 25.03 seconds. All four
cited social-media findings are recorded `resolved` with PLAT-233 as their
resolution, and the focused guidance tests pass.

No corrective code change was requested by this review. The remaining step is
operational: restart onto a build containing this guidance and observe a live
workflow call using the positional form.

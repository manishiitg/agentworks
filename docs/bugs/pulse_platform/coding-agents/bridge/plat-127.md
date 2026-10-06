[← platform / coding-agent-bridge](index.md)

# PLAT-127 — the tool-error suspect scan flags two documented successful outcomes as failures

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | coding-agents |
| Area | bridge |
| Summary | P3 — nothing is lost or broken; the `[TOOL_ERROR_SUSPECT]` |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — four suppressions shipped with fail-before/pass-after tests; live reverify pending |
| Last synchronized | `2026-08-19` |

- **Priority:** P3 — nothing is lost or broken; the `[TOOL_ERROR_SUSPECT]`
  channel exists specifically for humans reviewing logs, and these classes
  drowned out the genuine signal in it.
- **Owner:** `mcpagent/toolerr` (`Suspicious`, `problemReportingTools`)
- **Fixed in:** [manishiitg/mcpagent@2200bad](https://github.com/manishiitg/mcpagent/commit/2200bad)
  (original two), [manishiitg/mcpagent@521b73a](https://github.com/manishiitg/mcpagent/commit/521b73a)
  (third, 2026-08-19), [manishiitg/mcpagent@787a8ee](https://github.com/manishiitg/mcpagent/commit/787a8ee)
  (fourth, 2026-08-19) — a separate repo from this one, vendored into
  `agent_go` via `go.mod`'s local `replace` directive. No `agent_go` change
  was needed or made; this ticket documents and links the fix so it is
  discoverable from this register.

## Third instance, 2026-08-19: `notify_user`'s empty `"failed":{}` field

Same class, same file, same technique — found while investigating
`check-form-26as-xspaces`'s tool-error logs after PLAT-139. A successful
`notify_user` send (`exit_code:0`, `"status":"delivered"`) was flagged purely
because `"failed":{}` (zero channels failed) contains the word "failed".
Doubly counted in practice: `notify_user` is frequently relayed through
`execute_shell_command` (a python/curl wrapper hitting the same endpoint), so
one successful send produced two `[TOOL_ERROR_SUSPECT]` lines under two
different tool names.

**A blanket fix was considered first and rejected, correctly, by an existing
test.** The initial theory — trust `exit_code:0` and skip the generic
word-list scan entirely when it's present — is directly disproven by
`TestSuspiciousForToolCatchesHarnessEnvelopeWrappedInShellSuccess`, which
exists specifically because `exit_code:0` alongside
`"stdout":"ERROR: tool execution failed: ..."` is the exact masked-failure
class this whole file was built to catch (34 bridge failures rendered as
green checks, 2026-08-01). A blanket exit_code=0 trust would have reintroduced
that bug. The correct fix follows this file's existing narrow-suppression
discipline instead: a new `notifyUserEmptyFailedFieldPattern` regex strips
only the documented empty-object shape, the same technique as
`benignJSONOutcomePattern` for `agent_browser`'s `{"waited":"timeout"}`.

**The first regex attempt also failed, against the real captured payload, not
a guess.** `"failed"\s*:\s*\{\s*\}` (matching the existing pattern's style
exactly) missed the `execute_shell_command`-wrapped case: that result is
JSON-in-JSON — `notify_user`'s JSON is a string *value* of the wrapper's own
`"stdout"` field, so its inner quotes are backslash-escaped
(`\"failed\":{}`, not `"failed":{}`). Widened to
`\\*"failed\\*"\s*:\s*\{\s*\}` to match at any escaping depth. Verified: a
test built directly from the real captured log line
(`{"stdout":"{\"delivered\":[\"gmail\"],\"failed\":{},...`) failed against
the first pattern and passes against the second — the same discipline this
session repeatedly needed today: write the test against the actual
production string, not an idealized one.

A real per-channel failure (`"failed":{"whatsapp":"connection refused"}`) has
content inside the braces and is untouched by the pattern — still fires
normally, same as the other two suppressions in this file.

## Fourth instance, 2026-08-19: `agent_browser`'s tab-list page title/url content

Found investigating the same run's browser tool errors. A tab list on the
ICICI-BANK-PARSING session included a genuine page titled *"Sorry, you have
been logged out !"* — flagged `[TOOL_ERROR_SUSPECT]` purely because that is a
third-party page title, not a report on the tab-list command's own outcome.

**The exact triggering payload could not be recovered before writing a fix
was attempted.** The captured log line was truncated to 400 bytes (this
file's own `TruncateForLog` budget) and the specific text that matched could
not be located in the run's saved `execution/*-conversation.json` logs
(searched directly; no match for the exact 3-tab combination originally
logged). Rather than guess, the fix was built from the actual line-format
source instead: `agent_go`'s `pkg/browser/cdp_tabs.go` builds each tab-list
line as `- <tabID>[ active][ label=%q][ title=%q][ url=%q]`. `%q`
backslash-escapes internal quotes (confirmed by running `fmt.Printf`
directly), so the suppression pattern matches Go string-literal syntax —
`(?:[^"\\]|\\.)` — rather than a naive `[^"]*`, which would truncate at the
first escaped quote.

**A blanket `agent_browser`/`"not found"` suppression was considered and
rejected**, per this ticket's own standing warning that the tool has real
failures too. One candidate reason to keep the suppression narrow turned out
to be based on a misreading, caught before shipping: `cdp_tabs.go`'s own
empty-list case returns the bare sentinel `"(no tabs found)"`, initially
assumed to be a real signal the scoping needed to protect. Checked directly
(`python3 -c "'not found' in '(no tabs found)'"` → `False`) — "no tabs found"
does not contain "not found" ("no" vs "not") and was never flagged either
way. The comment and a test built on that false premise were corrected/dropped
rather than left as a misleading regression guard. The suppression's real,
verified boundary is instead a genuine element-not-found failure reported
*outside* a `title=`/`url=` attribute, which a dedicated test confirms still
fires normally.

## How it surfaced

Auditing a day's tool-error logs for PLAT-125/126 turned up 929 error-level
lines, of which 524 were `[TOOL_ERROR_SUSPECT]` — the detector's own
"reported success but reads like a failure" heuristic. Tracing every distinct
shape found two categories that were false positives of the detector itself,
not real tool problems:

**`agent_browser` "timeout" — 142 of 524 (27%).** `agent_browser`'s `wait`
action reports `{"waited":"timeout"}` when its poll deadline elapses without
the awaited condition firing. That is success — the tool did what it was
asked and reported what happened — but the substring scan for the word
"timeout" matched the value text of a field that was never an error signal.
Every one of the 142 carried `"success":true` alongside it.

**`get_route_description` — 12 of 12.** Called with no `route_id` (a full
route-catalog dump), its result is prose describing every configured route's
behavior. One of those descriptions happened to contain a word the substring
scanner treats as a failure signal. Zero of the 12 had any actual error
field. This is the exact shape that justified `problemReportingTools` in the
first place — a documented incident on that list states *"70 of 173 suspect
hits came from these five, and not one was a tool failure."*
`get_route_description` was doing the same thing and was not on the list.

## Fix

**Field-aware suppression, not a blanket exemption.** `agent_browser` has
real failures too (`SNAPSHOT_RESULT_TOO_LARGE`, CDP tab-selection errors), so
excluding the whole tool would hide genuine problems. `benignJSONOutcomePattern`
strips only the exact `"waited":"timeout"` field/value pair before the signal
scan runs — narrow by construction, since it removes text this codebase
itself produces with a known vocabulary, not general prose. A real timeout
reported anywhere else in the same payload is untouched and still fires;
pinned by a test that puts both in the same result.

**`get_route_description` added to `problemReportingTools`**, the existing
per-tool suppression list, alongside `get_workflow_command_guidance` — the
same shape, same justification, same mechanism.

## Verification

Four new tests in `toolerr/toolerr_test.go`, against the literal payload
shape from the live log line:

- `{"success":true, ..., "waited":"timeout"}` is not flagged
- the same field with `"waited": "timeout"` spacing is not flagged
- a genuine timeout (`context deadline exceeded`) *beside* a benign
  `"waited":"timeout"` field is still flagged — the suppression does not
  overreach
- `get_route_description` returning prose that would otherwise trip the scan
  is not flagged; an unrelated tool given the same text still is

Fail-before/pass-after confirmed by stashing `toolerr.go` alone and
re-running. Full `mcpagent` repo: `go build`, `go vet` clean; one pre-existing
failure (`TestAgentReviewsApproved`, unrelated to this change), identical
before and after.

## Not fixed here

- **The general substring-list design is unchanged**, deliberately. Its own
  documentation states the accepted trade — *"a false positive costs one log
  line, a false negative costs an investigation that starts with no
  evidence"* — and both fixes here are precise, evidenced exceptions to that
  design, not a rewrite of it.
- **No other `TOOL_ERROR_SUSPECT` category was audited for false positives**
  beyond these two; they were the largest by volume (154 of 524, ~29%) found
  in one day's data, not a claim that nothing else in the list ever
  misfires.

[← browser / automation](index.md)

# PLAT-224 — `agent_browser network requests` tab-scoping is a third-party CLI defect, not a repository fix

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | browser |
| Area | automation |
| Summary | P1 in the queue, but see conclusion below. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `investigated — no in-repo fix possible` |
| Last synchronized | `2026-08-29` |

- **Priority:** P1 in the queue, but see conclusion below.
- **Owner:** N/A — see conclusion.
- **Related:** same class as [PLAT-215](plat-215.md) (`agent_browser.download`),
  which was also filed as a browser-tool defect and turned out to be a
  diagnosis correction, not a code fix, because the affected behavior lived
  outside this repository's own code.

## Finding

Upwork `PUL-9CCE9488`: `agent_browser network requests` with an explicit tab
qualifier (`t31`) returned unrelated requests from other open tabs
(LinkedIn, Reddit, X/Twitter, Substack, BuiltIn NYC, Google Analytics) in a
shared CDP Chrome instance, instead of scoping to the requested tab.

## Investigation

Searched this repository for where `agent_browser network requests` is
implemented and where its results could be filtered by tab:

- `agent-browser` is invoked as an **installed external binary**
  (`agent_go/pkg/browser/executor.go:1683`: `exec.CommandContext(ctx,
  "agent-browser", ...)`). Its source is not vendored anywhere in this repo —
  confirmed by a repo-wide search for `agent-browser`/`agent_browser` outside
  `agent_go/pkg/browser/`, which returns nothing.
- The only reference to the `"network"` command inside this repo's Go code
  (`executor.go:263`, `cdpExclusiveFeatureAction`) handles exclusive-lock
  bookkeeping for `network har start/stop` (a different subcommand, HAR
  capture). It does not touch, parse, or forward tab identity for
  `network requests` at all.
- There is **no Go-side post-processing of the `network requests` output** —
  no JSON parsing, no field inspection, nothing this repo could extend into
  a client-side filter. The command's `--tab`/tab-selection argument is
  passed straight through to the external binary, and whatever tab-scoping
  logic exists (or doesn't) for the returned request list lives entirely
  inside that external tool's own implementation.

A client-side filter (reject/drop entries whose tab/target identifier
doesn't match the requested tab, after the external binary returns its
result) was considered and rejected for this pass: it would require knowing
the exact field the external tool's JSON output uses to identify a request's
owning tab (`tabId`, `targetId`, `frameId`, or something else), which is not
discoverable from this repository — the tool's source isn't here, and there
is no live CDP fixture in this investigation to observe a real response
against. Guessing at an unknown third-party JSON schema risks a filter that
silently drops legitimate requests or silently lets the bug through
unfiltered, which is worse than the current honest "unscoped" behavior.

## Conclusion

No platform-code fix is possible from within this repository. This matches
the finding's own `next_check`, filed verbatim by the original reviewer:
*"the platform's `agent_browser` network command is fixed to genuinely
scope by tab in shared CDP mode, OR its documentation is corrected to state
results are browser-wide rather than tab-scoped. No workflow-side change can
fix a shared tool defect."* Both of those repairs are inside the external
`agent-browser` tool or its bundled documentation, neither of which this
repository owns or ships.

**Recommended next action, outside this repository:** either fix
`agent-browser`'s own tab-scoping for `network requests` upstream, or
correct its bundled documentation (surfaced to agents verbatim via
`agent_browser(command="skills")`, `executor.go:1270`) to state results are
browser-wide, not tab-scoped, so a future safety check (e.g. "confirm no
unintended request fired") is not misled by an unscoped result.

## Verification

N/A — no files changed. This is a closure/reclassification record, matching
the pattern already used for PLAT-207/208/209/210/213/217.

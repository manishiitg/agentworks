[← platform / browser-automation](index.md)

# PLAT-028 — a recovered CDP tab leaks into page-action arguments

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | platform |
| Area | browser-automation |
| Summary | `agent_go/pkg/browser/executor.go` |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented` |
| Last synchronized | `2026-08-04` |

- **Priority:** P1
- **Owner:** `agent_go/pkg/browser/executor.go`
- **Evidence:** an agent issued a CDP click with arguments equivalent to
  `["--cdp", "http://localhost:9222", "t2", "e64"]`. The harness recognized
  bare `t2` as the tab and selected it, but invoked `agent-browser` as
  `click t2 e64`; the CLI then treated `t2` as the element reference and failed
  with `Element not found: t2`.
- **Root cause:** bare-tab recovery mutated `commandArgs` only after those
  arguments had already been copied into the subprocess command. Selection and
  execution therefore observed two different argument lists.
- **Implementation:** recover the narrow, unambiguous bare `tN` form during
  initial CDP normalization, before artifact/upload planning and subprocess
  construction. The recovered tab becomes routing metadata and is removed from
  page-action arguments. Recovery still ignores flag values such as
  `type --text t7`, and a call with no tab remains a hard error.
- **Regression test:**
  `TestAgentBrowserBareTabIsNotForwardedAsPageActionArgument` sends the exact
  `t2` + `e64` form through the real executor boundary and proves the final
  command selects `t2` but contains `click e64`, never `click t2 e64`.
- **Verification:** `go test -count=1 ./pkg/browser` and
  `go vet ./pkg/browser` pass.
- **Acceptance:** the next real shared-CDP workflow may use either canonical
  `tab/--tab` syntax or the recoverable bare `tN` form without forwarding the
  tab as an element selector. Missing-tab calls must still fail with the
  canonical retry guidance.

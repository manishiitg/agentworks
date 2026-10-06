# PLAT-536: a finished function call returns every step's full output, 400 KB

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | integrations |
| Area | mcp |
| Summary | fixed on main, not deployed: the call result lists each step's files and sizes; clients read the files they need. |

**State:** fixed on main (2026-10-05); not deployed; not yet checked with a live `review_pr` call on RTS. P3.

**Found:** 2026-10-05, RTS: `get_workflow_function_call` for the finished `review_pr` run returned 401,836 characters (every step's output files, including all research JSON the review gathered), over a client's output limit.

**Fix:** `workflowCallResultJSON` (`trigger_link_tools.go`): a workflow-backed call's result is `status`, `error`, `run_folder` and, per step, `files` (name, path, size_bytes), plus a note to read the files from `run_folder` with `read_file` / `get_run`. A Relay's declared output still comes from the Relay (unchanged). The inline `outputs` of every step (up to 128 KB per file, 2 MB total) are no longer part of the call result. Test `TestWorkflowFunctionCallStatusAndResultSize`.

**Left:** check the size of a finished `review_pr` result on RTS after the next deploy (owner's go). If a caller needs a small output inline, add a per-function declared result rather than all step files.

## Register notes

[PLAT-536](plat-536.md), P3, fixed on main, not deployed: the call result lists each step's files and sizes; clients read the files they need.

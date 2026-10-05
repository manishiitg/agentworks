# PLAT-536: a finished function call returns every step's full output, 400 KB

**State:** open. P3.

**Found:** 2026-10-05, RTS: `get_workflow_function_call` for the finished `review_pr` run returned 401,836 characters (every step's output files, including all research JSON the review gathered), over a client's output limit.

**Fix:** return the run status, error, and for each step its output file names and sizes, plus the function's declared result if it has one; the files are read with `read_file` / `get_run`. Keep a size cap with a clear "truncated, read X" pointer as a last resort. Check with the same call: the response stays small and the review result is still reachable.

**Left:** everything.

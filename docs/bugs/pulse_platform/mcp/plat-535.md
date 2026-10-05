# PLAT-535: a workflow function call reports `queued` for its whole run

**State:** open. P3.

**Found:** 2026-10-05, RTS, `call_workflow_function` on `rts-pr-reviweer` / `review_pr` (PR 180): `get_workflow_function_call` returned `status: queued` for about eight minutes while `list_executions` showed the run executing (gate done, review step running). It changed to `completed` only at the end.

**Likely cause (read, not verified live):** the call record is created `queued` (`product_webhooks.go`, Status "queued") and only moves to `running` when a Crew agent reports progress (`crew_functions.go`, the progress tool). A workflow-backed function never reports progress, so nothing moves it when its run starts.

**Fix:** set `running` when the run is accepted and starts (the same moment `list_executions` shows it), and `queued` only while it waits behind another run. Check with a real `review_pr` call: status must be `running` shortly after start.

**Left:** everything.

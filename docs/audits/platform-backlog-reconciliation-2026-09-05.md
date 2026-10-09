# Platform backlog reconciliation — 2026-09-05

Latest status: the [full remaining-report reconciliation](platform-open-report-reconciliation-2026-09-05.md)
subsequently reviewed all 96 remaining reports, resolved eight more exact cases,
and grouped the **88** still-open reports into **61 investigation scopes**.
The earlier counts below are dated intermediate snapshots, not the latest total.

## Subsequent verified fix/closure batch

The original inventory below is retained as a historical snapshot. After its
eight closures, seven additional exact findings are resolved, leaving **96 typed
harness findings** unresolved. Across today's original nine, subsequent eight,
and this seven, **24 of the original 120** have been closed. The 27 workflow
findings and ten unclassified candidates were not changed by this batch.

| Workflow | Finding | Disposition |
|---|---|---|
| salesoutreach | PUL-4719B06D | PLAT-258 local fix: evidenced same-run correction atomically updates state and audit, preserving previous receipts |
| salesoutreach | PUL-09511B0E | PLAT-258 local fix: active child work blocks premature parent failure |
| salesoutreach | PUL-E3F22BCC | Conflicting child-session receipt checker removed in a8aaaa012; parent-run validation verified |
| hetznerssh | PUL-76329350 | Same retired receipt-checker defect |
| tectonicusadaytrading | PUL-F9BA8AF2 | Same retired receipt-checker defect |
| build-in-public | PUL-FF23BCD5 | PLAT-288 local fix: Workshop fast_path_only gate was inverted; saved-script safety checks retained |
| instagram | PUL-51D2EC0F | PLAT-061 superseded field: db_access was intentionally retired and never enforced the claimed grant |

Each closure preserves the full previous run_concerns row inside an append-only
platform_tracking_resolved event. Only issue-tracking status and its resolution
audit changed; business data, historical module receipts and live workflows did
not. New fixes are uncommitted/undeployed; no live end-to-end success is claimed.

Verification (all passed):

- Server + guidance: `Test.*(Pulse|ConversationTurn|DurableFailedWorkflow|PlanDrift)`.
- Workflow package: `TestWorkshopFastPath|Test.*Scripted|TestMergeAgentConfigFieldsCoversEveryField`.
- `TestWorkflowStepDatabaseToolsThroughMCPBridge`: actual built MCP bridge and
  workspace SQLite/WAL handlers; the test's custom HTTP facade models the
  success/result envelope. Native, MCP and once-decoded HTTP payloads match.
- `git diff --check`.

DB envelope reports PUL-25BDBC42, PUL-76F2EE72 and PUL-A14F764F remain open:
the documented transport distinction explains the two possible shapes, but it
does not establish which path each historical reported call used. Tool schema
and bridge guidance now explicitly explain the distinction. No DB engine fix
or historical failure reproduction is claimed.

## Scope and result

Compared and grouped the 111 remaining typed harness findings across 14 local workflow databases after the earlier nine closures. This is an engineering triage inventory, not 111 independently reproduced defects or a complete code audit of every report. Eight were verified stale/fixed or retired, resolved in SQLite, and read-back verified; 103 remain unresolved. The other 27 workflow issues and 10 unclassified candidates are outside this pass. No production code changes or workflow execution are part of this reconciliation.

## Verified closures

### PLAT-280

`PUL-01B1E294`, `PUL-439F126E`, `PUL-C796CD97`, `PUL-F7362B0B`.

Fix commit 371820e30; current plan types all four steps regular, effective configs declare scripted, saved main.py exists. prepareScriptedStepUpdateTarget supports conversion. TestPrepareScriptedStepUpdateTarget and TestConfigureWorkflowDBSessionRetainsScriptedCompatibility pass. Does not claim every possible future DB permission error fixed.

### PLAT-240

`PUL-57CFE46C`, `PUL-B1E9F611`.

Old record_pulse_verification tool removed (d9223aa61); no Go registration remains. Current RecordPulseFindingDispositionsTx uses caller-specified check_text with verification UPSERT and accepts verification without a pre-existing attempt. TestAppliedFixNeedsNoLaterVerificationAttempt, TestNoAttemptVerificationHistoryAccumulatesAcrossPulseRuns and TestFixedVerifiedDispositionWhitespaceStillClosesFinding pass. Retired exact old-tool defects, not all lifecycle bugs.

### PLAT-218

`PUL-D7D173FB`.

Fix commit a8aaaa012; report_human_inputs.go now distinguishes invalid outer objects from malformed approved_scope/post_run_proof string fields. Exact issue linked in ticket. TestReportHumanInputToolReportsExactInvalidApplyContractField and TestReportHumanInputPersistsStructuredApplyContract pass.

### PLAT-219

`PUL-5D2B9495`.

Fix commit a8aaaa012; conversation_turn_lifecycle.go probes exact linked full-run durable failure after inactivity rather than indefinitely trusting a running child. Exact issue linked in ticket. TestDurableFailedWorkflowDescendant, TestConversationTurn, TestRunFullWorkflow and TestWorkshopExecution pass. Does not close LinkedIn stale list projection, missed cron fires, or wrong-turn injection.

All closures are internal tracking resolutions backed by source and focused tests; fresh deployed end-to-end verification is not claimed. Prior SQLite records are retained in platform_tracking_resolved event metadata. Historical run outcomes and business data are untouched.

## Remaining triage groups

Groups below are investigation buckets, not proven shared root causes. Exact duplicates have not been merged and uncertain reports remain open.

| Group | Remaining findings |
|---|---:|
| Browser tools / shared sessions / site behavior | 17 |
| Costs / telemetry / historical attribution | 6 |
| DB response / validation contracts | 6 |
| Evaluation / validation lifecycle | 6 |
| Execution / tool / model / prompt contracts | 11 |
| External services / credentials (triage, not confirmed platform defects) | 3 |
| Filesystem / DB grants / output paths | 21 |
| Pulse lifecycle / findings / decision contracts | 18 |
| Scheduling / run identity / lifecycle | 15 |

## Next investigation order

1. Pulse premature failures and parent/child receipt identity: compare fresh paths against PLAT-199/258 and interrupted-review recovery before proposing another patch.
2. DB response envelopes: Server A PUL-25BDBC42 and Upwork PUL-76F2EE72/PUL-A14F764F are similar reports; reproduce both transports before consolidating.
3. Scheduling/retention: separate missing runs, wrong injected turns, lost retries and rotation. PLAT-219 and PLAT-242 do not cover all of them.
4. Folder grants: distinguish declared db/research/db/growth paths from supported db/assets and actual runtime grants. Do not loosen raw SQLite access.
5. Cost history: PLAT-226/241 are explicitly deferred; they must not be closed merely because other cost improvements landed.
6. Browser/external behavior and remaining tools: verify exact tab/session/tool contracts; upstream UI or credential failures are not automatically platform defects.

## Complete 111-record disposition inventory

Exact ticket matches below mean the ticket mentions the identifier/fingerprint; they do not prove that the issue is fixed. A dash means no exact link found, not no related ticket exists. Rows labeled "Resolve: verified fixed/retired" were subsequently applied and read-back verified in SQLite (8 total).

| Workflow | Issue | Disposition | Group | Exact ticket / verified mapping | Recorded symptom |
|---|---|---|---|---|---|
| build-in-public | PUL-E2BACEEB | Open: needs further verification | Costs / telemetry / historical attribution | — | The cost-summary reader does not isolate the requested current execution when the canonical iteration/group folder is reused across scheduled runs. |
| build-in-public | PUL-0B679A3D | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | The Pulse backend generated an empty trusted verification allowlist ([]) for review run 2026-08-05T02-12-47.459Z_schedule-cron--c2e7578f_1785895230524138000 module strategy_auditor, while the same backend's get_pulse_state(view=backlog) ... |
| build-in-public | PUL-28FC41E1 | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | Issues created by record_run_concern carry no owning module (module=null, phase=execution/message-sequence/prevalidation). Calling record_pulse_finding with such a visible PUL id and a valid module fails every time with 'recorded Pulse f... |
| build-in-public | PUL-86C0991B | Open: needs further verification | Evaluation / validation lifecycle | — | The framework did not run auto-evaluation after a successful workflow execution, leaving the only evaluation report stale across a changed route contract. |
| build-in-public | PUL-115F32AF | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | Pulse deterministic intake elevates recovered message-sequence child-tool errors without reconciling the exact execution terminal artifacts and durable side effects. |
| build-in-public | PUL-69B66D52 | Open: needs further verification | Filesystem / DB grants / output paths | — | Message-sequence Folder Guard access projection contradicts the declared database contract: step-reddit-scan-draft must read db/README.md, while the enforced read allowlist exposes only db/assets. |
| build-in-public | PUL-B898D60C | Open: needs further verification | Scheduling / run identity / lifecycle | — | The shared scheduler terminal projection labels a response-bearing completed LinkedIn workflow as produced-no-response/orchestrator_agent_error, the same root previously proven on Reddit. |
| build-in-public | PUL-FF23BCD5 | Open: needs further verification | Scheduling / run identity / lifecycle | — | execute_step(fast_path_only=true) rejects a regular plan step whose effective config declares scripted mode and whose saved main.py exists, so the schedule's deterministic preflight contract cannot execute through the published fast path. |
| hetznerssh | PUL-05A81CA0 | Open: needs further verification | Scheduling / run identity / lifecycle | — | route_selections passed to run_full_workflow no longer reach the routing step: the preseed file execution/run-remediation-route/route_selection.json is not written, so run-remediation-route silently falls back to default_route_id. |
| hetznerssh | PUL-7AC95E26 | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | Re-checked this pass: pulse_review_log currently has zero rows in status='running' (7 completed, 4 failed, 0 running). The three restart-interrupted attempts Gate referenced (2 on 2026-08-09, 1 on 2026-08-10) all show status='failed' wit... |
| hetznerssh | PUL-24A93F72 | Open: needs further verification | Scheduling / run identity / lifecycle | — | The primary recurring audit schedule (b2234610-e3a2-49dd-b406-855e4ee878a8, cron '0 9 * * 1' UTC, enabled=true, group production-server) silently skipped its 2026-08-17T09:00:00Z occurrence. schedule-runs.json shows no schedule-cron entr... |
| hetznerssh | PUL-76329350 | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | The Fixer background agent (child session workshop-background-task-1787216416145666000) completed its full repair pass -- fixed and verified PUL-D712F0C6, dispositioned PUL-24A93F72 and PUL-CBC66FD8, wrote its checkpoint -- and its own f... |
| instagram | PUL-C93522E5 | Open: needs further verification | Evaluation / validation lifecycle | — | As filed, this issue claimed step-create-reel's validation_schema checks content_package.json in the wrong folder and that 'nothing in the plan copies or symlinks it up'. Both claims are disproven by the artifacts. planning/plan.json ste... |
| instagram | PUL-8E45EDCB | Open: needs further verification | Filesystem / DB grants / output paths | — | This finding was scoped to route-build-carousel with next_check gated on a carousel-format run. Today's run reproduced the identical defect in a different step on the reel pipeline: route-fix-illustrations' read_image call (PUL-1A9373A3)... |
| instagram | PUL-61347449 | Open: needs further verification | Filesystem / DB grants / output paths | PLAT-087 | The sub-agent session prompt lists workspace_advanced, workspace_browser and workflow_db with their full tool rosters, but at call time the servers have no registered tools and the call fails with 'tools_unavailable ... MCP server(s) [wo... |
| instagram | PUL-51D2EC0F | Open: needs further verification | Filesystem / DB grants / output paths | — | The tool's documented surface has no db_access parameter and no way to preserve or set it. Updating one unrelated field (lock_learnings_reason) on step-read-analytics removed "db_access": "read-write" from step-read-analytics, step-growt... |
| instagram | PUL-7935F620 | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | pulse_final_command_state for pulse_run_id=schedule-manual--bae435e5_1786432476119732000 shows backup/publish/notify all status=failed, started_at==finished_at==2026-08-11T09:48:06Z, reason='Finalizer ended without recording this command... |
| instagram | PUL-3DC592D9 | Open: needs further verification | Browser tools / shared sessions / site behavior | — | Shared CDP browser tab cap (max 4 labeled tabs) is shared across unrelated concurrent workflows, so route-pick-topic can be blocked from ever creating a discovery tab when other workflows already hold the 4 slots. |
| linkedin | PUL-3565D07C | Open: needs further verification | Scheduling / run identity / lifecycle | PLAT-080, PLAT-219 | list_schedules reports the completed 2026-08-06 Engage run as still running and leaves next_run in the past, while get_schedule_runs and run_metadata both record success. |
| linkedin | PUL-517B83AD | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | Pulse lifecycle identity and pending-attempt resolution still reject a matured passed verification for an active public issue. |
| rtslatency | PUL-D0BAC922 | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | ONE root cause, not six bugs: the platform's post-completion narration turns are not grounded in the step's produced artifacts. In every instance the step's actual output was CORRECT -- the JSON, the DB rows and the delivery ledger were ... |
| rtslatency | PUL-25BDBC42 | Open: needs further verification | DB response / validation contracts | — | query_workflow_db returns inconsistent response shapes across calls in the same run - sometimes the parsed {columns,rows} object directly, sometimes a double-encoded {success:true, result:"<JSON string>"} wrapper - for the same action=qu... |
| rtslatency | PUL-54D7A9FC | Open: needs further verification | External services / credentials (triage, not confirmed platform defects) | — | gh CLI host keyring loses/omits the mprealtrainingsys account between runs, silently reducing GitHub org visibility to ~1 public repo |
| rtslatency | PUL-5D98671B | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | update_tier_fallbacks silently no-ops under the workflow's current provider_profile llm_config shape. The real per-role fallback mechanism is set_workflow_llm_config(mode=explicit, ...), which a prior Pulse session found returned ACCESS ... |
| rtslatency | PUL-4AD362CD | Open: needs further verification | Execution / tool / model / prompt contracts | — | A second, tool-outage-degraded execution pass of step-daily-latency-report persisted output by prepending it to existing good files instead of replacing them. |
| rtslatency | PUL-15E18187 | Open: needs further verification | Filesystem / DB grants / output paths | — | step-daily-sprint-progress previously crashed writing sprint_progress.json to STEP_OUTPUT_DIR with FileNotFoundError even though the folder is documented as pre-existing |
| salesoutreach | PUL-C2CF5E0B | Open: needs further verification | DB response / validation contracts | — | The __automatic_final_validation__ gate for step-check-email-infrastructure has a db validation rule with an empty/missing sql query, so the gate fails unconditionally regardless of the actual state of the tables the step writes to. |
| salesoutreach | PUL-4719B06D | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | Recurrence confirmed 2026-09-02: recorded plan_drift_review result=failed at 04:13:51Z for pulse_run_id=schedule-cron--2d8c4f44_1788321613965006000 based on a genuine but premature read (no checkpoint/receipt existed yet). The dispatched... |
| salesoutreach | PUL-E3F22BCC | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | Pulse child-session receipt attribution is inconsistent between two platform mechanisms. record_pulse_result attributes the receipt to the pulse_run_id the child supplies, while run_in_background's required_pulse_review_modules gate look... |
| salesoutreach | PUL-6C7B04E9 | Open: needs further verification | Browser tools / shared sessions / site behavior | — | agent_browser open called before selecting/creating a CDP tab ("CDP shared-browser mode requires selecting or creating a tab before open") recurred once in step-research-and-draft-outreach iteration-1 on the 2026-08-28 run; the agent rec... |
| salesoutreach | PUL-985F2597 | Open: needs further verification | Scheduling / run identity / lifecycle | — | A platform contract-upgrade broadcast is able to occupy a scheduled cron-triggered workshop sessions turn slot ahead of that schedules own configured message, causing the schedules real work to never execute and the run to time out and e... |
| salesoutreach | PUL-C1B267AB | Open: needs further verification | Scheduling / run identity / lifecycle | — | Run folders are overwritten in place rather than rotated per completed run. Previously this caused stale old-failure metadata to resurface in deterministic scans (dubai-real-estate). Here the flip side occurred: a genuinely new failed ru... |
| salesoutreach | PUL-5D2B9495 | Resolve: verified fixed/retired | Verified already-fixed / retired | PLAT-219 | run_metadata.json for the dubai-real-estate run records status=failed, completed_at=2026-08-27T10:44:12.394792625Z -- a clean, well-formed terminal step status. Yet schedule-runs.json's own record for this same run (schedule 633c499d, ru... |
| salesoutreach | PUL-AAC278EF | Open: needs further verification | Execution / tool / model / prompt contracts | PLAT-225 | Deterministic-intake runtime_status_disagreement signals (completed runs with nonzero tools.errored_count) recur across multiple steps/runs; each instance needs to be checked for real data-integrity impact rather than assumed benign. |
| salesoutreach | PUL-B7F247D3 | Open: needs further verification | Browser tools / shared sessions / site behavior | — | LinkedIn profile UI has duplicate visible-text ("Connect") controls in the same viewport -- the target person's own Connect option (inside their top-card More menu) and unrelated sidebar "Invite X to connect" suggestions -- so a text-bas... |
| salesoutreach | PUL-2F70A97F | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | Retry of the identical record_pulse_finding call whose first attempt (this same pass) returned a success envelope (issue_id PUL-7AC60412, status open) but persisted no row in pulse_finding_details, pulse_finding_events, or run_concerns f... |
| salesoutreach | PUL-D81BEA7D | Open: needs further verification | Browser tools / shared sessions / site behavior | — | Recurring LinkedIn UI unreliability: the plain-connect click via profile More > Connect menuitem produces no observable success signal (no dialog, no confirmation), so the agent correctly withholds the outreach_activity write per the nev... |
| salesoutreach | PUL-09511B0E | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | The deterministic-intake boundary declared plan_drift_review failed at 2026-09-02T04:13:51Z with reason 'executor bg-plan-drift-review-30000 never reached its final persistence turn -- no checkpoint file exists', citing list_executions s... |
| sheet-analysis | PUL-45906870 | Open: needs further verification | Browser tools / shared sessions / site behavior | — | consolidate-prepare step has no Google Sheets access tool (no google_sheets MCP server, no browser tool) registered for its session |
| sheet-analysis | PUL-A9EE4AC3 | Open: needs further verification | Execution / tool / model / prompt contracts | — | consolidate-prepare step has no Google Sheets MCP tool registered in its session, so it cannot perform any of the required live Sheets reads/writes |
| sheet-analysis | PUL-756634F2 | Open: needs further verification | Execution / tool / model / prompt contracts | — | verification step tool set lacks any accessor for public.historical_payroll_snapshots / public.payslips required by the Salary and financial summary section |
| social-media | PUL-AE5325BD | Open: needs further verification | DB response / validation contracts | — | The managed DB validator rejects the exact profile-conversion validation SQL using json_each and local-date evaluation even after the required signal is persisted. |
| social-media | PUL-8E22A469 | Open: needs further verification | Costs / telemetry / historical attribution | PLAT-241 | Cost attribution remains keyed by mutable run-folder identity rather than an immutable execution identity, even when archived folder summaries differ correctly. |
| social-media | PUL-C4811EA4 | Open: needs further verification | Costs / telemetry / historical attribution | — | Reflection usage remains unattributed to immutable executions, so its cost and contribution yield cannot be judged. |
| social-media | PUL-0A1AFAF8 | Open: needs further verification | Costs / telemetry / historical attribution | — | Pre-2026-07-28 ledgers remain incomparable with current cache-priced ledgers because historical pricing basis is absent. |
| social-media | PUL-B20CB8E1 | Open: needs further verification | Execution / tool / model / prompt contracts | — | The durable-ledger half of the required landed-content union could not be independently checked; the report relied on verified current-run items. |
| social-media | PUL-007D74F8 | Open: needs further verification | Browser tools / shared sessions / site behavior | — | X reciprocal-follow badge state can be inconsistent across the unfollow flow and permit a temporary unfollow before the relationship is restored |
| social-media | PUL-8429C778 | Open: needs further verification | Filesystem / DB grants / output paths | — | Runtime path guard denies workflow-context reads during target-reply execution despite the step declaring those context roots readable |
| social-media | PUL-C9BBCF01 | Open: needs further verification | Evaluation / validation lifecycle | — | The execute-verify step requires one registered validation run, but no registered validator tool is exposed in the session. |
| social-media | PUL-8526B265 | Open: needs further verification | Browser tools / shared sessions / site behavior | — | X following-list scrolling can plateau at only the first 10 oldest-available rows despite continued scrolling |
| social-media | PUL-09B52349 | Open: needs further verification | Evaluation / validation lifecycle | — | A current measure producer exists and the evaluator is correctly route-scoped, but no natural evaluation execution has consumed it. |
| social-media | PUL-95D26C40 | Open: needs further verification | Filesystem / DB grants / output paths | — | Declared mandatory reads for soul, knowledgebase context, and db README cannot be completed under the active folder guard |
| social-media | PUL-F65412F5 | Open: needs further verification | DB response / validation contracts | — | The initial bounded current-cycle action_queue reconciliation query returned no rows even though the authoritative date-bound queue row was readable for refresh |
| social-media | PUL-E923640D | Open: needs further verification | Filesystem / DB grants / output paths | — | Direct reads of allowed workflow soul, knowledgebase, and learnings paths can be denied by runtime path mapping during an execution step. |
| social-media | PUL-C28E9B78 | Open: needs further verification | Browser tools / shared sessions / site behavior | — | PUL-75A58FDA was merged into PUL-A6A7E11E, but record_plan_drift_review rejects the canonical issue for step-0-browser-router while record_pulse_finding refuses to reopen the merged exact-step alias. |
| substack | PUL-CE5D7AFA | Open: needs further verification | Filesystem / DB grants / output paths | — | Real, currently-active recurrence of closed/suppressed PUL-CE5D7AFA: step-growth-baseline's enforced session Folder Guard excludes db/growth/ from writable paths, contradicting its own step-description contract ("Also write the same reco... |
| substack | PUL-AB711AC1 | Open: needs further verification | Filesystem / DB grants / output paths | — | step-check-reddit is instructed to write the durable dated packet to db/research/YYYY-MM-DD-reddit.json, but this step's folder guard excludes db/research/ from Allowed WRITE, and mutate_workflow_db explicitly rejects the CREATE TABLE ne... |
| substack | PUL-01F790F3 | Open: needs further verification | Filesystem / DB grants / output paths | — | step-check-hn folder guard denies write access to db/research/ entirely, so the durable HN packet the step contract requires (db/research/YYYY-MM-DD-hackernews.json) can never actually be created by this step, even though the automatic v... |
| substack | PUL-5903292F | Open: needs further verification | Evaluation / validation lifecycle | — | step-check-x cannot write to db/research/ despite its declared output contract and the final-validation regex requiring a db/research/ path |
| substack | PUL-C00607A6 | Open: needs further verification | Filesystem / DB grants / output paths | — | step-post-standalone-note reflection-turn description requires writing db/growth/YYYY-MM-DD-standalone-note-cadence.json, but the step folder guard never grants write access to db/growth/ for this route |
| substack | PUL-137C5EDF | Open: needs further verification | Filesystem / DB grants / output paths | — | step-check-hn cannot durably write db/research/YYYY-MM-DD-hackernews.json: the folder guard denies shell writes and diff_patch_workspace_file writes under db/research/, and mutate_workflow_db rejects CREATE TABLE for a same-dated mirror ... |
| substack | PUL-22656D6C | Open: needs further verification | Filesystem / DB grants / output paths | — | step-check-x (X/Twitter research sub-agent) is instructed to write db/research/YYYY-MM-DD-x_twitter.json directly, but its folder-guard write scope never includes db/research/, only its own step folder, Downloads, db/assets, and learning... |
| substack | PUL-F7B23729 | Open: needs further verification | Filesystem / DB grants / output paths | — | step-check-hn folder guard denies write access to db/research/, blocking the sub-agent from materializing its own durable YYYY-MM-DD-hackernews.json packet |
| substack | PUL-9CE79401 | Open: needs further verification | Filesystem / DB grants / output paths | — | Reddit source-research sub-agent write scope excludes db/research/, so the durable per-date research packet cannot be materialized directly by this step |
| substack | PUL-32C03E39 | Open: needs further verification | Filesystem / DB grants / output paths | — | Source-research sub-agent step folder guards exclude db/research/ from Allowed WRITE, but orchestrator/step instructions and stores.md still direct sub-agents to write durable packets directly to db/research/YYYY-MM-DD-<source>.json |
| substack | PUL-9694792A | Open: needs further verification | Browser tools / shared sessions / site behavior | — | agent_browser 'tab close <label-or-id>' fails to close a tab that the immediately-preceding 'tab' (no args) listing confirmed as active and owned by this route's own label |
| substack | PUL-B8FC2F51 | Open: needs further verification | Filesystem / DB grants / output paths | — | Source-research sub-agent routes (reddit/x_twitter/hackernews/websearch) folder guard denies direct writes to db/research/, forcing them to write a fallback copy to db/assets/ and set persistent_record success literals before the file ex... |
| substack | PUL-3237C0D8 | Open: needs further verification | Browser tools / shared sessions / site behavior | — | Execution-turn prompt for step-connect-creators shows only the output filename, not the step description own detailed Output connect_result.json required-field list, so pre-validation fails on the first pass for fields never surfaced at ... |
| substack | PUL-778F87BE | Open: needs further verification | Browser tools / shared sessions / site behavior | — | agent_browser CDP (port 9222) unreachable, forcing an undocumented RSS fallback |
| substack | PUL-224A1885 | Open: needs further verification | Filesystem / DB grants / output paths | — | step-check-hn's enforced write allowlist excludes db/research, but its own hn_findings.json validation gate requires persistent_record.path to literally match db/research/YYYY-MM-DD-hackernews.json with status 'exists' and verified_after... |
| substack | PUL-3792C10D | Open: needs further verification | Scheduling / run identity / lifecycle | — | todo_task orchestrator finalization can (intermittently) report execution status=failed via a post-completion human_feedback timeout even when the orchestrators own work and final-gate validation already passed cleanly. |
| substack | PUL-B0C1585F | Open: needs further verification | Filesystem / DB grants / output paths | — | Runtime ACCESS DENIED: writable folders for this session were only step-write-article, Downloads, db/assets, and /Users/mipl/Downloads -- db/drafts/ was excluded despite the banner claiming db. get_step_prompts(step-publish) shows the id... |
| tectonicusadaytrading | PUL-7FC528B9 | Open: needs further verification | Execution / tool / model / prompt contracts | — | Configured claude-haiku-4-5-20251001 tier trial for daily-signals still shows zero usage; every step across all 4 execution ledgers for 2026-08-20 ran on claude-sonnet-5 only. |
| tectonicusadaytrading | PUL-2E197257 | Open: needs further verification | Execution / tool / model / prompt contracts | — | execute_shell_command classifies a call as failed based on stderr content (permission/authorization-denial phrases) even when the underlying script completed successfully and its DB write landed, giving no stdout/stderr back to the caller |
| tectonicusadaytrading | PUL-0322D624 | Open: needs further verification | Execution / tool / model / prompt contracts | — | BOUNDARY REACHED AND WORSE THAN RECORDED: the close pass did not merely execute canceled turns, it did not fire at all on 2026-08-17 - the third consecutive weekday no-fire. |
| tectonicusadaytrading | PUL-BB5E62C0 | Open: needs further verification | Scheduling / run identity / lifecycle | — | The second collect_social.py execution was not an execute_shell_command retry: it was a second, concurrent daily-signals orchestrator dispatching collect-social into the same run folder. Reclassifying from harness_issue to workflow_issue... |
| tectonicusadaytrading | PUL-31203224 | Open: needs further verification | Scheduling / run identity / lifecycle | — | schedule-runs.json records the 2026-08-18T15:00:26Z run of schedule 9db4dc39 as status='error' with the platform-authored message 'interrupted: server restarted' and duration_ms=null. The identical record exists for 2026-08-12T13:55:56.2... |
| tectonicusadaytrading | PUL-4CC6EAC4 | Open: needs further verification | External services / credentials (triage, not confirmed platform defects) | — | StockTwits RapidAPI (stocktwits.p.rapidapi.com) intermittently returns HTTP 403 (curl exit 56, non-Cloudflare) for a double-digit fraction of the 19-symbol watchlist within collect_social.py, with a different symbol subset failing each o... |
| tectonicusadaytrading | PUL-74C2F334 | Open: needs further verification | External services / credentials (triage, not confirmed platform defects) | — | StockTwits RapidAPI stream endpoint intermittently returns HTTP 403 (curl exit 56, non-Cloudflare) for a large fraction of watchlist symbols per run, independent of the User-Agent/curl fix already in place |
| tectonicusadaytrading | PUL-57CFE46C | Resolve: verified fixed/retired | Verified already-fixed / retired | PLAT-240 | record_pulse_verification previously reported success without persisting to pulse_fix_verifications (reproduced 4x on 2026-08-21). Re-tested this pass; the specific silent-failure mode was not reproduced. |
| tectonicusadaytrading | PUL-3E984942 | Open: needs further verification | Scheduling / run identity / lifecycle | — | 9db4dc39 intermittently fails to fire all 3 scheduled slots on a given day, no backfill; owner=platform, external_action_required. |
| tectonicusadaytrading | PUL-F9BA8AF2 | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | Pulse review receipt attribution uses two conflicting id conventions in the same contract: record_pulse_result must be called with the parent pulse_run_id, but the background-child completion gate looks the receipt up by the child sessio... |
| tectonicusadaytrading | PUL-B1E9F611 | Resolve: verified fixed/retired | Verified already-fixed / retired | PLAT-240 | record_pulse_verification requires a pending internal attempt to resolve, but the Pulse review/Fixer contract's split (reviewer marks review done, a later Fixer applies repairs) means many genuinely-fixed issues never get one, permanentl... |
| tectonicusadaytrading | PUL-FBF1BE54 | Open: needs further verification | Execution / tool / model / prompt contracts | — | iteration-130 (2026-08-27, first-of-day fire): daily-signals timing.json shows 19 of 47 tool calls errored, 11 of which are execute_shell_command heredoc-drafting attempts that fail with exit_code=2 specifically when the body text contai... |
| upwork | PUL-7B849CC6 | Open: needs further verification | Scheduling / run identity / lifecycle | — | run_full_workflow dropped the search-find-and-shortlist human_inputs override, so the Aug 3 search scanned Most Recent despite an explicit two-feed run limit |
| upwork | PUL-DD9EDE3C | Open: needs further verification | Browser tools / shared sessions / site behavior | — | agent_browser snapshot results continue to overflow the tool-result size limit post-v1.0.21-purification (toptal-scan-jobs 68,223 chars, search-find-and-shortlist 130,705 chars on 2026-08-07), and the spilled-result fallback read also fa... |
| upwork | PUL-42742C7F | Open: needs further verification | Evaluation / validation lifecycle | — | bid-pick-job skip_reason closed-vocabulary compound boundary clause (b) requires a fresh eval_results row for group_name=daily-bid, step_id=eval-bid inside a daily-bid runs own window. Checked after the 2026-08-24T15:58-16:35Z daily-bid ... |
| upwork | PUL-F2B4C57F | Open: needs further verification | Costs / telemetry / historical attribution | — | Unchanged root, re-open condition CHECKED this pass and NOT met. The recorded re-measurement stands: the missing-metadata half is a harness measurement gap the workflow cannot write, not a contribution-yield problem. |
| upwork | PUL-D50AD8BC | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | record_run_concern is not registered in the step-runner tool profile, so a step that discovers a durable engineering defect mid-run has no way to file it and must downgrade the finding to a free-text CONCERNS line. |
| upwork | PUL-A8AB0913 | Open: needs further verification | Costs / telemetry / historical attribution | PLAT-073-REMAINING-BOARD, PLAT-081, PLAT-226 | Recording the change so a future Ops pass does not re-derive a claim that is now half wrong. costs/execution/__ungrouped__/2026-08-12.json is no longer a single date-wide blob - it holds 2 discrete execution records, each with its own ex... |
| upwork | PUL-76F2EE72 | Open: needs further verification | DB response / validation contracts | — | The managed query_workflow_db tool returns two different JSON shapes for the same call pattern -- a flat {columns,rows} shape and a wrapped {success,result:'<json string>'} shape -- with no documented trigger, and only one workflow step'... |
| upwork | PUL-A14F764F | Open: needs further verification | DB response / validation contracts | — | query_workflow_db still returns both flat columns/rows and wrapped success/result envelopes for valid read calls, so callers cannot rely on one declared response shape. |
| upwork | PUL-76B0B278 | Open: needs further verification | Browser tools / shared sessions / site behavior | — | Upwork's find-work feed tabs (Best Matches, Most Recent, My Feed) repeatedly hydrate only ~2 of the ~21-40 rendered card DOM slots into real content; the rest remain 4-character skeleton placeholders even after every documented recovery ... |
| upwork | PUL-62957A90 | Open: needs further verification | Browser tools / shared sessions / site behavior | — | Single occurrence in a busy shared-CDP session with rapid tab churn; plausibly a race between tab creation and id reassignment rather than a persistent defect. No workflow-side fix exists since this is agent_browser tool behavior, not pl... |
| upwork | PUL-2E420094 | Open: needs further verification | Browser tools / shared sessions / site behavior | — | Judged distinct from the existing browser-hydration-stall root cause: PUL-8DC7E43C (target_key upwork.com:find-work-feed-tab-virtualization, already linking recurrences PUL-76B0B278 and PUL-832D635C) describes Upwork's OWN virtualization... |
| upwork | PUL-A9401F93 | Open: needs further verification | Scheduling / run identity / lifecycle | — | The intermittent scheduler-silent-skip root remains unclosed, but 2026-08-27 produced real schedule-run records and retained run folders for both affected schedules, so the current failures are not silent skips. |
| upwork | PUL-C004DFAF | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | Duplicate pending decision card for the same strategic question, and no tool exists to retire the unlinked orphan without fabricating an answer. |
| upwork | PUL-565F9ED1 | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | record_pulse_result for module=technical_review is permanently blocked for this pulse_run_id because the reviewer's own terminal receipt (result=done) was written before Fixer work began, under the single-terminal-per-(module,pulse_run_i... |
| upwork | PUL-B2511806 | Open: needs further verification | Browser tools / shared sessions / site behavior | — | agent_browser snapshot refs{} dictionary key order is not a reliable proxy for DOM/tree sibling order, causing badge-to-entity misattribution on list pages with repeated sibling blocks unless the nested snapshot tree text or the individu... |
| upwork | PUL-169BD994 | Open: needs further verification | Execution / tool / model / prompt contracts | — | search_web_llm's default/first-listed providers for capability search_web (claude-code, pi-cli) fail with 'requires workspace auth' and cursor-cli returns non-search shell-command chatter instead of an actual web answer, so only codex-cl... |
| upwork | PUL-B6864197 | Open: needs further verification | Filesystem / DB grants / output paths | — | The historical profile-report learnings read denial belongs to the shared Folder Guard boundary and was not exercised because the current profile route failed earlier. |
| upwork | PUL-9C0D14D3 | Open: needs further verification | Scheduling / run identity / lifecycle | — | 3 independent timestamps agree precisely: run_metadata.json recorded status=failed/completed_at=00:51:15Z (the FIRST improve-report attempt's orchestrator_agent_error). A live plan.json edit (changelog-2026-08-24-00-55-07.json, update_me... |
| upwork | PUL-9CCE9488 | Open: needs further verification | Browser tools / shared sessions / site behavior | PLAT-224 | Calling agent_browser(command=network, args=[--cdp <endpoint>, tab, t31, requests]) on the toptal-submit steps own tab returned dozens of unrelated requests from OTHER open tabs in the same shared Chrome instance (LinkedIn, Reddit, X/Twi... |
| upwork | PUL-1EE39C89 | Open: needs further verification | Scheduling / run identity / lifecycle | — | Definitive evidence across the FULL schedule-runs.json history (177 rows, all schedules, going back weeks) shows every single scheduled run wrote run_folder=iteration-0 -- including schedule 78ba88d0 (job-search+daily-bid) which has fire... |
| upwork | PUL-D7D173FB | Resolve: verified fixed/retired | Verified already-fixed / retired | PLAT-218 | The HTTP custom-tool boundary still rejects create_human_input_request apply_contract as non-object even when jq constructs and transmits it as a JSON object. |
| upwork | PUL-E19F5F1B | Open: needs further verification | Pulse lifecycle / findings / decision contracts | — | record_pulse_result rejects a later independent Fixer pass after an earlier Fixer already recorded lifecycle outcomes against the same completed Technical Review. |
| upwork | PUL-01B1E294 | Resolve: verified fixed/retired | Verified already-fixed / retired | PLAT-280 | The plan still declares bid-record as message_sequence while step_config declares scripted and main.py requires DB_PATH, which the agentic message-sequence runtime does not receive; the typed plan surface has no safe in-place conversion ... |
| upwork | PUL-439F126E | Resolve: verified fixed/retired | Verified already-fixed / retired | PLAT-280 | search-save-jobs remains a message_sequence while its deterministic implementation requires direct SQLite access; the runtime supplied the correct absolute DB_PATH but the step sandbox could not open it, so no shortlist row or summary wa... |
| upwork | PUL-C796CD97 | Resolve: verified fixed/retired | Verified already-fixed / retired | PLAT-280 | The plan still declares improve-read-history as message_sequence while step_config declares scripted and main.py requires DB_PATH, which the agentic message-sequence runtime does not receive; the typed plan surface has no safe in-place c... |
| upwork | PUL-F7362B0B | Resolve: verified fixed/retired | Verified already-fixed / retired | PLAT-280 | The plan still declares outreach-record as message_sequence while step_config declares scripted and main.py requires DB_PATH, which the agentic message-sequence runtime does not receive; the typed plan surface has no safe in-place conver... |
| upwork | PUL-07504731 | Open: needs further verification | Execution / tool / model / prompt contracts | — | get_plan_prompt_health returned the exact pre-change description counts and duplicate clusters twice after four successful managed plan updates, while the canonical plan file and validate_plan_change already consumed the post-change desc... |

## Chat scroll jumps after a switch or "Apply in chat" — PLAT-455

[PLAT-455](pulse_platform/frontend-chat/plat-455.md), fixed on `main`, not deployed:
one request helper and one settled scroll (coalesced, after the list is quiet, manual
scroll wins) replace the timed `chat-scroll-to-bottom` bursts.

## Steps and Relay guidance mention no stores they lack — PLAT-454

[PLAT-454](pulse_platform/plans-contracts/plat-454.md), fixed on `main`, not deployed:
a step with no platform stores gets no database/KB/learnings text at all; the Relay
Builder prompt and skill say how data moves instead of what is missing.

## Plan and Browser move into the workflow toolbar's Ops group — PLAT-453

[PLAT-453](pulse_platform/frontend-chat/plat-453.md), fixed on `main`, not deployed:
Views keeps Pulse, Needs you and Activity; Plan and Browser are in Ops (a Relay keeps
its Graph in Views); the collapsed Ops label highlights when one of them is active.

## Remove the Builder/Pulse run_in_background tool — PLAT-452

[PLAT-452](pulse_platform/plans-contracts/plat-452.md), on `main`, not deployed, not
tested in a live Pulse run: the tool and its background agent code are gone;
Pulse's own agent does each review in its turn. Review limits are prose, not tool
filters.

## Slot/folder mismatch falls back to the app account — PLAT-451

[PLAT-451](pulse_platform/security-sandbox/plat-451.md), P1, fixed on main (not
deployed). The slot selector logs a refusal but returned the same value as a
non-slot launch; slottmux then passed it to the app account's tmux. A mismatch is
now an explicit error in the selector, slottmux and the provider; the root-owned
slottmux must be deployed with the matching release.

## Product owner migration follows cross-user manifest symlinks — PLAT-450

[PLAT-450](pulse_platform/security-sandbox/plat-450.md), P1, fixed on `main`, not
deployed. The owner scan and the owner writers now use anchored, symlink-refusing
opens (`[UNSAFE_PATH]`); regression tests use real temp files and symlinks.

## Editable project metadata selects another user's CLI identity — PLAT-449

[PLAT-449](pulse_platform/security-sandbox/plat-449.md), P1, fixed on `main`, not
deployed. Ownership is server-controlled (an owner registry in the state area,
else the path; product.json `owner_id` is information only), and a Code launch
whose registered owner is not the admitted caller is refused before any CLI starts.

## Relay migration history claims inapplicable upgrades were applied — PLAT-448

[PLAT-448](pulse_platform/plans-contracts/plat-448.md), fixed on `main`, not
deployed: applied history now uses the same product-filtered shared ladder as
pending upgrades; audit checks preserve all applicable Relay migrations and gates.

## Relay execution inherited workflow DB/KB/learnings — PLAT-447

[PLAT-447](pulse_platform/plans-contracts/plat-447.md), fixed on `main`, not
deployed: product.yaml disables platform stores for every Relay agent/script
execution, including script tools, Builder tests and published versions.

## Crew CLI turns run as the app account, not the owner's slot — PLAT-446

[PLAT-446](pulse_platform/security-sandbox/plat-446.md), decided 2026-10-04: keep the app account, nothing to build. Found in PLAT-442 step 2: a Crew CLI starts in an isolated
runtime folder, so the owner's and a reader's turn both run as the app account; the premise of decision 1 does not hold.

## Applying a decision in chat pointed at a Fixer that no longer exists — PLAT-445

[PLAT-445](pulse_platform/human-decisions/plat-445.md), fixed on `main`, not
deployed: every answered decision is applied by the Builder in chat, honoring any
option picked; the dead pre-run Fixer/drain routing is removed.

## Relay script-tool names can be advertised after registration drops them — PLAT-444

[PLAT-444](pulse_platform/plans-contracts/plat-444.md), P2, open. Reproduced:
a valid authored Relay advertises a normalized script name that collides with a
platform tool; its generic route fallback was removed by PLAT-441.

## Script-tool JSON reader follows symlinks outside its output folder — PLAT-443

[PLAT-443](pulse_platform/security-sandbox/plat-443.md), P1, open. Reproduced with
fake data: the server result reader returns JSON outside the assigned output
folder through a script-created symlink; parent reads need confinement.

## Identity is explicit: owner from the manifest, slot named by the platform, Crews at Crew/<id> — PLAT-442

[PLAT-442](pulse_platform/security-sandbox/plat-442.md), open, approved 2026-10-04. Ownership and the run-as slot stop being read from `_users/<id>` paths
(three copies today); Crews move to a shared root afterwards.

## Relay agents call saved Python scripts as tools — PLAT-441

[PLAT-441](pulse_platform/plans-contracts/plat-441.md), fixed on `main`, not
deployed: a Relay agent may own `script_only` scripted routes (named tools) and
keeps its authored prompt; sub-agents stay unsupported; Relays skip DB/KB migrations.

## Physical _users paths built from an unsanitized user id — PLAT-440

[PLAT-440](pulse_platform/security-sandbox/plat-440.md), open: found during PLAT-435; a few call sites build
`_users/<id>/` from a raw or differently sanitized user id, and the workspace module's default-user fallback differs
from the agent server's.

## Remove the product workspace inspector popup — PLAT-439

[PLAT-439](pulse_platform/frontend-chat/plat-439.md), fixed on main; deployment
pending. Remove the admin/reviewer inspection entry and popup from Code/Crew's
shared product surface after the user reported a Network Error on Excellence.

## Message-sequence prompts advertised KB read the sandbox denied — PLAT-438

[PLAT-438](pulse_platform/step-execution/plat-438.md), fixed on `main`, not
deployed: a sequence step's prompt now offers the knowledge base only when its
`knowledgebase_access` allows it.

## Builder chats treated as "provider changed" on every message — PLAT-437

[PLAT-437](pulse_platform/chat-reliability/plat-437.md), regression of PLAT-425 (deployed), fixed on main; deploy pending. A workflow whose manifest
names its own LLM, or a locked server (RTS), ignores the request's provider, but the check compared it with the retained CLI: every send queued behind a running turn and relaunched the CLI.

## Scripted steps never self-heal in a run — PLAT-436

[PLAT-436](pulse_platform/step-execution/plat-436.md), fixed on `main`, not
deployed: runs execute the saved script and fail on its error; only the
Builder's own `execute_step` authors or repairs scripts.

## Code and Crew pages could not connect their panel on multi-user servers — PLAT-434

[PLAT-434](pulse_platform/frontend-chat/plat-434.md), fixed on main; deployment pending. A project under `_users/<id>/` got the workflow
view list, so the page's connect call was refused (`invalid_state`) and the agent saw "browser disconnected".

## One workspace path type instead of `_users/` string handling everywhere — PLAT-435

[PLAT-435](pulse_platform/security-sandbox/plat-435.md), fixed on main for agent_go (package `pkg/workspaceref`, guard
test), not deployed. The `workspace/` module and a multi-user e2e fixture are left; PLAT-440 tracks unsanitized ids.


## Relays use external API triggers without timed schedules — PLAT-433

[PLAT-433](pulse_platform/scheduler-runs/plat-433.md), fixed on main; deployment
pending. Remove scheduling from Relay UI/Builder/runtime, keep function APIs and
shared delivery tracking; retire saved Relay timers without rewriting releases.

## Scripted routes are named tools — PLAT-432

[PLAT-432](pulse_platform/plans-contracts/plat-432.md), fixed on `main`, not
deployed: each saved scripted route is a named tool of its Agent step, returns
`route_result.json`, and may declare `script_parameters_schema`.

## Relays reuse workflow migrations, minus goal-driven ones — PLAT-431

[PLAT-431](pulse_platform/plans-contracts/plat-431.md), fixed on `main`, not
deployed: a Relay owes only the shared migrations and its Builder can run them;
the 1.0.45 bump no longer strands Relays without migration tools.

## Workflow Costs emphasizes numbers and removes explanatory copy — PLAT-430

[PLAT-430](pulse_platform/cost-telemetry/plat-430.md), fixed on main; deployment
pending. Short input/cache/output/cost columns, compact missing-data counters,
secondary run/model details, and independently expandable dates.

## A CLI chat said the UI control tools were unavailable (they are bridge tools) — PLAT-429

[PLAT-429](pulse_platform/coding-agent-bridge/plat-429.md), P3, fixed on `main`; deploy pending. The UI-control skill now says how to reach them.

## Relay creation, Builder editing and versioned runs through platform MCP — PLAT-427

[PLAT-427](pulse_platform/mcp/plat-427.md), fixed on main; deployment pending.
Relay product manifest owns MCP tools; explicit authoring consent, shared Builder
operations/publisher/scheduler, draft tests and immutable version calls.

## Build a release once, deploy everywhere — PLAT-426

[PLAT-426](pulse_platform/scheduler-runs/plat-426.md), P3, fixed on `main`; not deployed. `deploy.sh` builds once on the Hetzner
box (`/srv/_builds`, manifest with revisions, arch, glibc, file hashes; about 3 min warm) and Excellence, Confida, SparkQuill
and RTS only copy and activate it after verifying the manifest; `--build` deploys a chosen older build.

## Provider switch: next sends rejected with 409 delivery_uncertain — PLAT-425

[PLAT-425](pulse_platform/chat-reliability/plat-425.md), P1, fixed on `main`; deploy pending. A gone retained terminal now starts a fresh turn instead of 409.

## CLI delivery health: smoke test, duplicate and launch-death checks — PLAT-424

[PLAT-424](pulse_platform/chat-reliability/plat-424.md), P2, open (planned, not started). Notice duplicate sends, instant launch deaths and slow acceptance without a user report.

## Named custom Python tools for Relay agents — PLAT-423

[PLAT-423](pulse_platform/coding-agent-bridge/plat-423.md), retired before deployment: replaced by scripted routes as named tools (PLAT-432); Relay agent tools wait for routes on authored-prompt agents.

## Muse messages ran three or more times: stale refusal notice — PLAT-422

[PLAT-422](pulse_platform/coding-agent-bridge/plat-422.md), P1, fixed on `main`; deploy in progress. A refusal now needs a new notice or the draft still in the input box.

## Muse launch ended at once: sweep prelude could not find `muse` — PLAT-421

[PLAT-421](pulse_platform/coding-agent-bridge/plat-421.md), P1, fixed on `main`; deploy to Excellence and RTS in progress.

## Scripted steps use the managed DB helper — PLAT-428

[PLAT-428](pulse_platform/security-sandbox/plat-428.md), contract 1.0.45: scripts
read and write through `agentworks_db` (the managed tools), not `sqlite3` /
`$DB_PATH`; every workflow must be migrated before it runs.

## Bulk writes and paged reads in the DB tools — PLAT-420

[PLAT-420](pulse_platform/security-sandbox/plat-420.md), fixed on `main`, not
deployed: `mutate_workflow_db` takes `param_sets` (many rows, one transaction) and
up to 200 statements; `query_workflow_db` takes `offset`/`next_offset` and up to
10,000 rows.

## Codex on a Mac: own CODEX_HOME, personal MCP servers off — PLAT-418

[PLAT-418](pulse_platform/coding-agent-bridge/plat-418.md), fixed on `main`, not
deployed (Mac only): Codex loaded the person's own MCP servers and could not see
its session profile; it now runs with its own `CODEX_HOME` like under Landlock.

## What a workflow step may do (measured) — PLAT-419

[PLAT-419](pulse_platform/security-sandbox/plat-419.md), open question for the
owner: the permission map of agent steps vs scripted steps, from the new
`cli-step-contract`.

## Muse on a confined host: runtime folder, refused messages, probe folders — PLAT-417

[PLAT-417](pulse_platform/coding-agent-bridge/plat-417.md), fixed on `main`, not
deployed: registry_io warning (own runtime folder), "another run is still
starting" (retry), probe folder leak (sweep as the slot user). The mid-run
auth-file error is open.

## Codex Spark dropped; allowed-models 422 on a stale saved model — PLAT-416

[PLAT-416](pulse_platform/coding-agent-bridge/plat-416.md), P2, fixed on `main`; deploy pending. A model the account does not allow now runs on the first allowed one instead of failing the chat.

## Structured Python step output API — PLAT-415

[PLAT-415](pulse_platform/security-sandbox/plat-415.md), fixed on main;
deployment pending. Shared set_output(value) helper persists structured JSON
for downstream references without user-managed files; slot runtime imports
and artifact permissions are verified.

## Stop browser finds nothing, stuck tab recovery, tall screenshots — PLAT-414

[PLAT-414](pulse_platform/browser/plat-414.md), fixed on main, not deployed. The browser list and cleanup now see Chrome; a stuck
managed browser is closed and the command retried once; full-page screenshots above 16000 px are refused with a clear error.

## Coding-agent background tasks show one readable line, not raw payload — PLAT-413

[PLAT-413](pulse_platform/frontend-chat/plat-413.md), P3, fixed on `main`; deploy pending. Full text behind "Show details".

## Relay user message and validation guidance — PLAT-412

[PLAT-412](pulse_platform/coding-agent-bridge/plat-412.md), fixed on main;
deployment pending. Explicit prior-output references in authored messages,
JSON-only final responses and current validation limits are documented in the
manifest-owned builder prompt and skill.

## Relay agent tool errors in the invoice run — PLAT-411

[PLAT-411](pulse_platform/security-sandbox/plat-411.md), open. An archived invoice
run returned prose after JSON following missing database sandbox-path and
browser slot errors. The latest run succeeded with both outputs saved; the
failed tool paths still need isolated verification.

## Source Control listed and would have committed the private `.sandbox-cache` folder — PLAT-410

[PLAT-410](pulse_platform/frontend-chat/plat-410.md), P2, fixed on `main`; deploy pending. Panel git and sandbox git (agent, terminal) ignore the platform folder.
## Relays use the shared Ctrl+K switcher — PLAT-409

[PLAT-409](pulse_platform/frontend-chat/plat-409.md), fixed on main; deployment
pending. Include Relays in keyboard and custom-event entry points, label Relay
presets correctly, and open them through existing product-aware navigation.

## Workflow header Ops collapsed by default — PLAT-408

[PLAT-408](pulse_platform/frontend-chat/plat-408.md), fixed on main; deployment
pending. Keep the Ops label and expand its existing icons on click. Views and
icon-only Setup remain open.

## Models: allowed models per account, one Model card, changes between turns — PLAT-407

[PLAT-407](pulse_platform/frontend-chat/plat-407.md), fixed on `main`; deployed to Excellence only (the
between-turns change also in the RTS build from `ad3956735`). Optional `allowed_models` per provider account enforced on
the server; workflow Models tab with one Model card; a model or effort change waits for the running turn.

## Chat composer: New chat in Code, mic, layout, toolbar order — PLAT-406

[PLAT-406](pulse_platform/frontend-chat/plat-406.md), fixed on `main`; New chat deployed widely, mic and layout on
Excellence only. No model picker in the chat input; terminal and attach left, New chat, wand, mic and send right.

## Deploy behaviour and one runtime profile — PLAT-405

[PLAT-405](pulse_platform/scheduler-runs/plat-405.md), fixed on `main`; step 2 (standard profile written by the
rootless-linux deploy) still to reach Dominion and SparkQuill. Slack notices silent by default, deploys switch over at
once, stale `.deploying` markers no longer pin releases, drift report.

## Slot accounts: private home, system Chrome, socket folder, slot table, Usage terminal — PLAT-404

[PLAT-404](pulse_platform/security-sandbox/plat-404.md), P1, fixed on `main`; Chrome grant deployed to Excellence only.
Group-accessible sandbox home, one home per person in Code, `/opt/google/chrome` granted, browser socket folder always
set, slot table readable after re-init, Usage terminal slash allowlist, tunnels allowed in the Code prompt.

## Code terminal: own-account shell, tabs, colours, copy and scrolling — PLAT-403

[PLAT-403](pulse_platform/frontend-chat/plat-403.md), fixed on `main`; server scroll batching on Excellence only. Real
shell as the person's slot, up to 3 tabs, themed xterm.js, copy and wheel scroll through tmux; RTS has no terminal yet.

## Native agent tools always on for workflows and Relays — PLAT-402

[PLAT-402](pulse_platform/coding-agent-bridge/plat-402.md), P3, fixed on `main`; deployed to Excellence only. No switch any more; a stored "off" is ignored.

## Code project browser starts, screenshots and live-views under the sandbox — PLAT-401

[PLAT-401](pulse_platform/browser/plat-401.md), fixed on main; installed and proved on Excellence, other servers pending.
Managed Chrome launcher with a writable HOME and the resolved binary, installed by the deploy; regression tests and a
52-row browser matrix script.

## Supply-chain loader on the Excellence box; scanner missed it — PLAT-400

[PLAT-400](pulse_platform/security-sandbox/plat-400.md), P1, contained; sweep tool on main. Account owners must rotate
credentials; scheduled sweep not wired yet.

## Script outputs blocked by slot directory permissions — PLAT-399

[PLAT-399](pulse_platform/security-sandbox/plat-399.md), fixed on main; deployment
pending. Shared shell preparation makes granted output directories writable by
slots. Relay guidance requires file outputs; pip listing itself succeeded.

## Relay output labels and always-open workflow toolbar — PLAT-398

[PLAT-398](pulse_platform/frontend-chat/plat-398.md), fixed on main; deployment
pending. Output selector removed, output agent marked, authored user messages
named clearly; Automation moves into always-open Ops and setup is icons only.

## Full CLI uses its own subagents; run_in_background narrowed — PLAT-397

[PLAT-397](pulse_platform/coding-agent-bridge/plat-397.md), fixed on `main`, not
deployed: in-turn parallel work goes to the CLI's own subagents;
`run_in_background` only for read-only reviewers and long supervision loops.

## Full mode: no bridge edit tool — PLAT-396

[PLAT-396](pulse_platform/coding-agent-bridge/plat-396.md), fixed on `main`, not
deployed: with native tools on, the CLI edits natively and
`diff_patch_workspace_file` is not offered; kept for mcp_only, Pi and step agents.

## Chats read other workflows only when attached — PLAT-395

[PLAT-395](pulse_platform/security-sandbox/plat-395.md), fixed on `main`, not
deployed: the blanket read grant on `Workflow/` is gone; a chat reads its own
workflow and attached ones (bridge tools and the CLI sandbox alike).

## Seatbelt for every coding CLI on a Mac; full_unconfined removed — PLAT-394

[PLAT-394](pulse_platform/security-sandbox/plat-394.md), P1, on `main`, Mac-only
(servers unaffected): every CLI runs Full CLI under Seatbelt with the person's
home open; AgentWorks data outside the chat, blocked paths and open/osascript
are refused; nothing runs unconfined.

## Browser teaching restarted RTS Code; compact chrome and durable tabs — PLAT-393

[PLAT-393](pulse_platform/browser/plat-393.md), fixed on main, deployed to RTS
and verified in `7e2ea79-20261003164344`. Two compact header rows, profile-owned tab memory, direct teaching IPC
and bounded recovery of a previously controlled managed browser.

## Codex resumed chats cancelled after confinement migration — PLAT-392

[PLAT-392](pulse_platform/coding-agent-bridge/plat-392.md), fixed in the shared
provider, deployed to SparkQuill and verified with a real resumed Sol conversation.
The release and the affected restored conversation passed live checks. Only the selected thread is adopted into its
private home, and an old resume banner cannot block a newly ready pane.

## Gmail sender widening requires owner confirmation — PLAT-391

[PLAT-391](pulse_platform/security-sandbox/plat-391.md), implemented and locally validated; deployment pending. Additional senders require a private receipt
from the target owner's browser; agents cannot grant it. Public mailbox domains
are rejected and existing lists require review after deployment.


## Hybrid native-tools mode removed — PLAT-390

[PLAT-390](pulse_platform/coding-agent-bridge/plat-390.md), P1, on `main`, not
deployed: Native agent tools are `mcp_only` or `full` (sandboxed); Cursor and
Muse get full modes; one prompt per CLI per mode, tested against launch options.

## Relay API products retain Google apps, exclude Slack/WhatsApp — PLAT-389

[PLAT-389](pulse_platform/integrations/plat-389.md), fixed on main; deployment
pending. Reuses authorized Google connections and removes Relay Slack surfaces,
with manifest and runtime enforcement.

## Provider setup and product model selection — PLAT-386

[PLAT-386](pulse_platform/frontend-chat/plat-386.md), fixed on main; included in
the recorded RTS source release; app-level live qualification pending. Providers
owns agent/account setup; products select ready accounts, models and supported
effort. Includes the provider UI cleanup and Cursor effort forwarding. The
2026-10-04 removal of product provider search is fixed on main; deploy pending.

## Cursor account model availability — PLAT-387

[PLAT-387](pulse_platform/frontend-chat/plat-387.md), open, reproduced on RTS.
A curated/live catalog union still offers GLM 5.3/Flash to a key whose CLI rejects
them; discovery and filtering need selected-account identity.

## Cursor native model pricing — PLAT-388

[PLAT-388](pulse_platform/cost-telemetry/plat-388.md), open, reproduced on RTS.
Bare Grok selects Fast for this account while metadata assumes standard pricing;
live suffix IDs also need native-mode pricing resolution.

## Full CLI native writes ignored blocked paths on Linux — PLAT-385

[PLAT-385](pulse_platform/security-sandbox/plat-385.md), P1, **fixed** on `main`:
Landlock grants are split around blocked paths, so native tools cannot read or
write planning/ or the raw database; tested under the real launcher.


## RTS latency workflow cost ledger has damaged unique indexes — PLAT-384

[PLAT-384](pulse_platform/cost-telemetry/plat-384.md), P2, RTS indexes backed up,
rebuilt and integrity verified. Rows and financial values were preserved.
The original source of the index damage remains unconfirmed.

## Absolute host grants reached server sandboxes — PLAT-383

[PLAT-383](pulse_platform/security-sandbox/plat-383.md), P1, **fixed** on `main`:
workflow `folder_access` had no assigned-roots check and absolute grants reached
server CLI and shell sandboxes; now root-checked on update and kept only on a
person's own Mac.


## Browser toolbar and manual copy/paste — PLAT-382

[PLAT-382](pulse_platform/browser/plat-382.md), deployed and verified on RTS
(`f69f9fd-20261003144322`). Neutral three-row browser controls, native multiline
paste and selection copy behind exclusive manual control; real guarded Linux
paste passed. Cross-origin-frame selection copy remains a tracked follow-up.

## Decisions applied only in the UI and Builder chat — PLAT-381

[PLAT-381](pulse_platform/human-decisions/plat-381.md), P2, **fixed** on `main`:
scheduled runs no longer apply answered decisions (the PLAT-093 pre-run drain is
removed); Needs you keeps unapplied ones with "Apply in chat".

## Relay builder shell cannot inspect the host Google store — PLAT-380

[PLAT-380](pulse_platform/security-sandbox/plat-380.md), implemented and verified;
deployment pending. User slot shells exclude the automatic host Google store and
keyring environment. Old policy reproduced exit 125; corrected runner created a
JSON plan as the slot through Excellence's launcher without host-store access.

## Workflow notifications and Gmail setup guidance — PLAT-379

[PLAT-379](pulse_platform/integrations/plat-379.md), implemented and locally validated; deployment pending. `notify_user` is workflow-only. Incoming Gmail
explains Google Cloud/server setup in its pane and Builder instead of an
unexplained administrator warning.

## Relay commands and system prompt inspection — PLAT-378

[PLAT-378](pulse_platform/frontend-chat/plat-378.md), implemented and locally
verified; deployment pending. Relay commands come from its product manifest;
hidden files offers an owner-private, read-only finalized prompt snapshot.

## RTS Providers shows $242.35 as an unattributed user — PLAT-377

[PLAT-377](pulse_platform/cost-telemetry/plat-377.md), P2, fixed on main for
new workshop child runs; deployment pending. Authorized RTS historical repair
verified 753 rows ($250.52), including the entire reported $242.35 bucket.
Older legacy Code costs ($24.98) lack actor evidence and stay unattributed.
Detached workshop sessions discarded the launch user and channel, so child
steps/reviewers had workflow attribution but no user attribution.

## Blocked file sent agent shells to a weaker sandbox — PLAT-374

[PLAT-374](pulse_platform/security-sandbox/plat-374.md), P0, **fixed** on `main`
(`6f630cc25`), deploy pending: a blocked `db.sqlite` inside the project pushed
Code agent shells off Landlock onto the mount-namespace fallback, which ran as
the service account and could read the platform `.env`.

## Muse question shown as "Unknown Event Type" — PLAT-375

[PLAT-375](pulse_platform/frontend-chat/plat-375.md), P2, **fixed** on `main`,
deploy pending: the detailed chat view had no handler for
`coding_agent_question` (PLAT-354).

## Admin-managed provider terminal chdir failure — PLAT-376

[PLAT-376](pulse_platform/security-sandbox/plat-376.md), P1, **fixed** on `main`,
RTS deploy pending: a `global:` provider binding was confined like a personal
account and started with no working folder.

## Every local shell command refused — PLAT-373

[PLAT-373](pulse_platform/security-sandbox/plat-373.md), P0, **fixed** on `main`:
the folder-guard boundary check refused absolute host grants and the old
`Downloads/` link, so every local shell command returned HTTP 400.

## Relays product introduction — PLAT-373

[PLAT-373](pulse_platform/frontend-chat/plat-373.md), implemented and locally
verified; deployment pending. Relays shares Crew and Code's intro layout and
opens its existing creation dialog with the account create permission.

## Scripted steps could not write the workflow database — PLAT-372

[PLAT-372](pulse_platform/step-execution/plat-372.md), P1, **fixed** on `main`
(`7ce796701`), RTS deploy pending: a scripted step's script writes as the
group session, which never had the DB grant, so salesoutreach lead discovery
saved nothing from 2026-09-27.

## External crew/workflow interaction follow-ups — PLAT-365–370

The 2026-09-28 bidirectional MCP review found these. Three are fixed on
`main` and not yet deployed to RTS; three are open.

- [PLAT-365](pulse_platform/human-decisions/plat-365.md), P1, **fixed**
  (`ca65b9886`): workflow questions were stored without their session, so
  `run_status` never listed them and `run_reply_input` refused the answer.
  They now keep their session, choices and wait time.
- [PLAT-366](pulse_platform/security-sandbox/plat-366.md), P1, **fixed**
  (`39b468a8b`): a Crew-only token could read a known workflow call's result.
  Crew polling now requires a Crew target and re-checks current access.
- [PLAT-367](pulse_platform/human-decisions/plat-367.md), P1, open: workflow
  Ask's `wfask-` session fails the external reply API's `pat-` ownership
  check, even for its own caller.
- [PLAT-368](pulse_platform/human-decisions/plat-368.md), P2, **fixed**
  (`b616789cd`): stopping a run did not release its question wait. Now a
  cancellation withdraws the question at once and refuses late answers.
- [PLAT-369](pulse_platform/integrations/plat-369.md), P2, open: external
  function-call polling shows no pending questions and has no call-scoped
  reply tool.
- [PLAT-370](pulse_platform/human-decisions/plat-370.md), P2, open: questions
  and function-call lookup live in process memory, so a restart loses them.

## Project instruction files overwritten and deleted — PLAT-371

[PLAT-371](pulse_platform/security-sandbox/plat-371.md), P1, built on branches,
not merged or deployed. CLI adapters overwrote and deleted a project's own
`AGENTS.md` / `CLAUDE.md`, and `mcpagent`'s startup cleanup removed the whole
`.claude`, `.cursor`, `.pi`, `.codex` and `.agents` folders of the project (reproduced).
Overlapping Crew/Code sessions also collided on one file. Fix: a marked,
session-counted block, marker-only cleanup, `AGENTS.md` as the single file, one shared
Crew prompt with the reader role sent per message. Verified with real Claude and Codex
on isolated single- and multi-user instances. Design:
[project instruction files](../design/project_instruction_files.md); workflows follow
later ([plan](../design/workflow_shared_folder_plan.md)).

## Coding CLIs and the tmux socket are outside the sandbox — PLAT-364

[PLAT-364](pulse_platform/security-sandbox/plat-364.md) covers what the
private `/tmp` change (e59220636, deployed on RTS) left open.
- **Native reads:** coding CLIs run unsandboxed, so hybrid-mode native reads
  can see other users' files, CLI logins and `/proc/self/environ`.
- **tmux socket:** a Landlocked shell can still reach it (verified on RTS), so
  it can read or type into other users' CLI sessions. This is the most serious
  item.

tmux socket fixed and deployed on RTS (ab6bb0b4f): each sandboxed command
gets a private `/tmp` in its own namespace. QA is #236. Still open: run the
coding CLIs under the Landlock runner with per-user CLI homes, then Seatbelt
on macOS.

## Scheduled runs lost to pauses and to Pulse, and Pulse did not act — PLAT-363

[PLAT-363](pulse_platform/scheduler-runs/plat-363.md) explains 21 missed local
runs: 18 were skipped by a global "Pause all", and 3 were refused while a
Pulse fix run held the workflow's lock. Fix runs now use Pulse's own lock, so
they no longer block schedules.
- **Pulse:** its evidence now separates lost runs from deliberate skips.
  Technical Review may set `queue_latest` on a real-work schedule that lost a
  run, and Pulse holds off a step the workflow is running right now.
- **UI:** the missed badge says why a run didn't happen, pause and resume leave
  a history, and a resume offers a catch-up of the skipped runs.
Pushed to main; restart and deploy pending.

## Agents could act as another session or workflow — PLAT-362

[PLAT-362](pulse_platform/security-sandbox/plat-362.md) closes the path for an
agent to act as another user's session or workflow. Pulse platform tools and
decision answers now act only for the calling session's own workflow and owner.
Each agent's tool calls carry a token for its own session, signed with an
in-memory secret, and the server refuses any request that names another
session through the path, header, virtual-tool scope or model-supplied shell
env. The body-session execute routes now require the caller's own session.
Reviewed and pushed to main. Deployment, Pi/Cursor live checks, and follow-ups
D1–D7 (shared `/tmp` and tmux socket, unowned session routes, Cursor config in
the workspace, and more) are listed in the ticket.

## Scheduled runs and background agents stopped before their steps finished — PLAT-361

[PLAT-361](pulse_platform/scheduler-runs/plat-361.md) fixes salesoutreach's
email and LinkedIn schedules running 1 of 12 groups every day (Sep 19–26) and
Pulse reviewers ending without recording a verification result. `execute_step`
tells the agent to end its turn and wait for the result, but the scheduler and
background agents treated "turn ended" as "work done". Both now own the steps
they start, like a step owns its sub-agents: wait for every step, hand the
results back as the next turn, finish only when a turn starts nothing new.
Pushed to main; deployment and live acceptance pending.

## Completion turn colliding with a user turn closed the CLI — PLAT-360

[PLAT-360](pulse_platform/chat-reliability/plat-360.md) fixes a lost user
message on a Cursor tmux workflow chat. A background step's completion turn
started while the user's message was running in the retained CLI; `Session.Run`
refused it as "a turn is already in flight", but the server marked the session
`error`, and the tmux reaper then closed the CLI mid-turn. That refusal no
longer changes the session status or fails the terminal; the completion is
still queued for retry and runs after the user's turn. Fixed on main; deploy
and live verification pending.

## Plan Drift fired on cosmetic-only plan edits — PLAT-359

[PLAT-359](pulse_platform/plans-contracts/plat-359.md) fixes the trigger the
user reported as "even small change trigger a plan drift". Any persisted
plan-step field change, title included, flagged `plan_drift_review` due, by
design; a live audit found description-only edits were the single largest
share of the unreviewed changelog backlog. `planDriftMaterialFieldNames` now
gates both the per-step flag and the changelog backlog scan on whether a
changed field can actually affect a dependent (outputs, dependencies,
validation, routing); wording, review notes, schedule timing and model/tier
settings no longer count. An entry with no recorded field names at all
(untyped `update_step_config`, a step add/delete) stays conservative and is
still counted. Pushed to main; deployment and live acceptance pending.

## Builder-created Crews are born identity-complete — PLAT-358

[PLAT-358](pulse_platform/plans-contracts/plat-358.md) fixes issue #205
`BUG_ID_003`: `create_crew` took optional `description` plus optional
`purpose` and no `role`, so Builder-created Crews opened with the "needs a
role and purpose" banner. The tool now takes required `purpose` (stored in
the purpose slot, seeded into the starter brief) and required `role`
(stored in the identity block); `description` is removed. Pushed to main;
deployment and live acceptance pending.

## Crew functions: typed calls between Crews and workflows — PLAT-357

[PLAT-357](pulse_platform/integrations/plat-357.md) adds typed functions a
Crew or workflow declares (`functions.json`: input schema, result schema,
instructions). Other Crews and workflows call them with `call_function` or a
generated per-function tool; arguments and results are validated, a call
that finishes within 120 s returns its result directly, longer calls come
back as an `[AUTO-NOTIFICATION]`, and callers can poll progress or ask a
Crew target for an update mid-run. Built on the existing internal-trigger
bindings. Since 2026-09-24 functions are the only way to call a Crew (the
`*_target` tools are removed): every caller, whether a Crew, a workflow or
MCP/CLI, gets one continuing conversation with each Crew it calls, never its
main chat. The Triggers tab is now Webhooks (external only), and callers are
listed under Functions → Callers. Not yet deployed.

## Attached context made query_workflow_db "ambiguous" — PLAT-356

[PLAT-356](pulse_platform/step-execution/plat-356.md) fixes
`workflow database context is ambiguous`. When `DB_PATH` was not set, the
session resolver counted every folder grant as a candidate home. An attached
knowledgebase source (`Workflow/<other>/knowledgebase`) or an attached
workflow or Crew project (whole-root read grant) therefore made every
`query_workflow_db` / `query_workflow_costs` call fail, both in builder
sessions and in Crew chats with other Crews attached. The resolver now checks
the session's own writable roots and working directory first, then read
grants, then knowledgebase grants. The costs tool shares the same resolver.
Live-verified 2026-09-23.

## Scripted-step tool calls borrowed another run's executor — PLAT-355

[PLAT-355](pulse_platform/security-sandbox/plat-355.md) fixes the
intermittent `caller does not own this tool session` left open by PLAT-338. A
scripted step's `session-group-*` bridge session has no tool registry of its
own, so `CallCustomToolWithSession` fell back to the global executor map. The
last agent to start overwrites that map, so a concurrently starting schedule
could leave it bound to another run. mcpagent now resolves a live, registered
child to its parent run's registry and allow list. Unregistered children keep
the legacy path. Live-verified 2026-09-23.

## Show Muse native multiple-choice questions in chat — PLAT-354

[PLAT-354](pulse_platform/coding-agent-bridge/plat-354.html) proposes reading
Muse's structured `user_input_prompt_requested` questions and options from its
native `session.jsonl`, presenting them in chat, then submitting the user's
selections through the live tmux widget. The matching
`user_input_prompt_settled` event confirms the recorded answers. This replaces
the current automatic first-option behavior for user-facing questions.

## Crews get a read-only Run mode: single owner, inspect-and-run for everyone else — PLAT-353

[PLAT-353](pulse_platform/security-sandbox/plat-353.md) fixes `BUG_ID_001`
([issue #205](https://github.com/manishiitg/coding-agent-loop/issues/205)):
every Crew keeps a single owner and is read-only for everyone else, instead
of deriving permissions from linked workflows or keeping Crews private. Any
other signed-in user with the Crew product gets Run — chat with a read-only
tool deny-list and system prompt, mediated memory/file inspection with owner
transcripts and run databases excluded, and workflow-trigger invocation that
re-checks access. Reader conversations store under the reader's own account
and never touch the owner's manifest or live session; the proxy refuses raw
cross-user workspace traffic. The UI lists shared Crews with owner badges,
restricts them to chat plus Memory and a read-only file browser, and guards
all manifest writes. Implemented and pushed to main; deployment and live
acceptance on the Confida server pending.

## Chat reliability umbrella — PLAT-352

[PLAT-352](pulse_platform/chat-reliability/plat-352.md) is the single home
for chat-reliability work: every chat turn must execute exactly once,
journal durably, and restore identically. It covers the durable-log
restore refactor (shipped on main, RTS verification pending) and the
turn-delivery hardening track (turns silently dropped into dead retained
runtimes — diagnosed 2026-09-25 from Confida `ca9a753b` and local
news-monitor repros; fingerprint rebind + dead-runtime guard fix
deferred). Member tickets stay linked: PLAT-324 (continuity), PLAT-178
(delivery/transcript recovery), PLAT-340 (Stop/resume bindings),
PLAT-351 (retained-turn settle).

## Retained turns settle on durable runner outcome when the pane never idles — PLAT-351

[PLAT-351](pulse_platform/coding-agent-bridge/plat-351.md) fixes the false
"running" global monitor: the retained-turn observer settled only on
`CodingAgentPaneReady` pixels, which has no Muse case and returns false
forever, so a quiet-but-finished Muse turn stayed busy indefinitely. After
15s of stream quiet the observer now consults the provider durable sidecar
(`retainedturn.FinalResponse`, at most every 5s) and settles on runner
outcome; providers without a retained reader keep pane-only behavior, and a
one-time 30m warning (never a forced settle) covers turns where neither
signal fires. Implemented and pushed; live acceptance pending.

## Crew identity is role and purpose; fingerprint tracks it, not skills — PLAT-350

[PLAT-350](pulse_platform/coding-agent-bridge/plat-350.md) makes Crew role and
purpose required and user-owned, removes the user-editable instructions field
(behavior stays in the system-owned product template), and renders the project
description into the prompt as `Purpose:`. The retained native-session
fingerprint is now definition + MCP servers + role/purpose, so identity edits
relaunch retained sessions; skills are out of the key because the agent
re-reads them from disk. The setup banner opens Identity and asks for both
via the shared pane-to-chat queue. Implemented locally; live acceptance
pending.

## Chat delivery browser observability — PLAT-349

[PLAT-349](pulse_platform/frontend-chat/plat-349.md) closes the
browser-observability gap in chat delivery. The client records content-free
milestones (`sse_received`, `poll_received`, `catchup_received`,
`processed`) for visible chat events into a 100ms-batched queue that POSTs
fire-and-forget to `/api/client-telemetry/chat-delivery`; the server
validates a fixed schema and logs `[CLIENT_CHAT_TIMELINE]` lines. Message
text and tool arguments are never accepted. The `painted` phase is reserved
but not recorded yet. Implemented locally; live acceptance pending.

## RTS deploy blocked on stale active-session state — PLAT-347

[PLAT-347](pulse_platform/performance/plat-347.md) records the RTS release that
could not activate because `/api/health` reported `active_sessions: 1` with
zero in-flight work — retained chats keep that aggregate non-zero, so it was
never a safe gate. Decision: RTS activation is explicitly breaking (swap and
restart user services immediately; clients reconnect), and the deploy no
longer uses `DRAIN_TIMEOUT_SECONDS`. Fixed.

## Retained chat killed by provider idle timer, reported as tmux crash — PLAT-346

[PLAT-346](pulse_platform/coding-agent-bridge/plat-346.md) records the Crew
session whose active turn was canceled at 11:45 UTC and reported as a tmux
pane disappearance. Root cause: the 3-hour idle timer was never re-armed on
the retained-chat `SendClaudeCodeInput` path. Fixed by moving retention to a
shared lease (adapters keep no private timer state); pending RTS deployment.
Typed cleanup reasons remain an observability follow-up.

## Stopping a workflow response stranded the next message — PLAT-340 (duplicate chat-reliability PLAT-339 removed)

The duplicate [chat-reliability record](pulse_platform/chat-reliability/plat-339.md)
now points at [PLAT-340](pulse_platform/chat-reliability/plat-340.md),
which carries the Confida 2026-09-21 Stop/resume reproduction and fix.
(The PLAT-339 number belongs to the security-sandbox Crew-invocation
ticket.) Tracked under the [PLAT-352](pulse_platform/chat-reliability/plat-352.md)
chat-reliability umbrella.

## Retained-session workflow-open fan-out — PLAT-348

[PLAT-348](pulse_platform/performance/plat-348.md) records the RTS regression
where `/api/sessions/active` correctly retained 45 completed conversations for
continuity, but workflow navigation treated 43 terminal workflow rows as live
reconnect candidates. `rtsprreviweer` alone contributed 42 rows, multiplying a
single switch into per-session resolution/hydration while chat briefly showed a
false empty state. Live chat/dashboard/Pulse endpoint timings were only 1–55
ms. The local fix filters reconnect and streaming projection through the
canonical live-activity rule and masks stale/empty chat until workflow-keyed
durable selection settles. Focused tests and TypeScript build pass; deployment
and RTS acceptance remain pending.

## Workspace global write lock stalls all users — PLAT-344

[PLAT-344](pulse_platform/performance/plat-344.md) records the confida
2026-09-21 incident where one slow `POST /api/upload` held the workspace
service's single global write mutex for 3m10s, queueing every document PUT
behind it — including chat journal saves, which timed out at the agent's
15s budget, landed late, and orphaned a `delivery_uncertain` submission
that replays 409 forever. Long-term fix only (proposed, not implemented):
ingest-before-locking, per-path locks, lock-free append shapes, bounded
upload streaming. One live wedged submission remains on confida.

## Interactive workflow Stop/resume — PLAT-340

[PLAT-340](pulse_platform/chat-reliability/plat-340.md) records the Confida
workflow-chat failure where Stop canceled the response but also discarded the
request and workspace binding needed by the next message. Interactive chat Stop
now preserves those bindings while still canceling current work; scheduled,
webhook, and bot runs retain full teardown. Endpoint-level and guard regressions
are green; deployment and live acceptance remain pending.

## Read-only Crew workflow invocation — PLAT-339

[PLAT-339](pulse_platform/security-sandbox/plat-339.md) makes the bidirectional
Workflow ↔ Crew integration symmetric. A Crew with an authorized read-only
workflow attachment can discover triggers and create or reuse one secretless
internal trigger bound to that exact Crew, then invoke and poll it through the
existing internal dispatcher. This is a narrow authorization exception, not
general workflow write access: public webhooks and unrelated trigger management
remain protected, and attachment/access/caller checks fail closed. The change
and focused regressions are complete locally; deployment and live acceptance
remain pending.

## Workflow Automation Chats schedule visibility — PLAT-336

[PLAT-336](pulse_platform/frontend-chat/plat-336.md) records why
`twitter-automation`'s fresh scheduled conversations existed on disk and in
`chat-index.json` but were absent from Automation → Chats: the workflow caller
enabled `recentOnly` without the `includeAutomationChats` opt-in already used by
Crew, so the client requested only `kind=chat`. Commit `b812cdb48` wires the
workflow hub to the unified conversation index, and `69fd91776` labels every
row as Chat, Schedule, Trigger, or Bot (with richer names/platforms when known);
focused regressions are green, with deployment and live verification pending.

## Dashboard composition widgets — PLAT-335

[PLAT-335](pulse_platform/frontend-chat/plat-335.md) proposes six optional `window.report` composition widgets (`renderTabs`, `renderKpis`, `renderTable`, `renderActivity`, `renderActions`, `renderCollapsible`, shared card CSS) so dashboards become ~100–200-line composition files instead of 500–3800-line hand-rolled single HTML. Grounded in a 9-dashboard survey (8 local + the 3827-line Confida server dashboard) showing zero usage of the shipped goal/cost widgets, universal hand-rolled tabs/KPIs/cards/tables/activity, and Confida's 13 hand-wired action buttons motivating `renderActions`. The guidance rewrite subsumes removing the stale `renderEvaluations`/`getEvaluations` promises left over from the PLAT-333 eval removal. Proposal only; not implemented.

## Busy multiline tmux input false durable 409 — PLAT-334

[PLAT-334](pulse_platform/coding-agent-bridge/plat-334.md) records the Claude
Code incident where a multiline paste chip was already visible during an active
repainting turn, but whole-pane stability never occurred, Enter was never sent,
and the durable submission remained a 409. The Claude correction and exact
deterministic P0 are locally verified, as is transcript-based recovery of the
original receipt. The ticket remains open until busy multiline live input is a
required P0 certification for Claude Code, Codex CLI, Cursor CLI, Pi and Muse,
and live acceptance plus deployment evidence are recorded.

## Eval subsystem removal with producer-owned measurement — PLAT-333

[PLAT-333](pulse_platform/evaluation/plat-333.md) records the eval-subsystem removal and the final-review rejection of the mandatory 1.0.43 measurement migration (unneeded imposed topology, uncreatable router, nonexistent tool, unimplemented metrics table). No replacement migration ships: 1.0.41–1.0.43 are execution-compatible historical markers, measurement comes from producing steps' own stored outputs plus `record_goal_observations` for Pulse history, and old evaluation artifacts stay read-only. Implemented in `f13173f80` and pushed to `main`; deployment pending. See the [removal plan](../workflow/eval_removal_plan.md).

## Manual workflow contract preflight — PLAT-332

[PLAT-332](pulse_platform/plans-contracts/plat-332.md) closes the manual-execution migration gap: every interactive `run_full_workflow` and `execute_step` call rechecks `workflow.json` before starting. An old or unknown contract starts no execution and directs the owner to the manual Workshop upgrade path. The 2026-09-22 follow-up removes contract-upgrade turns from cron/calendar and `trigger_schedule` runs: schedules continue on their saved contract and cannot stamp migrations. Direct webhooks remain fail-closed. The original guard release `bb24ab5-20260918164118` is deployed and verified; the schedule-policy follow-up is implemented on `main` with deployment verification pending.

## Concurrent webhook route isolation — PLAT-331

[PLAT-331](pulse_platform/step-execution/plat-331.md) records the proven RTS PR Reviewer race where #87's eligible run branched correctly, then its review step reread a shared `db/assets/route_selection.json` overwritten by #82's closed delivery. Routing and branch steps now persist their resolved decision in their own iteration execution folder, plan mutations reject that specific shared-mirror pattern when a prior run-scoped producer exists, and the only unsafe workflow found by the production census was repaired. The remaining v1.0.42 scheduled cleanup turn is retired; the runtime and mutation guards remain. Intentional shared route inputs remain supported. Release `e63b6c7-20260918161036` is deployed and RTS PR Reviewer is migrated to v1.0.42; overlapping-delivery live acceptance remains pending.

## Workflow suggestions and permission refresh — PLAT-330 / PLAT-262

[PLAT-330](pulse_platform/security-sandbox/plat-330.md) tracks the bounded suggestion tool for Run/read-only workflow users. The [PLAT-262 follow-up](pulse_platform/security-sandbox/plat-262.md) fixes permission fingerprint checks before retained CLI delivery; [PLAT-324](pulse_platform/chat-reliability/plat-324.md) records same-chat continuity and [PLAT-102](pulse_platform/coding-agent-bridge/plat-102.md) records the fast-path compatibility boundary. These changes are tested locally; deployment and live acceptance are pending.

## Cross-workflow knowledgebase write access — PLAT-325 (PLAT-310 follow-up)

Implemented locally, not yet deployed to RTS: `knowledgebase_sources` now
accepts `access: "write"` (confined to the source's `notes/` folder), gated
by an explicit `kb_write_grants` consent from the source workflow's own
owner rather than the audience-overlap check read relies on. Concurrent
writers into the same target are explicitly not serialized in this pass —
a known, accepted gap, not an oversight. See
[PLAT-325](pulse_platform/learnings-knowledge/plat-325.md) and the updated
[shared KB contract](../workflow/shared_knowledgebase_sources.md).

## Builder MCP installation admission and persistence — PLAT-307 follow-up

Deployed to RTS as `009b0d6c1`: AgentWorks `product.yaml` chat capability
policy, interactive Builder MCP registration, explicit execution/Pulse/read-only
boundaries, native catalog refresh and durable MCP config across releases.
The follow-up selecting Builder/Run Markdown prompts and core skills from YAML
is implemented and tested locally; deployment is pending for that follow-up.
See [PLAT-307](pulse_platform/security-sandbox/plat-307.md) for scope and tests.

# Pulse Platform-Issue Register

## Workflow navigation transcript isolation — PLAT-343

[PLAT-343](pulse_platform/frontend-chat/plat-343.md) prevents the previous
workflow's transcript from remaining visible after a different workflow is
selected. The workflow chat surface now verifies that the active tab belongs to
the selected workflow and displays its loading state during asynchronous tab
resolution. Focused resolver tests and the frontend build pass; deployment and
service health are green in RTS release `6c47129-20260921082037`; interactive
acceptance remains.

## Ctrl+K first-open latency — PLAT-342

[PLAT-342](pulse_platform/performance/plat-342.md) removes the quick switcher's
first-use chunk fetch, renders its cached session list immediately, and stops
forcing an active-session request or resetting the overlay when preset data
changes. The focused performance contract and frontend build pass; deployment
and service health are green in RTS release `6c47129-20260921082037`;
interactive timing verification remains.

## Native transcript recovery CPU saturation — PLAT-341

[PLAT-341](pulse_platform/performance/plat-341.md) records the RTS
performance regression where 13 durable transcript-recovery markers were
reconciled every 30 seconds forever and the LCS merge repeatedly normalized
full message text inside every matrix comparison. Production pprof attributed
55% of agent CPU to that recovery path. Message keys are now precomputed,
historical retries use bounded backoff/age/attempt limits with one worker, and
older attempts cannot overwrite newer recovery demands. Focused regressions
pass. Production logging now also has bounded rotation, opt-in debug chatter,
safe shell-command fingerprints, and deduplicated registry diagnostics. The
runtime is protected by these bounds, but the ticket now records the remaining
architectural risk and long-term cutover: stable structured turn/message IDs as
the sole Formatted Chat source, with native transcripts retained only as an
idempotent, terminal-state emergency repair path rather than a whole-history
polling and text-matching mechanism. RTS
release `6c47129-20260921082037` is healthy and completed its first rotation;
the active agent/workspace logs are now small. Longer steady-state profiling
remains pending. The first local cutover slice now durably journals semantic
structured boundaries before SSE publication and persists retained structured
completions by exact turn checkpoint; token streaming stays live-only and does
not synchronously commit to SQLite. Slow-append and journal-size telemetry are
included, session removal no longer resurrects a durable tail, and status reads
do not trigger hydration. Native whole-history recovery is skipped when the
structured append succeeds. Universal provider event IDs, exact-turn emergency
jobs, checkpoint-safe compaction, deployment and live acceptance remain
pending.

## Scheduled scripted-tool child ownership — PLAT-338

[PLAT-338](pulse_platform/scheduler-runs/plat-338.md) records why a scheduled
Twitter workflow's direct `agent_browser` probe succeeded while its scripted
CDP preflight repeatedly received `caller does not own this tool session`.
The authenticated-tool boundary admitted only the parent schedule session and
mistook the workflow's registered `session-group-*` bridge child for an
unrelated caller. The boundary now accepts only live, registry-proven children
of that exact parent; forged and unrelated sessions remain blocked. Focused
tool-context, scheduler, and browser tests are green; live external-action
acceptance remains owner-triggered.

## Legacy schedule action-tool identity — PLAT-337

[PLAT-337](pulse_platform/scheduler-runs/plat-337.md) records why every action
tool in `twitter-automation` schedule chats returned `requires an authenticated
session` while read-only bridge discovery still worked. The workflow predates
ownership metadata, so the scheduler passed no user identity into the stricter
shared tool boundary introduced on 2026-09-17. The scheduler now explicitly
uses the local owner for ownerless legacy workflows in single-user mode and a
recorded access owner when available, while ownerless multi-user workflows
remain safely blocked. Regression coverage is green; restart and live schedule
identity verification are complete on `e21f799a7`. All 29 local schedules now
register with the authenticated `default` principal; the next naturally due
action run is the remaining acceptance check. The follow-up also unifies saved
trigger execution: API/internal triggers and configured Slack workflow triggers
use the workflow owner's resource/secrets scope. Slack remains constrained to
Run mode with its bot principal and external sender retained for authorization
and audit; WhatsApp retains its paired-user identity model.

## Builder plan-tool consolidation — PLAT-329

[PLAT-329](pulse_platform/plans-contracts/plat-329.md) tracks the locally implemented consolidation of typed step/route/group/conversion/maintenance tools and replacement of the dedicated plan-review launcher with existing skill-driven background review. Validation and deployment status are recorded in the ticket.

## Pulse persistence simplification — PLAT-326

[PLAT-326](pulse_platform/pulse-governance/plat-326.md) completed its corrected
schema-v2 rollout across the local workspace after the `Workflow/testing`
canary. Twelve workflow databases are current, integrity-clean and contain only
Plan Drift, Technical, Architecture and Strategic review identities; a second
pass was a no-op. RTS, other laptops and deployments such as Confida remain
pending until this code is deployed. Reviewer skills now use one result,
canonical issues and genuine human decisions; Gate owns only scheduling,
Activity reuses the result, and the separate impact tool is no longer exposed.
UI/read cutover and removal of rollback-window compatibility tables remain.

## Crew memory visibility — PLAT-345

[PLAT-345](pulse_platform/learnings-knowledge/plat-345.md) tracks the first-class
Crew Memory view. It renders the canonical project `MEMORY.md`, distinguishes
durable context from reusable procedures, shows only project-local Crew skills
and opens skill management directly from Memory. Dashboard remains the
initial default view.

## Chat reliability — PLAT-178, PLAT-323 and PLAT-324

[PLAT-178](pulse_platform/chat-reliability/plat-178.md) owns durable live-input
message persistence and native-transcript recovery when the chat UI falls behind
the coding terminal. Its 2026-09-21 follow-up also covers rapid-message
visibility: RTS proved that busy Cursor submissions may spend 15–19 seconds
waiting for a safe composer before truthful `sent_to_cli` acceptance. Formatted
Chat now stages every pending user row before its serialized delivery lane,
while the single and double delivery ticks remain governed by the durable-ack
contract; no premature server success is reported. Deployment and live Cursor
multi-send verification remain pending. [PLAT-323](pulse_platform/chat-reliability/plat-323.md) owns
workflow-local, per-user Builder chat storage, access isolation and the one-time
legacy migration. Together they define the Chat Reliability category; purely
visual chat defects remain under frontend-chat.
[PLAT-324](pulse_platform/chat-reliability/plat-324.md) owns Work/Crew tab
continuity across browser reloads, backend restarts and deployments: an
already-open tab must rebind its saved application session before its next
message can reach a provider conversation. Its latest deployed correction also
prevents a stale second hydration from removing a Cursor final that was already
visible in the live chat timeline. The 2026-09-21 follow-up `15ec6141a` removes
the frontend's completion-time provider-native transcript merge: formatted Chat
now consumes only structured events and persisted AgentWorks history, while
tmux remains the CLI process host/raw Terminal and backend adapters alone may
normalize native recovery into structured events. Focused frontend and retained
turn contract tests pass; deployment and live acceptance remain pending.
Additional 2026-09-17 multi-user/tab isolation, durable acceptance and recovery
hardening, cold-workflow recovery, duplicate snapshot/notification fixes and
shared queue ownership for decision/report buttons are deployed to RTS in
`eb93a59-20260917081444`, with health verified;
the user live continuity acceptance matrix remains pending. See the ticket for current status.

## Persistent managed browser ownership — PLAT-322

[PLAT-322](pulse_platform/browser-automation/plat-322.md) is the canonical browser ownership
contract: one persistent managed-headless browser per workflow, shared by all
authorized users, Builder sessions, runs, schedules, triggers, steps and
delegated agents, with separate profile directories across workflows. Crew
projects separately receive one owner-and-workspace-scoped persistent browser.
The 2026-09-22 follow-up fixes live discovery misclassifying Crew's shared
`workflow.json` as a real Workflow manifest and filtering its healthy stream
from the Browser panel. It
also contains the merged delegation-inheritance, CDP owner/quota and server-CDP
incident history; the three superseded source tickets were removed. The
implementation is pushed as `a707df258`; deployment and live cross-user/
cross-workflow verification remain pending. The second 2026-09-22 follow-up
(issue #210) routes bundled capture through a shared canonical
session-workspace classifier so Crew sessions capture into their project
root instead of `CAPTURE_ACCESS_DENIED`; artifact-destination denials were
verified as correct enforcement, not a policy gap.

## Immutable scheduled-run folders and Builder `iteration-0` isolation

[PLAT-320](pulse_platform/scheduler-runs/plat-320.md) specifies the compatibility migration
that reserves mutable `iteration-0` for Builder/manual workflow execution and
allocates an immutable `iteration-N-sched` folder for every workflow-producing
saved-schedule occurrence before its first tool call. Pulse-only review and
platform-maintenance occurrences retain their own lifecycle identity and target
existing evidence rather than creating empty workflow runs. The ticket covers exact occurrence/run
identity, direct `execute_step`, paired evaluation, logs and validation
evidence, cost/history attribution, retention, APIs/UI, AgentWorks guidance and
Pulse awareness. Existing plain iterations and `-hook` runs remain readable.
The core implementation is on `main` in `70660472b` and `53e5a0cc0`, including
uniform per-family retention from `workflow.json::run_retention_count`; the
ticket remains open for live linkage and comprehensive evidence mismatch
handling.
Agent-declared resource/file claims are explicitly not a safety mechanism.
[PLAT-321](pulse_platform/scheduler-runs/plat-321.md) now has the runtime/schema/guidance core:
schedules remain sequential by default, while an independently approved schedule
can opt into overlap only with the fixed warning that shared workflow state and
external actions may conflict, overwrite or duplicate. Authenticated approval
provenance and the complete UI confirmation flow remain open.

## Chat notification ownership — PLAT-106 follow-up

[PLAT-106](pulse_platform/frontend-chat/plat-106.md) records the locally tested,
not-yet-deployed notification ownership repair. Integration notes are now on
[PLAT-095](pulse_platform/scheduler-runs/plat-095.md),
[PLAT-100](pulse_platform/coding-agent-bridge/plat-100.md),
[PLAT-113](pulse_platform/coding-agent-bridge/plat-113.md),
[PLAT-117](pulse_platform/coding-agent-bridge/plat-117.md),
[PLAT-255](pulse_platform/evaluation/plat-255.md), and
[PLAT-293](pulse_platform/frontend-chat/plat-293.md). Their original lifecycle,
queueing, early-notice, and report-security acceptance remains separate; none
is closed by the routing fix. Live concurrent Schedule/trigger + Chat
verification is still required.

## Notification duplicate rendering

[PLAT-319](pulse_platform/integrations/plat-319.md) fixes Slack's visible plain-text plus
formatted-attachment duplication. Email MIME regression passes, but the reported
email symptom remains unverified. Slack release verification pending.

## Builder webhook creation and Plan loading follow-ups

[PLAT-309](pulse_platform/integrations/plat-309.md) now records the missing Builder registrar,
authenticated testing tool and webhook skill, plus the deployed four-delivery
server limit and its read-only `max_concurrency` projection. [PLAT-318](pulse_platform/frontend-chat/plat-318.md)
tracks concurrent Plan loading and stale-result isolation. The latest webhook
concurrency/introspection releases are `1e87e01-20260916183335` and
`bdcbb00-20260916184549`.

## Builder user management and Pulse/picker follow-ups

[PLAT-317](pulse_platform/security-sandbox/plat-317.md) adds Builder-only user/access management
with live owner/admin checks. [PLAT-315](pulse_platform/pulse-governance/plat-315.md) removes the
implicit metric-setup gate from the Pulse switch; [PLAT-262](pulse_platform/security-sandbox/plat-262.md)
fixes double handling of workflow-picker arrow keys. Deployed to RTS as `3a37a1c75`.

## September 12 — workflow capabilities and RTS fixes

Implemented and deployed in RTS release `bb7ac6d-20260912135437`. Each ticket
records implementation commits, test evidence and remaining live acceptance limits.

- [PLAT-308](pulse_platform/security-sandbox/plat-308.md) — Admin-managed global secrets, including promotion from another workflow.
- [PLAT-309](pulse_platform/integrations/plat-309.md) — Authenticated workflow webhooks and visible schedule/trigger routes.
- [PLAT-310](pulse_platform/learnings-knowledge/plat-310.md) — Attach multiple workflow knowledge bases with read-only shell access.
- [PLAT-311](pulse_platform/pulse-governance/plat-311.md) — Goal metric groups and linked supporting measurements.
- [PLAT-312](pulse_platform/security-sandbox/plat-312.md) — Shared gog authentication store with direct terminal access.
- [PLAT-313](pulse_platform/coding-agent-bridge/plat-313.md) — Preserve intermediate CLI agent messages in real-time chat.
- [PLAT-314](pulse_platform/coding-agent-bridge/plat-314.md) — Cursor live-input false 409 from empty composer footer matching.
- [PLAT-315](pulse_platform/pulse-governance/plat-315.md) — Pulse slash command resolves the open chat workflow.
- [PLAT-316](pulse_platform/frontend-chat/plat-316.md) — Animate the Pulse heartbeat when human decisions are pending.
- [PLAT-262](pulse_platform/security-sandbox/plat-262.md) — fresh workflow picker permissions and server-side authorization of attached chat context.


## Manual workflow runs could manage MCP servers — PLAT-307

[PLAT-307](pulse_platform/security-sandbox/plat-307.md) fixes a manual Run-button execution
(`agent_mode: "workflow"`) getting the full MCP server management toolset
(search/inspect/install/add/edit/remove/logs/discovery) that only the workflow
builder chat and regular chat are meant to have — scheduled/cron runs were
already correctly excluded via a different `agent_mode` value. Also makes
`remove_mcp_server` scan and warn about workflows left with a dangling
reference after an account-wide removal. Implemented and regression-tested
locally; deployment pending.

## Minimal review recording — PLAT-306

[PLAT-306](pulse_platform/pulse-governance/plat-306.md) replaces mandatory review Markdown
bookkeeping with an optional concise SQLite note on the existing result call.
The same note serves future reviews and the UI; old reports remain readable.
Runtime interruption tracking remains independent of agent-authored notes.
Deployment and measured live reporting overhead are pending.

## QA, Architecture, Strategy and improvement outcomes — PLAT-305

[PLAT-305](pulse_platform/pulse-governance/plat-305.md) implements independent
review roles, sequential scheduling with protected research dates, authorized
research access, linked decision/application/outcome tracking, and the revised
Pulse workspace. Goals, metrics and Strategy are primary; Technical,
Architecture and Plan Drift are platform-health controls. Technical,
Architecture and Strategy have persistent automatic toggles plus one-off manual
run actions; Plan Drift stays mandatory and blocks downstream runs while due.
[PLAT-303](pulse_platform/pulse-governance/plat-303.md) now gives Architecture
the longer evidence horizon and Strategy the shorter meaningful-outcome horizon.
Strategy is explicitly prevented from treating recurring operational labels such
as `booking-heavy` as its whole agenda. These changes are locally tested on
2026-09-17; deployment and live workflow acceptance are pending. Immutable
partial-run evidence (PLAT-047/089) and comprehensive builder prevention
(PLAT-257/298) remain separate unfinished foundation work.

## September 9 Pulse audit: shared fixes and remaining strategy work

[PLAT-304](pulse_platform/security-sandbox/plat-304.md) fixes authorized retention updates blocked
by the raw manifest guard. [PLAT-303](pulse_platform/pulse-governance/plat-303.md) fixes strategic
completion being failed by unrelated technical repair debt and records the
independent strategic schedule/outcome follow-through work. At the time of this
September 9 audit, Gate was exception-driven but independent cadence was not yet
implemented; the September 17 PLAT-303/305 follow-through above supersedes that
historical limitation.

[PLAT-229](pulse_platform/evaluation/plat-229.md) now covers valid empty-array per-item
contracts. [PLAT-163](pulse_platform/pulse-governance/plat-163.md) makes completed-run child errors
factual advisory signals. [PLAT-056](pulse_platform/evaluation/plat-056.md) was stale: its
automatic recorder was already removed and current tests confirm that.
PLAT-047/089 evidence immutability and PLAT-257 generated-contract acceptance
remain open in their existing tickets. These changes require deployment and
live verification.

## MCP catalog name collision can strand a user's custom connector — PLAT-301

[PLAT-301](pulse_platform/integrations/plat-301.md) — PR #191 (merged) made the "Add via
JSON" editor treat any overlay entry whose name matches one of the 113 base
catalog connectors as a connection record, but the unchanged runtime merge
(`loadMergedConfig`) still resolves a same-name collision the other way
(overlay wins). A user whose pre-existing custom server happened to be named
e.g. Figma, Dropbox, or Postman — all newly reserved by #191 — now has that
entry permanently hidden and uneditable in the UI while it keeps running
underneath, and can never reach the new official connector of the same name.
Open, not yet fixed; identified during PR review, not reproduced live.

## Gmail send-only default and Google Workspace connections — PLAT-300

[PLAT-300](pulse_platform/integrations/plat-300.md) makes Gmail send-only the default (with
an opt-in read toggle) and fixes the identity/status gap that left a
send-only connection unable to name itself or report as authenticated. Adds
per-service Drive/Sheets/Docs/Slides/Calendar grants (read-only default,
opt-in write) to a connection, plus a `google_workspace_cli` agent tool that
runs `gog` directly under the resolved connection's credential rather than a
bespoke tool per operation. Implemented and deployed to confida; no live
Drive/Sheets connection exercised yet. 2026-09-20 follow-up implemented
locally (not yet pushed/deployed): imported gog accounts report
`valid: true` with empty scopes, so post-re-OAuth status read as fully
revoked; fix is consent-time `tokeninfo` scope persist plus
stored-scopes fallback, with regression tests passing.

## Streaming report video/audio evidence — PLAT-299

[PLAT-299](pulse_platform/frontend-chat/plat-299.md) adds `window.report.mediaUrl`, file-scoped
expiring playback credentials, authenticated range streaming, and report guidance
for durable test recordings. Implemented locally; targeted tests pass and
deployment pending. Existing `fileUrl` behavior remains compatible.

## Workflow code layout and consistent scripted execution — PLAT-298

[PLAT-298](pulse_platform/step-execution/plat-298.md) is **implemented; production acceptance pending**. New workflows use a versioned
`code/` source tree with workflow-wide step read/write access, shared helpers and
direct execution. Existing workflows remain on their legacy layout. Builder
tests, schedules and repair retries must share paths, environment, interpreter,
permissions and validation. Includes documented code requirements, dependency
provisioning, preflight and failure diagnostics. Focused tests and frontend type-check pass;
live builder/schedule/repair acceptance is pending. Persistent RTS EBS
workspace storage and installation checks are documented in the ticket. The
2026-09-08 extension adds one persisted `script_parameters` contract shared by
builder, orchestrator, runtime and repair; validated values reach `main.py` as
`STEP_PARAMS_JSON`. `call_sub_agent` remains instruction-only for agent routes;
the implemented `call_scripted_sub_agent` is parameter-only for scripted routes
and preserves its contract across asynchronous dispatch. Builder `execute_step`
accepts the same validated object. Help is generated from the saved contract
rather than handwritten per-script help. The 2026-09-22 enforcement accepts
only contract 1.0.44 for execution and requires `code_layout_version == 1`
on manual, webhook, Slack-trigger, and scheduled paths; older workflows get
a migration prompt or a prepended `upgrade-nested-agent-artifacts` turn.
Live acceptance of the migrated fleet is pending.

## Codex retained-session, resume and structured-event reliability — PLAT-297

[PLAT-297](pulse_platform/coding-agent-bridge/plat-297.md) records the completed Codex reliability
work across AgentWorks, `mcpagent` and `multi-llm-provider-go`. Codex rollout
JSONL is now authoritative for completion, final responses, failures, usage and
semantic MCP tool events when bound; tmux remains the interactive control
channel and retains a compatibility fallback if transcript binding fails.
Resume preserves Builder/Run mode and the private absolute runtime, physical
path trust prevents unattended startup prompts, filesystem-identity matching
binds JSONL even when macOS changes the directory's case spelling, and each
retained agent pins one managed CLI release. Unit suites plus live retained-session and isolated
two-step workflow P0 checks pass. All implementation commits are on `main`;
The cross-provider path/resume audit and remaining live/two-user acceptance
are tracked by PLAT-296.

## Isolate workflow chat CLI prompts and skills across Owner and Run sessions — PLAT-296

[PLAT-296](pulse_platform/security-sandbox/plat-296.md) records the delivered two-mode execution
model. Builder/Workshop and Run chats now pin distinct prompts, tools and skills
per tab and execute from private session runtimes while guarded tools continue
to use the same authoritative workflow workspace. Concurrent same-user Builder
and Run chats were live-tested: Builder received 128 tools, Run received 67,
both read the shared testing workflow, and Run refused a plan mutation. Normal
schedules use the smaller Run surface and private per-schedule runtimes; bounded
maintenance and Pulse turns elevate only their own requests. Mode-stable resume,
conversation continuity and cost attribution are covered by focused tests, with
Codex resume reliability completed in PLAT-297. **Implemented on `main`; full
live cross-provider restart and real two-user authorization remain follow-up
acceptance.** The follow-up audit fixes Cursor metadata cleanup and wrong-chat
transcript fallback, Pi alias lease conflicts, and shared path identity. Offline
projection acceptance now covers all four coding providers; saved v1 runtime
identifiers remain compatible. The audit also fixes an outdated `mcpagent`
provider pin that failed standalone compilation while local `go.work` masked it.

## Human-decided branch replaces yesno/multiple_choice human_input for new steps — PLAT-295

[PLAT-295](pulse_platform/human-decisions/plat-295.md) — `branch` gains `route_source: "human"`: the routes are the options, `branch_question` the prompt; preseeded sources (`route_selections`, `route_selection.json`, `route_source_file`, `context_dependencies`) still win so schedules answer up front, unattended runs use `default_route_id` or fail actionably, interactive runs ask through the existing `HumanFeedbackStore` prompt. `add_human_input_step` now rejects `yesno`/`multiple_choice` (points at the equivalent `add_branch_step` call) and keeps `text` for free-form value capture. Add-time only — **no migration, existing `human_input` steps untouched by owner decision.** 11 new tests pass with the full package; canvas badge added; not committed, not deployed, no live UI run yet.

## At most one routing step per plan — PLAT-294

[PLAT-294](pulse_platform/step-execution/plat-294.md) — the review's objection to a positional "routing before any work" check is accepted and that proposal is dropped. A census of every workflow showed exactly one `routing` step per plan is the mode selector schedules pick via `route_selections`, and every second one (hetznerssh, linkedin, social-media, sheet-analysis) is a `branch` by `branch.md`'s own definition; the agentic `routing_best_practices` check passed two of them and never ran on the others. Implemented as two deterministic rules on the two mutations that can introduce a routing step (`add_routing_step`, `convert_routing_branch_step_type` to routing): at most one routing step per plan, and no routing route straight to `end` (routing = a sub-workflow of many steps; a mode that does nothing is a simple if-condition → branch). **No load-time check and no migration by owner decision**: existing plans keep their routing steps as they are. Seven new tests pass locally with the full package; not committed, not deployed.

## Report action capability and isolation — PLAT-293

[PLAT-293](pulse_platform/frontend-chat/plat-293.md) now has a local `window.report.sendChatMessage` implementation: a host message review panel, the existing Ask in chat queue, an explicit new-chat choice, queued/cancelled receipts, and per-view duplicate protection. Report authors can save an existing approval before offering the scoped action message. This is a chat capability, not an atomic approval-consumer dispatcher. The iframe's existing same-origin access remains separate isolation work. Tests/build and simulated browser interaction passed; not pushed/deployed.

2026-09-05 local PLAT-259 follow-up: the plan canvas gives major routes separate wider columns and adds click-to-trace with faded unrelated nodes/edges, keyboard clearing, and Fit plan to view. Verified against Build in Public locally; see [PLAT-259](pulse_platform/step-execution/plat-259.md). Not deployed; live ticket counts unchanged.


## Route-specific Daily Actions and Pulse summaries — PLAT-259

[PLAT-259](pulse_platform/step-execution/plat-259.md) now extends routes-as-sub-workflows to
Notify, Activity, and the existing Daily Action report source from
[PLAT-279](pulse_platform/frontend-chat/plat-279.md). One digest carries independent route
outcomes; history preserves quiet routes, and Pulse scope comes from actual
review evidence. Contract `1.0.40` supplies the bounded existing-report upgrade.
Implemented/tested locally; commit, deployment and producing-run verification
remain. No live issue-resolution counts changed.

## Shared agent UI control framework — PLAT-292

[PLAT-292](pulse_platform/frontend-chat/plat-292.md) proposes a scoped, acknowledged UI-control
framework building on PLAT-278: capability discovery, UI-state inspection,
semantic actions, browser receipts and generated contracts. Covers all 22
current workspace views plus AgentWorks, Video Studio, Dominion, Activity,
file/media previews and report widgets. Includes permissions, privacy,
multi-tab/reconnect handling, phased rollout and acceptance tests.
**Baseline deployed to RTS; partially implemented.** The acknowledged AgentWorks baseline and
Notify instruction expansion are implemented; deeper adapters, product coverage
and full end-to-end rollout proof remain open. No change to live
issue-resolution counts. See PLAT-292's implementation record. The 2026-09-22
update makes SSE, view-state changes, and adapter swaps the primary sync
wake-ups with coalesced trailing syncs; the 10s poll only renews the 15s
lease and recovers lost events.

## Public finding ID preservation — PLAT-291

[PLAT-291](pulse_platform/pulse-governance/plat-291.md) fixes a reproduced apparent non-persistence
case: writes succeeded, but a later automatic merge deleted the returned issue
ID. New writes reuse matching legacy identities and automatic merges preserve
public-ID aliases. Tests pass locally; not deployed. The mixed LinkedIn report
PUL-2F70A97F stays open, so the remaining report count is unchanged.

## Prompt-health stale snapshot — 2026-09-05 (latest)

Owner-requested follow-up in PLAT-290 removes the session plan cache entirely:
current tools read fresh workspace plans, execution uses context-scoped
snapshots, and historical prompt/recovery lookups use retained run revisions.
No additional SQLite issues are closed by this architectural follow-up.

[PLAT-290](pulse_platform/plans-contracts/plat-290.md) fixes Upwork PUL-07504731: the actual
registered `get_plan_prompt_health` tool reused `approvedPlan` after persisted
descriptions changed. It now reads a fresh workspace snapshot on every call,
leaves the execution cache untouched, and fails explicitly on read/parse errors.
The regression failed before the fix and passes afterward, including successive
counts/duplicate changes; evaluation snapshot and existing health tests pass.

The exact report is resolved in SQLite with its prior records retained in an
audit event. **77 typed harness reports remain; 43 of the original 120 are
closed, across 59 remaining investigation groups.** Source changes are local
and uncommitted; deployment and a fresh producing Upwork run are not claimed.

## Substack research contract reconciliation — 2026-09-05 (latest)

[PLAT-289](pulse_platform/learnings-knowledge/plat-289.md) closes ten repeated research-folder reports:
PUL-AB711AC1, PUL-01F790F3, PUL-5903292F, PUL-137C5EDF, PUL-22656D6C,
PUL-F7B23729, PUL-9CE79401, PUL-32C03E39, PUL-B8FC2F51, PUL-224A1885.

The live plan already uses final `db/assets/research/` packets. Corrected three
contradictory learning statements (old canonical path, invented promotion step,
and fake success after denied writes). Retained September 5 packets and matching
forward outputs verify 15 + 5 + 29 + 8 = 57 findings, matching the parent summary.
A new production guarded-file-client/HTTP-handler regression persists and reads
temporary packets while denying legacy and raw SQLite writes. No grant broadened;
no producing run or historical business repair performed. Prior records and
evidence hashes preserved in SQLite events; all ten closures read-back verified.

**78 typed harness reports remain; 42 of the original 120 are closed.** Of the
prior audit's 61 open investigation groups, the one research-path group is now
resolved (60 remain). Separate growth/drafts reports and generic cross-file
validation limitations are not claimed fixed. Changes are uncommitted.

## Full remaining-report reconciliation — 2026-09-05 (latest)

All 96 reports remaining after the preceding batches were compared with the
register and grouped; eight more exact reports were resolved in SQLite and
read-back verified. **88 typed harness reports remain open across 61 investigation
groups; 32 of the original 120 are now closed.** Neither number is a count of
independently reproduced current bugs. Grouping does not merge issue identities.

| Tickets | Resolved exact reports | Basis |
|---|---|---|
| PLAT-198 / PLAT-240 | PUL-0B679A3D, PUL-517B83AD | Old verification-marker/allowlist and pending-attempt endpoint retired; current applied-fix/public-issue lifecycle tests pass |
| PLAT-214 | PUL-28FC41E1 | Exact step-concern to reviewer update fixture passes on current same-handle identity reload |
| PLAT-206 / PLAT-258 | PUL-565F9ED1, PUL-E19F5F1B | Supplemental Fixer dispositions accepted; obsolete child-session receipt gate removed; current state/audit consistency tests pass |
| PLAT-123 / PLAT-211 | PUL-D50AD8BC | Registration requirement intentionally superseded by ordinary-step concern-tool retirement |
| PLAT-001 | PUL-7B849CC6 | Keyed input parsing, isolated step contexts, explicit-input precedence and prompt tests pass |
| PLAT-078 / PLAT-200 | PUL-DD9EDE3C | Bounded snapshot recovery, full spill and session-granted read path tested |

Prior concern rows and typed detail snapshots are preserved in resolution event
metadata. No historical business/module outcome was repaired and no deployed
replay is claimed. No production implementation changes were made by this
reconciliation pass; existing local work is preserved.

The [complete 96-report audit](../audits/platform-open-report-reconciliation-2026-09-05.md)
records every group, related ticket, disposition and next evidence. In particular,
do not close PLAT-072-related PUL-E2BACEEB from the old UUID fix: its detailed
record contains post-fix recurrence. PLAT-322 and PLAT-066 explicitly remain
unproven for their reported live incidents. Ten Substack db/research observations
are grouped for one contract-boundary investigation, not treated as ten unrelated
platform defects or closed by broadening raw DB access.

## Verified backlog batch — 2026-09-05

Seven additional exact SQLite findings resolved for internal tracking, with prior
records retained in resolution events. Typed unresolved harness findings fall
from 103 to 96 (24 closed across today's three batches, from the original 120).

| Ticket | Exact disposition | Verification boundary |
|---|---|---|
| [PLAT-258](pulse_platform/pulse-governance/plat-258.md) | PUL-4719B06D / PUL-09511B0E: correction consistency and premature parent failure fixed; PUL-E3F22BCC / PUL-76329350 / PUL-F9BA8AF2: conflicting child-session checker retired | Focused Pulse, conversation lifecycle and guidance tests pass; new fixes local, deployment pending |
| [PLAT-288](pulse_platform/step-execution/plat-288.md) | PUL-FF23BCD5: inverted Workshop scripted fast-path gate fixed | Mode-gate and scripted tests pass; no live preflight run |
| [PLAT-061](pulse_platform/step-execution/plat-061.md) | PUL-51D2EC0F: superseded inert db_access field | Supported-field merge guard passes; no permission expansion |

DB response-envelope findings PUL-25BDBC42, PUL-76F2EE72 and PUL-A14F764F remain
open. Native/MCP/decoded HTTP payload equivalence tests pass and tool guidance
now distinguishes transports, but the exact historical inconsistent calls have
not been reproduced. See [the dated audit](../audits/platform-backlog-reconciliation-2026-09-05.md)
for scope, tests and the earlier inventory; do not treat its original 111-row
snapshot as a fresh status query.
## PLAT-285 — Remove stale session plan cache

[PLAT-285](pulse_platform/plans-contracts/plat-285.md) resolves the stale prompt-health consumer
reported by Upwork PUL-07504731 and removes the shared controller plan cache.
Current tools read fresh workspace plans, executions use context-scoped snapshots,
and historical prompt/recovery lookups use content-verified retained revisions.
Local tests pass; deployment and a fresh producing workflow run are not claimed.
This ticket was prepared locally as PLAT-284 before that ID was published for
unrelated package-cache work; PLAT-285 is its final identity.

## Status

Triage snapshot captured 2026-08-03 from the workflow-local Pulse databases.
This is the canonical cross-workflow register for defects that a workflow-level
Pulse Fixer cannot repair. It is not evidence that every historical finding is
still reproducible on the latest binary: items marked **reverify** have an
implementation change but no post-change producing run yet.

The 2026-08-04 RTS Latency run supplied the first broad post-change runtime
reverification. It proved several fixes, but also reopened PLAT-009, showed that
PLAT-010 is incomplete at the projected lifecycle boundary, and exposed two new
platform issues in finalization authority and Pulse-agent cost accounting.
Follow-up UI/runtime inspection added PLAT-020 (scheduled-session continuation)
and PLAT-021 (proposal/decision projection). See the dated sections below.
The subsequent tool-error review added PLAT-022 through PLAT-026 and assigned
that independent batch to Claude Code.
The later Social Media runtime investigation expanded PLAT-003 to uniform
workflow-step DB read/write and added PLAT-027 (live child visibility) and
PLAT-028 (CDP tab argument normalization).
Electron runtime verification then added and closed PLAT-034: completed Raw
tmux panes were replacing their recorded stream with a final-screen capture,
which destroyed scrollback at the live-to-settled boundary.
Tectonicus Pulse then supplied new shared cost-reconciliation and changelog
coverage evidence, and added PLAT-036 (context-usage saturation) and PLAT-037
(learning-freshness attribution).
Hetzner SSH then confirmed the remaining readable-diff gap in PLAT-033 and
added PLAT-038 (per-attempt pre-validation evidence retention). Its advisor
findings also exposed PLAT-039: a missing durable route made product
recommendations appear as engineering repairs.
The subsequent Hetzner manual-trigger investigation added PLAT-040: the UI
silently discarded a busy response, while manual Pulse and full schedules were
incorrectly sharing one durable workflow lease despite chat/schedule
concurrency being an explicit product contract.
The Confida QA review added three distinct shared boundaries: PLAT-041 gives
every expected cron occurrence durable identity before execution, PLAT-042
records provable human-answer attribution, and PLAT-043 permits bounded
read-only SQLite integrity PRAGMAs through the guarded database tool.
Local process inspection added PLAT-048: scheduled main agents inherited
interactive-chat tmux retention, completed-live terminals could not be stopped,
and shutdown cleanup missed the real Claude session-name prefix.
The Upwork review added PLAT-044 through PLAT-047 and PLAT-049: false
finalizer-ownership findings, linked-decision loop closure, typed verdict
boundary hardening, the open immutable artifact-identity design, and shared
platform mechanics leaking into workflow artifacts.
The scheduler audit then added PLAT-050: Pulse review selection, fixing,
recovery, and tmux polling were duplicated in Go instead of being owned by one
continuing main-agent conversation.
The Upwork v1.0.21 migration then exposed PLAT-051: upgrade guidance named an
internal helper instead of a registered agent tool. Its scheduled follow-up
also exposed PLAT-052: known consecutive scheduler turns unnecessarily closed
and resumed the native Claude session.
The next Upwork Pulse run exposed PLAT-053: `run_in_background` gave Pulse
review/fix children a workflow-step subset rather than the full workshop tool
surface, so they could diagnose but not persist or repair findings.
A 2026-08-10 RTS Latency cron trace then added PLAT-067: after a successful
background child, the parent Claude terminal disappeared; recovery timed out,
but the scheduler still sent the next turn into that unusable transport.
A cross-workflow scheduler audit then added PLAT-054, which gates this register
itself: the idle watchdog terminates scheduled runs whose child work is
demonstrably still running, so the producing runs that ~40 `runtime_reverify`
entries are waiting on keep being destroyed before they can supply evidence.
That audit also supplied the fresh reproduction PLAT-017 has been blocked on.
A cross-workflow learnings-store audit then added PLAT-055: a step's reflection
turn can only write `learnings/`, so facts, run results and harness bug reports
that belong to the database, knowledgebase or Pulse are filed there instead —
producing 110 KB and 89 KB "skill" files whose own contents warn readers not to
trust them.
A new `scripts/pulse_health.py` sweep of every workflow's
`external_action_required` findings (nothing had done this systematically
before) found 78 candidates across all 11 Pulse-enabled workflows. Triaging
Instagram's 11 collapsed to one root cause and added PLAT-056: the
`__automatic_final_validation__` repair loop's concern recorder files a durable
finding for a prevalidation failure even when a later iteration in the same
attempt already fixed it and the attempt's terminal result passed. The
remaining candidates (build-in-public, tectonicusadaytrading, linkedin,
rtslatency, social-media) are not yet triaged. That script's coherence check
also added PLAT-057.

**The measured shape of the backlog, 2026-08-09 (baseline snapshot committed
alongside this register).** Across 8 workflows: 406 active findings, overall
closure 54.7%, and **verification debt 69.7% — 354 of 508 repair attempts could
not be proven.** That ratio is the loop: a fix reaches `awaiting_verification`,
only a producing run can settle it, the run dies (PLAT-054), and the next pass
re-examines the same finding. Of the 406 active, **115 are run-gated** (a
producing run closes them — PLAT-054's direct signal), 213 sit in the
capacity-limited engineering queue, and 78 need platform tickets and will never
close from runs. Reverification of this register's ~40 `runtime_reverify`
entries and the drainage of those 115 are the same event.

This document records platform ownership and deduplication. The authoritative
per-workflow lifecycle remains `db/db.sqlite`; detailed single-defect incident
documents remain authoritative where linked.

## Why this register exists

Pulse currently stores a platform defect inside the workflow that observed it.
The same harness root cause can therefore appear as several workflow findings,
consume reviewer slots, recur forever, and invite a Fixer that has no authority
to change the platform. A cross-workflow register is needed until the lifecycle
has a first-class platform issue and linkage table.

A finding belongs here only when the failed boundary is owned by the workflow
runtime, scheduler, bridge, tool contract, shared persistence, or shared UI—not
by the workflow plan or its data.

## Finding new candidates — `scripts/pulse_health.py`

Nothing sweeps workflow Pulse state into this register automatically — every
entry above was added by someone manually querying a workflow's `db.sqlite`
after noticing `external_action_required` findings. `scripts/pulse_health.py`
(repo root, read-only, all workflows at once) automates the discovery half and
three related diagnostics:

| Section | Answers |
|---|---|
| `--section loop` | Is Pulse converging or looping? Closure rate, **verification debt** (share of repair work that could not be proven), recurrence, and the run-gated / engineering-queue / platform split |
| `--section category` | Which classification+step clusters look like one shared root cause — this is what made **PLAT-056** (Instagram's 11 findings, one cause) obvious |
| `--section coverage` | **Whether a finding can be verified at all.** Routes nest and compose, so end-to-end paths are not enumerable (LinkedIn: 6 routers, 16 branches, 256 combinations — and its runs executed 2, 6, and 1 of 24 steps). Coverage therefore models *step execution*, not paths: a finding whose step has not run is **untested**, not failed. It also reports findings frozen by a reviewer lens disabled at the Gate, which likewise cannot close |
| `--section coherence` | Contradictory states the Go invariant cannot retroactively repair (today: `harness_issue` + `queued_for_engineering`) |
| `--section untriaged` | `external_action_required` findings citing no `PLAT-NNN` — the candidate feed for this register |

An `UNTRIAGED` row is not automatically a new ticket; it may be a fresh instance
of an existing one in different words. Read it against the tickets below before
filing. The script never files tickets — matching a finding to an existing
PLAT-NNN, or deciding it is genuinely new, stays a judgment call.

**Before/after measurement.** `--json <path>` writes a snapshot;
`--baseline <path>` diffs against one and reports how many of the *specific
findings open at baseline* actually closed. Use that rather than total counts:
disabling a reviewer lens at the Gate lowers the finding count on its own, which
is suppression, not improvement. The pre-restart baseline for the
PLAT-054/PLAT-055 verification lives at
`docs/bugs/pulse_platform/baseline-2026-08-09-pre-restart.json`.

The cohort diff separates **`stuck`** (same status, and its step did run — the
fix genuinely did not work) from **`untested`** (same status, but its step never
ran — no verdict is available). Those two look identical in a raw status count,
and conflating them is how a working fix gets blamed for a route that was never
taken. There are consequently three reasons an open finding cannot close, and
only the first is a real failure:

1. its step ran and the fix did not work — `stuck`
2. its step never ran — `untested` (see `--section coverage`)
3. its owning reviewer lens is disabled at the Gate, so nothing re-reviews it —
   **90 of 406 active findings** at the 2026-08-09 baseline

## Two-agent repair board

This file is the read-mostly platform index. Each issue has a canonical ticket
fragment under `docs/bugs/pulse_platform/` so **Codex** and **Claude Code** can
repair different boundaries concurrently without editing one large document.

Rules:

1. Every implementation ticket has one platform key, one narrow acceptance
   boundary, and exactly one assigned agent: `Codex`, `Claude Code`, or
   `Unassigned`.
2. An agent changes the assigned agent and state in the ticket fragment to
   `in_progress` before editing code and lists the main files it expects to
   touch. The other agent may review that work but does not implement the same
   ticket unless ownership is explicitly handed off. The shared index does not
   need editing at claim time.
3. A broad platform issue may have multiple suffix tickets (`PLAT-003-A`,
   `PLAT-003-B`) when its API, runtime, UI, or migration boundaries can be fixed
   independently. Do not combine unrelated PLAT keys into one coding task.
4. `implemented` means focused tests pass. `runtime_reverify` means the code is
   complete but a real producing workflow/Pulse run is still required. `done`
   requires both where runtime evidence is part of acceptance.
5. Each fragment records its evidence, acceptance boundary, and test command.
   The index is synchronized once at handoff, review, or completion—not on every
   working update. Create another incident document only when large raw
   evidence would make the ticket fragment unreadable.
6. When new evidence changes a ticket's diagnosis, scope, or implementation
   choice, preserve a **Decision history** in the ticket fragment. Each entry
   records the date, the decision made, the evidence/reason, and whether it
   supersedes an earlier belief. Do not silently rewrite the old conclusion:
   future reviewers need to distinguish an abandoned approach from a regression
   of the current one. Routine code edits that do not change the reasoning do
   not need another entry.

### Active ownership lanes

| Ticket | Boundary | Assigned agent | State | Primary files |
|---|---|---|---|---|
| [PLAT-001-A](pulse_platform/human-decisions/plat-001.md) | Reverify keyed human-input propagation | Claude Code | `runtime_reverify` | workflow orchestration/handoff |
| [PLAT-002-A](pulse_platform/coding-agent-bridge/plat-002.md) | Reverify canonical nested tool-failure status | Claude Code | `runtime_reverify` | tool bridge and terminal status |
| [PLAT-003-A](pulse_platform/step-execution/plat-003.md) | Reverify uniform workflow-step DB read/write | Codex | `runtime_reverify` | DB capability materialization |
| [PLAT-005-A](pulse_platform/step-execution/plat-005.md) | Reverify multi-name API-spec lookup | Claude Code | `runtime_reverify` | mcpagent API bridge |
| [PLAT-006-A](pulse_platform/step-execution/plat-006.md) | Reverify workflow-step shell cwd | Claude Code | `runtime_reverify` | step session/bridge cwd |
| [PLAT-007-A](pulse_platform/integrations/plat-007.md) | Exercise workflow image verification E2E | Claude Code | `runtime_e2e_reverify` | media tool path/model routing |
| [PLAT-009-A](pulse_platform/cost-telemetry/plat-009.md) | Merge grouped cost shards for an iteration-only query | Codex | `runtime_reverify` | `token_usage_store.go` |
| [PLAT-010-A](pulse_platform/pulse-governance/plat-010.md) | Finish partially migrated finding-event identities | Codex | `runtime_reverify` | `pulse_finding_lifecycle.go` |
| [PLAT-011-A](pulse_platform/cost-telemetry/plat-011.md) | Complete non-tier LLM-role UI acceptance | Claude Code | `ui_acceptance_pending` | LLM configuration API/UI |
| [PLAT-012-A](pulse_platform/plans-contracts/plat-012.md) | Reverify dependent-artifact changelog coverage | Claude Code | `runtime_reverify` | managed mutation changelog |
| [PLAT-014-A](pulse_platform/pulse-governance/plat-014.md) | Reverify reviewer skill delivery on Tectonicus | Claude Code | `partial_runtime_reverify` | guidance and skill delivery |
| [PLAT-015-A](pulse_platform/evaluation/plat-015.md) | Persist an explicit skipped-evaluation sentinel | Codex | `runtime_reverify` | `evaluation_execution.go`, `evaluation_types.go` |
| [PLAT-016-A](pulse_platform/evaluation/plat-016.md) | Preserve legitimate numeric zero evaluation scores | Codex | `runtime_reverify` | `evaluation_types.go` |
| [PLAT-017-A](pulse_platform/scheduler-runs/plat-017.md) | Reconcile proved terminal completion when the final schedule-run projection fails | Codex | `implemented locally; focused verification passed; live acceptance pending` | scheduler and run-metadata finalization |
| [PLAT-018-A](pulse_platform/pulse-governance/plat-018.md) | Use validated dashboard artifact as final-command proof | Codex | `runtime_reverify` | `pulse_final_commands.go` |
| [PLAT-019-A](pulse_platform/cost-telemetry/plat-019.md) | Price only unpriced Pulse-agent usage | Codex | `runtime_reverify` | `pulse_agent_metrics.go`, `costledger/ledger.go` |
| [PLAT-020-A](pulse_platform/coding-agent-bridge/plat-020.md) | Keep converted scheduled chat on the same session/tmux and prevent runtime reconciliation from reverting it to read-only Schedule | Codex | `implementation_complete_runtime_reverify` (2026-08-15 recurrence fixed; live reverify pending) | `WorkflowChatTabs.tsx`, `workflowRuntimeTabProjection.ts`, retained-input routing |
| [PLAT-021-A](pulse_platform/human-decisions/plat-021.md) | Separate proposals from answerable user decisions | Codex | `ui_reverify` | `pulseFindingPresentation.ts`, `PulseWorkspace.tsx` |
| [PLAT-022-A](pulse_platform/step-execution/plat-022.md) | Restore `get_api_spec` registration in the affected message-sequence session | Claude Code | `assigned` | registration path / workflow-phase tool setup |
| [PLAT-023-A](pulse_platform/step-execution/plat-023.md) | Make large-file diff context failure recoverable without unsafe fuzzy apply | Claude Code + Codex follow-up | `implemented` | `workspace/handlers/diff_patch.go` |
| [PLAT-026-A](pulse_platform/frontend-chat/plat-026.md) | Keep selected running workflow visible exactly once | Claude Code | `implemented` | `GlobalActivityMonitor.tsx` |
| [PLAT-024-A](pulse_platform/coding-agent-bridge/plat-024.md) | Populate missing tool names in canonical error markers | Claude Code + Codex follow-up | `implemented` | mcpagent/provider logging |
| [PLAT-025-A](pulse_platform/step-execution/plat-025.md) | Bound workspace-shell stdout memory without corrupting scripted JSON | Claude Code | `queued` | `workspace/handlers/shell.go` |
| [PLAT-027-A](pulse_platform/step-execution/plat-027.md) | Keep a live asynchronous child visible after parent completion | Codex | `implemented` | terminal execution-tree projection |
| [PLAT-028-A](pulse_platform/browser-automation/plat-028.md) | Remove a recovered CDP tab from final page-action arguments | Codex | `implemented` | browser executor argument normalization |
| [PLAT-029-A](pulse_platform/coding-agent-bridge/plat-029.md) | Close stale live metadata before attaching to a missing tmux | Codex | `implemented` | terminal live-attach lifecycle |
| [PLAT-031-A](pulse_platform/cost-telemetry/plat-031.md) | Persist one immutable execution identity across cost-ledger date shards | Codex | `runtime_reverify` | execution-keyed cost/evaluation persistence and projection |
| [PLAT-032-A](pulse_platform/cost-telemetry/plat-032.md) | Include child-agent calls in parent step telemetry | Claude Code | `implemented` | child dispatch telemetry and usage aggregation |
| [PLAT-033-A](pulse_platform/plans-contracts/plat-033.md) | Replace placeholder changelog refs with truthful artifact evidence | Claude Code | `implemented` | managed mutation/changelog writer |
| [PLAT-034-A](pulse_platform/coding-agent-bridge/plat-034.md) | Retain Raw tmux scrollback after process completion | Codex | `done` | terminal live-attach and chat-history persistence |
| [PLAT-036-A](pulse_platform/cost-telemetry/plat-036.md) | Suppress context percentage when coding-CLI usage is aggregate rather than a context snapshot | Codex | `runtime_reverify` | coding-CLI adapters and cost telemetry writer |
| [PLAT-037-A](pulse_platform/learnings-knowledge/plat-037.md) | Attribute learning freshness to the actual writer | Unassigned | `new` | learnings freshness ledger writer |
| [PLAT-038-A](pulse_platform/evaluation/plat-038.md) | Retain complete pre-validation evidence for every attempt | Codex | `runtime_reverify` | shared validation artifact writer |
| [PLAT-039-A](pulse_platform/pulse-governance/plat-039.md) | Preserve and enforce advisor recommendation routes | Codex | `runtime_reverify` | advisor artifact/lifecycle projection |
| [PLAT-040-A](pulse_platform/scheduler-runs/plat-040.md) | Let a chat/manual Pulse coexist with one full schedule and show trigger failures | Codex | `runtime_reverify` | scheduler lease lanes and Schedule UI |
| [PLAT-041-A](pulse_platform/scheduler-runs/plat-041.md) | Persist every expected cron occurrence before execution | Codex | `runtime_reverify` | scheduler tick and durable fire decisions |
| [PLAT-042-A](pulse_platform/human-decisions/plat-042.md) | Preserve human-answer actor kind and audit events | Codex | `runtime_reverify` | report human-input persistence |
| [PLAT-043-A](pulse_platform/step-execution/plat-043.md) | Allow bounded read-only SQLite integrity PRAGMAs | Codex | `runtime_reverify` | guarded workflow DB query policy |
| [PLAT-044-A](pulse_platform/pulse-governance/plat-044.md) | Reconcile false stage-ownership findings when finalization succeeds | Codex | `runtime_reverify` | review guidance and final-command lifecycle |
| [PLAT-045-A](pulse_platform/pulse-governance/plat-045.md) | Consume an answered decision when its finding reaches an outcome | Codex | `runtime_reverify` | finding and human-input lifecycle |
| [PLAT-046-A](pulse_platform/pulse-governance/plat-046.md) | Reject an empty reviewer verdict at the typed tool boundary | Codex | `runtime_reverify` | typed Pulse reviewer tools |
| [PLAT-047-A](pulse_platform/scheduler-runs/plat-047.md) | Bind active/retained runs to explicit lifecycle, execution identity, and executable plan revision | Codex | `implementation_in_progress` | run-folder retention and execution identity |
| [PLAT-048-A](pulse_platform/coding-agent-bridge/plat-048.md) | Bound retained tmux to interactive chats and close completed-live processes | Codex | `runtime_reverify` | coding-agent modes, terminal leases/routes, server shutdown |
| [PLAT-049-A](pulse_platform/plans-contracts/plat-049.md) | Keep shared platform mechanics out of workflow artifacts | Codex | `runtime_reverify` | plan guards, review guidance, and version upgrade |
| [PLAT-050-A](pulse_platform/pulse-governance/plat-050.md) | Keep Pulse reasoning in one continuing agent conversation | Codex | `runtime_reverify` | scheduler Pulse orchestration and event-driven completion |
| [PLAT-051-A](pulse_platform/plans-contracts/plat-051.md) | Stamp contract upgrades through a real registered agent tool | Codex | `runtime_reverify` | version-upgrade guidance and Workshop tool surface |
| [PLAT-052-A](pulse_platform/coding-agent-bridge/plat-052.md) | Keep one native CLI alive across known scheduler turns | Codex | `runtime_reverify` | scheduler request lifecycle and coding-agent mode |
| [PLAT-053-A](pulse_platform/coding-agent-bridge/plat-053.md) | Give background workshop children the complete parent tool surface | Codex | `runtime_reverify` | background-agent construction and direct tool definitions |
| [PLAT-054-A](pulse_platform/scheduler-runs/plat-054.md) | Never expire a scheduler turn whose child work is still running | Claude Code | `runtime_reverify` | scheduler idle/turn-completion waits |
| [PLAT-055-A](pulse_platform/learnings-knowledge/plat-055.md) | Give the reflection turn every store it must route to | Claude Code | `runtime_reverify` | step post-completion turns, learnings/KB contribution contract |
| [PLAT-056](pulse_platform/evaluation/plat-056.md) | Stop the repair-loop recorder from filing durable concerns for same-attempt superseded iterations | Codex | `implemented; existing recorder removal verified locally` | prevalidation / `__automatic_final_validation__` repair loop |
| [PLAT-057](pulse_platform/pulse-governance/plat-057.md) | A harness_issue must not be parked in the workflow's own engineering queue | Claude Code | `runtime_reverify` | finding disposition/status coherence |
| [PLAT-058](pulse_platform/learnings-knowledge/plat-058.md) | Keep learnings one topic-organised workflow skill, not per-step files | Claude Code | `runtime_reverify` | step reflection turn learnings target |
| [PLAT-059](pulse_platform/learnings-knowledge/plat-059.md) | A learnings lock must state why | Claude Code | `superseded by PLAT-263` | legacy contract; access-only migration replaces the lock |
| [PLAT-060](pulse_platform/step-execution/plat-060.md) | Ops-owned config decisions must carry their reason into step_config.json | Claude Code | `runtime_reverify` (deferred: llm_ops_review disabled) | update_step_config tier/model/mode path |
| [PLAT-061](pulse_platform/step-execution/plat-061.md) | step_config field audit: dead field, orphans, phantom clears, incomplete merge | Claude Code | `implemented` | AgentConfigs surface |
| [PLAT-062](pulse_platform/step-execution/plat-062.md) | Scripted prompt named a write target the folder guard forbids | Claude Code | `runtime_reverify` | scripted execution prompt |
| [PLAT-063](pulse_platform/frontend-chat/plat-063.md) | Report pane flashed mid-run: a view flip or outer React render reloaded it | Claude Code + Codex | `done` (two independent frontend paths fixed; live reverified) | workflow canvas mounting and report iframe |
| [PLAT-064](pulse_platform/frontend-chat/plat-064.md) | An entire workflow_* event family is dead; one completion check had no live fallback | Claude Code | `implemented` | frontend event-type consumers |
| [PLAT-065](pulse_platform/pulse-governance/plat-065.md) | Gate recorded a due module and nothing ever resolved it | unassigned | `open` (detection shipped, proximate cause isolated) | scheduler Pulse orchestration (`abortIfTurnStillBusy`, `runPostRunMonitor`) |
| [PLAT-066](pulse_platform/step-execution/plat-066.md) | route_selections correctly supplied, never seeded; router defaulted to the live-action route | unassigned | `open` (interim mitigation shipped) | step-based workflow orchestrator (`seedRouteSelectionsForRun`) |
| [PLAT-067](pulse_platform/scheduler-runs/plat-067.md) | Do not dispatch a scheduled continuation to a missing/unready parent coding-agent transport | unassigned | `implemented` (root cause of the transport loss still open) | scheduler parent-session recovery and background completion delivery |
| [PLAT-068](pulse_platform/learnings-knowledge/plat-068.md) | The step-type checklist names an automated owner that never loads it | unassigned | `implemented` (verification blocked: `llm_ops_review` disabled at the Gate) | Pulse guidance (`review/ops-review.md`, `builder/design-plan.md`) |
| [PLAT-069](pulse_platform/cost-telemetry/plat-069.md) | Nothing measures whether a workflow gets cheaper/faster/more accurate over time | unassigned | `open` (design written) | Pulse trend measurement + Pulse popup |
| [PLAT-070](pulse_platform/scheduler-runs/plat-070.md) | A failed run-folder listing makes the scheduler blame an old failure on today's run | unassigned | `implemented` (runtime reverify pending) | scheduler run-outcome reconciliation, workspace state loading |
| [PLAT-071](pulse_platform/scheduler-runs/plat-071.md) | An idle-wait timeout is treated as proof the workflow never ran | unassigned | `implemented` (record corruption); session stall still open | scheduler workshop turn loop |
| [PLAT-072](pulse_platform/pulse-governance/plat-072.md) | `external_action_required` has no exit path, so solved problems keep being re-reported as open | unassigned | `implemented` (sweep tool + version stamping; board 81→75) | Pulse finding lifecycle |
| [PLAT-073](pulse_platform/pulse-governance/plat-073-remaining-board.md) | Working list of the remaining `external_action_required` board that PLAT-072's triage sweep produced — a durable tracking document, not a single defect, kept so the remaining items stay assignable without re-deriving them from `pulse_close_stale.py --list` | unassigned | `open` (tracking document for the post-triage backlog) | `pulse_close_stale.py` + the external_action_required backlog |
| [PLAT-074](pulse_platform/plans-contracts/plat-074.md) | 6 of 16 plan-mutation call sites never fed the changelog writer real diff/snapshot data, collapsing before_ref/after_ref to a meaningless placeholder | unassigned | `implemented` (4 of 6 call sites fixed; runtime reverify pending) | plan changelog writer (`planning_agent.go`) |
| [PLAT-075](pulse_platform/evaluation/plat-075.md) | Auto-evaluation starts before its target execution is finalized | Codex | `runtime_reverify` | batch execution / auto-evaluation boundary |
| [PLAT-076](pulse_platform/learnings-knowledge/plat-076.md) | Learning and scripted metadata record claims instead of runtime facts | Codex | `runtime_reverify` | learning detection and scripted metadata persistence |
| [PLAT-077](pulse_platform/human-decisions/plat-077.md) | Human-input answer/dismiss had no concurrent-writer guard; harness findings could split invisibly across two fingerprints | unassigned | `implemented` (runtime reverify pending) | report-human-input lifecycle, pulse finding identity migration |
| [PLAT-078](pulse_platform/security-sandbox/plat-078.md) | Spilled bridge tool output (large agent_browser snapshots) landed outside every granted read path | unassigned | `implemented` (folder-guard fix only; snapshot size cap still open) | step execution folder guard |
| [PLAT-080](pulse_platform/scheduler-runs/plat-080.md) | An old cron schedule with no durable fire-decision row restarted from “now” and silently lost earlier due occurrences | Codex | `implemented` (runtime reverify pending) | scheduler cron-cursor bootstrap |
| [PLAT-081](pulse_platform/cost-telemetry/plat-081.md) | Workflow-builder chat cost writer merged cumulative usage repeatedly; first replacement fix could erase other chats | Codex | `implemented` (per-chat delta fix tested, 2 findings documented, 1 left design-only) | phase token-usage cost ledger |
| [PLAT-082](pulse_platform/step-execution/plat-082.md) | Failed async child agents were reported completed because the internal sync boundary erased their Go error | Codex | `implemented` (runtime reverify pending; 2 companion findings classified) | todo-task sub-agent execution boundary |
| [PLAT-083](pulse_platform/pulse-governance/plat-083.md) | No-run Pulse Finalizer instructed the agent to record an invalid "dashboard" command, surfaced live once PLAT-073-A made the rejection visible | unassigned | `implemented` (runtime reverify pending) | scheduler Pulse Finalizer prompt |
| [PLAT-084](pulse_platform/pulse-governance/plat-084.md) | Scheduled runs using execute_step directly had no Pulse evidence signal, so Gate/Review+Fix/Fixer/dashboard/publish were silently skipped | unassigned | `implemented` (runtime reverify pending) | scheduler Pulse evidence detection, execute_step registration |
| [PLAT-086](pulse_platform/scheduler-runs/plat-086.md) | Route-backed and direct-sequence schedules had different lifecycle guarantees but agents could not see or record that design choice; follow-up messages could also suppress saved route selections entirely | Codex | `implemented` (v1.0.29: route always dispatches before follow-up; saved-schedule cleanup and live reverify pending) | schedule execution-model + finalizer-ownership contract |
| [PLAT-087](pulse_platform/step-execution/plat-087.md) | Message-sequence children advertise MCP servers and tools that are not actually registered | unassigned | `open` | child-session AgentSpec/tool materialization |
| [PLAT-088](pulse_platform/cost-telemetry/plat-088.md) | Every scheduled workflow and Pulse turn was billed to `chat`, making Pulse-vs-goal cost unmeasurable | unassigned | `implemented` (runtime reverify pending) | cost scope attribution (`handleQuery`, `pkg/costobserver`) |
| [PLAT-089](pulse_platform/scheduler-runs/plat-089.md) | Grouped runs leave previous-run attempt logs in the active evidence folder | unassigned | `open` | grouped-run cleanup and execution-evidence identity |
| [PLAT-090](pulse_platform/cost-telemetry/plat-090.md) | Daily Pulse cost is ledgered, but no durable per-Pulse-run/stage cost and timing record exists, so reviewer/fixer spend cannot be tied to one pass and its outcome | unassigned | `open` (2026-08-16 live distinction verified; per-pass measurement not built) | Pulse run measurement surface + cost ledger read path |
| [PLAT-091](pulse_platform/evaluation/plat-091.md) | Evaluation step children never complete, pinning the session busy so Pulse loses Review+Fix, Finalize, backup and notify | unassigned | `implemented` (runtime reverify pending) | background-agent completion for evaluation step executions |
| [PLAT-092](pulse_platform/human-decisions/plat-092.md) | Answered operator decisions are never applied or consumed; 26 stranded across 6 workflows, oldest 31 days | unassigned | `implemented` (structured application contract shipped; runtime reverify pending) | Pulse Review+Fix decision lifecycle |
| [PLAT-093](pulse_platform/human-decisions/plat-093.md) | Answered decisions were applied after the run, so the run they were meant to change had already happened | unassigned | `implemented` (structured pre-run routing + mandatory manual targeted-Fixer intake shipped; runtime reverify pending) | scheduler pre-run turn sequence |
| [PLAT-094](pulse_platform/pulse-governance/plat-094.md) | A stale busy signal aborted Pulse Finalize on a turn that had already finished, matching the PLAT-071 race at a different call site | unassigned | `implemented` (runtime reverify pending) | Pulse step-boundary busy check |
| [PLAT-095](pulse_platform/scheduler-runs/plat-095.md) | Scheduled messages had no exact lifecycle identity, so session-wide idle/event heuristics could advance early or stall forever. Replaced both waiters with one query-rooted recursive execution tree, linked child-result continuations, shared the runtime projection with Global Monitor, and removed Go-inferred Pulse final-command failures | Codex | `implemented` (live reverify pending) | query dispatch + scheduler + runtime activity lifecycle |
| [PLAT-096](pulse_platform/plans-contracts/plat-096.md) | Contract upgrades had two delivery paths (Pulse still carried them four weeks after the pre-run preflight replaced it, bundling four unverified rungs), an unfenced stamp a closed turn could still write, and no way for an unattended turn to finish or escalate | Claude Code | `implemented` (runtime reverify pending: confida-login has not yet climbed 1.0.20 → 1.0.25) | contract-upgrade preflight, stamp authorization, Pulse dispatch |
| [PLAT-097](pulse_platform/scheduler-runs/plat-097.md) | `update_schedule(messages=[])` and `messages=null` both silently no-op instead of clearing, blocking any messages-based schedule from migrating to the route-backed model | unassigned | `implemented` (runtime reverify pending) | update_schedule argument parsing, SchedulerCallbacks.UpdateSchedule |
| [PLAT-098](pulse_platform/plans-contracts/plat-098.md) | Contract upgrades were invisible to a workflow owner (no tool, no API, no UI — only a version and a counter in run history) and unrunnable by hand; PLAT-096's stamp fence then removed the one informal manual route. Adds get_contract_upgrades, scopes the fence to scheduler sessions, and guards an operator stamp against skipping the ladder | Claude Code | `done` | workshop tool surface, stamp authorization, operator visibility |
| [PLAT-099](pulse_platform/coding-agent-bridge/plat-099.md) | Updating a workflow from one coding-agent provider to another could leave continuation metadata on the old provider, causing both live-input paths to reject messages despite a healthy new-provider tmux | Codex | `implemented_pending_live_reverify` — 2026-09-18 Confida provider/account recurrence and cross-route receipt correction; live retry acceptance pending | retained coding-agent routing + account binding + canonical submission project |
| [PLAT-100](pulse_platform/coding-agent-bridge/plat-100.md) | Workshop executions and live-steered retry continuations could detach from the initiating query, allowing a schedule to advance while a full-workflow orchestrator tree was still running | Codex | `implemented` (live reverify pending) | workshop execution launch + live completion continuation + exact conversation-turn lifecycle |
| [PLAT-101](pulse_platform/coding-agent-bridge/plat-101.md) | Claude capacity exhaustion can strand a workflow as running; preserve structured reset timestamps, try fallbacks, then durably pause and resume the exact unfinished run | unassigned | `open` (design agreed; implementation pending) | Claude adapter + mcpagent typed errors + workflow continuation scheduler |
| [PLAT-102](pulse_platform/coding-agent-bridge/plat-102.md) | Warm retained messages are fast, but cold coding-agent startup and response observation mixed unrelated waits into perceived send latency | Codex | `partially implemented` (audited 2026-08-14: startup timing, snapshot and trailing-wait fixes landed; Pi latency instrumentation, total request latency, and the latency E2E outstanding) | retained input delivery + Codex interactive startup timing |
| [PLAT-103](pulse_platform/coding-agent-bridge/plat-103.md) | Retained turns completed without a structured final response; adjacent user/assistant event races could render duplicate chat messages | Codex | `implemented` (runtime reverify pending) | retained-turn output contract + formatted transcript reconciliation |
| [PLAT-104](pulse_platform/frontend-chat/plat-104.md) | HTTP acknowledgement and SSE durable events act as competing frontend message producers instead of reconciling one client-generated identity | unassigned | `open` (design recorded; implementation deferred) | frontend chat transport + event-store reconciliation |
| [PLAT-105](pulse_platform/coding-agent-bridge/plat-105.md) | Retained delivery works through a durable mcpagent Session, but the 2026-08-15 Social Media run proved the per-turn lifecycle is still broken: Codex captured the final response and exited while AgentWorks remained busy and Formatted mode never received the answer. Every accepted turn needs one stable `turn_id`, exactly one canonical completion, host settlement from that event, and a P0 proving the final answer is visible, busy clears, and the same Session remains reusable. | Codex | `p0_blocking` (live regression reproduced; completion producer/bridge boundary must be traced and fixed) | mcpagent ↔ AgentWorks per-turn completion ownership |
| [PLAT-106](pulse_platform/frontend-chat/plat-106.md) | Concurrent Chat and Schedule sessions can leak answers or auto-notifications across conversation boundaries; only the execution's initiating chat/session should receive completion | Codex | `runtime_reverify` — 2026-09-17 follow-up implemented/tested locally, not deployed: delayed queue delivery bound to original tab/session; backend registration and known-parent ownership checks; 54 frontend tests, targeted backend tests and TypeScript checks pass. Exact live incident mechanism and concurrent Schedule/trigger + Chat verification remain pending; preserves prior retained-answer repair history | retained-answer binding, session-event isolation and auto-notification routing |
| [PLAT-107](pulse_platform/frontend-chat/plat-107.md) | A sequential scheduled main-agent turn can be projected as its own child, auto-selected, and shown as a blank terminal placeholder | unassigned | `partially implemented` (2026-08-15: self-parent turns now collapsed out of the rail entirely; runtime verification pending) | execution-tree terminal projection + selection |
| [PLAT-108](pulse_platform/coding-agent-bridge/plat-108.md) | Coding-agent transcripts are located by working directory + recency instead of conversation identity; the same defect was fixed independently in Cursor and Codex and is still reachable in Codex completion detection and structured streaming | unassigned | `partially implemented` (2026-08-15: Codex interactive transport fully bound — retained, wrapped, completion detection, streaming; structured transport and the contract capability + certification pending) | coding-agent transcript identity contract + certification |
| [PLAT-113](pulse_platform/coding-agent-bridge/plat-113.md) | Session turn occupancy was decided by sessionBusy, a display flag never set for workflow turns, so every background-agent completion during a scheduled run skipped its queue and blocked on the input lane — 25 never-started synthetic turns piled behind one 5-hour turn until the idle-wait watchdog killed a healthy run | unassigned | `partially implemented; unverified at runtime` (2026-08-16: lane authoritative + register-after-acquire landed; sessionBusy demotion pending. Backend down since 10:08 IST, so neither fix has executed once — next check is the 15:00 IST slot) | session turn occupancy + auto-notification queueing |
| [PLAT-109](pulse_platform/frontend-chat/plat-109.md) | Switching workflows can falsely show no chats, retain the previous workflow selection, or focus a runtime tab until refresh even though the destination workflow has durable Chat history | unassigned | `open` (live recurrence 2026-08-15; trace workflow-switch hydration and selection ordering before fixing) | frontend workflow switch + chat-index hydration + canonical Chat selection |
| [PLAT-110](pulse_platform/security-sandbox/plat-110.md) | Codex verified/isolated launch can retain an undeclared allowlisted credential and expose declared secret values in process arguments | unassigned | `open_deferred` (not reachable in current compatibility mode; release gate before strict-mode rollout) | `multi-llm-provider-go/internal/clisandbox` + AgentWorks strict-mode E2E |
| [PLAT-111](pulse_platform/frontend-chat/plat-111.md) | Cost Analysis blocks first paint on an unbounded all-history ledger/file scan and then serially loads logs for every historical run | platform | `implemented_pending_live_reverify` (bounded 30-day summary + exact SQL headlines + cursor pagination; initial log fan-out removed 2026-08-16) | cost API + ledger rollups + `CostsPopup` lazy detail loading |
| [PLAT-112](pulse_platform/frontend-chat/plat-112.md) | Production workflow UI exposes the internal terminal/child-agent debug rail and continues terminal observation work even when nobody is debugging | platform | `implemented_pending_live_reverify` (default-off server/client diagnostic gates; normal Chat/Schedule now use session events without terminal/tree polling, 2026-08-16) | runtime projection + terminal observation APIs + workflow workspace UI |
| [PLAT-114](pulse_platform/coding-agent-bridge/plat-114.md) | Background agents (Pulse's Gate/reviewers/Fixer included) had no durable execution record — only a receipt a module's Fixer turn may never write, and a 200-event UI cache overwritten on session reuse | unassigned | `implemented` (durable log shipped and tested; no query surface yet) | background-agent lifecycle |
| [PLAT-115](pulse_platform/pulse-governance/plat-115.md) | Gate/Review+Fix/Finalize always ran inside the same session as the run they reviewed. Shipped periodic mode as opt-in (frequency-gated), then that same-day migration revealed the frequency gate used the wrong signal — PLAT-113's original incident happened on an infrequent workflow too. Policy changed: periodic mode is mandatory, with agent-selected fast Pulse requests durably coalesced and dispatched through the separate Pulse-only schedule when material run evidence warrants earlier review | unassigned | `implemented` (dedicated lifecycle, mandatory policy, and agentic fast-request handoff shipped; ordinary runs never review inline or rewrite cron) | scheduler Pulse orchestration, workflow manifest, schedule tools, pulse-gate.md |
| [PLAT-116](pulse_platform/coding-agent-bridge/plat-116.md) | A completed coding-CLI turn can leave the canonical formatted stream open even though the provider-native final answer is already durable and visible. The 2026-08-20 Social Media recurrence pinned the Codex instance to a cross-session lock inversion during rollout resolution and exposed the missing provider-neutral per-turn lifecycle contract. | unassigned | `implemented_pending_restarted_ui_reverify` — Codex lock inversion fixed; `mcpagent` now owns stable turn IDs and exactly-once unified completion; live retained-session P0 passed for Codex, Claude Code, Cursor, and Pi; live concurrent Codex and race tests passed. Remaining proof is one restarted AgentWorks Social Media run. | provider adapters + mcpagent canonical turn lifecycle + AgentWorks completion consumer + scheduler safety net |
| [PLAT-117](pulse_platform/coding-agent-bridge/plat-117.md) | Per-step workflow progress records are registered as background agents purely so the UI keeps polling, but the same registry is the authority for `RunningChildren` — so one dropped progress end-event made `terminal()` permanently unreachable and guaranteed an idle-wait timeout. This is the mechanism behind social-media's false "workflow did not start" emails: a post had landed and been verified, yet two orphaned step-0 mirrors held the turn open, burned the 3-hour live-child grace, and the operator was told nothing ran | unassigned | `implemented` (progress mirrors excluded from turn liveness but still counted for progress/display; orphan settling completed; PLAT-071 regression restored — fail-before/pass-after verified) | background-agent registry + conversation turn lifecycle + workflow progress bridge |
| [PLAT-118](pulse_platform/security-sandbox/plat-118.md) | Codex | `implemented; core runtime verified` (Landlock rootless backend + capability-based selection + fail-closed `SANDBOX_UNAVAILABLE`; live-verified on the hardened EC2 host as uid 999. Code claims independently re-reviewed 2026-08-17 — all hold. Two items deliberately open: nested `BlockedWritePaths` precedence cannot be expressed in additive Landlock rules and fails closed instead, and a fal.ai auth-only probe was not run) | `new` (code claims independently verified 2026-08-17 — all hold; review added to the ticket noting the stderr/reporting question, the pre-existing unguarded exec path, and Landlock ABI/contract caveats) (live root cause reproduced 2026-08-17; rootless Landlock backend, capability detection, truthful preflight, and runtime reverify required) | shared workspace shell sandbox + Linux deployment health |
| [PLAT-119](pulse_platform/pulse-governance/plat-119.md) | Every Pulse step opens with "load builder-reference and follow it exactly", but the Pulse session is built from the workflow's `selected_skills` and never gets that platform skill — so on a workflow that lacks it, Gate/Review+Fix/Finalize improvise. Nothing checks, nothing warns, and the pass is recorded as normal; the only reason this was found is that one Gate run volunteered it | `implemented` (root cause was narrower than first written: the reference surface was attached INSIDE `if workshopSession != nil`, and workshop creation is skipped for an already-stopped session — exactly the state a Pulse finalizer runs in after the run's transport dies. Moved outside the guard; fail-before/pass-after regression test pins the structure) | `new` (root cause confirmed in code: `selected_skills: sctx.Capabilities.SelectedSkills`; salesoutreach had `["agent-browser"]` only, exactly matching the agent's report) | scheduler Pulse orchestration + platform skill attachment |
| [PLAT-120](pulse_platform/integrations/plat-120.md) | Video Studio voice dictation (sherpa-onnx-go + Nemotron streaming STT, capability-gated via `agentprofiles.RuntimeCapabilities.Voice`, SparkQuill-parity UX) shipped and passed two independent non-mic proofs (WAV file, synthetic tone), but no real-speech end-to-end pass has succeeded on the one machine it's been tested on | unassigned | `implemented_pending_live_reverify` (blocked on PLAT-122) | `pkg/voicestt`, `cmd/server/voice_stt_routes.go`, `frontend/src/voice/*` |
| [PLAT-121](pulse_platform/integrations/plat-121.md) | SparkQuill's release process was already split into its own CI workflow with its own tag namespace and path-scoped triggers, contrary to how it was carried as open on the task list; 5 most recent `main` CI runs are green | unassigned | `implemented` (CI-verified; no further work identified) | `.github/workflows/sparkquill-desktop.yml` |
| [PLAT-122](pulse_platform/integrations/plat-122.md) | A dev machine's real microphone reads exact digital silence (`rms=0.0000`) through every app, not just this one — full elimination chain ruled out the STT pipeline, wrong device, browser-automation fake device, SparkQuill holding the device, and third-party audio HAL drivers | unassigned | `open` (environment-level, not code-owned; blocks PLAT-120's live reverify) | none identified yet — OS/driver layer; see `voice_dictation_mic_captures_silence.md` |
| [PLAT-123](pulse_platform/step-execution/plat-123.md) | `record_run_concern`'s trusted session identity was wired up correctly, but three of four places that build a workflow step's tool list never added the tool itself to `SelectedTools`/`enabledTools` — so it silently no-oped for any step outside the one path (a custom `EnabledCustomTools` allowlist) that already force-included it. Confirmed live: confida-login's survey-app-and-refresh-knowledge step named the tool in its own reflection-turn prompt and got `tools_unavailable: unknown=[record_run_concern]` | unassigned | `implemented` (fixed all three call sites to match the one already-correct one; two new regression tests pin the default-branch gap; live reverify on a freshly-started run still pending) | `controller_agent_factory.go` (`applyStepConfigToAgentConfig`, `createTodoTaskOrchestratorAgent`, `prepareCustomTools`) |
| [PLAT-124](pulse_platform/browser-automation/plat-124.md) | An oversized agent_browser snapshot (>24000 runes) returned an error and DISCARDED the tree, so the agent had to pick a narrower --selector for a page it was never allowed to see; separately, the bridge's spill target (tool_output_folder) was granted by setupExecutionFolderGuard but not by the two parallel guard builders, so a step handed "full output saved to <path>" was forbidden to open it. Confirmed live on confida-login: 4 blind retries at ~30.4k runes, plus an "outside every workspace root" dead end | unassigned | `implemented` (truncated head + explicit incompleteness banner, new opt-in `--full-snapshot`, tool_output_folder granted in all three builders and pinned by a parity test; live reverify pending) | `pkg/browser/executor.go` + message_sequence/KB-update folder guards |
| [PLAT-125](pulse_platform/step-execution/plat-125.md) | Every workflow step agent — orchestrator, message_sequence, routing, regular, sub_agent alike — is handed the workshop chat's 41-doc builder reference bundle, so a step holding 8 tools is instructed to call provider-configuration tools it does not have; failing that, it invented provider names, producing 19 further `search_web_llm` errors | unassigned | `implemented` — capability-derived selection shipped with regression tests; live reverify pending | `pkg/orchestrator/agents/workflow/step_based_workflow/supplementary_prompts.go`, `cmd/server/guidance` |
| [PLAT-126](pulse_platform/step-execution/plat-126.md) | An unquoted JSON path to `json_extract` (`json_extract(col, $.field)` instead of `json_extract(col, '$.field')`) fails with "unrecognized token: \"$\"", which names the character SQLite choked on but never the fix — 22 identical failures on one workflow in one day, none self-corrected | unassigned | `implemented` — hint shipped with fail-before/pass-after tests against the real production path; live reverify pending | `cmd/server/virtual-tools/workflow_db_tools.go` |
| [PLAT-127](pulse_platform/coding-agent-bridge/plat-127.md) | The tool-error suspect scan flagged two documented successful outcomes as failures — agent_browser's `{"waited":"timeout"}` wait outcome (142 of 524 suspects on one workflow in one day) and get_route_description's route-catalog prose (12 of 12) | unassigned | `implemented` in [manishiitg/mcpagent@2200bad](https://github.com/manishiitg/mcpagent/commit/2200bad) — fail-before/pass-after tests; live reverify pending | `mcpagent/toolerr` (vendored via go.mod replace, no agent_go change) |
| [PLAT-128](pulse_platform/learnings-knowledge/plat-128.md) | Guidance tests asserted content for review-improve-log and review-code, both deliberately deleted 8 days before ("simplify Pulse workflow reviews") — plus two unrelated test/production drift bugs (a missing DBGuidance template key, an impossible "must never mention the tool it explicitly forbids" check); 5 of a 24-failure baseline fixed | unassigned | `implemented`, 19 pre-existing failures remain (wording drift, out of scope) | `cmd/server/guidance/render_all_test.go`, `pkg/orchestrator/agents/workflow/step_based_workflow` prompt tests |
| [PLAT-129](pulse_platform/learnings-knowledge/plat-129.md) | 13 more of PLAT-128's 19 remaining failures traced to specific renames/removals (call_generic_agent -> run_in_background, READ-ONLY REVIEW -> READ-ONLY STRATEGY AUDIT, finding_id -> "no invented identifier" for 3 specialists, 5 improve-* docs merged into shared Engineering Review lenses, goal-advisor's 538-line rewrite, builder/improve.html dashboard markup gone everywhere) | unassigned | `implemented`, 6 remain (2 trace to PLAT-080's scheduler mechanism, 2 to content that git history shows was never written, 1 to an unfamiliar persistence-model change, 1 untraced) | `cmd/server/guidance/render_all_test.go` |
| [PLAT-130](pulse_platform/scheduler-runs/plat-130.md) | Stopping a schedule marks it stopped but does not stop the work: cancelTrackedExecutionsForSession/cancelBackgroundAgents only flip an in-memory status flag read by watchers -- the goroutine actually driving a message_sequence item loop has no cancel-aware context and keeps executing queued items (including side-effecting ones) after the schedule is shown as "stopped" | unassigned | `implemented` — 2026-08-20: first live reverify reproduced it via a third mechanism (handleQuery's auto-resume/materialize-guard relaunching a killed coding-agent tmux after Stop); gated on isSessionMarkedStopped. Second live reverify same day is clean: no relaunch, in-flight turn cancelled honestly | `cmd/server/workflow_execution_tracker.go`, `cmd/server/session_lifecycle.go`, `cmd/server/server.go` |
| [PLAT-131](pulse_platform/frontend-chat/plat-131.md) | Session enrichment overwrote preset_query_id/preset_name/workspace_path unconditionally from a tracked execution, so a running scheduled session whose only live execution was a workshop_background one (which carries no identity) had all three erased -- the frontend resolves a workflow by exactly those fields, so clicking the rtslatency activity pill opened a Schedule tab under whichever workflow was already on screen instead of switching | unassigned | `implemented` -- fail-before/pass-after against the exact live payload; live reverify pending a restart | `cmd/server/polling.go` |
| [PLAT-132](pulse_platform/integrations/plat-132.md) | A permanently-broken MCP server is re-attempted on every restart: startup tool-cache discovery walks every configured server (correctly — it feeds the `GET /api/tools` catalogue, which must list what is *available*, not what is used), but `discoveryFailedServers` only remembers **auth** failures. `MiniMax` dies on import (`ModuleNotFoundError: mcp.server.fastmcp`, no version pin — `google-sheets` next to it pins `mcp>=1.8,<2` and works), surfaces as `transport closed`, is never classified permanent, and burns ~30s + 4 error-level lines every restart for a server **zero** of 19 workflows select | unassigned | `open` (diagnosed, deliberately not fixed — needs a call on what counts as deterministic vs transient, and whether the skip should expire) | `agent_go/cmd/server/tools.go` (`initializeToolCache`, `runBackgroundDiscovery`, `discoveryFailedServers`) |
| [PLAT-133](pulse_platform/evaluation/plat-133.md) | Claimed the P0 coding-agent contract enforces that a certification has a registered proof but never that the proof tests what the certification claims, citing the Pi hang as the example that slipped through | unassigned | `closed / not a defect` (2026-08-18: premise disproven on both halves. (1) There was no Pi hang — the `agent_settled`-never-fires claim was a logging artifact, refuted by live runs. (2) The coverage said to be missing is present: no provider is registered `Transport: structured` (it means *primary* transport), so all four get 16-17 required P0 certs incl. `structured_multi_turn`/`structured_streaming`. And a two-turn resume proof *does* test termination by construction — turn one must complete and exit or there is nothing to resume. All four P0 enforcement tests pass) | `multi-llm-provider-go` certification framework (`coding_agent_certification.go`, `coding_agent_contract.go`) |
| [PLAT-134](pulse_platform/security-sandbox/plat-134.md) | Ordinary and product chat were assembled as a generic multi-agent orchestrator, constructing delegation, schedule and tier-selection machinery even when product profiles explicitly forbade those capabilities; the live path is now direct prompt + skills + tools + conversation, with compatibility naming/dead-code extraction still open | Codex | `partially_implemented` (runtime simplified and automated checks pass; live reverify plus compatibility cleanup pending) | direct/product chat request and runtime construction + chat UI model/config projection |
| [PLAT-135](pulse_platform/security-sandbox/plat-135.md) | `TestDelegationUsesTheReadOnlyProfileLookup` asserted that a sub-agent resolves its parent profile through the read-only lookup, so it could never receive a wider tool surface than the product declared; PLAT-134 removed chat delegation and the assertion lost its subject, leaving the containment property unasserted on the workflow sub-agent path that still spawns them | unassigned | `open` | `cmd/server/agent_profile_runtime.go`, workflow sub-agent spawn path |
| [PLAT-136](pulse_platform/cost-telemetry/plat-136.md) | Every run executes in runs/iteration-0 and records it, but iteration-0 is only the live slot and the next run rotates it to a permanent iteration-N; run history was never repointed (24 of 25 hetznerssh entries said iteration-0 while disk held iteration-21..25), so the schedule popup — which looks cost up per folder — showed the CURRENT run's spend identically on every historical row | unassigned | `implemented` — rotation repoints run history; live reverify pending | `controller_run_manager.go` (rotatePairedIterationZero) |
| [PLAT-140](pulse_platform/frontend-chat/plat-140.md) | A server restart cleared the in-memory state the app's chat record is built from; the resume rebuilt a partial history and persistence wrote it straight over the full one — salesoutreach went from 242 user turns (still intact in Claude Code's own transcript) to 2, with one copy on disk and no backup | unassigned | `implemented` — overwrite guard shipped; flattened records not recoverable | `chat_history_persistence.go`, `server.go` conversation write sites |
| [PLAT-141](pulse_platform/coding-agent-bridge/plat-141.md) | NO interactive (tmux) adapter emits a tool-call end on ANY provider — inherent, since a terminal pane carries no structured tool events — so a call that Claude Code's transcript shows completing in 41ms never produced a tool_call_end store event, so the chat showed a finished command as unresolved and the compensating settle displayed a fabricated 45.4s duration; tools dispatch under sub-session ids while open calls are tracked under the parent schedule session, but the pairing hypothesis is not yet proven | unassigned | `partially implemented` — Claude Code interactive recovery shipped, codexcli/cursorcli/picli interactive still share that gap. Separately (2026-08-20): a THIRD, provider/transport-agnostic mechanism found and fixed — `logToolCallTelemetry` had no case for `ToolCallErrorEvent`, so a tool call that failed with a real error (not silence) stayed "open" forever and was swept into the same synthetic settle as genuinely unreported calls, on a structured (non-tmux) pi turn. Fail-before/pass-after verified against the exact production log line | interactive adapters (all providers), `internal/events/event_store.go` |
| [PLAT-142](pulse_platform/pulse-governance/plat-142.md) | Pulse's own review evidence has the same orphaned-tool-call gap PLAT-141 found in the chat UI — 15,601 tool calls across 745 execution logs, 2,563 (16.4%) with no recorded result, up to 88% on one workflow — because the two are independent consumers of the same incomplete event stream, not a pipeline; recovery shipped for regular/message_sequence steps on Claude Code, todo-task steps and the other three providers left as named follow-up | unassigned | `partially implemented` | `context_aware_bridge.go`, `controller_execution.go` |
| [PLAT-137](pulse_platform/pulse-governance/plat-137.md) | Strategy Auditor and Goal Advisor duplicated one lifecycle; the manual Strategy slash wrapper also retained the old nested-reviewer path and could complete without the canonical `strategic_review` receipt | Codex | `implemented` — merged strategic sequence, direct standalone execution, exact-session receipt enforcement, and interference-domain experiment control shipped; live post-restart/compaction verification pending | Pulse module registry/Gate, strategic Review+Fix and standalone dispatch/guidance, typed persistence, experiment lifecycle, Pulse UI projection |
| [PLAT-138](pulse_platform/pulse-governance/plat-138.md) | Engineering Review buried verification, discovery, Stores/Operations evidence, fixing and persistence inside one oversized contract; the first live backlog-drain then proved its “complete backlog” rule was also unbounded, assigning 198 roots to one child | Codex | `implemented` — compaction-safe sequence plus one agent-chosen coherent repair objective per pass; live post-restart verification pending | Pulse Review+Fix message-sequence dispatch/guidance, bounded progress, run-scoped checkpoints and typed persistence |
| [PLAT-139](pulse_platform/coding-agent-bridge/plat-139.md) | An `ICICI-BANK-PARSING-v2` workflow step finished its real work at 09:45 but held its caller ~65 minutes, never reporting completion | unassigned | `fixed` (2026-08-19: the original diagnosis in this ticket was WRONG and is corrected inline. It concluded "pi's process is gone, so cmd.Wait() is stuck on an internally-owned stderr pipe" — but the `ps` query used could not match pi, which runs as `COMM=pi`, not as a node process. Listing the server's real children during a live recurrence showed pi ALIVE (27m and 65m, elapsed matching the stall warnings, zero zombies), so cmd.Wait() was blocked correctly. Real cause: pi finishes its work and fails to EXIT — its MCP child keeps Node's event loop alive while waiting on a stdin pi never closes — and the `agent_settled` teardown that breaks this had been deleted from the pi adapter. Restored in `@3d9bcc6`; stall log now records pid + whether the terminal event was seen (`@000b917`) so the two cases cannot be confused again. The stderr-pipe fix `@6d8e4e9` is a real separate defect and is retained) | `multi-llm-provider-go` picli structured adapter |
| [PLAT-148](pulse_platform/pulse-governance/plat-148.md) | PLAT-116 orphan cleanup closed the Build-in-Public Pulse conversation after Gate timed out, then the scheduler immediately sent Review+Fix into that closed session, stranding the remaining sequence and later schedules | unassigned | `implemented` — orphan cleanup is now turn-scoped and preserves the conversation | per-turn lifecycle + scheduled conversation continuation |
| [PLAT-143](pulse_platform/frontend-chat/plat-143.md) | Workflow restoration is represented by one global frontend boolean, so hydration of another session can cover LinkedIn with a repeatedly re-armed `Restoring previous session…` screen while LinkedIn's own APIs are healthy | unassigned | `implemented` — active restore state is keyed by session | keyed frontend restore lifecycle |
| [PLAT-144](pulse_platform/scheduler-runs/plat-144.md) | Logically dependent schedules can only approximate ordering with cron offsets; Tectonic Daily Pulse fired 15 minutes after close, while close normally required 22+ minutes, and collided | Codex | `implemented and regression-tested` — same-day all-of dependency fan-in, terminal policy, delay/deadline, maximum runtime, authoring validation and restart-safe release; deployment pending | schedule dependencies + runtime bounds |
| [PLAT-145](pulse_platform/scheduler-runs/plat-145.md) | A busy workflow lease terminally discards every occurrence as `skipped_busy`; three Tectonic occurrences were durably observed and then permanently lost because no queue/coalesce/retry policy exists | unassigned | `implemented` — durable skip/queue-latest/retry/coalesce with atomic lease claim and visible waiting state shipped | durable scheduler collision policy |
| [PLAT-146](pulse_platform/scheduler-runs/plat-146.md) | Manual launch preserves schedule prose but no typed safety mode; the Tectonic “market close” schedule launched before cutoff could reach ordinary entry logic because close-only existed only as a time assumption | unassigned | `implemented` — runtime mode reaches and gates the script boundary | typed schedule inputs + side-effect enforcement |
| [PLAT-147](pulse_platform/pulse-governance/plat-147.md) | The managed backup contract allowed a generated Git bundle to be tracked inside its own source repository; Tectonic reached a ~67 GB bundle and ~133 GB `.git` through recursive self-inclusion | unassigned | `contained` — canonical path guard, future-agent Git contract, bundle deletion, verified history rebuild, and external recovery bundle shipped; managed archive lifecycle/health remains separate work | backup destination validation + health |
| [PLAT-149](pulse_platform/coding-agent-bridge/plat-149.md) | Two independent mechanisms report the same bridge tool call under different identities — a toolcalllog-backed HTTP hook (reliable, provider-agnostic) and a second, unlocated mechanism carrying the model's own id that drops ~10% of results — both flowing to the same consumers unreconciled | unassigned | `implemented` — shared recovery shipped for chat UI + Pulse evidence; unreliable mechanism's construction site still unlocated | `pkg/toolcallrecovery`, `pkg/agentwrapper/llm_agent.go` |
| [PLAT-150](pulse_platform/coding-agent-bridge/plat-150.md) | live-attach started its tmux control client with `pty.StartWithSize` (an `exec.CommandContext`) and cleaned up with only `ptmx.Close()` + `cmd.Process.Kill()`. Kill terminates but does not REAP, so every attach left a `<defunct>` child of the server plus an os/exec `watchCtx` goroutine blocked forever on a send only `Wait` drains — one leaked PID and one leaked goroutine per attach, until restart | unassigned | `fixed` (2026-08-19: confirmed from a live production pprof dump — five zombies against exactly five `watchCtx` goroutines parked on `chan send`, four of whose Start-callers had already exited. Fixed with a deferred reap covering both the ctx-cancel and self-exit paths. Hard to find because a zombie is anonymous, the Start-vs-Wait heuristic reports clean, the cleanup code looks correct, and a simultaneous unrelated 64-minute `syscall.Wait4` hang masked it) | `agent_go/cmd/server/terminal_live_attach.go` |
| [PLAT-152](pulse_platform/coding-agent-bridge/plat-152.md) | Claimed Pi CLI's interactive adapter emits native tool-call chunks while Claude Code's and Codex CLI's do not, making the coding-agent adapters non-standardized | unassigned | `closed / not a defect` (2026-08-19: the asymmetry does not exist. Counting emission sites per PACKAGE rather than per file, all four providers emit `StreamChunkTypeToolCall*` — Claude at `claudecode_transcript_stream.go:211,220`, Codex at `codexcli_transcript_stream.go:142,152`, Cursor at `cursorcli_transcript_stream.go:258,261` — and all are live-tailed on a poll ticker, not end-of-turn. The original analysis grepped only `*_interactive_adapter.go` and inferred provider-level absence from file-level absence; Pi differs in file layout only, because its JS harness gives it a marker side-channel. Cursor, unexamined originally, has the identical structure. Acting on the finding would have duplicated every tool-call chunk) | `multi-llm-provider-go/pkg/adapters/*` |
| [PLAT-153](pulse_platform/coding-agent-bridge/plat-153.md) | A structured pi turn had no overall ceiling — TOOL_EXECUTION_TIMEOUT only wraps individual tool calls, and nothing bounded the whole pi process's lifetime. Confirmed live: a form-26as pi sat with a completely idle Node event loop (native `sample`, not a goroutine dump — main thread parked in kevent, no thread in any syscall) for over an hour with zero progress. Matches pi's own upstream issue (earendil-works/pi#8004: real 5.5h/8.7h freezes, "no general tool-call timeout") — not our bug, but our blast radius | unassigned | `fixed and confirmed live` (2026-08-19: piMaxTurnDuration adds a 45m default ceiling, PI_STRUCTURED_MAX_TURN_DURATION-overridable. A second real bug found proving it: exec.CommandContext's default cancellation only kills the tracked pid, not the process group Setpgid was set up for — a grandchild survived as a reparented orphan three times before the test was strengthened to actually check for it. Fixed with cmd.Cancel doing a proper group kill, matching procshutdown's existing correct pattern. Both fail-before/pass-after verified. Confirmed same-day: fired at exactly 45m0.029s on a real recurrence, group-killed cleanly, workflow recovered within 2 minutes via retry) | `multi-llm-provider-go` picli structured adapter |
| [PLAT-151](pulse_platform/step-execution/plat-151.md) | Todo routes advertised `context_to_pass`, but runtime never delivered it to child sessions; descriptions, real file dependencies, and per-call instructions are now the only context channels | Codex | `implemented` — dead field removed from backend/frontend and mutation schemas; focused tests pass | todo-route plan contract |
| [PLAT-154](pulse_platform/pulse-governance/plat-154.md) | `record_pulse_finding` successfully updated a step-owned issue, then reloaded through the observing reviewer's module filter, could not see the row, and falsely returned an internal-lifecycle error that triggered retries | Codex | `implemented` — unfiltered exact-identity reload and cross-module regression proof pass; live restart/retry verification pending | typed Pulse finding write/reload boundary |
| [PLAT-155](pulse_platform/pulse-governance/plat-155.md) | Pulse flattened 98 raw workflow observations and 2 reviewer-confirmed issues into one “100 open” backlog, then let the reviewer sequence repair its own mixed queue | Codex | `implemented` — explicit observation/issue projection and independent Review→Fix stages; focused tests pass, live convergence verification pending | Pulse evidence projection, backlog/Gate contract, reviewer promotion, and Fixer dispatch |
| [PLAT-156](pulse_platform/pulse-governance/plat-156.md) | `review_plan` injected a 553 KB plan plus a Go-authored step-config interpretation into the reviewer prompt instead of letting the agent inspect authoritative source files selectively | Codex | `implemented` — reviewer now reads soul/plan/step config itself with targeted queries; live reverify pending | `runReviewPlanAgent` + review-plan prompt |
| [PLAT-158](pulse_platform/pulse-governance/plat-158.md) | Legacy `post_run_monitor` remained a second Pulse trigger beside a dedicated review schedule, so ordinary workflow runs still launched Gate/Review+Fix inline and could spend hours doing review work at the wrong cadence | Codex | `implemented` — dedicated `pulse_review_only` schedule is now the sole recurring-Pulse authority; restart/live reverify pending | manifest + scheduler Pulse lifecycle dispatch |
| [PLAT-159](pulse_platform/frontend-chat/plat-159.md) | Scheduler/cron `workflow_phase` sessions set WorkflowLabel/PresetName from the raw workspace folder name instead of the workflow's configured `label`, so the Global Activity Monitor could show a session's own workflow as a different one running elsewhere (folder `social-media`, configured label `twitter-automation`) | unassigned | `implemented` — fail-before/pass-after; live reverify pending | `cmd/server/server.go`, `cmd/server/polling.go` |
| [PLAT-160](pulse_platform/coding-agent-bridge/plat-160.md) | Interactive tool-call completion is reconstructed by polling the CLI's transcript file, whose loop exits on turn-end without a final read and can lose the last event; PLAT-149's proposed reliable alternative (the bridge-execution hook) turns out to have a dead live-delivery path of its own (noop tracer, and unregistered on the workflow-step code path) | unassigned | `partially implemented` — settled-timestamp and shell-settle-presentation symptoms fixed; claudecode/codexcli transcript tailers now do a final read on cancel. A live Claude final-flush regression then proved that a detached producer could send after `WithObservability` closed `StreamChan`, crashing AgentWorks; `ed317ca` now cancels and joins that producer before close, with regression coverage. The larger architectural fix (promote toolcalllog to primary) remains unstarted | `multi-llm-provider-go` interactive transcript tailers, `agent_go/pkg/agentwrapper/llm_agent.go`, `agent_go/internal/events/event_store.go` |
| [PLAT-161](pulse_platform/frontend-chat/plat-161.md) | bd184c1f6's report-frame height fix collapsed the platform iframe to 0px to measure content, which also collapsed any `vh`-sized content inside the report (a report's own nested iframe, in salesoutreach's case) for the duration -- its GTM-strategy tab measured 319px against a real 2052px, with nothing to scroll to reach the rest | unassigned | `implemented` — verified against a real Chromium browser loading the actual affected report | `frontend/src/components/workflow/reportWidgets/HtmlWidgetFrame.tsx` |
| [PLAT-164](pulse_platform/coding-agent-bridge/plat-164.md) | Background agents (`run_in_background`, todo-task delegates, scheduled/Pulse children) had only a durable lifecycle summary (PLAT-114), not a transcript — intermediate assistant messages and tool calls existed only in the capped/reused live UI-event cache, unrecoverable once trimmed or after a restart | unassigned | `implemented` — generic per-background-agent structured transcript, keyed by session+agent below `builder/conversation/background/` (corrected from this ticket's own original `sessions/` proposal, which had no other usage anywhere in this codebase); write-failures are visible via new `background_agent_log.transcript_status`. Scope-fix same day: the initial scoping check reused a pre-existing "workflow-step:" prefix classifier that matches nothing real in this codebase, so the bridge-side appender was fabricating spurious transcripts for plain workflow-step/todo-task step executions (which already have their own conversation+timing trace via `controller_execution.go`/`controller_todo_task.go`); it now only appends when cmd/server already registered a header. Build/test verified, live reverify pending | `cmd/server/background_agent_transcript.go`, `pkg/orchestrator/context_aware_bridge.go`, `pkg/orchestrator/base_orchestrator_background_transcript.go`, `pkg/orchestrator/events/background_transcript.go` |
| [PLAT-166](pulse_platform/cost-telemetry/plat-166.md) | The SQLite cost ledger (`pkg/costledger`, what Cost Analysis' `CostsPopup.tsx` actually reads) has no phase attribution, so a step's execution-turn and reflection-turn spend sum into one indistinguishable per-step row; a different, older token-usage-file system already split this (PLAT-068) but feeds a different consumer entirely | unassigned | `implemented` — `Entry.Phase` + `ExecutionAggregate.ByPhase` in the ledger, `costobserver.Observer.SetPhase` toggled around the reflection turn (a completely separate `AgentEventListener` from the bridge push the code had always assumed sufficed), `CostsPopup.tsx` renders a reflection sub-line when present. Scope-fix same day (caught by review): every observer had defaulted to `PhaseExecutionOnly`, so *every* execution — not just ones with a reflection turn — grew a redundant `by_phase.execution_only` duplicate of its own total in every API response; now defaults to untagged (`""`), which writes no `by_phase` entry at all. Build/test verified, live reverify pending | `pkg/costledger/ledger.go`, `pkg/costledger/sqlite.go`, `pkg/costobserver/observer.go`, `pkg/orchestrator/agents/workflow/step_based_workflow/reflection_turn_run.go`, `frontend/src/components/workflow/CostsPopup.tsx` |
| [PLAT-167](pulse_platform/cost-telemetry/plat-167.md) | PLAT-166's Phase/ByPhase mechanism is generic (any string, aggregated automatically) but only one caller (the reflection turn) ever used it; a `message_sequence` step's individual items (user_message, prevalidation repairs, foreach rows) all funnel through one call site and had no phase tag, so their cost/time stayed merged into one row | unassigned | `implemented` — `executeMessageSequenceUserMessage` tags every item's turn with `"item:<id>"` via the same `SetPhase` mechanism; `CostsPopup.tsx`'s render generalized from a hardcoded single reflection check to iterating every tagged phase. Build/test verified, live reverify pending | `pkg/orchestrator/agents/workflow/step_based_workflow/controller_message_sequence.go`, `frontend/src/utils/costActivityBreakdown.ts`, `frontend/src/components/workflow/CostsPopup.tsx` |
| [PLAT-168](pulse_platform/learnings-knowledge/plat-168.md) | After a successful `query_workflow_db` call, an agent redundantly hand-rebuilt the identical HTTP request via inline Python inside `execute_shell_command`, hit `SyntaxError: unterminated triple-quoted string literal`, and retried 19+ variations of the same fragile shell-smuggled-script pattern for 45 minutes until PLAT-153's turn ceiling caught it — 212 tool calls, never silent, never converging. Also records a design decision reached the same day: a real-time consecutive-failure/loop detector was considered and explicitly rejected (no reliable live signal separates 'about to converge' from 'never will'; the ceiling stays a dumb resource cap, root-causing recurring patterns is Pulse's job, done after the fact with the full transcript, not a live control's) | unassigned | `filed` — root cause identified (redundant re-verification of an already-successful tool call); fix is a guidance addition to `stores.md` stating the tool result is authoritative, not attempted in this pass | `agent_go/cmd/server/guidance/templates/system/stores.md` |
| [PLAT-169](pulse_platform/frontend-chat/plat-169.md) | `ToolSelectionSection.tsx`'s server checkbox compares `selectedServers` by exact string, so a manifest saved under a legacy MCP server spelling (`google_sheets`) renders the current spelling's checkbox (`google-sheets`) unchecked; toggling it appends the new spelling instead of replacing the old, and the backend's own hyphen/underscore duplicate validator then permanently blocks every future save of that workflow until the JSON is hand-edited | unassigned | `implemented` — alias-aware checkbox comparisons plus save-time self-heal (`dedupeServerNames`); build/test verified, live reverify pending | `frontend/src/components/ToolSelectionSection.tsx`, `frontend/src/components/ModePresetBar.tsx`, `frontend/src/utils/mcpServerAlias.ts` |
| [PLAT-170](pulse_platform/step-execution/plat-170.md) | Product presentation tools validated files through canonical `_users/<id>/...` paths, then reused that server-internal path for `ui_presentations`; the user-scoped workspace DB API correctly rejected it, so valid videos, characters, and documents disappeared behind a generic tool-failure envelope | Codex | `implemented and deployed; direct tool retry pending` — shared path conversion, focused tests, production DB-path probe, and healthy rootless release complete | `agent_go/pkg/presentations/presentations.go` |
| [PLAT-171](pulse_platform/coding-agent-bridge/plat-171.md) | A chat session's own dying terminal cancels *any* work registered under its session id, including a `run_full_workflow`-launched group execution's tracked steps that are independently running and share nothing with the dead terminal but that one shared field — `BackgroundAgentRegistry.CancelAll`/`cancelTrackedExecutionsForSession` select by session-id equality with no owner-vs-watcher distinction. Live-observed: an unrelated "Rakesh Yadav" group run's Verification step was marked canceled and its LLM turn context-canceled mid-flight when the launching chat session's tmux pane died for an unconnected reason | unassigned | `open` — root cause fully traced to file:line; fix needs a design decision (separate ownership id vs. an explicit "detached launch" marker) before implementation | `cmd/server/session_lifecycle.go`, `cmd/server/background_agents.go`, `cmd/server/workflow_execution_tracker.go`, `cmd/server/delegation.go` |
| [PLAT-173](pulse_platform/learnings-knowledge/plat-173.md) | The KB half of the merged reflection turn carried none of the anti-append discipline the learnings half states five different ways, so steps appended a fresh dated section every cycle instead of correcting the existing one — and `stores.md` compounded it by promising an automatic "notes compact themselves past 20KB / 30 sections" that has never been implemented anywhere, giving steps a positive reason to keep appending. Live: confida-login's `app-structure.md` grew past every stated threshold, and `page-agreement-workflows.md` accumulated an unreconciled contradiction (a 2026-07-19 "confirmed broken" entry sitting alongside later entries showing the same path working) | unassigned | `fixed` — KB-half guidance + corrected `stores.md`, fail-before/pass-after test; live reverify pending on a fresh survey cycle | `pkg/orchestrator/agents/workflow/step_based_workflow/reflection_turn.go`, `cmd/server/guidance/templates/system/stores.md` |
| [PLAT-174](pulse_platform/step-execution/plat-174.md) | `step_config.json` was read exactly once per run (controller.go's `populateRuntimeFields` sweep at run start), so an `update_step_config` change made after the run started never reached a step that hadn't begun yet, silently, for every field, on every step — no error told the caller it didn't apply. Live: confida-login pinned `execute-browser-and-capture-apis` to pi-cli/gemini-3.7-flash 16 minutes before that step began; it ran three times in that run and used claude-code/claude-sonnet-5 every time, because "before this step started" was the wrong boundary — "before the run started" was the one that mattered, and the run's first step had already begun 15 minutes before the change | unassigned | `fixed` — `executeSingleStep` now refreshes a step's runtime config from disk immediately before dispatch, fail-open on a read error; fail-before/pass-after test against a real workspace-API stand-in; live reverify pending | `pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`, `controller.go` |
| [PLAT-175](pulse_platform/step-execution/plat-175.md) | Commit `a960df20` correctly blocked raw `db.sqlite` filesystem access for message_sequence steps, but dropped the whole `db/` grant to do it — taking `db/assets/` down as collateral, the one durable location a step can write an arbitrary file to (`mutate_workflow_db` is SQL-only). A code comment mislabeled this "PLAT-169 follow-up"; PLAT-169 is a real, unrelated ticket (MCP server checkbox dedup) — corrected in code and here. Live: confida-login's `survey-app-and-refresh-knowledge` step is instructed every cycle to sync `db/assets/business-context/` via shell (compare `.source_sha`, add/remove/overwrite files, rewrite the manifest) and had no legal path to do so — its own reflection turn flagged it, silently, since nothing upstream had changed on the runs it was checked | unassigned | `fixed` — `setupMessageSequenceFolderGuard` now grants `db/assets/` specifically (a sibling of db.sqlite, not a child, so it doesn't reopen the Landlock conflict) to both read and write paths; existing regression test narrowed from a blanket `/db` substring check to specifically `db.sqlite`; new fail-before/pass-after test; live reverify pending | `pkg/orchestrator/agents/workflow/step_based_workflow/controller_message_sequence.go` |
| [PLAT-176](pulse_platform/scheduler-runs/plat-176.md) | Execution evidence is named from `retryAttempt`/`loopIteration`, both counters LOCAL to one dispatch of a step — both restart at 1 on every dispatch — so a re-dispatched step recomputes the identical path and `WriteWorkspaceFile` overwrites the previous dispatch's result, conversation, timing, and prompts in place. Live: confida-login's `execute-browser-and-capture-apis` was dispatched 5 times in one run and every dispatch clobbered the last; the same timing file read minutes apart returned two different executions (2675000ms/21 tool calls, then 104248ms/14 tool calls), and a diagnosis built on the stale read was wrong. A looping run therefore erases the evidence that it looped — defeating the after-the-fact review PLAT-168 makes the platform depend on, since the loop is already invisible to every live control (longest turn 18.5min, all steps COMPLETED, re-dispatches are not retries) | unassigned | `fixed` — prior dispatch's four evidence files are moved into a stamped `superseded/` subfolder before the write; canonical names still hold the newest dispatch so no reader changes behavior, and a subfolder stays off the `execution-attempt-*` globs readers Sscanf; best-effort, never fails a step; fail-before/pass-after tests; live reverify pending | `pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go` |
| [PLAT-177](pulse_platform/coding-agent-bridge/plat-177.md) | A resumed Claude Code native session (workflow "substack") told the user it had lost access to plan-editing tools it had used successfully earlier in the same conversation. Three initial platform explanations were ruled out by direct evidence. The incident's actual error text (`get_api_spec` returning `tools_unavailable: unknown=["delete_schedule"]...`) pointed to a fourth: virtual-tool scope staleness across resume — `latestVirtualScopeBySession` exists specifically to recover "older restored bridge processes [that] still carry a stale session:vt:\<trace\> scope," but its retry trigger (`shouldRetryVirtualToolWithLatestScope`) only matched a legacy error phrasing `get_api_spec`'s current wording no longer produces anywhere — a real, confirmed dead safety net, though not proven to be this exact incident's mechanism (no captured scope-ID evidence) | unassigned | `fixed` — retry trigger now also matches the current error wording, scoped narrowly so real outages/permission denials still don't retry; two new tests (one reproducing the incident's literal error text); build/test verified (one pre-existing unrelated failure confirmed on origin/main); live reverify pending, and the exact incident mechanism remains unconfirmed | `mcpagent/agent/codeexec/registry.go` |
| [PLAT-178](pulse_platform/chat-reliability/plat-178.md) | Retained coding-agent delivery could advance the terminal without advancing durable chat history; later recurrences exposed missing native turn boundaries and multiple conversation producers making independent occupied-session delivery decisions | Codex | `shared durable conversation-turn dispatcher implemented locally; push, deployment, and fresh-client verification pending` — a workspace-backed FIFO now covers regular chat, Crew, Workflow Builder, live input, schedules, triggers/webhooks, and bots, with immediate structured queued receipts and restart recovery | `agent_go/cmd/server/conversation_turn_queue.go`, `agent_go/cmd/server/server.go`, `agent_go/cmd/server/bot_session_starter.go`, frontend receipt rendering, and their regression matrix |
| [PLAT-179](pulse_platform/coding-agent-bridge/plat-179.md) | A retained (tmux-delivered) coding-agent turn is declared complete the instant any non-empty assistant text appears in the transcript — even explicit intermediate commentary a prompt labeled "not your final answer" — with no check for a pending tool call. Affects every tmux-backed provider using retained/live-steer delivery, not one, since each provider's reconstruction bundles text+toolCall into one message the same way. Live: an IC-11 retained turn completed in 2.2s with a progress-update as its final_result, before the tool's own `sleep 2` could finish. Root cause: mcpagent's `finalResponse()` picked the last AI message with any text, never checking whether that same message also carried a pending ToolCall part. A first fix attempt (pane-text matching, pi-cli only) shipped then had to be reverted — the pi CLI build under test never printed "idle" in its status line, so the gate never fired and the turn hung until timeout, worse than the original bug | unassigned | `fixed` — `finalResponse()` now skips any AI message that also carries a ToolCall part, a single shared fix protecting all four providers; paired fix in `picli_transcript.go` (was silently dropping the ToolCall block before it ever reached `finalResponse`); fail-before/pass-after tests in both repos; confirmed live end-to-end for pi-cli; claude-code/cursor-cli not yet live-audited | `mcpagent/agent/retainedturn/retained_turn.go`, `multi-llm-provider-go/pkg/adapters/picli/picli_transcript.go` |
| [PLAT-180](pulse_platform/coding-agent-bridge/plat-180.md) | A retained turn's tool_call_start/tool_call_end events carried NO turn_id at all (not merely the wrong one — a code-reading-only first pass assumed staleness, but the live event's ToolCallID prefix `direct-` versus `toolcalllog`'s `toolu_` proved a different mechanism entirely). Real cause: `observedDirectToolExecutor` (mcpagent), the sole tool-call-event source for a retained delivery, runs with a bare HTTP-request-derived context that never carries the session's `canonicalTurnLifecycle`, so `emitTypedEvent` never stamps turn_id at all. Fixed via `Agent.attachActiveTurnLifecycle`, attaching the session's live lifecycle to that context before emitting. A real-but-insufficient-alone first fix (`Session.ActiveTurnID()` / `LLMAgentWrapper.currentTurnID()`, fixing a genuine but different staleness risk in `toolcalllog`'s hook) was kept alongside it | unassigned | `fixed` — confirmed live end-to-end for all four retained-capable providers (pi-cli, codex-cli, claude-code, cursor-cli via the PLAT-179 test-only tmux bypass, since cleanly reverted) via a real dev-server restart and `--retained-window-p0-only` re-run; downstream consequence of the previously-missing turn_id not assessed | `mcpagent/agent/tool_registry.go`, `mcpagent/agent/turn_session.go`, `agent_go/pkg/agentwrapper/llm_agent.go` |
| [PLAT-182](pulse_platform/scheduler-runs/plat-182.md) | A step's re-read result was selected by scanning every `execution-attempt-N-iteration-M.json` in its log folder and keeping whichever had the numerically highest `(attempt, iteration)` pair — counters local to one dispatch, reset every re-run, with nothing archiving prior dispatches' files. Live: "instagram"'s `step-create-reel` folder held today's real, successful attempt-1 dispatch alongside a genuinely-failed attempt-2/attempt-3 dispatch from 37 days earlier (2026-07-17); `3 > 1` let the July failure's exact "Validation history: attempt 1 failed...; unresolved after 3 attempt(s)" text get served verbatim as today's completion notification. Review follow-up found the deeper gap: the log-folder re-read this fix still relies on has no way to distinguish its own dispatch's file from a genuinely concurrent dispatch of the same step (confirmed unserialized: neither the session-scoped step registry nor the scheduler's workspace lock cover a chat-triggered execute_step racing a scheduled run) | unassigned | `fixed` — selects by `completed_at`; `runExecutionPhase` now also returns the exact in-memory result of the step it just ran via an out-parameter, used directly instead of a log-folder re-read whenever available (routing/todo_task steps and the inner-step-override path still fall back to the read); `completed_at` raised to nanosecond precision to shrink the residual tie window. Original selection fix has fail-before/pass-after tests; the exact-binding follow-up has none (no test harness exists for `runExecutionPhase`) — verified by full build + regression suite only. Live reverify pending for both | `pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`, `controller_workshop.go`, `controller_batch_execution.go`, `evaluation_execution.go`, `timing_persistence.go` |
| [PLAT-183](pulse_platform/coding-agent-bridge/plat-183.md) | Proposal (not a defect): make `mcpagent` the sole owner of multi-LLM provider contracts and implementation. Use a staged migration: preserve the unchanged P0 baseline, remove `agent_go`'s direct provider imports through an mcpagent facade, move code/tests with history, then retain the old repo through a production soak before archiving. Local workspaces use live source, but released builds currently pin different provider commits | unassigned | `open` — staged proposal + mandatory safety gates only; explicitly not implemented or scheduled | `multi-llm-provider-go` (whole repo), `mcpagent`, `agent_go` (direct importers), P0 contract suites, deploy scripts, docs |
| [PLAT-184](pulse_platform/cost-telemetry/plat-184.md) | Pulse and the Workflow Builder cannot access the cost ledger (`_system/costs.sqlite`) at all — confirmed via folder-guard scoping, `_system/` sits outside every workflow's read scope regardless of role, and no agent-facing tool exists to compensate (unlike Pulse's own state, `_system/pulse-platform.sqlite`, which has a whole family of dedicated tools). Consequence: PLAT-166/167's phase/item-level cost breakdown is entirely invisible to Pulse's own `model_cost_fitness` review lens and PLAT-090's "measure pulse cost" requirement. **Fixed:** dual-write (no backfill) to a new per-workspace `Workflow/<name>/costs/costs.sqlite`, plus a new capability-derived `query_workflow_costs` tool reachable by workflow steps, Pulse's background reviewers, and the Workflow Builder; guidance (`pulse-gate.md`, `file-layout.md`, Workflow Builder runtime instructions) updated to point at it. Still open: the workspace-less chat/builder writer path has no per-workspace ledger to land in, unchanged from before this fix — deliberately out of scope this pass | unassigned | `fixed` (`5552fbe71`, `main`) — real tool-call-path isolation acceptance test proves a session scoped to one workflow reads its own phase/item breakdown and cannot read another workflow's ledger | `pkg/costledger`, `pkg/costobserver`, `cmd/server/virtual-tools/workflow_costs_tools.go`, `cmd/server/tool_setup.go`, `pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go`, `interactive_workshop_manager.go` |
| [PLAT-185](pulse_platform/security-sandbox/plat-185.md) | A scripted step's sandbox (`validate-browser-evidence`, workflow `confida-login`) cannot read a sibling step's log folder despite a correctly-built, correctly-timed `additional_read_paths: ["runs"]` grant, silently defeating a fabrication check (falls back to "unverifiable" = pass, not fail-closed). Live-proven not a wrong-path, timing/race, glob-pattern, `DB_PATH`-injection, or stale-sandbox-profile bug — each ruled out with direct filesystem/code evidence, not assumption. The exact files existed at the exact expected path 8+ minutes before the denied read. Root mechanism inside the live sandbox enforcement not yet isolated; needs side-by-side instrumentation on a real failing run (declared `ReadPaths` vs. the script's actual attempted path/errno), which static tracing cannot supply | unassigned | `open` — real incident confirmed live; root cause not yet found despite exhaustive code tracing | `pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go`, `controller_scripted.go`, `workspace/handlers/shell.go`, `workspace/security/isolator.go` |
| [PLAT-186](pulse_platform/coding-agent-bridge/plat-186.md) | pi-cli's MCP `directTools` (already configured `true`) never activated, forcing every pi-cli tool call through the third-party `pi-mcp-adapter`'s fragile double-JSON-encoded `mcp(tool, args)` proxy wrapper — confirmed the structural cause behind a live incident where Gemini malformed that encoding and failed a whole run. Root cause traced to source in a third dependency, not guessed: `pi-mcp-adapter`'s own `computeServerHash` hashes the MCP server's entire declared `env` object to decide cache/direct-tool-registration validity, and our own `MCP_READY_FILE` (deliberately randomized per launch to prevent stale-marker false positives) busted that hash on every single call, unconditionally. Also confirmed: moving volatile env values to the parent process doesn't work (the MCP stdio SDK only inherits a small hardcoded allowlist, not full parent env); `MCP_API_TOKEN`/`MCP_API_URL` were already stable and not contributors | unassigned | `fixed` — ready-file path made deterministic per workspace/agent identity with delete-before-launch instead of randomization; pi-cli's interactive adapter also gained raw pre-recovery `ToolArgs` (a pre-existing gap vs. every other adapter) so a live P0 could distinguish native direct-tool calls from a proxy call recovery merely renamed. `TestPiCLIRealDirectToolsActivateOnRepeatedStableConfig` — live, real Gemini, PASS | `mcpagent/agent/coding_agents_bridge.go`, `mcpagent/cmd/mcpbridge/main.go`, `multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`, third-party `pi-mcp-adapter` (not ours) |
| [PLAT-187](pulse_platform/coding-agent-bridge/plat-187.md) | pi-cli's model-visible `mcp()` proxy tool has no boundary against custom (non-MCP) tools; a live `trading` workflow schedule run tried `mcp({tool: "get_human_input_request", args: "..."})` — a curl-only custom tool never declared on the MCP server — got an instant "not found", retried the identical losing pattern, and stalled without applying any of 5 pending decisions. Root cause in two parts: (1) `get_human_input_request` was never reachable through MCP by design (curl-only, `$MCP_CUSTOM`), and (2) nothing told the model, or prevented it, from trying — the routing prompt taught the `mcp()` wrapper syntax without scoping it away from custom tools, and `pi-mcp-adapter` keeps its generic `mcp` proxy tool visible by default even once `directTools` fully covers the small native set (confirmed via its own `index.ts`: `shouldRegisterProxyTool` only respects `settings.disableProxyTool`, which we never set) | unassigned | `fixed` — `normalizePiMCPConfig` now defaults `settings.disableProxyTool=true` (confirmed safe: still registers the proxy on a cold direct-tools cache, per `pi-mcp-adapter`'s own logic); the routing prompt's `mcp()`-teaching bullet removed entirely. `TestPiCLIRealMcpProxyToolIsUnavailableOnWarmDirectToolsCache` — live, real Gemini, against the actual `mcpbridge` binary (not a stub), PASS; pi's own response confirmed the tool is absent from the model's list entirely once warm | `mcpagent/agent/coding_agent_bridge_routing_prompt.go`, `multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`, third-party `pi-mcp-adapter` (not ours) |
| [PLAT-188](pulse_platform/coding-agent-bridge/plat-188.md) | `resolveServerAPIToken` regenerates `MCP_SERVER_API_TOKEN` fresh on every server restart (no explicit env var set), which busts pi-mcp-adapter's `computeServerHash` cache for every workspace's first pi-cli launch after any restart — the same class of gap PLAT-186 closed for `MCP_READY_FILE`, but for the token instead. Live-confirmed post-restart on `trading`: `mcp({search:...})` still answered (proxy visible) on one call, while `api_bridge_execute_shell_command` worked as a native direct call moments later in the same session — proving the cache warmed mid-session and the earlier proxy call was this one-time gap, not a PLAT-187 regression. Bounded, self-healing (one throwaway launch per workspace per restart, then normal) | unassigned | `open` — deferred by explicit operator decision ("for now security is fine"); full trade-off recorded (fixed token removes an implicit restart-triggered credential-rotation property in exchange for eliminating the post-restart cache-miss window) for a later revisit, not implemented | `agent_go/cmd/server/server.go` (`resolveServerAPIToken`) |
| [PLAT-189](pulse_platform/evaluation/plat-189.md) | `message_sequence` steps never proactively surfaced `validation_schema` in the opening turn, unlike `regular` (`execution_only_agent.go`) and `todo_task` (`todo_task_orchestrator_agent.go`), which both already did — `message_sequence` only showed it reactively after a failed pre-validation attempt, despite being the plan-design default step type. Found while investigating a user challenge to new `prompt-engineering.md` guidance ("are you sure the agent has to guess the shape? I think we send it already") — the user was right for `regular`/`todo_task`, wrong only for `message_sequence`. Separately, the user flagged a recurring authoring pattern: authored `validation_schema`s tend to check more than what's load-bearing ("many times we overdo the schema with what's not required") | unassigned | `fixed` (proactive surfacing) + `open` (schema-bloat guidance is judgment-only, no code enforcement — deliberately, since a raw check-count threshold can't distinguish a lean schema from a bloated one and no run data exists yet to calibrate one) | `pkg/orchestrator/agents/workflow/step_based_workflow/controller_message_sequence.go`, `cmd/server/guidance/templates/system/prompt-engineering.md`, `cmd/server/guidance/templates/review/ops-review.md`, `cmd/server/guidance/guidance.go` |
| [PLAT-190](pulse_platform/learnings-knowledge/plat-190.md) | Started as a proposal to hard-enforce "read the related skill before calling this tool"; revised after reviewing `docs/design/agent_tool_surface_single_source.md` and PLAT-125, both of which argue against a broad runtime gate for this class of rule (focus/discipline belongs in the prompt, not code). Revised fix: embed the load-bearing rule directly in each tool's own description instead. Audited all 50+ registered tools and fixed the real gaps found — `update_message_sequence_step`/`update_scripted_step`/`update_todo_task_route` (bare `update_*` schemas missing the rules their `add_*` counterparts had), `mutate_workflow_db` (contradicted `stores.md`'s upsert-over-DELETE rule), five skill-management tools (missing the install-does-not-attach rule), and three schedule tools — which surfaced a genuinely dead `references/schedule-management.md` reference (never registered, no file) left stale in two gated e2e tests after multi-agent chat's global schedule feature was removed; those stale test cases were also removed | unassigned | `partially implemented` — description-embedding fix shipped and verified (build clean, `TestAllSchemaFunctionsReturnValidJSON` passes, confirmed no regressions via baseline diff); the narrow hard-gate option for `set_workflow_llm_config` remains unbuilt. Side finding, not yet triaged: `cmd/server` has 15 pre-existing failing tests, larger than previously known this session | `pkg/orchestrator/agents/workflow/step_based_workflow/planning_agent.go`, `interactive_workshop_manager.go`, `cmd/server/virtual-tools/workflow_db_tools.go`, `cmd/server/multi_agent_chat_refdoc_e2e_real_test.go` |
| [PLAT-191](pulse_platform/scheduler-runs/plat-191.md) | Four `confida-login` platform-harness findings claimed the scheduler has "no durable missed-slot (misfire) recovery" — factually wrong about the code (a real, deliberate catch-up mechanism was added 2026-08-07, `2ebb0397`) and disproven by directly querying `schedule_fire_decisions`: every occurrence for all 4 of the workflow's schedules from 2026-08-19 to 2026-08-24 was evaluated exactly on time and correctly skipped with reason `"global scheduler pause is active"` — a human left a global pause on for 6 days, not a defect. Root tool gap: `get_schedule_runs` only reads actual-run rows, so a `skipped_paused` decision (which never creates one) was structurally invisible to every reviewer that hit this — not reviewer laziness | unassigned | `fixed` — new `SchedulerService.ListFireDecisions` wires the already-existing-but-uncalled `schedulerstate.Store.ListFireDecisions` into `get_schedule_runs`'s output (new "Skipped/Non-Run Occurrences" section); `ops-review.md` updated to check it before theorizing about scheduler internals, with this incident named as the cautionary example; the four harness-table rows have no status field to close in place, so this ticket is their correction record. New test `TestListFireDecisionsSurfacesSkippedOccurrences` passes; build clean; baseline failure counts unchanged (17/2/2) | `agent_go/cmd/server/scheduler.go`, `agent_go/cmd/server/server.go`, `agent_go/cmd/server/scheduler_test.go`, `agent_go/cmd/server/guidance/templates/review/ops-review.md`, `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go` |
| [PLAT-192](pulse_platform/step-execution/plat-192.md) | `diff_patch_workspace_file` reported `applied:true` on a confida-login two-hunk patch while silently leaving the second hunk unapplied and deleting an unrelated trailing line — a false-positive success on a platform-wide file-editing tool. Traced the real implementation (a separate `workspace/` Go module, not `agent_go`) through its apply cascade (strict `patch(1)` → corrected-diff retry → a custom fallback matcher). The fallback matcher is fail-closed by design (refuses ambiguous hunks) — not the bug. Built a direct repro for the leading hypothesis (premature hunk truncation in the diff auto-corrector); it did not reproduce. Tried to recover the exact original failing diff via the run's conversation log; the run folder had been overwritten by later cycles (same mechanism as PLAT-182), so the exact root cause could not be confirmed | unassigned | `partially fixed` — root cause unidentified; shipped a defense-in-depth apply-path-agnostic invariant instead: `verifyDiffApplied` refuses to report success if the actual line-count change doesn't match what the diff's own +/- lines claim, regardless of which internal strategy produced the result. Its first version had a real line-counting bug (empty-file / no-trailing-newline edge cases), caught by the existing `TestDiffPatchCreationWithControllingTTY` e2e test before shipping, fixed via a dedicated `countContentLines` helper. New tests prove both the catch and no false positive; full `workspace` module suite passes. **2026-08-29 addendum:** Twitter/social-media `PUL-D66C6684` (filed 2 days before this fix shipped) independently reported the inverse-shaped symptom — `applied:false` with a partial write left behind. Confirmed the shipped fix's write-ordering (`os.WriteFile` strictly after `verifyDiffApplied`; every failure path returns before it) makes that exact scenario unreachable now, closing this second independently-reported symptom without changing the ticket's own honest root-cause-unidentified status | `workspace/handlers/diff_patch.go`, `workspace/handlers/diff_patch_test.go` |
| [PLAT-193](pulse_platform/coding-agent-bridge/plat-193.md) | `execute_shell_command` reported a successful `cat` (exit_code 0) as a tool failure — `toolerr.canonicalFailureValue`'s shared masked-failure classifier recursed into `stdout`'s content, JSON-decoded it, and found a `success:false` field that actually belonged to a captured API error response the file legitimately contained (arbitrary domain data, not a transport signal). Reproduced directly during a confida-login Pulse verification pass, not just theorized | unassigned | `fixed` — first fix attempt (remove stdout/stderr from recursion entirely) was too broad and broke two existing tests (stdout's own plain text literally starting with "ERROR:"/"tool execution failed:" is a genuine self-reported signal); caught by the test suite, reverted. Precise fix: new `jsonEnvelopeFields` allowlist gates the JSON-decode-and-recurse step to fields that actually carry nested envelopes (`content`/`text`/`result`/`error`); stdout/stderr still get checked as plain text (prefix matching) but never re-interpreted as structure. New regression test plus the full pre-existing 22-test `toolerr` suite pass; build clean across the whole `mcpagent` module | `mcpagent/toolerr/toolerr.go`, `mcpagent/toolerr/toolerr_test.go` |
| [PLAT-195](pulse_platform/step-execution/plat-195.md) | `query_workflow_db`'s read-pragma allowlist genuinely blocked `integrity_check`/`quick_check`/`foreign_key_check` when this finding was filed (2026-08-03) — confirmed via `git log -L`. But the same commit that fixed PLAT-191's scheduler misfire-recovery gap (`2ebb0397`, 2026-08-07) also replaced the old single-regex allowlist with `isSafeReadPragma`, which explicitly allows all three. Confirmed live-wired into `/api/query` (what `query_workflow_db` actually calls) and confirmed SQLite itself doesn't block these under `query_only=true` (tested directly). The finding was accurate when filed and stale for the 21 days since the fix shipped | unassigned | `resolved` — correction/closure record only, no code change; already fixed 4 days after being reported, never re-verified against current code. Second instance of this exact pattern (PLAT-191 was the first) in one review pass — worth watching for a third | N/A — no files changed |
| [PLAT-194](pulse_platform/scheduler-runs/plat-194.md) | A schedule reported `[success]` while the workflow run it triggered actually died 17 minutes into a 69-minute invocation. Ruled out two plausible causes with real tracing before finding the actual one: (1) a run stuck at `status:"running"` forever — but that state is deliberately treated as ambiguous/fail-open by design, confirmed via the function's own doc comment, not touched; (2) the async `run_full_workflow` dispatch's completion-wait silently timing out — traced `waitForConversationTurnTree` and confirmed a genuine stuck child does propagate as a real error. Real cause: `reconcileWorkshopRunOutcome` only treated a folder as "touched by this invocation" if its NAME was absent from the pre-invocation snapshot — for a workflow whose runs always reuse the same folder name (`iteration-0/confida-staging`, confirmed live in PLAT-182), that's every cycle after the first, so its metadata was never inspected at all, regardless of what it said. The sibling function `workshopRunProducedEvidence`, called moments earlier for a different purpose, already had a `StartedAt`/`CreatedAt`-vs-invocation-start fallback for exactly this; this function lacked the same one | unassigned | `fixed` — added the matching since-based fallback (new `since time.Time` param). Ambiguous-state fail-open behavior from the original design is completely unchanged — this only changes which folders get inspected. New test `TestReconcileWorkshopRunOutcomeDetectsFailureInAReusedFolderName` reproduces the exact shape; all 5 pre-existing reconciliation tests still pass unchanged; full `cmd/server` suite failure count (17) matches baseline exactly | `agent_go/cmd/server/scheduler.go`, `agent_go/cmd/server/scheduler_test.go` |
| [PLAT-196](pulse_platform/pulse-governance/plat-196.md) | A background Pulse review child reported `required technical_review receipt for child session workshop-background-task-1787636631348580000 was not recorded: sql: no rows in result set` even though the receipt appears durably present. Traced the write path (`complete_pulse_review` → `pulseRunIDForSession` → `pulse_review_log`), the read path (`requireBackgroundPulseReviewReceipts` vs. `config.MCPSessionID`), and the shell-bridge session-scoping mechanism — initially believed background task agents were missing `configureWorkshopToolAgentBridgeSession` (a documented fix for a sibling bug class), but a deeper trace showed they get it transitively via `configureWorkshopToolAgentSession` → `setupWorkshopToolAgentSession`; that hypothesis is ruled out. `"current"`-resolution via `mcpexecutor.SessionIDFromContext` traced back to its one HTTP call site; how a *native* (non-HTTP) tool call gets a session id on its ctx could not be confirmed from static tracing alone | unassigned | `open` — no confirmed root cause, no fix. Shipped diagnostic-only logging instead of guessing: `pulseRunIDForSession` now logs every `"current"` resolution (including explicit-empty), `complete_pulse_review` logs the `review_run_id` it writes under, and `requireBackgroundPulseReviewReceipts` logs a dump of recent `review_run_id`s actually written for the module (new read-only `RecentPulseReviewRunIDsForModule`) whenever the expected receipt is missing. Build clean; same pre-existing test failures before/after (confirmed via `git stash`), no regression; no new tests (nothing to unit-test in a logging-only diagnostic). **2026-09-03:** a workspace-wide scan found this had actually occurred 56 times across 8 workflows between 2026-08-19 and 2026-08-28 (far more common than originally scoped), but zero times since the diagnostic logging shipped on 2026-08-28 — recent Pulse runs on the highest-frequency workflows are all `completed`. Not a confirmed fix, but real evidence the failure mode is dormant for now; see the ticket's 2026-09-03 update | `agent_go/cmd/server/pulse_runtime_guard.go`, `agent_go/cmd/server/pulse_worklist.go`, `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`, `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/pulse_review_log.go` |
| [PLAT-197](pulse_platform/plans-contracts/plat-197.md) | Managed plan changes recorded the writing tool but lost the true initiator and could close Artifact Review without proving every dependent surface was reconciled | Codex | `implementation_in_progress` — trusted change IDs/origin, six-surface evidence closure, unresolved-issue links, Pulse backlog projection, and typed impact-ledger linkage implemented 2026-08-28; on 2026-09-17 Plan Drift was bound to exact-entry receipt closure with a terminal-result guard and made an exclusive prerequisite Pulse pass, preventing later receipt-only Technical work or concurrent reviews against a stale plan; plan-revision fields, source message IDs, enforced measurable/not-measurable closure, and live reverify remain | managed plan changelog, plan-mod registration context, Artifact Review marker, Pulse impact ledger |
| [PLAT-198](pulse_platform/pulse-governance/plat-198.md) | Pulse retained successfully applied fixes in a separate verification queue and exposed text-derived fingerprint identity | Codex | `implementation_in_progress` — close-on-applied lifecycle, stored non-derived `issue_id`, closed-issue semantic index, merged-alias recurrence routing, issue-ID-only API/UI projection, and workflow-contract v1.0.32 reconciliation implemented 2026-08-28; Social Media migration will close 5 applied verification-debt rows and retire 2 reopened aliases on upgrade | Pulse finding lifecycle, reviewer/Fixer contracts, Pulse issue UI |
| [PLAT-199](pulse_platform/pulse-governance/plat-199.md) | Pulse discarded Technical Review context into a fresh Fixer and stretched one repair across repeated expensive passes | Codex | `implementation_in_progress` — live reverify showed the receipt-unlock phase split blocked the code-execution bridge before its receipt; replaced with one retained Review+Fix task that records the receipt at completion, fresh end-to-end Pulse reverify pending | scheduled Pulse dispatch, background task runtime, receipt identity, tool authorization |
| [PLAT-200](pulse_platform/browser-automation/plat-200.md) | `harness:oversized-tool-result-recovery` verified that wide/flat snapshots can make `--depth` a no-op and that truncated excess was discarded | unassigned | `fixed` — preserved full snapshots now point to the registered `execute_shell_command` with bounded `sed` reads, replacing an erroneous reference to the test-only `read_workspace_file` helper; the same session-granted `tool_output_folder` remains the only spill target | `agent_go/pkg/browser/executor.go`, `agent_go/pkg/browser/cdp_tabs_test.go`, `agent_go/cmd/server/virtual-tools/workspace_browser_tools.go` |
| [PLAT-201](pulse_platform/step-execution/plat-201.md) | Two harness findings (`tool:diff_patch_workspace_file` + its `:unicode-context-mismatch` twin, same incident filed twice) theorized "silent normalization or transport-level mangling of non-ASCII characters" caused a large hand-copied hunk (em dashes, arrows, curly quotes) to fail context-matching while a `difflib`-generated diff from the same file succeeded. Traced the apply path end to end: context comparison is a plain `strings.TrimSpace` + `==` byte comparison, no Unicode folding anywhere | unassigned | `resolved` — proved with a live test that byte-exact Unicode context applies cleanly and ASCII-folded context (em dash→`-`, arrows→`->`, curly quotes→`"`, exactly what an LLM commonly does when retyping instead of copying) is correctly rejected as a genuine mismatch, not silently corrupted — reproducing the finding's exact symptom without any platform-side mangling. Root cause is consistent with agent-side hand-copy drift, not a tool bug; no code behavior changed. Shipped guidance instead: the tool's own `diff` parameter description now warns against hand-retyping Unicode punctuation, matching PLAT-190's embed-the-rule-in-the-tool-description precedent | `agent_go/pkg/workspace/advanced_tools.go`, `workspace/handlers/diff_patch_test.go` |
| [PLAT-202](pulse_platform/step-execution/plat-202.md) | `harness:diff_patch_workspace_file` reported `applied:true` while a two-hunk JSON diff partly applied and deleted trailing content | unassigned | `resolved` — PLAT-192's line-count check catches this exact reported result (expected +1, actual +0). This does not establish coverage for every partial-apply mechanism or a corrupt result with the same net line delta | `workspace/handlers/diff_patch_test.go` |
| [PLAT-203](pulse_platform/cost-telemetry/plat-203.md) | `harness:cost-ledger:reflection-attribution-and-call-count` found `google/gemini-3.7-flash` (pi-cli-routed) had `llm_call_count` present but no `total_cost_usd` key at all. Traced two compounding gaps: (1) pi-cli's `GetModelMetadata` (multi-llm-provider-go) never sets any `*CostPer1MTokens` field for any model it serves — a structural gap for the whole provider, not one stale model entry; (2) `ModelTokenUsage.TotalCost` had `omitempty`, so an unpriced $0 and a genuinely-free $0 were indistinguishable — the key just vanished | unassigned | `fixed` — `calculatePricingFromModelData` now returns `pricingFound bool` (true only when a real nonzero rate exists, not merely "metadata lookup didn't error"); `ModelTokenUsage` gained `Unpriced bool` and `TotalCost` lost `omitempty`, so an unpriced call now explicitly shows `total_cost_usd:0, unpriced:true` instead of a missing key, with a `[COST]` log line naming the exact provider/model. Did not guess real pi-cli pricing (unconfirmed billing model) — explicit "unknown" beats a plausible wrong number. New tests prove both the gap and the fix, and prove a real Claude rate-card call's JSON is byte-identical to before (no `unpriced` key at all when priced). Full suite: 3 pre-existing failures unchanged, none in touched packages | `agent_go/pkg/orchestrator/base_orchestrator_tokens.go`, `agent_go/pkg/orchestrator/base_orchestrator_types.go`, `agent_go/pkg/orchestrator/cost_storage.go`, `agent_go/pkg/orchestrator/unpriced_model_cost_test.go` |
| [PLAT-204](pulse_platform/browser-automation/plat-204.md) | `harness:agent_browser_cdp_shared_tab_visibility` (seen twice): every `performance_baseline` row in a cycle had `lcp_ms=NULL` while CLS/TTFB were numeric; a dedicated labeled tab created specifically to fix it didn't move the null rate. Live-tested directly against real Chrome via CDP: `agent-browser tab <ref>` correctly sets the switched-to tab's `visibilityState` to `"visible"` in isolation — the tool itself works. The gap is concurrency: CDP mode shares one real Chrome across every concurrent workflow session (confirmed live in PLAT-322), so another session can steal foreground between this session's switch and its capture; a dedicated *labeled* tab only fixes identity, not foreground. LCP is withheld by W3C spec whenever visibilityState isn't visible — unfixable from page-side script | unassigned | `open` — confirmed mechanism, no code fix. Presented the user a choice between documenting this as a known structural limitation vs. implementing a dedicated non-shared CDP browser per performance step (the real fix, but architecturally the same size as PLAT-322's still-open work); user chose documentation. Added a bullet to `ops-review.md` explaining the mechanism so Pulse stops re-filing this each cycle without the architecture change landing first | `agent_go/cmd/server/guidance/templates/review/ops-review.md` |
| [PLAT-205](pulse_platform/plans-contracts/plat-205.md) | Two findings (same `finding_id`, two keys — `changelog:learnings-main.py-edits-uncaptured` + `harness:changelog:direct-file-write-gap`): a `learnings/<step>/main.py` edit (and separately `db/README.md`) made in the same turn as a `plan.json` change via `update_scripted_step` produced no changelog entry of its own — only the plan.json field change was recorded | unassigned | `resolved` — confirmed `writePlanChangelogEntry` has exactly 2 call sites, both plan-mutation tools; never wired to `diff_patch_workspace_file`/`update_workspace_file`. Also confirmed (per user's own clarification) that `planning/` is never a granted write path for any other tool, so `plan.json` itself is always fully tool-gated and truthfully captured — the gap is exclusively non-plan files. Presented extend-the-changelog vs. document-the-boundary; user chose documentation. Added a note to `review-artifact-drift.md` clarifying the scope, noting the checklist already inspects `main.py` directly for any step a changelog entry does flag, and naming the narrower residual gap (an isolated code/doc edit with zero accompanying plan-tool call in the same turn never enters the review cursor at all) | `agent_go/cmd/server/guidance/templates/review/review-artifact-drift.md` |
| [PLAT-206](pulse_platform/pulse-governance/plat-206.md) | `pulse:module_result:split-reviewer-fixer-ordering`: in the split reviewer/Fixer dispatch, the reviewer's terminal `record_pulse_result` closed the module for the whole `pulse_run_id`, so a separately-dispatched Fixer's later `record_pulse_result(result=changed, finding_dispositions=[...])` for real repairs was rejected outright as "already terminal." Confirmed still-live via code read after checking (and ruling out) PLAT-198's concurrent lifecycle work as already covering it — 3 real confida-login repairs stayed forever mislabelled `queued_for_engineering`, invisible to Gate as resolved. Also checked PLAT-199 (same rebase): it eliminates split dispatch for the *automatic* scheduled flow, but explicitly keeps `/pulse-fixer` and the "PRE-RUN TARGETED FIXER" operator-decision dispatch as standalone-Fixer-against-already-reviewed-queue paths — this fix directly serves those, not made moot | unassigned | `fixed` — relaxed the retry guard from "same result" to "same result OR real dispositions supplied"; `result=changed` already requires non-empty dispositions upstream, so a genuine Fixer call always qualifies, while a mismatched result with zero dispositions still rejects exactly as before. Module's own `last_result` stays the reviewer's original verdict, never overwritten. Testing surfaced (didn't introduce) a secondary property: `pulse_module_audit` upserts per (workspace,module,run) rather than appending, so the Fixer's write collapses the module-level audit snapshot — the load-bearing FINDING lifecycle itself is genuinely append-only and correctly preserves both the review and the fix, verified directly. 2 new tests (positive + negative-guard); full suite 3 pre-existing failures unchanged | `agent_go/cmd/server/pulse_worklist.go`, `agent_go/cmd/server/pulse_worklist_test.go` |
| [PLAT-207](pulse_platform/human-decisions/plat-207.md) | `db:report_human_inputs:consumed_at`: its own reopen condition ("re-open only if a future pass observes a row with answered_at set and consumed_at empty") was already satisfied in the resolved direction when filed — 18 answered rows all `status=consumed`, 0 rows `status=answered` | unassigned | `resolved` — correction/closure record only, no code change; confirmed via direct SQL matching the finding's own reproduction exactly | N/A — no files changed |
| [PLAT-208](pulse_platform/human-decisions/plat-208.md) | `db:report_human_inputs:operator-identity`: the finding's own text says a platform improvement already shipped — 7 rows answered since 2026-08-12 carry `human_ui` provenance + a real session id; only pre-2026-08-12 rows are permanently `legacy_unattributed` | unassigned | `resolved` — informational closure, no code change; the finding's own next_check already states "No workflow change available," this ticket just gives it a durable PLAT record | N/A — no files changed |
| [PLAT-209](pulse_platform/coding-agent-bridge/plat-209.md) | `host:machine-sleep:llm-session-continuity`: a "35-minute hung LLM call" was actually 5.4 min of real LLM time + 30.2 min of untracked wall-clock while the host was asleep (85% suspended, not hung) — the finding's own conclusion is no bounded-timeout change is warranted | unassigned | `resolved` — informational closure, no code change; confirmed via the finding's own cited execution-attempt tracked/untracked split and provider error text. `PUL-048B1B72` (an abort-time `qa_cycles` terminalization gap the finding names as fixable) is workflow-local per this register's own scoping rule, not migrated in | N/A — no files changed |
| [PLAT-210](pulse_platform/scheduler-runs/plat-210.md) | `schedule:a5f7750a-f6a6-434c-bce0-34633f196a0f`: `get_schedule_runs` reported "no runs found" for a daily 18:00 IST review schedule, reasonably read as "is this schedule firing at all?" | unassigned | `resolved` — same pattern as PLAT-191/195: already fixed, never re-verified. Direct `schedule_fire_decisions` query shows the schedule evaluating correctly every day (1 real `started` fire, 10 consecutive `skipped_paused` matching PLAT-191's already-documented global pause, 1 `skipped_busy`) — PLAT-191's `get_schedule_runs` fix (surfacing skip decisions) already covers this, it just predates this finding's own evidence-gathering pass | N/A — no files changed |
| [PLAT-211](pulse_platform/step-execution/plat-211.md) | Legacy/advisor `CONCERNS:` lines wrapped in Markdown code spans were not parsed | unassigned | `retained legacy compatibility` — conservative whole-line code-span parsing remains for historical artifacts and strategic-advisor validation, but ordinary step-summary scraping is retired; comments corrected from the erroneous PLAT-208 reference | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/run_concerns.go`, `run_concerns_test.go` |
| [PLAT-212](pulse_platform/plans-contracts/plat-212.md) | `harness:workflow-upgrade-preflight:contradictory-version-and-schema-target` (`Workflow/HDFC-Personal-Accounts`, critical, RECURRED 4 times 2026-08-02 through 08-11): the workflow-upgrade preflight aborted an entire 8-group scheduled run before any step executed, requiring an out-of-band manifest stamp to recover each time | unassigned | `resolved` — same "real when filed, already fixed, never re-verified" pattern as PLAT-191/195/201/202 (5th instance this session). PLAT-096's stamp-fencing fix (`79ba90168`, 2026-08-12) is the exact matching mechanism, and its own text names HDFC by name as one of three workflows that completed the rung the night the fix landed. Independently confirmed against HDFC's own live evidence: the 08-11 abort is the LAST preflight error in `schedule-runs.json`, the very next run succeeded, and `workflow.json` is now at `1.0.26` — one rung past the version that was blocked | N/A — no files changed |
| [PLAT-213](pulse_platform/pulse-governance/plat-213.md) | `harness:record_pulse_result` (`Workflow/ICICI-BANK-PARSING`, high): a throwaway probe call (`result=done`) followed minutes later in the same session by the real, correct call (`result=changed`, real evidence) was rejected as "already terminal," permanently losing the real outcome | unassigned | `resolved` — same code path PLAT-206 already fixed, different trigger (same-session self-correction, not a split reviewer/Fixer dispatch). Verified directly: a scratch test reproducing this finding's exact reported call sequence passes against current code; not committed as a permanent test since PLAT-206's own test already pins the identical mechanism | N/A — no files changed |
| [PLAT-214](pulse_platform/pulse-governance/plat-214.md) | `record_pulse_finding` intermittently returned a false negative after updating an existing issue | unassigned | `fixed pending live confirmation` — removed the avoidable separate-connection reload in favor of a same-handle point query. This eliminates the failure-prone path, but the former reload race is not claimed as conclusively proven until live confirmation | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/pulse_finding_details.go` |
| [PLAT-215](pulse_platform/browser-automation/plat-215.md) | `harness:agent_browser.download` reported data loss when a third-party download wrote to a denied destination | unassigned | `resolved` — corrected stale diagnosis: guarded `agent_browser` calls rewrite the browser destination to managed staging and the trusted workspace server validates/publishes only authorized destinations. The third-party CLI never receives the denied path | `agent_go/pkg/browser/artifact_broker.go`, `workspace/security/browser_artifact.go` |
| [PLAT-216](pulse_platform/step-execution/plat-216.md) | `harness:scripted-step-fallback:protective-abort-overridden-by-agentic-retry` (`Workflow/HDFC-Personal-Accounts`, high): a scripted step's BR5-01 fail-closed guard deliberately refused to overwrite bank balance history it could not verify (`sys.exit(1)`, explicit ABORT message) — the runtime's own agentic fallback read the refusal, agreed it was correct, and performed the exact write and database mutation the guard refused to do | unassigned | `fixed` — `decideScriptedFastPath`'s own doc comment confirms the design: any nonzero exit is treated identically, with no distinction between "crashed" and "deliberately refused." User chose a reserved exit code over a text-marker signal (avoiding the false-positive/negative risk of script-output sniffing). New `ScriptedTerminalRefusalExitCode=2`: a script opts in by exiting exactly this code; any other nonzero code (including the still-conventional 1) keeps today's relearn-fallback unchanged. Aborts the step outright, same shape as the existing harness-rejection path; does not count against lock_code_stats. `code-authoring.md` updated so script authors know the convention exists. 4 new tests; full suite 3 pre-existing failures unchanged. Did not edit HDFC's own main.py — that workflow's own domain | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_scripted.go`, `controller_execution.go`, `scripted_harness_error_test.go`, `agent_go/cmd/server/guidance/templates/system/code-authoring.md` |
| [PLAT-217](pulse_platform/pulse-governance/plat-217.md) | `harness:pulse-review-persist:malformed-marker-discards-whole-review` (`Workflow/HDFC-Personal-Accounts`, high): a 2026-08-03 review run's `eval_health` artifact went entirely missing, evidence citing a "quarantine invalid markers, keep raw markdown" contract that was apparently unmet | unassigned | `resolved` — searched the current codebase for the described mechanism (one atomic Markdown blob with embedded `PULSE_VERIFICATION_JSON` markers, validated/persisted all at once): zero matches anywhere, for either the marker format or "quarantine" logic. The two functions that actually persist review outcomes today (`CompletePulseReview`, `RecordPulseReviewFinding`) are both per-module/per-finding typed tool calls with no shared blob or transaction spanning multiple findings — the same PLAT-055 typed-shape migration already confirmed for PLAT-211 removes this failure mode by construction, not by a targeted fix | N/A — no files changed |
| [PLAT-218](pulse_platform/human-decisions/plat-218.md) | Upwork `PUL-D7D173FB`: `create_human_input_request` rejected an approval contract and claimed the outer object was not an object | Codex | `implemented` — exact retained HTTP evidence proved the bridge preserved the outer object; `approved_scope` was an object and `post_run_proof` an array despite both being string fields. The handler now names the exact invalid nested field and the schema/guidance make the wire shapes explicit | `agent_go/cmd/server/report_human_inputs.go`, `agent_go/cmd/server/report_human_inputs_test.go`, Pulse Review+Fix guidance; SQLite backlog reconciled 2026-09-05 — verified stale records resolved; see [audit](../audits/platform-backlog-reconciliation-2026-09-05.md), deployed verification not claimed |
| [PLAT-219](pulse_platform/scheduler-runs/plat-219.md) | Sales Outreach `PUL-5D2B9495`: the full run durably failed, but its missing in-memory completion callback kept the schedule running until restart | Codex | `implemented; runtime reverify` — full-run identity is recorded at launch and the scheduled turn consults the exact run's authoritative failure after 30 seconds without progress instead of extending the live-child wait for hours | `agent_go/cmd/server/conversation_turn_lifecycle.go`, `workflow_execution_tracker.go`, `delegation.go`, `planning_exports.go`; SQLite backlog reconciled 2026-09-05 — verified stale records resolved; see [audit](../audits/platform-backlog-reconciliation-2026-09-05.md), deployed verification not claimed |
| [PLAT-220](pulse_platform/pulse-governance/plat-220.md) | Pulse tool-surface simplification: `get_pulse_review_focus_agenda` folded into `get_pulse_state(view="focus_agenda")`; `record_pulse_lifecycle_reconciliation`/`record_pulse_actionable_backlog_reconciliation` merged into one `record_pulse_migration_reconciliation(scope=...)` | unassigned | `implemented` — user-scoped consolidation, reconciled against the concurrent `a8aaaa012` commit that separately removed `complete_pulse_review`; all callers/tests/guidance updated, full suite passes | `agent_go/cmd/server/pulse_worklist.go`, `workflow_version_upgrades.go`, `pulse_review_tools_test.go`, `pulse_tool_surface_test.go`, `workflow_schedule_contract_test.go`, 3 guidance templates |
| [PLAT-221](pulse_platform/step-execution/plat-221.md) | LinkedIn `PUL-B995BF46`/`PUL-3BD9F422`: neither `mutate_workflow_db` nor any other path could ever apply a workflow's own agent-authored `db/migrations/*.sql` file — an already-built backend primitive (`/api/db/initialize`) existed but was never wired to an agent tool | Claude Code | `implemented and reachable; PUL-3BD9F422 migration still undesigned; runtime reverify` — new `apply_workflow_db_migration` tool; backend allow-list widened from CREATE-only to idempotent CREATE/DROP IF (NOT) EXISTS plus ALTER TABLE RENAME/ADD COLUMN/DROP COLUMN, PRAGMA/ATTACH excluded, automatic pre-migration VACUUM INTO backup for destructive statements. **Code review follow-up:** the tool was registered/tested but absent from 3 separate real-agent allowlists (workflow-step, todo-task, Workshop/Pulse) — fixed, now genuinely callable; added a durable `schema_migration_log` ledger plus bounded backup retention and fixed leading-comment parsing. **Independent policy review:** broad schema authority for ordinary read-write agents and an agent-accessible ledger were explicitly accepted by the user as intentional—agents should receive more access, not a narrowly partitioned schema capability. `PUL-B995BF46` already resolved independently; `PUL-3BD9F422` remains separate undesigned workflow migration work | `agent_go/cmd/server/virtual-tools/workflow_db_tools.go`, `agent_go/pkg/workspace/initialize_workflow_db.go`, `workspace/handlers/query.go`, `workspace/models/query.go`, `controller_agent_factory.go`, `interactive_workshop_manager.go`, `guidance.go`, `stores.md` |
| [PLAT-222](pulse_platform/pulse-governance/plat-222.md) | Sales Outreach `PUL-1E38F625`: `record_pulse_finding` deduplicated a new `technical_review` finding into an existing `strategic_review` issue purely because both chose the same `target_key`, silently overwriting the `strategic_review` issue's content while leaving its module attribution unchanged | Claude Code | `implemented; runtime reverify` — `pulseFindingCanonicalFingerprint` now scopes `target_key`-based identity by reporting module for every issue kind except `harness_issue` (a deliberately shared cross-workflow platform identity, left unchanged); `finding_id`-based identity stays module-agnostic for every kind, matching the already-pinned reviewer-change-survival contract. 3 new regression tests, full suite passes | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/pulse_finding_details.go`, `pulse_finding_lifecycle_test.go` |
| [PLAT-223](pulse_platform/learnings-knowledge/plat-223.md) | Upwork `PUL-EDFF0710`: injected step guidance unconditionally told every step to read `db/README.md`, but two real steps' Folder Guard grants did not include it (`toptal-submit`, `profile-skill-harvest`) | Claude Code | `implemented; runtime reverify` — low severity, no correctness impact observed. Reworded `BuildManagedWorkflowDBGuidance` to read `db/README.md` first when readable, falling back to `query_workflow_db describe` (always available) otherwise, rather than auditing every distinct FolderGuard construction site across step types to guarantee the grant everywhere | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/prompt_sections.go`, `prompt_sections_test.go` |
| [PLAT-224](pulse_platform/browser-automation/plat-224.md) | Upwork `PUL-9CCE9488`: `agent_browser network requests` returns unrelated requests from other open tabs in shared CDP mode instead of scoping to the requested tab | Claude Code | `investigated — no in-repo fix possible` — `agent-browser` is an installed external binary with no vendored source in this repo; there is zero Go-side output parsing to build a client-side tab filter on top of, and guessing at the external tool's unknown JSON schema would risk a worse silent failure than the current honest unscoped behavior. Same class as PLAT-215. Needs an upstream fix or documentation correction outside this repository | N/A — no files changed |
| [PLAT-225](pulse_platform/coding-agent-bridge/plat-225.md) | Sales Outreach `PUL-AAC278EF`: a chained `execute_shell_command` script whose trailing command returned nonzero surfaced only a bare `exit_code=N` error, discarding the real stdout earlier commands in the chain had already produced | Claude Code | `implemented; runtime reverify` — root cause in the sibling `mcpagent` repo's `cmd/mcpbridge/main.go`: the bridge's stdio tool-call handler discarded `result.Result` entirely whenever the HTTP response said `success=false`, even though it carries the same output a caller would get on success. Now appended after the error text whenever non-empty. New production-transport E2E test in `mcpagent`; full suites pass in both repos | `mcpagent: cmd/mcpbridge/main.go`, `cmd/mcpbridge/failure_result_preserved_e2e_test.go` |
| [PLAT-226](pulse_platform/cost-telemetry/plat-226.md) | Upwork `PUL-A8AB0913`/`PUL-E717A5E1`: orchestrator, Pulse, and Builder overhead cost all collapse onto the identical `workflow_orchestrator` ledger bucket | Claude Code | `root cause confirmed in code — fix deferred, needs live instrumentation design` — traced to `context_aware_bridge.go`'s token-attribution fallback, reached by any event with no plan-step phase/ID pushed (true for the real orchestrator, but also every Pulse review/fixer turn and the Builder session). Checked and ruled out `currentAgentName` as an existing disambiguator: no caller sets it for these three sources either. A real fix needs new explicit session-kind signaling at each of the three separately-configured call sites, then live verification against a real same-day run/Pulse-pass/Builder-activity mix this session cannot perform statically — shipping a guess here risks a *confidently wrong* ledger, worse than the current honestly-merged one | N/A — no files changed, concrete next-steps left in the ticket |
| [PLAT-227](pulse_platform/evaluation/plat-227.md) | LinkedIn `PUL-E45BE152`: `update_evaluation_plan` had no editable `pre_validation` field, so a step's producer-route gate and its pre-run evidence requirement could never be realigned atomically after they drifted apart | Claude Code | `implemented; runtime reverify` — added `pre_validation` to the tool's editable-field whitelist and JSON schema (shape confirmed against LinkedIn's own evaluation plan); the executor already forwards any whitelisted field generically, so no other code changed. Also checked the audit's own grouping of this with `PUL-E67413EC`/`PUL-90D1E2C9`: the former is genuinely `workflow_issue`-owned config, reclassified out of this queue; the latter has no structured detail to investigate yet and remains untouched. New regression test realigns route gating + pre_validation atomically and confirms both land together; full suite passes | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/evaluation_plan_tool.go`, `evaluation_plan_tool_test.go` |
| [PLAT-228](pulse_platform/learnings-knowledge/plat-228.md) | Upwork `PUL-B0A88D49`: the `"bug-review"` guided flow mandated `read_skill(...references/post-run-monitor.md)`, a file not bundled with `builder-reference` | Claude Code | `resolved` — same "real when filed, already fixed via restructuring, never re-verified" pattern as PLAT-191/195/201/202/210/212/213. Confirmed `"bug-review"` no longer exists as a guidance kind anywhere in the codebase; its successor `engineering-review` mandates entirely different, genuinely-bundled reads (`pulse-review-fixer.md`, `ops-review.md`). A test comment in `render_all_test.go` documents the exact restructuring: Bug Review mechanics were deliberately extracted from the retired post-run-monitor doc into `pulse-bug-review.md` — precisely what the finding's own workaround asked for. PLAT-190's suggested materialization-time `read_skill` validator remains separate, real, unstarted future work | N/A — no files changed |
| [PLAT-229](pulse_platform/evaluation/plat-229.md) | LinkedIn `PUL-61C84987` (severity high): a JSON array value (e.g. `$.notes: ["some note"]`) could pass a `value_type=string` pre-validation check, since the shared JSONPath value extractor could not distinguish "this path's own value is an array" from "a wildcard path matched multiple locations" and defaulted to unwrapping either shape to its first element for a scalar check — a string-containing array's first element is itself a string | Claude Code | `implemented; adjacent wildcard predicates fixed; wildcard value_type=array fixed; runtime reverify` — definite-path array values are no longer unwrapped, and every wildcard result is now checked for `value_type`. **Independent review** flagged that `min_length`/`max_length`, numeric range, and regex `pattern` still inspected only the first wildcard result — fixed by routing all four checks through the same every-match predicate the `value_type` fix introduced. A second review found `value_type="array"` on a wildcard path was special-cased to check the *whole match-results slice* (trivially always a slice) instead of each individual matched value — fixed by removing the special case so `value_type=array` follows the same every-match rule as every other check. Does not reopen the original `PUL-61C84987` disposition | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/pre_validation.go`, `pre_validation_test.go` |
| [PLAT-230](pulse_platform/plans-contracts/plat-230.md) | LinkedIn `PUL-17E6F19A`/`PUL-7607952E`: changelog coverage gap and a lost `builder/improve.html` Artifact Sync Cursor | Claude Code | `resolved` — `PUL-17E6F19A` is the identical mechanism [PLAT-205](pulse_platform/plans-contracts/plat-205.md) already closed by explicit user decision (document the plan-mutation-only scope boundary, don't extend the changelog); no new mechanism surfaced. `PUL-7607952E`'s entire premise no longer exists: `builder/improve.html` is confirmed absent from LinkedIn's live `builder/` folder, and its workflow.json (v1.0.31) is ten versions past the mandatory [PLAT-055](pulse_platform/learnings-knowledge/plat-055.md) artifact-purity preflight (v1.0.21) that retired that file platform-wide | N/A — no files changed |
| [PLAT-231](pulse_platform/browser-automation/plat-231.md) | Upwork `PUL-13197E02`: `bid-submit` hit two CDP tab-acquisition errors before succeeding on retry, root cause (workflow vs. harness) unconfirmed by the finding itself | Claude Code | `investigated — reclassified, harness working as designed` — both error strings are exact matches to deliberate input validation in `cdp_tabs.go` (missing `--label` on `tab new`; a non-URL string passed as a navigation target), correctly rejecting a malformed call rather than any transient or race condition. Confirmed not a PLAT-028 duplicate — that ticket fixed a different mechanism (a recovered tab id leaking into a click's element argument, not tab creation). Root cause resolves to workflow-authored: `bid-submit`'s own call was malformed on its first attempt and self-corrected via retry, exactly as specific error messages are meant to enable | N/A — no files changed |
| [PLAT-232](pulse_platform/browser-automation/plat-232.md) | Twitter/social-media `PUL-B6B9F1D7`, `PUL-83421488`, `PUL-57F0FE8D`, `PUL-A319446F`, `PUL-BEE6AC69`: five independent findings converged on the identical mechanism — `agent_browser click` on a toggle control (like/follow) returns `success=true` while the live DOM state never actually flips | Claude Code | `guidance mitigation applied; independent review wording corrections applied; live reverify` — the dispatch/verify/recover recipe is appropriate and the two quote-compose findings remain correctly separate. **Independent review** flagged three wording overreaches, now corrected in `browser-usage.md`: `success=true` proves command completion, not page-event delivery; verification now explicitly requires a fresh scoped snapshot or stable-selector re-query rather than a stale `@eN`; and the closing paragraph no longer claims no platform-side fix could ever exist, naming an opt-in caller-supplied-postcondition wrapper as a possible future improvement instead. A second review found step 2 itself contradictory — it named `data-testid` as both a stable selector to re-locate the element and a value expected to change (X's like/follow controls literally flip `data-testid` between two values) — fixed by locating the element by stable characteristics separately from reading its now-current `data-testid` value | `agent_go/cmd/server/guidance/templates/system/browser-usage.md` |
| [PLAT-233](pulse_platform/browser-automation/plat-233.md) | Twitter/social-media `PUL-3A639FC5`, `PUL-F7094097`, `PUL-0C1112FB`, `PUL-F1E0ACE7`: four independent findings, all a short intended `agent_browser wait` pacing delay failing with `Wait timed out after 25000ms` | Claude Code | `guidance fixed; independently reproduced against the real binary; live reverify` — `agent-browser 0.35.1 wait --help` documents the positional millisecond form. Independent review reproduced `wait 75` succeeding and `wait --ms 75` failing after ~25.03s, confirmed all four SQLite resolutions, and found no corrective code change necessary. Restart onto the new guidance and observe a live workflow call | `agent_go/cmd/server/guidance/templates/system/browser-usage.md` |
| [PLAT-234](pulse_platform/coding-agent-bridge/plat-234.md) | Twitter/social-media `PUL-743BAC2B`, `PUL-CA44C7EC`, `PUL-0B1A2E4B`: three independent findings, all `search_web_llm(provider=codex-cli, model_id=gpt-5.5)` timing out at a self-chosen `deadline_seconds=180`, recovered via the sanctioned WebSearch fallback | Claude Code | `guidance fixed; no platform 180s timeout exists; review wording corrections applied` — checked every layer: `search_web_llm` has no timeout parameter, the live server's outer `TOOL_EXECUTION_TIMEOUT` is `90m` rather than 180s, and the codex-cli adapter's provider-level interactive-turn timeout defaults to unbounded. Every direct `search_web_llm(codex-cli)` call receives a fresh bounded session identity, and the workflow's own 180s budget ended these calls. Independent review confirmed the classification and all three SQLite resolutions, and flagged two wording overreaches — now corrected in the guidance text and ticket doc: say "no platform-imposed 180-second timeout" (not "no timeout", since the 90-minute outer ceiling is itself a backend timeout), and attribute the observed >180s duration to the full search turn scoped to `codex-cli`, not to cold start alone or to every CLI-backed provider. A second review found the causal framing too absolute ("a timeout means the budget was too tight, not that the provider is broken") for a single day's evidence — reworded so a timeout no longer *proves* the budget was too tight, with an explicit escalation path (widen and retry; treat *persistent* timeouts as evidence of a real problem) | `agent_go/cmd/server/guidance/templates/system/workspace-media-tools.md` |
| [PLAT-235](pulse_platform/browser-automation/plat-235.md) | Twitter/social-media `PUL-5629287B`, `PUL-0752CD98` (aggregating 6 further deduped observations across intent queue, targeting audit, verify, profile health, unfollow cleanup, and remediation): managed CDP tab listing timing out after exactly 15 seconds against an endpoint a Stage-0 connection test had proven reachable minutes earlier, forcing safe-skip/block for the rest of the run | Claude Code | `implemented; runtime reverify` — the general CDP command dispatcher already retries certain read-only commands once on timeout (`shouldRetryCDPTimeout`), but `listCDPTabs` — the internal helper all 6 tab-listing call sites funnel through — bypassed that dispatcher entirely and had zero retry of its own. `"tab"` can't simply join the shared allowlist because that name also covers side-effecting `tab <id>`/`tab new` forms; added a scoped retry directly inside `listCDPTabs` instead, which only ever issues the always-side-effect-free bare list form. 2 new regression tests; `go test ./pkg/browser/...` passes | `agent_go/pkg/browser/executor.go`, `cdp_tab_list_retry_test.go` |
| [PLAT-236](pulse_platform/evaluation/plat-236.md) | Twitter/social-media `PUL-08AC60BB`, `PUL-ED75E920`: `$.reach_snapshot_table_updated`'s validation check paired `value_type=boolean` with a string-only `pattern`, an unsatisfiable combination no JSON value could ever pass | Claude Code | `implemented; update_evaluation_plan gap closed; DB-check coverage added; runtime reverify` — the underlying schema was already fixed by the workflow's own `update_validation_schema` call on 2026-08-27 (2 days after these findings were first seen), same "already resolved independently" shape as PLAT-228/230. Added `validateValueTypePatternCompatibility` alongside the 3 existing write-time schema validators at all 4 call sites that accept a validation_schema/pre_validation payload. **Independent review** confirmed the fix and flagged two real gaps, both fixed: (1) `update_evaluation_plan`'s own write path called none of the 4 validators at all — closed via `validateSchemaLikeUpdateField`; (2) all 4 validators only walked `schema.Files`, never `schema.DB`, though `DBValidationRule.Checks` hits the identical runtime evaluator — closed via a shared `forEachSchemaCheck` iterator now used by all 4. 9 new tests total; full suite passes | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_agent.go`, `evaluation_helpers.go`, `evaluation_plan_tool.go`, `evaluation_plan_tool_test.go`, `value_type_pattern_compatibility_test.go` |
| [PLAT-237](pulse_platform/browser-automation/plat-237.md) | Twitter/social-media `PUL-7074FD09`, `PUL-5A977094`: Repost→Quote sometimes navigates to X's compose route but renders reply mode (no quote embed, disabled Reply control) instead of quote mode | Claude Code | `investigated — reclassified, no in-repo fix possible or needed` — `agent_browser click` on a menu item has zero Go-side handling, same passthrough boundary as PLAT-224/232; nothing in this repo influences which compose mode X's own SPA renders. The workflow's own `quote-tweet-execution.md` learnings already have a dedicated "Compose-mode guard" section matching this exact failure signature verbatim, and both findings' own evidence confirms the workflow already recorded them as skipped/blocked with real UI evidence, not fabricated success — mature, already-correct workflow behavior, not a platform gap | N/A — no files changed |
| [PLAT-238](pulse_platform/step-execution/plat-238.md) | Twitter/social-media `PUL-9780ADDE`: a step's DB validation invariant declared SQL `REGEXP`, and the managed `query_workflow_db` endpoint rejected every attempt with `no such function: REGEXP`, regardless of whether the underlying data satisfied it | Claude Code | `implemented; runtime reverify` — SQLite's `REGEXP` operator has no built-in implementation; it requires an application-registered `regexp(pattern, value)` scalar function, which this repo never registered on the `modernc.org/sqlite` driver despite importing it. Added a package-level `init()` in `workspace/handlers/query.go` registering a deterministic `regexp` function via `RegisterDeterministicScalarFunction`, applying process-wide to every future connection (`openQueryOnlyDB` and `openMutationDB` both covered with no other changes needed). NULL operands return NULL (matching SQLite comparison-operator convention); an invalid pattern surfaces as a query error. 3 new tests; full `workspace` module suite passes | `workspace/handlers/query.go`, `query_regexp_test.go` |
| [PLAT-239](pulse_platform/pulse-governance/plat-239.md) | Twitter/social-media `PUL-DA42695E`: `changed_unverified` disposition had a contradictory `next_check` requirement — the decoder rejected any placement of the field as `unknown field next_check`, while omitting it entirely was validator-rejected as required | Claude Code | `resolved` — `git log -S` confirmed commit `c60f1f830` ("Simplify Pulse review, repair, and workflow run lifecycle", 2026-08-28, one day before this session) removed the exact requirement block the finding's evidence quotes near-verbatim; the current validator has no `NextCheck` check for `changed_unverified` at all. Same "real when filed, already fixed via a same-era commit" shape as PLAT-228/230/237 | N/A — already fixed in `c60f1f830` |
| [PLAT-240](pulse_platform/pulse-governance/plat-240.md) | Twitter/social-media `PUL-3880D006`: `record_pulse_verification` reported success while silently preserving an older verdict — evidence pointed to a `UNIQUE(attempt_id,fingerprint,check_text)` collision the tool exposed no `check_text` parameter to avoid | Claude Code | `resolved` — `git log -S` confirmed the standalone `record_pulse_verification` tool was removed entirely in commit `d9223aa61` ("Simplify Pulse review and repair flow", 2026-08-29 00:57, hours before this session), part of the ongoing Pulse tool-surface consolidation (PLAT-220). Its replacement, `record_pulse_result`'s `finding_dispositions[].verification[]`, already uses a real `INSERT ... ON CONFLICT DO UPDATE` keyed on a caller-supplied non-empty `check_text` — the exact collision mechanism the finding describes cannot recur through it | N/A — tool removed in `d9223aa61`, replacement already correct; SQLite backlog reconciled 2026-09-05 — verified stale records resolved; see [audit](../audits/platform-backlog-reconciliation-2026-09-05.md), deployed verification not claimed |
| [PLAT-241](pulse_platform/scheduler-runs/plat-241.md) | Twitter/social-media `PUL-8E22A469`: per-step `pre_validation_*.json` timing documents keep encoding `run_folder=iteration-0/default` even after that run's folder rotates to a permanent iteration-N name | Claude Code | `root cause confirmed live — fix design specified (corrected), implementation deferred` — confirmed against a real archived file (iteration-294) that this is current, not stale. Three sibling "archive on rotation" functions already exist for other data surfaces (cost, evaluation scores, schedule history) hit by the identical mechanism; per-step timing docs are a fourth surface never given the same treatment. `get_cost_summary`, the consumer this finding actually exercised, is already correct. **Correction:** the first fix design copied the cost-ledger convention of adding a parallel `archived_run_folder` field, but no Go-side consumer of `run_folder` exists on this struct at all — an additive field nobody is required to prefer would reproduce the same bug one field over. Corrected to overwrite `run_folder` in place, since (unlike cost/eval records) there is no immutable-record constraint here. Deferred rather than rushed: critical run-rotation path, thin-to-zero sibling test coverage, one directory level deeper than the existing pattern | N/A — no files changed, fix design left in the ticket |
| [PLAT-242](pulse_platform/scheduler-runs/plat-242.md) | Historical failed runs were attributed to a current schedule after iteration rotation (PUL-6403A889, PUL-BA1C940F) | Codex | `resolved locally` — execution timestamps now override renamed folders; focused regression tests pass. Internal closure 2026-09-05; changes uncommitted and deployment verification pending. Legacy timestamp-less fallback and general concurrent identity remain outside this fix | `agent_go/cmd/server/scheduler.go`, `scheduler_test.go` |
| [PLAT-243](pulse_platform/evaluation/plat-243.md) | LinkedIn `PUL-90D1E2C9`: the shared evaluation-report aggregator lost a valid 6/10 `eval-strategy-loop` score — `context_output.json` correctly held score/max_score, but `evaluation_report.json` recorded `score=0, score_captured=false, "No output_content found for this step"` | Claude Code | `implemented; runtime reverify` — confirmed live: `eval-strategy-loop`'s real `context_output.json` legitimately nests a `files` array shaped exactly like a validation schema (self-documenting its own checks) alongside its real score/reasoning/evidence. `isValidationSchemaLikeJSON`'s purely structural check classified the whole real, scored document as a validation-schema echo, so `extractEvalVerdictFromOutputContent` never ran for it. Added one guard: a document with its own top-level `score` key is never treated as a schema echo, regardless of what else it nests. New test reproduces the exact linkedin shape; full suite passes | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/evaluation_execution.go`, `evaluation_output_content_test.go` |
| [PLAT-244](pulse_platform/security-sandbox/plat-244.md) | Retired provider-media tools could be reintroduced through legacy `read_image` definitions/executors and `workspace_image*` categories despite being hidden from the primary registry | Codex | `implemented; runtime reverify` — removed shared-agent media definition/executor/category escape hatches, dropped legacy workflow category entries, narrowed active guidance and capability discovery to text generation and hosted-MCP web search, and removed frontend image/media configuration, ElevenLabs/Deepgram discovery, and unused media cost registration. Family Server upload image reading, Pi text sub-provider routing, and the separate Video product remain intentionally out of scope | registry/category/prompt wiring; frontend model provider/config state; shared query/workflow config; capability discovery |
| [PLAT-245](pulse_platform/pulse-governance/plat-245.md) | LinkedIn's `1.0.32` Pulse lifecycle upgrade was schema-rejected because the scheduler-generated calls omitted the required `workspace_path`; the same defect affected the `1.0.33` and `1.0.34` prompts | Codex | `implemented; scheduler restart and LinkedIn retry required` — bind the exact scheduled workflow path into both `record_pulse_migration_reconciliation` and `get_pulse_state` before delivering each migration turn; status output shows the same concrete calls. All three rungs have regression coverage and `go test ./cmd/server/...` passes | `agent_go/cmd/server/workflow_version_upgrades.go`, `scheduler.go`, `workflow_contract_upgrade_status.go`, `workflow_schedule_contract_test.go` |
| [PLAT-246](pulse_platform/learnings-knowledge/plat-246.md) | Workflow Builder exposed `generate_text_llm` and `search_web_llm` but its active-tools reference provided signatures rather than actionable scripted-use, tier-selection, bridge-safety, and failure/evidence guidance | Codex | `implemented; runtime reverify` — active-tools guide now carries use/tier rules and bridge-only scripted calls; compact prompts require both the active-tools and bridge references; capability-scoped regression tests pass | `agent_go/cmd/server/guidance/templates/system/workspace-media-tools.md`; `pkg/instructions/workspace_special_tools.go`; guidance and workflow prompt tests |
| [PLAT-247](pulse_platform/integrations/plat-247.md) | PLAT-244's media-tool retirement swept `read_image` out alongside the generation tools with no stated reason; a workflow-step coding agent lost the ability to read a screenshot for an ordinary CSS debugging task | Claude Code | `implemented` — restored `read_image`/`image_gen`/`image_edit` (definitions, executors, categories, folder-guard classification, guidance, including the pre-existing native-CLI-provider routing via codex-cli/cursor-cli/claude-code); video/audio/music generation stay retired | `pkg/workspace/advanced_tools.go`, `tools.go`; `cmd/server/virtual-tools/workspace_tool_registry.go`; `pkg/common/tool_categories.go`; `cmd/server/server.go`, `instructions.go`; `pkg/orchestrator/base_orchestrator_folder_guard.go`, `base_orchestrator_tools.go`; `interactive_workshop_manager.go` |
| [PLAT-250](pulse_platform/frontend-chat/plat-250.md) | A chat tab left open across a server restart polled forever with no visible activity, until manually refreshed — reported live by the user, blocking real work in two separate workflows | Claude Code | `implemented; runtime reverify` — the session event store is in-memory only; a restart wipes it, but an already-open tab's `lastEventIndex` survives in the frontend store, permanently past the end of the fresh process's much shorter post-restart event log. The backend already signals this exactly (`LastProcessedIndex: -1` for `!exists`), but `processEventsResponse` never handled it — added a reset-and-resync path when a tab that previously tracked real progress (`priorIndex > 0`) receives `-1`. Confirmed live: the backend fully completes turns even on the stale tab (`live-input` succeeds, turn streams and ends clean) — only the tab's own polling loop could never see any of it, matching "it shows after i refresh page" exactly. `npx tsc --noEmit` and `npm run build` both pass | `frontend/src/components/ChatArea.tsx` |
| [PLAT-251](pulse_platform/frontend-chat/plat-251.md) | A sent live-input message was echoed twice in the Workflow Builder: once as a real chat bubble, once as a composer "Sent to X" banner underneath it — reported live by the user with a screenshot | Claude Code | `implemented; runtime reverify` — two independent, un-deduplicated rendering paths for the same submission: `ChatArea.tsx` appends a real optimistic chat bubble for `delivery_status` `sent_to_cli`/`next_turn_started` on every surface, while `ChatInput.tsx`'s composer banner only suppressed itself for that case on product-surface chat, not the Workflow Builder. Narrowed the suppression to both surfaces for those two statuses; queued/local-save/sending/failed states, which never get a bubble, still show the banner unchanged. `npx tsc --noEmit` and `npx eslint` both clean | `frontend/src/components/ChatInput.tsx` |
| [PLAT-252](pulse_platform/frontend-chat/plat-252.md) | A scheduled/bot-run tab whose session data never arrives (backend event store gone, or the initial fetch failed and was silently swallowed) was stuck on "Restoring previous session..." forever, with no escape — reported live by the user with a screenshot of a `full-run [default / iteration-0]` tab | Claude Code | `implemented; runtime reverify` — `resolveChatSurface.ts`'s `isReadOnlyRunView` deliberately forces `'restoring'` while a scheduled/bot-run tab has no content (so it never bounces to the landing panel), but unlike a normal resumed tab's `resumeGaveUp`, it had no give-up timer at all. Added a matching `readOnlyRunViewGaveUp` timer in `ChatArea.tsx`: past the timeout, the spinner is replaced by an explicit "Couldn't load..." message with a "Try again" button that re-fetches the session's events; `resolveChatSurface`'s returned surface and its landing-avoidance guarantee are unchanged. `npx tsc --noEmit` and `npx eslint` both clean | `frontend/src/components/ChatArea.tsx` |
| [PLAT-253](pulse_platform/frontend-chat/plat-253.md) | A Pulse "Needs your decision" card's context body rendered raw markdown syntax (e.g. literal `## Post`) instead of formatted text — reported live by the user with a screenshot | Claude Code | `implemented; runtime reverify` — `ReportHumanInputPanel.tsx`'s `HumanInputContext` rendered `section.body` as plain `<p className="whitespace-pre-line">` text, even though the same "decision/feedback context" pattern elsewhere in the codebase already runs this kind of text through a markdown renderer. Switched to `PlainMarkdown` (the lighter, no-store-dependency renderer meant for read-only operational text); numbered `section.items` left untouched, already correct via a plain `<ol>`. `npx tsc --noEmit` and `npx eslint` both clean | `frontend/src/components/workflow/ReportHumanInputPanel.tsx` |
| [PLAT-254](pulse_platform/frontend-chat/plat-254.md) | Execution Logs panel showed misleading recency data — a step's model chip picked the older of two attempts because "Attempt N" is a fixed retry-slot label, not chronological order (confirmed live: attempt 2 was ~11h *older* than attempt 1); every validation entry was labeled "Validation attempt 1" regardless of which of 5 real, distinct phases/executions it was; no datetime shown anywhere; `execute_step` never stated which run_folder/group it resolved to — reported live by the user across several screenshots while debugging a real workflow | Claude Code | `implemented` — `getStepModel()` now compares real `completed_at`/`started_at` timestamps instead of array position; attempt and validation rows show real datetimes; validation labels use `validation_phase`/`execution_attempt`; `execute_step`'s response states its resolved `run_folder`; the panel's own refresh now also refreshes the run-folder list itself (previously only the outer canvas refresh did). Also: the "FULL CALL TIMELINE" summary rows became expandable to show real tool-call args/responses, and the "Back to all steps" sticky header shrinks on scroll (with hysteresis, after a flicker report). Two live-checked "staleness" reports turned out to be the panel already being accurate — recorded so they aren't re-litigated | `frontend/src/components/workflow/ExecutionLogsPopup.tsx`, `ConversationViewer.tsx`, `canvas/WorkflowCanvas.tsx`; `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go` |
| [PLAT-255](pulse_platform/evaluation/plat-255.md) | Pre-validation failures were silent to the builder until all retries exhausted, or invisible entirely if a later retry recovered — operator wanted early visibility to distinguish a real bug from a transient/schedule issue while a step is still retrying; separately, the `prompt-engineering` reference skill's name didn't match its actual step-description/validation_schema-authoring content | Claude Code | `implemented` — one `[AUTO-NOTIFICATION]` per step per run on the first pre-validation failure (regular/todo_task steps), before retries exhaust; re-enabled the previously-silenced per-item message_sequence pre-validation notification (operator explicitly accepted the cost); `running-steps.md` updated so the builder agent knows this is an early signal, not a final outcome. Renamed `prompt-engineering.md` → `step-description.md`; added a `read_skill(...)` hint to `add_*_step`/`update_*_step` responses — caught and fixed a wrong first draft that used `get_workflow_command_guidance`, a tool that only serves a separate registry (`allKinds`) and would have returned "unknown kind" | `pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`, `controller_todo_task.go`, `controller_message_sequence.go`, `planning_agent.go`; `cmd/server/guidance/guidance.go`, `templates/system/running-steps.md`, `templates/system/plan-design.md`, `templates/system/step-description.md` (renamed) |
| [PLAT-256](pulse_platform/frontend-chat/plat-256.md) | Platform feature (not a bug): in-app HTML reports were read-only, with no way for a report to write back to its own workflow data — requested live by the user for a "show generated emails, approve inline" use case that Pulse's decision-card mechanism doesn't scale to | Claude Code | `implemented; runtime reverify` — added `window.report.updateField`/`updateFields`, backed by a new structured `/api/report-field` endpoint that validates table/column against the live schema (rejects platform-reserved tables and any column that identifies or timestamps the row) and matches the row on its own primary key — no caller-supplied SQL. Multi-field writes are atomic (all-or-none) in one transaction alongside a durable per-column audit log. 6 new tests cover every guard, including a mixed valid+guarded call proving no partial write; `go build`/`go test`/`tsc`/`eslint`/frontend build all clean | `workspace/handlers/query.go`, `workspace/models/query.go`, `workspace/server.go`; `frontend/src/services/api.ts`, `frontend/src/components/workflow/reportWidgets/{reportEmbedContext,HtmlWidgetFrame}.tsx`, `frontend/src/components/workflow/ReportViewer.tsx` |
| [PLAT-257](pulse_platform/learnings-knowledge/plat-257.md) | Step learnings/knowledge access contract diverged across prompts, filesystem guards, execution-agent retries/recovery, and Builder guidance | Codex | `implemented; runtime reverify` — one resolver now governs prompt and filesystem learning reads across regular, scripted, todo, message-sequence, retry, and recovery paths; evaluation suppresses learnings without changing configured DB/KB behavior; stale KB default descriptions now match runtime `read` semantics; focused package tests pass | `controller_learning_helpers.go`, execution/factory/scripted/todo/message-sequence/recovery call sites, Builder schema/source comments, access-contract tests |
| [PLAT-258](pulse_platform/pulse-governance/plat-258.md) | Multi-phase platform feature (not a single bug): plan-step edits regularly leave DB access/tables, learnings, KB, description, or validation_schema silently drifted; the existing `/review-artifact-drift` mechanism is real but confirmed unexercised (zero invocations in ~2 months of logs) — requested live by the user, who correctly suspected the mechanism and PLAT-197's prior gating fix don't actually close the loop | Claude Code | `all acceptance items built, two self-found integration gaps fixed` — fourth/fifth rounds fixed six independent-review integration gaps in the review-and-fix redesign; sixth round built deletion coverage (workflow-level `WorkflowDriftReviewStepID` sentinel) and slash/scheduled parity (`/review-artifact-drift` Part 1 dispatches plan_drift_review's real collector/repair/completion writer). Seventh round fixed two gaps in the sixth round's own new mechanisms: (1) Part 1's `record_pulse_result(module="plan_drift_review")` was itself broken — that write is due-gated on a Gate-recorded worklist row for the exact pulse_run_id, which a standalone slash session never has; new `record_pulse_module_due` tool lets a manual invocation establish its own due claim first, refusing (rather than corrupting) when a real scheduled Pulse pass is concurrently mid-flight for the same module, since `pulse_module_state` is one row per module per workspace, not scoped per run; (2) the deletion trigger/evidence pair was best-effort with no retry — fixed by retrying the workflow-level flag write once (matching the established `clearDriftReviewAfterPlanUpdateRetried` pattern) and surfacing a persistent failure loudly in `delete_plan_steps`'s own response rather than only logging it. `go build`/`go vet`/`gofmt` clean; full `go test ./...` green, no failures. See plat-258.md's "Seventh round" section for full detail | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/{planning_agent.go,planning_graph_integrity_test.go,planning_agent_artifact_review_test.go,interactive_workshop_manager.go}`, `agent_go/cmd/server/{pulse_worklist.go,pulse_worklist_test.go,pulse_tool_surface_test.go,guidance/templates/review/review-artifact-drift.md}` |
| [PLAT-259](pulse_platform/step-execution/plat-259.md) | Platform feature (phase A + phase B corrective work landed, acceptance items resolved, not a bug): today's single `routing` step is used interchangeably for two different needs — a small in-flow "if this then step A" decision, and what should really be a major, self-contained sub-workflow fork — user-initiated design discussion, not an incident | Claude Code | `third independent review's findings resolved` — the canonical workshop prompt and every named reference doc (`plan-design.md`, `planning-steps.md`, `message-sequence.md`, `regular.md`, `workflow-tools.md`) now offer `branch` as the fixed-choice alternative to `routing`, locked in by two new guidance-contract tests; Execution Logs (`handleGetExecutionLogs`/`ExecutionLogsPopup.tsx`) now reports a branch run's real step type instead of hardcoding `routing`; the platform's generic plan add/update HTTP API (`handleAddStep`/`updateStepInPlan`, distinct from the Builder-native tools) now accepts branch steps. Frontend per-route reporting is now implemented (2026-08-31) across Execution Logs, Costs, and Evaluation — `collectSelectedRoutes`/`computeRouteMembership` in `workflow.go` tag every step reached through a run's actually-selected route (not just the plan's declared routes), and all three UIs filter/group by it; the live manual reverify against a real workflow run remains the only open item; added two TEMPORARY operator diagnostic slash commands (`/verify-branch-step`, `/migrate-routing-to-branch`) at the user's request to actually close the live-reverify item, to be removed once used; two more P2 findings fixed: Execution Logs now persists `step_type` into `routing-evaluation.json` at execution time and prefers it over the current plan.json (a reclassified step's historical runs no longer relabel), and a new permanent `convert_routing_branch_step_type` tool atomically relabels a step in place (same id, same step_config.json row) instead of the flawed delete-and-recreate procedure the migration command originally claimed preserved history — `routing`/`RoutingPlanStep` stays completely unchanged in code (zero migration risk) and is reinterpreted going forward as the "route" (major fork) concept; new `branch` step type (distinct `BranchPlanStep` Go struct, distinct `add_branch_step`/`update_branch_step` tools) shares the exact same executor via a new `routeSwitchStep` interface. An independent review found several *other* `*RoutingPlanStep`-only switches never migrated to the interface (canonical plan validation, `populateRuntimeFields`, `ApplyStepConfigFromFile`, `getAgentConfigs`, post-execution navigation, `validateNextStepIDReferences`, route-scoped validation, nested sub-agent identity normalization) — the corrective audit re-grepped every file containing `RoutingPlanStep` (not just the `StepTypeRouting` string, which the original phase A audit had grepped and which misses concrete type assertions) and fixed every site, plus `setStepIdentity` (found by this audit, implied but not named by the review) and `mergePartialStepUpdate`/`PartialPlanStep.BranchQuestion` (a bug this session's own earlier phase A work introduced — `update_branch_step` silently no-op'd on any field update — caught during the re-check, not by the review's text). Added `TestBranchStepEndToEndLifecycle` (adds a branch, validates, round-trips, applies config, resolves navigation for a selected route — the exact lifecycle the review said the original 7 tests didn't exercise) and `TestBranchStepDanglingNextStepIDCaughtByValidation`/`TestSetStepIdentityAcceptsBranchStep`. Also fixed two real frontend UI bugs found while auditing "where the plan itself is displayed" per user request: `RoutingStepNode.tsx`'s untitled-step fallback label and `WorkflowCanvas.tsx`'s node-inspector section title both said "Routing" unconditionally even for a branch step. New `branch.md` guidance doc plus updated `routing.md`/`plan-design.md`; frontend recognizes/renders/excludes branch alongside routing (reusing existing node/edge components, distinct cyan Execution Logs badge). Phase B: asked the user directly whether the two route best-practice checks should be a deterministic Go graph-traversal or an LLM judgment check — user chose judgment check, matching their original "not enforceable in golang code" preference; `plan_drift_review` (PLAT-258)'s `PlanDriftCandidate` gained a precomputed `StepType` (reads plan.json alongside step_config.json) and `plan-drift-review.md` gained two new Group-3 judgment checks gated to `step_type=="routing"` only (`route_structural_isolation`, `route_eval_pairing` via the already-real `applies_to_routes` eval field) — extends PLAT-258's infrastructure rather than a second mechanism, per plan. `go build`/`go test`/`gofmt`/`go vet`/`tsc`/`npm run build` all clean; 11 tests in `controller_branch_test.go` plus 2 in `plan_drift_candidates_test.go`; three pre-existing unrelated `cmd/server`/`cmd/server/guidance` test failures and pre-existing `go vet` findings all confirmed reproducible with this ticket's changes fully stashed out | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/{planning_agent.go,controller_routing.go,controller_routing_deterministic.go,controller_execution.go,controller_todo_task.go,planning_exports.go,planning_management.go,step_config.go,interactive_workshop_manager.go,controller_branch_test.go,integrity_hardening_test.go,plan_drift_candidates.go,plan_drift_candidates_test.go,plan_drift_review_tool.go,plan_drift_review_tool_test.go,interactive_workshop_manager.go}`, `cmd/server/{toolset_invariant_test.go,workflow.go,workflow_execution_logs_test.go,workflow_branch_step_api_test.go,guidance/guidance.go,guidance/branch_step_type_distinction_test.go,guidance/templates/system/{branch.md,routing.md,plan-design.md,plan-drift-review.md,planning-steps.md,message-sequence.md,regular.md,workflow-tools.md},guidance/templates/review/{verify-branch-step.md,migrate-routing-to-branch.md}}`; `frontend/src/utils/{stepConfigMatching.ts,evaluationReport.ts}`, `frontend/src/services/api-types.ts`, `frontend/src/components/workflow/{ExecutionLogsPopup.tsx,LearningsView.tsx,CostsPopup.tsx,EvaluationPopup.tsx,canvas/WorkflowCanvas.tsx,hooks/usePlanToFlow.ts,nodes/{index.ts,RoutingStepNode.tsx},edges/index.ts}`, `frontend/src/commands/builtin-commands.tsx` |
| [PLAT-260](pulse_platform/pulse-governance/plat-260.md) | Pulse needs to govern learning/KB write yield, but the original proposal added lifecycle ownership around overlapping locks | Codex | `superseded by PLAT-263` — Pulse may make mature/no-op contributors read-only and restore write access on drift; no separate learning or workflow-wide KB lock lifecycle remains | PLAT-263/265 access-only store ownership model |
| [PLAT-261](pulse_platform/security-sandbox/plat-261.md) | Workflows cannot receive explicit access to user-selected folders outside `workspace-docs`, apart from narrowly managed exceptions such as CDP host Downloads access | Unassigned | `fixed` — persists approved, host-local `folder_access` grants in `workflow.json` (stable id, alias, canonical path, read_only/read_write); trusted UI approval flow (native picker or user-supplied exact path) with Builder inspect/request tools only, no self-authorization; FolderGuard snapshot propagates through session creation and child-session inheritance for `execute_shell_command` and a new authenticated external `diff_patch_workspace_file` route; canonicalization, traversal/symlink rejection, and read-only write denial enforced in both bridge validation and the OS sandbox (macOS sandbox + Linux Landlock). No basic file tool (`read_workspace_file` etc.) was restored. Renumbered from a duplicate PLAT-247 assignment during a concurrent-session merge — PLAT-247 was already in use for the `read_image`/`image_gen`/`image_edit` restoration ticket | `agent_go/pkg/workspace/{advanced_tools.go,client.go,diff_patch_workspace_file.go,diff_patch_workspace_file_test.go,execute_shell_command_test.go}`, `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/{workspace_path_policy.go,workspace_path_policy_test.go}`, `agent_go/cmd/server/{server.go,workflow_manifest.go,workflow_manifest_routes.go,workflow_manifest_test.go}`, `workspace/handlers/{shell.go,shell_env_test.go,external_diff_patch.go,external_diff_patch_test.go}`, `workspace/security/{isolator.go,isolator_test.go,isolator_linux_test.go}` |
| [PLAT-262](pulse_platform/security-sandbox/plat-262.md) | Platform feature (design agreed, not a bug): the user asked whether AgentWorks has an admin-vs-read-only user model — it does not; authentication supports multiple users but there is no authorization boundary once logged in. User wants a genuine read-only user | Claude Code | `implemented, live-verified locally (WorkshopMode is the single gate for tools/prompt/skills; two real folder-guard bugs found+fixed and one dead security check removed during live testing — see ticket)` — restores the `read` permission tier in `workflow_permissions.go` that was deliberately removed 2026-08-17 (never configured in any deployment, and its removal cleaned up a dead workshop-mode query-access gate that cost real debugging time in PLAT-125), wired to concrete enforcement this time. Original design forced `WorkshopMode="run"` server-side for read-access users and gated Run mode's tool set; found mid-implementation that a dedicated design doc (`docs/design/agent_tool_surface_single_source.md`) deliberately removed mode-based tool filtering after 3 real production incidents (a coding CLI caches its tool catalog once at launch; a tool silently vanishing from a live session's catalog is unrecoverable, unlike a clean call-time rejection). First correction (RCA #1): gated the mutating tool subset on `WorkflowAccessLevel` directly instead of `WorkshopMode` (matching `prepareReadOnlyBackgroundAgentTools`'s precedent), keeping `WorkshopMode` prompt-only/untouched. Second correction (RCA #2), after the user reviewed a real read-only session and found the generic Builder prompt misleading: found there is no live UI path for a normal user to toggle `WorkshopMode` at all (only Bot Connector routes, the scheduler, and the agent-profile runtime ever set it to `run`), so RCA #1's same-session-toggle risk didn't actually apply — `WorkshopMode` became the single gate for tools/prompt/skills, with `WorkflowAccessLevel` doing exactly one thing: force-pinning a read-only identity's session to `WorkshopMode="run"` every turn. Shell/file write paths stay keyed on `WorkflowAccessLevel` directly (explicitly out of scope for RCA #2 — would also cut Bot Connector write access). No new user-creation mechanism needed (existing `AUTH_USERS` + `WorkflowAccessPopup.tsx`/`WORKFLOW_USER_PERMISSIONS` infrastructure just gains a third `read` option) | `agent_go/cmd/server/{workflow_permissions.go,workflow_permissions_test.go,server.go,workflow_phase_tools.go,secrets_tools.go,agent_profile_workflow.go,tool_setup.go}`, `agent_go/pkg/common/types.go`, `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/{interactive_workshop_manager.go,interactive_workshop_manager_test.go,planning_exports.go,workshop_readonly_gate_test.go,prompt_size_test.go,controller_agent_factory_test.go}`, `frontend/src/{components/workflow/{WorkflowAccessPopup.tsx,WorkflowChatTabs.tsx,WorkflowCapabilitiesPanel.tsx},components/ModePresetBar.tsx,components/scheduler/WorkflowScheduleRunsPanel.tsx,utils/workflowPermissions.ts,services/api.ts}` |
| [PLAT-263](pulse_platform/learnings-knowledge/plat-263.md) | Per-step `lock_learnings` duplicated `learnings_access`, created contradictory UI/API/runtime paths, and required a separate lifecycle for a state already represented by `read` | Codex | `implemented` — `learnings_access` is the sole learning permission; legacy true locks migrate to `read` without widening explicit `none`; tool/UI/runtime metadata and the obsolete 1.0.22 audit were removed; `lock_code` remains | step-config read/repair migration; Builder and HTTP config surfaces; runtime and guidance |
| [PLAT-264](pulse_platform/integrations/plat-264.md) | Org Dashboard parsed stale optional Builder HTML cards, then scheduled child sessions lost the trusted workspace destination required to persist current summaries | Codex | `parent-to-child notification handoff fixed; server verification passed; live scheduled-run/UI reverify pending` — added the durable provider and now clones complete backend-owned routing through schedule → group → message-sequence/tool sessions; agent headers remain non-authoritative | notification registry/manager/provider, workflow child-session creation, org-dashboard API and frontend |
| [PLAT-265](pulse_platform/learnings-knowledge/plat-265.md) | Workflow-wide `lock_knowledgebase` overlapped with per-step KB ownership, described a retired writer architecture, and could suppress unrelated legitimate contributors | Codex | `implemented` — removed the global lock from runtime/session state, Builder config/status/prompts, and current guidance; KB writes are governed only by each step's `knowledgebase_access` plus `knowledgebase_contribution`; legacy manifest fields are consumed and removed during normal config normalization | workflow config normalization; workflow/controller/session propagation; Builder tool surface and guidance |
| [PLAT-266](pulse_platform/security-sandbox/plat-266.md) | The mount-namespace Landlock fallback (used when Landlock itself rejects a policy shape it can't express) assumed every guarded path was a directory; bind-mounting a plain file onto the freshly-`mkdir -p`'d directory target failed with `mount(2): Not a directory`, aborting every `execute_shell_command` call in scope, unconditionally — found live on Dominion via a scheduled step's own correct self-diagnosis | Claude Code | `fixed, live-verified on the Dominion deployment` — placeholder creation (directory vs. touched file) now happens in one pass for every configured path strictly before any bind-mount runs, fixing both the file-vs-directory bug and a related read-only-parent-blocks-touch ordering bug found while testing | `workspace/security/{isolator.go,isolator_linux_test.go}` |
| [PLAT-267](pulse_platform/security-sandbox/plat-267.md) | Scheduled runs resolved workflow secrets against a hardcoded empty/`"default"` user identity instead of the workflow's real owner, so every scheduled run of an affected workflow silently lost access to its own configured secrets (`KeyError` on API keys) while the identical workflow worked fine driven interactively — found live on Dominion, confirmed for two consecutive days | Claude Code | `fixed, live-verified on the Dominion deployment` — added `WorkflowManifest.CreatedBy` (stamped at creation from the authenticated request) and threaded it through `ScheduleContext.OwnerUserID` into both `startSessionInternal` scheduled call sites, replacing the hardcoded `""`; existing production workflow manually backfilled with its real owner's user ID | `agent_go/cmd/server/{workflow_manifest.go,workflow_manifest_routes.go,scheduler.go,scheduler_test.go}` |
| [PLAT-268](pulse_platform/frontend-chat/plat-268.md) | Workflow Builder chat could show only the user's message while its retained tmux produced normal assistant output and tool calls | Codex | `implemented; restarted UI reverify pending` — every submitted foreground turn now has a lightweight REST event catch-up loop alongside SSE; event IDs dedupe concurrent delivery and terminal/stopped sessions stop the timer | `frontend/src/components/ChatArea.tsx`; foreground session event transport |
| [PLAT-269](pulse_platform/step-execution/plat-269.md) | `todo_task` carried a second 2000-line turn loop that duplicated the message_sequence executor (PLAT-189 and PLAT-255 each had to be fixed twice), a scripted fast path no live workflow used whose own eligibility rule described work that should not be an orchestrator, and dead todo-list tools/events that gave the step its name | Claude Code | `implemented, pushed, live e2e verified 2026-09-02` — scripted orchestrator path removed; dead todo list deleted and `todo_id` renamed `task_id`; the orchestrator runs on the message_sequence executor through a delegation seam (routes, async child reconcile after every item, narrower folder guard, todo_task log shape kept for the UI); plan type renamed `orchestrator` with `todo_task` read alias, tool/drift/guidance renames, contract v1.0.35 `migrate_orchestrator_step_type` | `controller_todo_task.go`, `controller_message_sequence.go`, `sub_agent_tools.go`, guidance `todo-task.md` |
| [PLAT-270](pulse_platform/pulse-governance/plat-270.md) | A workflow could declare `db-sqlite` backup coverage even though agents are correctly forbidden from reading the live DB/WAL files; the resulting partial backup also suppressed an otherwise valid publish | Codex | `implemented; runtime reverify pending` — trusted WAL-aware, integrity-checked managed snapshot at `backup/database/db.sqlite`; scheduled/manual finalization prepares it deterministically; backup failure no longer suppresses publish or notify | workspace DB snapshot endpoint/client/tool; scheduler pre-finalizer hook; backup/finalizer guidance and tests |
| [PLAT-271](pulse_platform/pulse-governance/plat-271.md) | `get_pulse_state(view=module)` and the scheduler's fast-Pulse poll returned `SQLITE_BUSY` for ~10 minutes on confida-login while `view=backlog` and `query_workflow_db` succeeded against the same file, so the Gate skipped the whole review/fix layer of that pass. The reads were not reads: every schema-ensure helper on the path (`ensureReportHumanInputSchema` on every open, `ensurePulseModuleStateSchema`'s merged-module/focus-catalog migrations, and the lifecycle/review-log ensures) ran idempotent `UPDATE`/`DELETE`/`INSERT..SELECT` migrations that still take SQLite's write lock in WAL mode even when they match nothing, ×5 opens per `view=module` call, so any real writer holding the lock past `busy_timeout` failed them all (finding `ce45ba95` / PUL-7774A6D0; reproduced against SQLite directly) | Claude Code | `fixed` — every no-op migration on a read path is probed with a `SELECT` first (`sqliteExecIfRows` / `pulseExecIfRows`); migration semantics unchanged when legacy rows exist (existing migration tests pass unmodified); two new tests hold `BEGIN IMMEDIATE` on a second connection and assert the reads complete (fail-before: 5 s busy_timeout then error; pass-after: instant). Live reverify pending on the next confida-login Pulse pass | `agent_go/cmd/server/sqlite_write_guard.go`, `report_human_inputs.go`, `pulse_worklist.go`; `step_based_workflow/pulse_write_guard.go`, `pulse_finding_lifecycle.go`, `pulse_review_log.go` |
| [PLAT-272](pulse_platform/security-sandbox/plat-272.md) | Workflow secrets were stored per user (`_users/<uid>/workflow_secrets/`, ciphertext bound to that user's ID) and the three `/api/secrets/workflow/*` routes had no per-workflow gate, so a read-only reviewer, a co-owner, or a scheduled run under any other identity resolved empty `$SECRET_*` values — raised live by the user while moving `rtslatency` to RTS ("these are workflow secrets, should be accessible across users") | Claude Code | `implemented; RTS live reverify pending` — one shared document per workflow at `_users/_shared/workflow_secrets/<hash>.json` (reserved pseudo-user, so every existing `_users/` guard covers it by construction), ciphertext bound to the workflow path; store/delete/reveal owners-only, list visible-to-readers names-only; `loadSelectedSecrets` reads the shared document so every identity resolves the same value; lazy one-shot migration of legacy per-user entries (requester, creator, owners); Builder tools and the Secrets pane follow (read-only users see names, controls disabled). Reusable user secrets, provider credentials and globals unchanged | `agent_go/cmd/server/{workflow_shared_secrets.go,workflow_shared_secrets_test.go,secrets_routes.go,secrets_tools.go,server.go}`, `agent_go/pkg/chathistory/shared_secrets.go`, `frontend/src/{api/secrets.ts,components/secrets/SecretSelectionSection.tsx}`, `.gitignore` |
| [PLAT-273](pulse_platform/coding-agent-bridge/plat-273.md) | A chat message sent into a large (~735K-token) Pi CLI session came back `409 Conversation Error` from both `/live-input` and the `/api/query` fallback, was not recorded, and its reply never surfaced — while Pi's own marker stream shows it received the message **four times** per attempt and answered. `ensurePiInputSubmitted` judged the send only from the tmux pane with a 1.5 s budget; on a big session the status line still reads `idle` until the first model event and the submitted text is echoed inside the 24 lines the adapter treats as the editor, so "idle + draft visible" held past the deadline and each retry layer above re-typed the message (confida-login, 2026-09-03 13:21/13:24 IST, operator-reported) | Claude Code | `fixed` — the send is now confirmed from the bundled extension's `message_end{role:user}` acknowledgement, read from a marker offset snapshotted before typing (so an earlier identical message cannot confirm a new one), with the pane heuristics kept as the fast path and unchanged for panes without a marker file; fail-before/pass-after tests; needs a dev-server rebuild+restart (the Pi pane survives it) | `multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`, `picli_submit_confirmation_test.go` |
| [PLAT-274](pulse_platform/coding-agent-bridge/plat-274.md) | On a deployment locked to Cursor (RTS), the structured `--print` launcher never wrote the `Mcp(<server>:*)` allowlist `cli.json` the tmux path projects, so headless cursor-agent auto-rejected every non-read-only bridge tool call (`User rejected MCP: api-bridge-execute_shell_command`); the same transport rendered thinking as one card per token fragment, doubled every streamed sentence (assembled span repeated after its fragments, distinguishable only by `timestamp_ms`), and showed a denied built-in `read` as ✓ | Claude Code | `fixed` — allowlist projected (multi-llm `4b7b02f`), thinking deltas flagged and folded into a collapsible block (`8dc96d6`, mcpagent `59dd7ae`, agent_go `a4a65103c`/`18c0da5a9`), assembled repeat dropped (`063aedd`), error envelopes classified (mcpagent `f7b6927`); live-verified on RTS 2026-09-03 | `multi-llm-provider-go/pkg/adapters/cursorcli/cursorcli_structured_adapter.go`, `mcpagent/toolerr/toolerr.go`, `frontend/src/utils/thinkingDeltas.ts` |
| [PLAT-275](pulse_platform/coding-agent-bridge/plat-275.md) | Every agent start retried an MCP connector whose user had no OAuth login through two accidentally nested retry loops (3 inner × 4 outer with both backoffs) — ~19 s of pure sleep per pass, twice per Builder turn, 38 of a 52 s `hi` on RTS before the model launched | Claude Code | `fixed` — missing OAuth token is non-retryable and `ConnectWithRetry` no longer nests `Connect`'s loop (mcpagent `13dabbb`, `b39c978`); live pre-launch time 38.4 s → 0.4 s; follow-ups listed in the ticket | `mcpagent/mcpclient/client.go` |
| [PLAT-276](pulse_platform/security-sandbox/plat-276.md) | A secret created by `set_workflow_secret` reached workflow steps immediately but not the chat agent's own `execute_shell_command` in the same turn — the shell client cloned its env at turn start, and the executor factory's comment claiming in-place propagation had been false since the clone was introduced | Claude Code | `fixed` — locked `SetExtraEnv` on the workspace client, per-session live-client registry, secret hooks push/remove `SECRET_<name>` (agent_go `f732a0939`); unit-verified under the race detector, live reverify pending | `agent_go/pkg/workspace/client.go`, `agent_go/cmd/server/virtual-tools/workspace_advanced_tools.go` |
| [PLAT-277](pulse_platform/frontend-chat/plat-277.md) | After every deploy, open tabs failed their next lazy import with `Failed to fetch dynamically imported module` (previous build's hashed chunk names) and showed "Something went wrong" | Claude Code | `fixed` — once-per-session auto-reload on that error and previous-release assets carried into each new release (agent_go `9d48c370c`); verified on RTS release `9d48c370c-20260903135124` | `frontend/src/utils/staleChunkReload.ts`, `deploy/aws-ec2/deploy-rootless.sh` |
| [PLAT-278](pulse_platform/frontend-chat/plat-278.md) | The workflow agent could describe the 21-view workspace toolbar but never open one, refresh a view it had just changed, or point inside one; a step run also vanished into a tool chip and the composer switched off mid-turn | Claude Code | `implemented` — `open_workspace_view` / `refresh_workspace_view` with an optional per-view `target`, a `workspace-views` reference doc, a run activity row, and a composer that stays usable during a run (agent_go `c94f21123`, `6ab127a58`); `report:focus` unverified until a report implements the listener. 2026-09-05 Codex follow-up locally implemented/tested: Pulse decision-card discoverability and immediate refresh from saved chat decision receipts; deployment and live verification pending | `agent_go/cmd/server/workflow_view_tool.go`, `frontend/src/components/workflow/useWorkflowViewPresentations.ts`, `frontend/src/components/TerminalEventTranscript.tsx` |
| [PLAT-279](pulse_platform/frontend-chat/plat-279.md) | The required report activity tab had no sanctioned data source, so workflows added a step or table just to fill it — while every notify_user run summary was already writing the same facts into org_dashboard_notifications in the report's own db.sqlite | Claude Code | `implemented` — run summaries are the documented default source (markdown rendered via window.report.renderMarkdown), a bespoke view is now the parent's explicit choice, and contract `1.0.36` offers existing workflows the switch without deleting anything unasked (agent_go `8b24ae459`, `f66cd6aa2`, `134cac35e`) | `agent_go/cmd/server/guidance/templates/system/reporting-policy.md`, `agent_go/cmd/server/workflow_version_upgrades.go` |
| [PLAT-280](pulse_platform/step-execution/plat-280.md) | `upwork`'s scripted-mode DB steps (`search-save-jobs`, `bid-record`, `outreach-record`, `improve-read-history`) lost `$DB_PATH` because their plan type (`message_sequence`) never matched their declared execution mode (`scripted`) — user-reported live, jobs found were never saved | Claude Code | `fixed` — `update_scripted_step` now atomically converts a declared-scripted `message_sequence` step to true `regular` in place (mirrors the existing reverse legacy-upgrade path); `update_step_config` rejects the invalid combination going forward; contract `1.0.37` migrates any other workflow with the same drift; upwork's 4 steps converted live and verified | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_agent.go`, `controller_message_sequence.go`, `interactive_workshop_manager.go`, `agent_go/cmd/server/workflow_manifest.go`, `workflow_version_upgrades.go`; SQLite backlog reconciled 2026-09-05 — verified stale records resolved; see [audit](../audits/platform-backlog-reconciliation-2026-09-05.md), deployed verification not claimed |
| [PLAT-281](pulse_platform/security-sandbox/plat-281.md) | Landlock's fixed system baseline had no grant for a deployment's own installed CLI tools outside standard system dirs, so every exec of one from a workflow step failed `rc=126: Permission denied` — Dominion's `tectonicusadaytrading` had placed zero paper trades since 2026-08-31 because `place-paper-trades` could never launch the `alpaca` CLI, regardless of login state; a second gap (the CLI's `$HOME/.config` lookup also outside every granted path) surfaced once the first was fixed | Claude Sonnet 5 | `fixed, live-verified on the Dominion deployment` — new `SANDBOX_EXTRA_SYSTEM_PATHS` env var (`landlockSystemReadPaths`, same pattern as `AGENT_BROWSER_EXECUTABLE_PATH`) lets a deployment grant its own tool/config paths without a code change; Dominion's `.env` now covers `/srv/dominion/tools` and `/srv/dominion/home/.config`; verified end-to-end against the real shipped binary on the actual target OS (rc=126 before, real `alpaca account get` response after); next scheduled run is the live reverify | `workspace/security/landlock_runner_linux.go`, `landlock_runner_linux_test.go` |
| [PLAT-280](pulse_platform/step-execution/plat-280.md) | `upwork`'s scripted-mode DB steps (`search-save-jobs`, `bid-record`, `outreach-record`, `improve-read-history`) lost `$DB_PATH` because their plan type (`message_sequence`) never matched their declared execution mode (`scripted`) — user-reported live, jobs found were never saved | Claude Code | `fixed` — `update_scripted_step` now atomically converts a declared-scripted `message_sequence` step to true `regular` in place (mirrors the existing reverse legacy-upgrade path); `update_step_config` rejects the invalid combination going forward; contract `1.0.37` migrates any other workflow with the same drift; upwork's 4 steps converted live and verified | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_agent.go`, `controller_message_sequence.go`, `interactive_workshop_manager.go`, `agent_go/cmd/server/workflow_manifest.go`, `workflow_version_upgrades.go` |
| [PLAT-281](pulse_platform/security-sandbox/plat-281.md) | Landlock's fixed system baseline had no grant for a deployment's own installed CLI tools outside standard system dirs, so every exec of one from a workflow step failed `rc=126: Permission denied` — Dominion's `tectonicusadaytrading` had placed zero paper trades since 2026-08-31 because `place-paper-trades` could never launch the `alpaca` CLI, regardless of login state; a second gap (the CLI's `$HOME/.config` lookup also outside every granted path) surfaced once the first was fixed | Claude Sonnet 5 | `fixed, live-verified on the Dominion deployment` — new `SANDBOX_EXTRA_SYSTEM_PATHS` env var (`landlockSystemReadPaths`, same pattern as `AGENT_BROWSER_EXECUTABLE_PATH`) lets a deployment grant its own tool/config paths without a code change; Dominion's `.env` now covers `/srv/dominion/tools` and `/srv/dominion/home/.config`; verified end-to-end against the real shipped binary on the actual target OS (rc=126 before, real `alpaca account get` response after); next scheduled run is the live reverify | `workspace/security/landlock_runner_linux.go`, `landlock_runner_linux_test.go` |
| [PLAT-282](pulse_platform/evaluation/plat-282.md) | `evaluation/evaluation_plan.json` had `update_evaluation_plan` but no way to delete a step, so an agent removing one had no sanctioned path and edited the file directly — the exact changelog-blind-spot failure `update_evaluation_plan` itself exists to prevent (AR-20260729-2), just for deletion instead of update | Claude Sonnet 5 | `fixed` — new `delete_evaluation_step` mirrors `delete_plan_steps` (batch, required reason, atomic all-or-nothing, full deleted-step JSON captured for revert) but needs no downstream-reference revalidation, since eval steps carry no graph to each other; registered in both the tool registrar and the Workshop-mode allowlist (missing the latter would have shipped an invisible tool — `TestToolSetInvariants` caught it); 6 new tests, full `step_based_workflow`/`cmd/server` suites pass | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/{evaluation_plan_tool.go,planning_agent.go,interactive_workshop_manager.go}`, `agent_go/cmd/server/toolset_invariant_test.go` |
| [PLAT-283](pulse_platform/security-sandbox/plat-283.md) | A sandboxed step could never install a Python/Node package on any Linux deployment — `pip3 install X` was refused as externally-managed, `--break-system-packages` then hit a raw Landlock permission error, and even `python3 -m venv` failed the same way; documented on 2026-08-31 as a permanent limit, but Landlock here is filesystem-only (no network rule exists) and no Linux deployment has an egress firewall, so PyPI was always reachable | Claude Sonnet 5 | `fixed, live-verified on the Dominion deployment` — three stacked gaps: native-mode shells now set `PIP_BREAK_SYSTEM_PACKAGES=1` (Docker mode already did); Landlock shells now redirect `PIP_CACHE_DIR`/`XDG_CACHE_HOME`/`PYTHONUSERBASE`/`npm_config_cache`/`TMPDIR` into a subtree of the step's own granted write path instead of the unwritable real `$HOME`; and the launcher's read baseline grants all of `/etc` read-only alongside the enumerated entries (a real install died on `/etc/debian_version`, then `/etc/mime.types` — enumerating was whack-a-mole; read-only `/etc` exposes nothing DAC doesn't already allow, verified on the box; the explicit entries stay because a rule on `/etc` doesn't reach a symlink's target — `resolv.conf` → `/run/...` on systemd-resolved hosts, and `/etc` alone broke DNS). Generic to every Linux deployment using this launcher. Proven with a real `pip install --user yfinance` completing end-to-end inside the shipped runner (PyPI over HTTPS, yfinance 1.7.0) | `workspace/security/environment.go`, `workspace/security/isolator_linux.go`, `workspace/security/landlock_runner_linux.go` |
| [PLAT-284](pulse_platform/security-sandbox/plat-284.md) | PLAT-283 made installs possible but routed them into the step's run folder (`runs/iteration-N/`), which rotates — so every run reinstalled ~15 packages from PyPI and carried PyPI/network/version-drift risk forever; the workflow folder itself persists, nothing pointed pip at it | Claude Fable 5.1 | `fixed, live-verified on the Dominion deployment` — one reserved `Workflow/<id>/.sandbox-cache/` granted writable to every execution step; the sandbox recognises it by name and routes pip user-site/cache, npm cache+prefix, Go, Cargo, pipx into it with their `bin/` folders on PATH and `SANDBOX_PERSISTENT_DIR` exported; applied on all three backends; run 1 installs, run 2 `pip install --user --no-index` (no network allowed) is already satisfied; backups skip it; agents told via tool description + guidance | `workspace/security/sandbox_tool_env.go`, `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go`, `agent_go/pkg/workspace/advanced_tools.go`, `agent_go/cmd/server/workflow_backup.go` |
| [PLAT-286](pulse_platform/step-execution/plat-286.md) | No tool could change a step's type: "scripted mode" is a step_config setting that exists only for the internal `regular` type, `update_step_config` rightly refuses it on a `message_sequence`, and PLAT-280's conversion only repairs a sequence already declared scripted — so moving a conversational step to scripted meant `add_scripted_step` + `delete_plan_steps` + rewiring by hand (found when the trading agent was asked to make `place-paper-trades` scripted) | Claude Fable 5.1 | `fixed` — new `change_step_type(step_id, target_type, reason)` converts scripted ↔ message_sequence in place using the runtime's existing normalizers (id/description/dependencies/validation/next_step_id/position kept, nested + orphan steps), writes plan and step_config as one idempotent operation so type and declared mode can never disagree (PLAT-280's failure), full plan validation, revertable before/after changelog entry; `update_step_config`/`update_scripted_step`/workshop guidance now point at it; 7 tests + `TestToolSetInvariants` | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/{change_step_type_tool.go,planning_agent.go,interactive_workshop_manager.go,planning_management.go}` |
| [PLAT-287](pulse_platform/step-execution/plat-287.md) | `declared_execution_mode` is a leftover of the model where plan type and execution mode were independent: implied on `regular` (scripted by definition; a regular step WITHOUT it is the legacy agentic shape the runtime silently runs as a sequence), forbidden on `message_sequence`; deleting it outright would flip every legacy agentic regular step on disk to scripted with no `main.py`, so removal is staged | Claude Fable 5.1 | `implemented` (both halves) — half 1 (v1.0.38): trusted, idempotent `migrate_declared_execution_mode` touches only `plan.json` (legacy agentic regular → the sequence it already ran as, declared-scripted sequence → regular; refuses when a declared-scripted step has no `main.py`); a first cut stripped the field there — review caught that the runtime still read it, fixed before it ran anywhere. Half 2 (v1.0.39): the runtime reads the plan type only (`isScriptedStep`: regular = scripted, message_sequence = conversational, evaluation step per its new `execution_mode`), every writer stopped, `update_step_config` rejects the key with a pointer to `change_step_type` / `update_evaluation_plan`, `strip_declared_execution_mode` removes it from both `step_config.json` files after marking declared-scripted eval steps on the eval plan (refuses while a legacy agentic regular step remains; reasons preserved in the changelog); frontend derives the mode from the type; guidance/docs rewritten. Transitional shim keeps an unstripped legacy agentic regular step off the scripted path; the two `Legacy…` struct fields + shim are the final cleanup once every live workflow is on 1.0.39. Neither upgrade run on a real workflow yet | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/declared_execution_mode_migration.go`, `agent_go/cmd/server/workflow_manifest.go`, `agent_go/cmd/server/workflow_version_upgrades.go` |
| [PLAT-327](pulse_platform/evaluation/plat-327.md) | Cross-install repair for producer-step IDs used as context dependency filenames, missing governed creation of the first evaluation step, and regression coverage for version-1 code layout | Codex | `implemented locally; deploy and runtime reverify` — runner compatibility resolves legacy producer IDs to declared outputs while Plan Drift flags canonical cleanup; `add_evaluation_step` validates, rejects cross-plan collisions and records full changelog evidence; version 1 is proven not to fall back to `learnings/` | `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/{prompt_sections.go,controller_execution.go,plan_drift_candidates.go,evaluation_plan_tool.go,code_layout_test.go}`, `agent_go/cmd/server/toolset_invariant_test.go` |
| [PLAT-328](pulse_platform/step-execution/plat-328.md) | RTS PR Reviewer deterministic routing rejected an absolute `context_dependencies` run path even though it was inside the workflow grant | Codex | `deployed as ancestor of f7c5f632b; RTS run-scoped live reverify pending` — the shared BaseOrchestrator path boundary canonicalizes absolute paths under known workspace roots before Folder Guard; outside paths remain fail-closed; PLAT-331 proves the workflow's temporary `db/assets` mirror must now be removed | `agent_go/pkg/orchestrator/base_orchestrator_workspace.go`, `agent_go/pkg/orchestrator/base_orchestrator_workspace_test.go` |
| [PLAT-162](pulse_platform/step-execution/plat-162.md) | HTTP-backed workflow tools published schemas through direct definitions/get_api_spec but executed unchecked argument maps, so guessed plan-tool fields reached handlers and caused repeated retry storms | Codex | `implemented` — canonical registration-boundary validation and corrected plan schemas; restart/runtime reverify pending | `mcpagent/agent`, AgentWorks plan-tool registration |
| [PLAT-163](pulse_platform/pulse-governance/plat-163.md) | Technical and strategic reviews need durable route-aware focus rotation and history; routine runtime errors must not monopolize the single review slot | Codex | `implemented` — original focus/coverage work shipped; 2026-09-05 correction locally implemented/tested: runtime signals no longer force Technical, Gate assesses step impact/recovery and fresh comparable runs since the actual review, exposes retained review receipts, and may skip all three. Deterministic plan safeguards remain. Rebuild/deployment and live verification pending | Pulse Gate guidance, worklist enforcement, existing module audit receipts, focus persistence, and Pulse UI |
| [PLAT-165](pulse_platform/scheduler-runs/plat-165.md) | The Schedule history UI is a filtered chat archive, so it cannot show which durable schedule occurrences ran, were missed, failed, or can resume | unassigned | `partially implemented` — UI now projects durable job/run records; unified occurrence read model/stage aggregation remains | scheduler occurrence history + workflow Schedule activity UI |

Assignment reserves the lane; it does not claim that work has started. An agent
sets its fragment to `in_progress` when it actually begins. PLAT-004, PLAT-008,
and PLAT-013 remain unassigned because they are already runtime verified.
PLAT-017 is assigned to Codex. Its `pulse_review_log` half shipped with
PLAT-054, and the canonical `run_metadata` outcome contract was implemented
locally on 2026-09-17. Deployment and live successful/failed/canceled/restart
acceptance remain.

For new work, create the smallest independent fragment and add one link here.
Claude Code should claim its currently active tickets in their fragment rather
than relying on chat-only coordination; that makes ownership survive restarts
and handoffs without creating an index-edit conflict with Codex.

## Evidence collection

The snapshot scanned all 11 databases under:

```text
workspace-docs/Workflow/*/db/db.sqlite
```

The structured set is the join of:

```sql
SELECT c.status, c.seen_count, d.finding_id, d.issue_kind,
       d.detail_json, c.step_id, c.text
FROM run_concerns c
JOIN pulse_finding_details d USING (fingerprint)
WHERE c.status NOT IN ('resolved', 'rejected')
  AND d.issue_kind = 'harness_issue';
```

That returned 12 active or acknowledged records across Build-in-public,
LinkedIn, RTS Latency, and Upwork. A second compatibility scan included older
records whose text describes a scheduler, bridge, Folder Guard, working
directory, tool-registration, or media-tool failure but predates
`issue_kind=harness_issue`.

## Priority summary

| Platform key | Priority | Current workflow evidence | State |
|---|---:|---|---|
| PLAT-001 Human-input propagation | P0 | Upwork | **implementation fixed; runtime reverify** |
| PLAT-002 Tool-failure status precedence | P0 | Upwork, Build-in-public, Social Media | **canonical CLI/runtime fix implemented; runtime reverify** |
| PLAT-003 Workflow DB tool exposure | P0 | Build-in-public, Instagram, RTS Latency, Social Media | **expanded to uniform workflow-step read/write; runtime reverify** |
| PLAT-004 Scheduler completion detection | P0 | RTS Latency | **runtime verified 2026-08-04** |
| PLAT-005 `get_api_spec` multi-name contract | P1 | RTS Latency | **fixed in mcpagent; runtime reverify** |
| PLAT-006 Workflow-step shell cwd contract | P1 | RTS Latency | **reverify** implemented fix |
| PLAT-007 Workflow image verification | P1 | Instagram | implemented; runtime/E2E reverify |
| PLAT-008 Phase cost pricing | P1 | Build-in-public, RTS Latency, Tectonicus | **core pricing runtime verified; phase/daily-to-execution reconciliation remains open** |
| PLAT-009 `get_cost_summary` run resolution | P1 | Build-in-public, Social Media, RTS Latency | **implementation repaired; runtime reverify** |
| PLAT-010 Finding identity split | P1 | RTS Latency | **implementation completed; runtime reverify** |
| PLAT-011 LLM role visibility | P2 | Build-in-public, RTS Latency | **runtime evidence positive; full UI acceptance pending** |
| PLAT-012 Changelog mutation coverage | P2 | LinkedIn, RTS Latency | **plan mutation verified; learning-tree boundary still reverify** |
| PLAT-013 Legacy regular-step editing | P1 | RTS Latency | **runtime verified 2026-08-04** |
| PLAT-014 Reviewer reference loading | P1 | RTS Latency, Tectonicus | **RTS runtime verified; Tectonicus reverify remains** |
| PLAT-015 Evaluation skipped-sentinel handling | P1 | Social Media | **implementation fixed; runtime reverify** |
| PLAT-016 Evaluation report drops real zero scores | P1 | Social Media | **implementation fixed; runtime reverify** |
| PLAT-017 Scheduler success leaves workflow metadata running | P1 | Social Media, Upwork | **Canonical workflow outcome implemented locally 2026-09-17: one four-state `run_metadata` result, warnings separated from status, Pulse legacy normalization, and no completed-step rewrite of an existing outcome. Live successful/failed/canceled/restart acceptance remains open.** |
| PLAT-018 Pulse finalizer cannot record dashboard completion | P1 | RTS Latency | **implementation fixed; runtime reverify** |
| PLAT-019 Pulse agent metrics remain unpriced | P1 | RTS Latency | **implementation fixed; runtime reverify** |
| PLAT-020 Converted scheduled chat must retain its session/tmux | P0 | RTS Latency | **August 15 reconciliation regression fixed; UI/runtime reverify** |
| PLAT-021 Proposals masquerade as pending user decisions | P1 | RTS Latency | **implementation fixed; UI reverify** |
| PLAT-022 `get_api_spec` absent from one workflow-phase session | P1 | Job Search | **assigned to Claude Code** |
| PLAT-023 Diff patch context recovery on large files | P1 | Tool-error logs | **implemented 2026-08-04 (large-file fixtures + corrected-retry round trip); nearest-candidate counting is accurate and tied near-matches refuse an arbitrary hint; runtime reverify remains** |
| PLAT-024 Tool-error marker omits tool name | P2 | Cross-workflow logs | **implemented 2026-08-04 (mcpagent d1eca1f + Codex follow-up); identity is recovered before per-tool failure classification, with a narrow envelope fallback and explicit "unknown"; runtime reverify remains** |
| PLAT-025 Workspace shell stdout buffer is unbounded | P1 | Platform availability | **queued for Claude Code** |
| PLAT-026 Selected running workflow hidden from global activity | P1 | RTS Latency | **implemented 2026-08-04 (a first pass missed the same-workflow-sibling case per Codex review; corrected — see ticket); runtime reverify remains** |
| PLAT-027 Async todo-task turn falsely completes its parent and hides the live child | P0 | Social Media | **completion gate runtime verified; placeholder 404 fixed and tested 2026-08-04; rebuilt UI reverify remains** |
| PLAT-028 Recovered CDP tab forwarded as action argument | P1 | Social Media | **implemented and executor-tested 2026-08-04; runtime reverify remains** |
| PLAT-029 Missing tmux remains live and reconnects forever | P0 | Social Media | **implemented and regression-tested 2026-08-04; rebuilt runtime reverify remains** |
| PLAT-031 Cost ledger loses run identity across UTC midnight | P1 | RTS Latency, Hetzner SSH | **execution-keyed cost/evaluation ledger implemented 2026-08-06: date/group files are shards only; immutable execution/evaluation IDs own their records, rotation updates only archived path metadata, and server projections preserve that identity. Historical folder-only rows remain explicitly legacy; runtime reverify remains** |
| PLAT-032 Child-agent calls omitted from parent telemetry | P1 | Social Media | **root cause fixed 2026-08-05 (`mcp-agent-builder-go` cdc3d1a76): async sub-agent dispatch now propagates the parent step's timing-capture ID to the child context; parent/child/total breakdown and failed-child/E2E tests not built; the separate failed-child status claim remains unreproduced; runtime reverify remains** |
| PLAT-033 Managed changelog contains placeholder refs | P1 | Social Media, Tectonicus, Hetzner SSH | **partially implemented 2026-08-05: truthful target/snapshot refs for the two reproduced writers, plus deterministic value-free `workflow.json` field-path evidence for `write_workflow_manifest`; Tectonicus still shows broad coverage/provenance gaps across managed mutation history, so the complete caller audit remains open and runtime reverify is pending** |
| PLAT-034 Completed Raw tmux terminal loses scrollback | P1 | Social Media / Electron | **fixed and runtime verified 2026-08-05 (`mcp-agent-builder-go` b984e6c5c); retained stream survives completion and remains scrollable** |
| PLAT-035 Retained tmux follow-up stays globally busy after returning to its prompt | P0 | Social Media, LinkedIn | **stream-driven fix implemented and regression-tested 2026-08-05; runtime reverify after rebuild remains** |
| PLAT-036 Context-usage percentage always saturates | P2 | Tectonicus | **implemented 2026-08-05; coding-CLI aggregate usage now marks the percentage unavailable; runtime reverify remains** |
| PLAT-037 Learning freshness misattributes out-of-band writes | P2 | Tectonicus | **new; freshness ledger assigns edits to the next step rather than the writer** |
| PLAT-038 Pre-validation attempts overwrite each other | P1 | Cross-mode validation | **implemented 2026-08-05; every new validation invocation retains a run-local attempt artifact while the compatibility latest pointer remains** |
| PLAT-039 Advisor recommendations default to engineering repairs | P1 | Hetzner SSH | **implemented 2026-08-06; route persistence, backend validation, lifecycle compatibility, and legacy UI fallback complete; runtime reverify remains** |
| PLAT-040 Full schedule silently blocked by chat/Pulse | P1 | Hetzner SSH | **implemented 2026-08-06; manual Pulse and producing schedules now use separate durable lanes, only a real full workflow blocks another schedule, and trigger errors are visible; runtime reverify remains** |
| PLAT-041 Expected cron occurrence can disappear without a decision | P0 | Confida QA | **implemented 2026-08-07; occurrence identity, restart cursor, gap classification, and focused tests complete; runtime reverify remains** |
| PLAT-042 Human answers lack provable actor attribution | P1 | Confida QA | **implemented 2026-08-07; server-derived actor metadata and append-only events complete; runtime reverify remains** |
| PLAT-043 DB integrity PRAGMAs blocked by read policy | P2 | Confida QA | **implemented 2026-08-07; bounded allowlist and rejection tests complete; runtime reverify remains** |
| PLAT-044 Normal finalizer ownership filed as a platform defect | P1 | Upwork | **implemented; stable reason-code reconciliation and runtime reverify remain** |
| PLAT-045 Resolved finding leaves linked decision open | P1 | Upwork | **implemented transactionally; runtime reverify remains** |
| PLAT-046 Empty reviewer completion verdict | P2 | Upwork | **storage already rejected it; typed tool boundary hardened and tested; runtime reverify remains** |
| PLAT-047 Grouped run artifacts lack immutable physical identity | P1 | Upwork, Social Media | **forward run index, per-producing-group execution ID, content-addressed plan revision, fail-closed metadata binding, and deterministic Pulse intake implemented 2026-08-28; immutable history for repeated partial-group executions and live `PUL-DB292C50` reverify remain** |
| PLAT-048 Scheduled/main tmux processes outlive their useful session | P0 | Local backend process audit | **implemented; only user-interactive main agents retain tmux, with a one-hour ceiling and live cleanup; runtime reverify remains** |
| PLAT-049 Shared platform mechanics leak into workflow artifacts | P1 | Upwork + cross-workflow scan | **Builder guard, Engineering Review audit, and idempotent workflow v1.0.21 purification preflight implemented; migration/runtime reverify remains** |
| PLAT-050 Pulse reasoning split between agent and Go | P1 | Upwork recovery-Fixer investigation | **implemented; one continuing main-agent conversation and event-driven ordering need runtime reverify** |
| PLAT-051 Upgrade prompt names an unregistered tool | P0 | Upwork | **implemented 2026-08-07: `set_workflow_contract_version` replaces the internal-helper instruction; restart and one v1.0.20 upgrade reverify remain** |
| PLAT-052 Scheduled turns visibly close/reopen Claude Code | P1 | Upwork | **implemented 2026-08-07: known consecutive scheduler turns retain their native CLI; restart and schedule lifecycle reverify remain** |
| PLAT-054 Idle watchdog kills live child work | P0 | Social Media, Tectonicus, Upwork, LinkedIn, Instagram, RTS Latency, Substack, Build-in-public | **implemented 2026-08-09: both wait paths consult a shared liveness predicate before expiring a turn, bounded by a 3 h ceiling; `pulse_review_log` added to the startup sweep. Runtime reverify is the gate for the ~40 other reverify entries** |
| PLAT-055 Reflection turn can only write learnings | P1 | Social Media, RTS Latency, Upwork, LinkedIn | **implemented 2026-08-09: KB and learnings merged into one reflection turn (regular and message_sequence paths) with `knowledgebase/notes` write, `db/` read and a structured `record_run_concern`; prompt now carries a routing rule, the workflow's real table names, per-step file ownership, a measured size signal and a compaction rule. Behavioural reverify required** |
| PLAT-056 Repair-loop recorder files durable concerns for self-healed iterations | P2 | Instagram (11 findings, one root cause) | **implemented — September 9 code/tests confirm automatic concern recording was already removed; historical evidence is retained** |
| PLAT-059 A learnings lock could be set with no stated reason | P2 | LinkedIn (6 of 6 steps locked, none justified) | **superseded by PLAT-263: the redundant lock and reason were removed; `learnings_access="read"` is the single consume-without-writing state** |
| PLAT-060 Ops config changes carried no reason into the config | P2 | cross-workflow | **implemented 2026-08-09: `execution_tier` / `execution_llm` / `declared_execution_mode` each require a paired reason at write time, each rejection naming its hidden consequence AND `create_human_input_request` as the escape hatch so the field cannot induce confabulation. UI tier setter removed. Objectives deliberately NOT gated — they get a yield loop instead. Reverify deferred: llm_ops_review is disabled at the Gate** |
| PLAT-062 Scripted prompt named a forbidden write target | P2 | hetznerssh | **implemented 2026-08-09: the MODE NOTE told the agent to save to `learnings/{step-id}/main.py`, which `setupExecutionFolderGuard` never opens for writes, while the same prompt's Code Execution section correctly said `code/main.py`. The step obeyed the wrong one, was denied, and filed a concern that persistence had failed — it had not, the platform saved it back 42 s later byte-identical. Surfaced only because PLAT-061 removed the `learn_code_max_fix_iterations: 0` artifact that had stopped these steps ever attempting a repair** |
| PLAT-065 Gate recorded a due module, session ended, nothing resolved it | P1 | social-media (new P0 dropped 6+h); 4 more found back to July 25 | **open 2026-08-10 — a Gate pass correctly found a real P0 (two graders disagree on public content) and recorded workflow_review=due; 42s later `abortIfTurnStillBusy` fired because the step's outcome wasn't `completed` and the session still read busy, so it stamped backup/publish/notify all `failed` with the same reason and returned — before the existing durable-worklist recovery logic (built for exactly this case) ever ran, since the busy-check is ordered ahead of it. Confirmed proximate cause via `pulse_final_command_state` rows, all three timestamped identically. Same-night cron run on upwork completed normally, so not universal. Still open: why `sessionIsBusy` read true 42s after Gate's tool call had already succeeded — needs a diagnostic log at the call site, not yet added; log window for this incident has rotated out. Fix designed (recover-before-abort reorder) but not shipped — needs a safety call on whether sending the next turn immediately once busy clears risks a real overlap, vs. needing a bounded wait. Detection shipped: `scripts/pulse_health.py --section stranded` found this instance plus 4 pre-existing ones, oldest 374h** |
| PLAT-064 Dead workflow_* event family; one completion check had no fallback | P3 | frontend-wide | **implemented 2026-08-09: tracing PLAT-063's dead entries found workflow_start/workflow_progress/workflow_end/batch_execution_end are ALL dead — orchestrator_end is the only real completion signal. checkTabCompletion (useChatStore + useWorkflowStore) had no orchestrator_end fallback for workflow mode, so a workflow completing without human feedback could not satisfy it — currently dead code itself, fixed as a landmine. EVENT_TYPES.COMPLETION trimmed to the one real signal. Display components and generated types deliberately left inert rather than deleted** |
| PLAT-066 route_selections correctly supplied, never seeded; router silently defaulted to the live-action route | P1 | social-media (Weekly Strategy Discovery run) | **open 2026-08-10 — proven with hard evidence: the schedule's `run_full_workflow` payload correctly carried `route_selections={"step-run-mode-router":"propose_new"}` (confirmed from the raw HTTP payload in the server log), yet `step-run-mode-router`'s own routing-evaluation.json says `"No route_selection.json found; using default_route_id \"execution\""`. `seedRouteSelectionsForRun`'s own success log line never fired for this session — the write path did not complete, exact reason not yet isolated (not called vs. early-return vs. write error vs. a path mismatch between the seed and read call sites). Consequence: `execute-allocate`/`step-execution-pipeline` (the live-action route) ran and failed prevalidation on an unrelated pre-existing bug, while the agent separately drove `propose-new-strategies-direct` by hand to still satisfy the schedule — leaving the wrongly-triggered execution job orphaned (`list_executions` still shows it `running` under a `cancelled` parent; `stop_step` and a full-cancel both gave contradictory answers about it, a related but separately unresolved bookkeeping defect). Interim mitigation shipped this session (commit `eaa089b`): removed `default_route_id` from `step-run-mode-router` in plan.json, so a future seeding gap fails loudly instead of silently running the live-action route — the seeding bug itself is still open** |
| PLAT-067 Scheduled parent continues after its coding-agent terminal disappears | P0 | RTS Latency cron run, 2026-08-10 | **implemented 2026-08-10 — the defect was a single swallowed error at `server.go:5850`, not a missing recovery system: `StartAgentTransportSession` already performs the verify-and-single-replacement (relaunch with `--resume`, wait for a verified ready prompt), and the 5-minute timeout was it correctly concluding the transport was unusable — but its error was logged and stepped over, so `StreamWithEvents` fired 57 lines later into a dead pane. Two consecutive ~32-minute turns burned on one run, killing every producing step after them. Now aborts with a named `parent_transport_unavailable` error using the same `sendError(..., true)` + `return` the same function already uses for a streaming failure, leaving any queued child completion intact so recovery never re-runs a successful child. Still open: why the parent tmux server disappeared at all.** |
| PLAT-063 Report pane flashed during runs | P3 | reported on the live UI | **fixed 2026-08-09 and extended 2026-08-11/12: view flips no longer unmount the viewer; ordinary renders no longer reapply iframe `srcDoc`; pending-decision background polls are visually silent and retain unchanged state; and the self-triggering scroll-repair loop captured live in browser logs was removed. The iframe now reloads only for a new document or explicit Refresh. Also records that `workflow_end` and `batch_execution_end` are dead entries in the frontend COMPLETION list** |
| PLAT-061 step_config carried a dead field and an incomplete merge | P2 | 10 of 12 workflows carried `db_access` | **implemented 2026-08-09: `db_access` removed (ignored by `resolveDBAccess`, yet documented and agent-settable — the `global_skill_objective` shape); `MergeAgentConfigFields` completed from 19/28 fields, three of the nine dropped ones gated writes; reflection-based guard added. 4 orphan fields deleted (incl. `disable_tier_optimization`, a loophole around PLAT-060, and `learn_code_max_fix_iterations`, whose every stored value was a migration artifact that silently disabled script repair); phantom clear-names now acknowledged no-ops rather than silent successes. `execution_max_turns` was misclassified — it carries the workflow-wide setting — and stays** |
| PLAT-058 Per-step learnings files fragmented one workflow skill | P1 | Social Media (5 forked files, first live PLAT-055 run) | **implemented 2026-08-09: supersedes PLAT-055 item D. Learnings are one topic-organised skill every step improves; step-named orphans are folded and deleted; frontmatter description kept accurate. Found by the steps themselves via `record_run_concern`, one classifying it `harness_issue`** |
| PLAT-057 harness_issue parkable in the workflow engineering queue | P2 | Upwork (2 of 48 harness findings) | **implemented 2026-08-09: `queued_for_engineering` is rejected for a `harness_issue` at disposition time, naming both exits; `issue_kind` promoted to shared constants; 4 regression tests incl. the untouched normal path. The 2 pre-existing rows are reported by the coherence check, not migrated** |

### Social Media classification correction — 2026-08-05

The three cards shown in the Platform queue do not represent three proven
platform repairs:

1. `HARNESS-SUBAGENT-DISPATCH-STATUS` contains one proven platform defect and
   one historical claim. The proven telemetry undercount is now PLAT-032. Its
   claim that a failed child is reported as completed was not reproduced in the
   cited run and remains evidence to reproduce, not code to change.
2. `HARNESS-CHANGELOG-REF-PLACEHOLDER` is a current, independently reproduced
   platform defect and is now PLAT-033.
3. `PUL-93AE14C6` is **not presently a platform ticket**. It reports that reuse
   of `runs/iteration-0/default` erased the prior occupant even though the
   workflow declares `always_use_same_run=false`. The Fixer marked it
   `blocked` only because it did not reach the item in that pass, while its own
   note says the finding remains actionable next pass. That disposition routed
   it to Platform incorrectly. Keep the finding open and Pulse-owned until a
   repair attempt diagnoses a platform boundary or fixes the workflow/runtime
   configuration.

PLAT-031 is already implemented on its documented writer boundary and is not
one of these new Social Media repairs.

The two tool-error findings below are one family, not two independent repair
projects. The database-tool symptoms across three workflows are also one shared
capability defect until evidence proves otherwise.

### Why fixed platform findings appeared again in Social Media

Social Media run `schedule-cron--4128e261_1785724255412842000` began at
2026-08-03T02:30:55Z and its Pulse finalizer backed up at
2026-08-03T07:13:51Z (12:43 IST). The platform-reliability bundle
`46bf02dff` and canonical mcpagent tool-error change `7820b74` landed at
18:56 IST. Therefore the Social Media observations for nested tool-error status
and cost/run-folder attribution occurred before those implementations existed;
they are verification evidence for PLAT-002 and PLAT-009, not proof that the
new fixes regressed. This remains true even if the server restarted before the
Social Media run.

The scheduler evidence is different. PLAT-004 fixed premature success while
required child work was still in flight. Social Media's discovery children did
finish, but scheduler success and durable `run_metadata.status=running`
disagreed afterward. That is a terminal metadata/provenance reconciliation
defect and is tracked separately as PLAT-017 rather than being treated as a
PLAT-004 regression.

### RTS Latency runtime reverification — 2026-08-04

Run identity:

```text
workflow run: 33798848-0ed7-4132-821c-e05592e3ec4e
pulse_run_id: schedule-cron--42eca39a_1785810615371091000
workflow:     08:00:15–09:37:00 IST (96.75 minutes)
reviewers:    09:40:06–10:14:05 IST
fixer:        10:14:05–10:48:22 IST
```

**The new reviewer/Fixer topology worked.** Gate started exactly two independent
reviewers with concurrency enabled: `workflow_review` and `strategy_auditor`.
Their executions overlapped. `workflow_review` used the new multi-turn sequence
(7 LLM calls); Strategy Auditor remained an independent lens. Goal Advisor was
skipped on its own recorded boundary. The reviewer barrier waited for both, then
one consolidated Fixer ran. `pulse_agent_metrics.role` correctly distinguishes
`reviewer` from `fixer`.

**Verification-before-discovery worked.** The workflow reviewer processed 14
allowlisted attempt/finding tuples across 12 awaiting-verification findings:
13 passed, 1 failed, and 0 were inconclusive. The failed finding was reopened
and the Fixer repaired the plan-level source it named. Runtime evidence closed
the prior DB-persistence, DB-probe, variable-read, KB reachability, language/
`stt_routing`, digest-freshness, and scheduler-completion findings. In
particular, both collectors persisted 2026-08-04 rows through the sanctioned
`$MCP_CUSTOM` DB route, and the full scheduled workflow reached every required
step before success was recorded.

**The Fixer made real, audited changes.** It corrected the `audio_status`
contract conflict, added the mandatory `source=voice` gate, repaired workflow
root derivation, made declared production log-group configuration observable,
and changed the report's DB instructions from direct `sqlite3` access to the
sanctioned route. Ten managed plan mutations appear in
`planning/changelog/changelog-2026-08-04-04-18-12.json`. It also recorded three
impact interventions and ten goal observations. Five behavioral changes remain
correctly `awaiting_verification`; only the next producing workflow run can
settle them.

**PLAT-009 failed its runtime acceptance and is reopened.** At 09:51:07 IST,
`get_cost_summary(workspace_path="Workflow/rtslatency")` first tried the retired
`runs/iteration-0/token_usage.json`, then enumerated only
`costs/execution/__ungrouped__`. It did not select the authoritative
`costs/execution/dev/2026-08-04.json` shard. The reviewer consequently reported
that all nine scheduled step rows and `claude-sonnet-5` were absent, while the
authoritative dev ledger contains them. This is post-change current-binary
evidence, not a stale pre-fix observation.

**PLAT-010 is only partially repaired.** Migration has produced one canonical
`pulse_finding_details` row and one `run_concerns` row for
`HARNESS-REFDOC-REVIEW-ARTIFACT-DRIFT`, but `pulse_finding_events` still carries
ordinary `external_action_required` events under both the old and canonical
fingerprints. The projected backlog shown to the reviewer therefore still
presents two lifecycle items, and the Fixer explicitly reconciled both halves.
Preserving history is correct; preserving the old events as live lifecycle
events rather than explicit identity-merge history is not.

**PLAT-008's core arithmetic worked, but a separate accounting boundary did
not.** The phase and execution ledgers now stamp `pricing_model_id`,
`pricing_version`, fresh/cache token components, and non-zero totals for both
`claude-opus-5` and `claude-sonnet-5`. However, the three corresponding
`pulse_agent_metrics` rows have `usage_status=captured`, model usage populated,
and `total_cost_usd=0`; their embedded model objects report
`unpriced_call_count` for every call. That inconsistency is PLAT-019 rather than
a reopening of the phase-ledger arithmetic in PLAT-008.

**PLAT-012 is only partly exercised.** Every managed plan edit emitted a typed
changelog entry with target, actor, dependency class, and before/after refs.
The run also changed `learnings/_global/references/*.md`, but this pass does not
prove the runtime-learning-turn tree-hash path because those edits were made by
the Fixer rather than the dedicated learning turn. Keep that half in reverify.

**Run-quality note, not yet separate platform keys.** The Fixer recovered from
several avoidable tool failures: one absolute-path Folder Guard denial, one
failed assertion while rewriting, one protected evaluation-plan diff attempt,
and several schema-probing failures against `record_pulse_result` /
`record_pulse_impact`. These did not invalidate its terminal results, but they
are evidence that the prompt/schema ergonomics still waste calls. Do not count
them as workflow defects without first proving a shared contract problem.

### Platform repair pass — 2026-08-04

This pass repaired the current-binary defects above without restarting the
backend. Verification is focused/unit-level until the next real Pulse or
schedule-to-chat producer exercises each boundary:

- PLAT-009 now treats an iteration-only run such as `iteration-0` as the parent
  of its grouped ledger shards, enumerates every group/date shard, and merges
  only matching child run keys. Explicit `iteration-0/dev` queries remain
  exact. Tests prove dev+production merge and reject another iteration.
- PLAT-010 now finishes interrupted historical migrations: events left under
  an old fingerprint are moved to the canonical finding ID/fingerprint, with
  colliding tuples retained as explicit `identity_merge` history rather than a
  second live case.
- PLAT-015 persists a skipped step as `skipped: true` with explicit
  reason/evidence through JSON and SQLite. The PLAT-016 audit found the earlier
  repair incomplete beyond the workflow serializer: server projection could
  still omit zero, fractional values were truncated, and missing scores lacked
  an explicit presence bit. Workflow, SQLite, API, and UI now use
  `score_captured`; preserve fractional values; and display genuine zero.
- PLAT-018 now uses the scheduler's successful dashboard artifact validation
  as the command's proof and marks only `dashboard=done`. It does not grant the
  agent general DB write authority and does not infer success for backup,
  publish, or notify.
- PLAT-019 reprices only the unpriced slice of each captured model aggregate
  with the same immutable model rate card used by phase/execution ledgers.
  Mixed priced/unpriced aggregates cannot double-charge; genuinely unknown
  models remain explicit as `captured_unpriced` with a reason.
- PLAT-020 now retains the scheduled conversation's session ID when it becomes
  interactive. A live retained tmux receives the message directly; if the pane
  is gone, the backend resumes the same native coding-CLI conversation. It does
  not fork the chat or discard its context.
- PLAT-021 separates informational proposals from answer-blocked decisions.
  A finding appears under **Your decisions** only when it carries a linked
  `human_input_id`; a missing link is Pulse-owned repair work, and a
  `proposal_recorded` item appears under **Proposed improvements** instead.
- PLAT-017 was deliberately not changed. The retained Social Media
  `run_metadata.json` now has a terminal `failed` state, so the historical
  scheduler-success/metadata-running contradiction cannot be reproduced from
  the current artifact. Capture a fresh current-binary mismatch before
  changing shared scheduler terminal-state ownership.

Focused verification passed:

```text
go test ./agent_go/pkg/costledger ./agent_go/pkg/orchestrator ./agent_go/pkg/orchestrator/agents/workflow/step_based_workflow ./agent_go/cmd/server \
  -run 'TestReadRunAcrossDates|TestFindingIdentityMigration|TestReconcilePulseDashboard|TestPulseAgentMetrics|TestPhasePricingUses' -count=1
npm test -- --run src/components/workflow/pulseFindingPresentation.test.ts
npx tsc -b --pretty false
```

### Additional workflow-runtime repairs — 2026-08-04

Three related changes were implemented after the repair pass above. None
required a backend restart during development:

1. **PLAT-003 — uniform workflow DB access.** Every workflow execution step,
   including evaluation and message-sequence children, now gets the same
   managed read-write capability. Legacy `db_access=read` and item-level DB
   flags no longer create parent/child capability drift. Agentic access remains
   mediated by `query_workflow_db` and `mutate_workflow_db`; this does not
   reopen raw SQLite/WAL/SHM access.
2. **PLAT-027 — asynchronous-child visibility.** The terminal rail now projects
   live children directly from the session execution tree while their detailed
   terminal snapshot is pending. The placeholder reconciles with the real
   terminal by execution identity, so the child remains visible without a
   duplicate after its parent turn completes.
3. **PLAT-028 — CDP tab normalization.** A bare, unambiguous tab ID such as
   `t2` is now recovered before command planning and subprocess construction.
   `click t2 e64` therefore routes to tab `t2` and invokes `click e64` instead
   of treating the tab as an element selector.
4. **PLAT-003 — DB query argument compatibility.** The canonical read argument
   remains `sql`, but the natural `query` spelling is now a documented alias
   and reaches the same query-only backend. Conflicting aliases fail before a
   database request.
5. **PLAT-034 — Raw terminal retention.** The live terminal byte stream now has
   a distinct `tmux_stream` source. Settled detail and chat-history persistence
   preserve it instead of overwriting it with tmux's final visible screen. Raw
   remains the default; the Electron runtime recheck confirmed scrolling after
   completion.

Focused verification covered workflow-step capability/folder-guard routes,
terminal execution-tree projection, and the real browser executor boundary.
Each fragment records its exact acceptance and remaining runtime recheck.

## Ticket files

The fragment is canonical for current ownership, implementation notes,
verification evidence, and acceptance. This index supplies cross-ticket
priority and historical run context.

| Ticket | Ticket | Ticket | Ticket |
|---|---|---|---|
| [PLAT-001](pulse_platform/human-decisions/plat-001.md) | [PLAT-002](pulse_platform/coding-agent-bridge/plat-002.md) | [PLAT-003](pulse_platform/step-execution/plat-003.md) | [PLAT-004](pulse_platform/scheduler-runs/plat-004.md) |
| [PLAT-005](pulse_platform/step-execution/plat-005.md) | [PLAT-006](pulse_platform/step-execution/plat-006.md) | [PLAT-007](pulse_platform/integrations/plat-007.md) | [PLAT-008](pulse_platform/cost-telemetry/plat-008.md) |
| [PLAT-009](pulse_platform/cost-telemetry/plat-009.md) | [PLAT-010](pulse_platform/pulse-governance/plat-010.md) | [PLAT-011](pulse_platform/cost-telemetry/plat-011.md) | [PLAT-012](pulse_platform/plans-contracts/plat-012.md) |
| [PLAT-013](pulse_platform/step-execution/plat-013.md) | [PLAT-014](pulse_platform/pulse-governance/plat-014.md) | [PLAT-015](pulse_platform/evaluation/plat-015.md) | [PLAT-016](pulse_platform/evaluation/plat-016.md) |
| [PLAT-017](pulse_platform/scheduler-runs/plat-017.md) | [PLAT-018](pulse_platform/pulse-governance/plat-018.md) | [PLAT-019](pulse_platform/cost-telemetry/plat-019.md) | [PLAT-020](pulse_platform/coding-agent-bridge/plat-020.md) |
| [PLAT-021](pulse_platform/human-decisions/plat-021.md) |  |  |  |
| [PLAT-022](pulse_platform/step-execution/plat-022.md) | [PLAT-023](pulse_platform/step-execution/plat-023.md) | [PLAT-024](pulse_platform/coding-agent-bridge/plat-024.md) | [PLAT-025](pulse_platform/step-execution/plat-025.md) |
| [PLAT-026](pulse_platform/frontend-chat/plat-026.md) | [PLAT-027](pulse_platform/step-execution/plat-027.md) | [PLAT-028](pulse_platform/browser-automation/plat-028.md) | [PLAT-029](pulse_platform/coding-agent-bridge/plat-029.md) |
| [PLAT-030](pulse_platform/coding-agent-bridge/plat-030.md) | [PLAT-031](pulse_platform/cost-telemetry/plat-031.md) | [PLAT-032](pulse_platform/cost-telemetry/plat-032.md) | [PLAT-033](pulse_platform/plans-contracts/plat-033.md) |
| [PLAT-034](pulse_platform/coding-agent-bridge/plat-034.md) | [PLAT-035](pulse_platform/coding-agent-bridge/plat-035.md) |  |  |
| [PLAT-036](pulse_platform/cost-telemetry/plat-036.md) | [PLAT-037](pulse_platform/learnings-knowledge/plat-037.md) | [PLAT-038](pulse_platform/evaluation/plat-038.md) | [PLAT-039](pulse_platform/pulse-governance/plat-039.md) |
| [PLAT-040](pulse_platform/scheduler-runs/plat-040.md) | [PLAT-041](pulse_platform/scheduler-runs/plat-041.md) | [PLAT-042](pulse_platform/human-decisions/plat-042.md) | [PLAT-043](pulse_platform/step-execution/plat-043.md) |
| [PLAT-044](pulse_platform/pulse-governance/plat-044.md) | [PLAT-045](pulse_platform/pulse-governance/plat-045.md) | [PLAT-046](pulse_platform/pulse-governance/plat-046.md) | [PLAT-047](pulse_platform/scheduler-runs/plat-047.md) |
| [PLAT-048](pulse_platform/coding-agent-bridge/plat-048.md) | [PLAT-049](pulse_platform/plans-contracts/plat-049.md) |  |  |
| [PLAT-050](pulse_platform/pulse-governance/plat-050.md) | [PLAT-051](pulse_platform/plans-contracts/plat-051.md) | [PLAT-052](pulse_platform/coding-agent-bridge/plat-052.md) | [PLAT-053](pulse_platform/coding-agent-bridge/plat-053.md) |
| [PLAT-054](pulse_platform/scheduler-runs/plat-054.md) | [PLAT-055](pulse_platform/learnings-knowledge/plat-055.md) | [PLAT-056](pulse_platform/evaluation/plat-056.md) | [PLAT-057](pulse_platform/pulse-governance/plat-057.md) |
| [PLAT-058](pulse_platform/learnings-knowledge/plat-058.md) | [PLAT-059](pulse_platform/learnings-knowledge/plat-059.md) | [PLAT-060](pulse_platform/step-execution/plat-060.md) | [PLAT-061](pulse_platform/step-execution/plat-061.md) |
| [PLAT-062](pulse_platform/step-execution/plat-062.md) | [PLAT-063](pulse_platform/frontend-chat/plat-063.md) | [PLAT-064](pulse_platform/frontend-chat/plat-064.md) | [PLAT-065](pulse_platform/pulse-governance/plat-065.md) |
| [PLAT-066](pulse_platform/step-execution/plat-066.md) | [PLAT-067](pulse_platform/scheduler-runs/plat-067.md) | [PLAT-068](pulse_platform/learnings-knowledge/plat-068.md) | [PLAT-069](pulse_platform/cost-telemetry/plat-069.md) |
| [PLAT-070](pulse_platform/scheduler-runs/plat-070.md) | [PLAT-071](pulse_platform/scheduler-runs/plat-071.md) | [PLAT-072](pulse_platform/pulse-governance/plat-072.md) | [PLAT-073](pulse_platform/pulse-governance/plat-073-remaining-board.md) |
| [PLAT-074](pulse_platform/plans-contracts/plat-074.md) |  |  |  |
| [PLAT-075](pulse_platform/evaluation/plat-075.md) | [PLAT-076](pulse_platform/learnings-knowledge/plat-076.md) | [PLAT-077](pulse_platform/human-decisions/plat-077.md) | [PLAT-078](pulse_platform/security-sandbox/plat-078.md) |
| [PLAT-080](pulse_platform/scheduler-runs/plat-080.md) | [PLAT-081](pulse_platform/cost-telemetry/plat-081.md) | [PLAT-082](pulse_platform/step-execution/plat-082.md) | [PLAT-083](pulse_platform/pulse-governance/plat-083.md) |
| [PLAT-084](pulse_platform/pulse-governance/plat-084.md) | [PLAT-085](pulse_platform/frontend-chat/plat-085.md) |  |  |
| [PLAT-086](pulse_platform/scheduler-runs/plat-086.md) |  |  |  |
| [PLAT-087](pulse_platform/step-execution/plat-087.md) | [PLAT-088](pulse_platform/cost-telemetry/plat-088.md) | [PLAT-089](pulse_platform/scheduler-runs/plat-089.md) | [PLAT-090](pulse_platform/cost-telemetry/plat-090.md) |
| [PLAT-091](pulse_platform/evaluation/plat-091.md) | [PLAT-092](pulse_platform/human-decisions/plat-092.md) | [PLAT-093](pulse_platform/human-decisions/plat-093.md) | [PLAT-094](pulse_platform/pulse-governance/plat-094.md) |
| [PLAT-095](pulse_platform/scheduler-runs/plat-095.md) | [PLAT-096](pulse_platform/plans-contracts/plat-096.md) | [PLAT-097](pulse_platform/scheduler-runs/plat-097.md) | [PLAT-098](pulse_platform/plans-contracts/plat-098.md) |
| [PLAT-099](pulse_platform/coding-agent-bridge/plat-099.md) | [PLAT-100](pulse_platform/coding-agent-bridge/plat-100.md) | [PLAT-101](pulse_platform/coding-agent-bridge/plat-101.md) |  |
| [PLAT-102](pulse_platform/coding-agent-bridge/plat-102.md) | [PLAT-103](pulse_platform/coding-agent-bridge/plat-103.md) | [PLAT-104](pulse_platform/frontend-chat/plat-104.md) |  |
| [PLAT-105](pulse_platform/coding-agent-bridge/plat-105.md) | [PLAT-106](pulse_platform/frontend-chat/plat-106.md) | [PLAT-107](pulse_platform/frontend-chat/plat-107.md) | [PLAT-108](pulse_platform/coding-agent-bridge/plat-108.md) |
| [PLAT-109](pulse_platform/frontend-chat/plat-109.md) | [PLAT-110](pulse_platform/security-sandbox/plat-110.md) | [PLAT-111](pulse_platform/frontend-chat/plat-111.md) |  |
| [PLAT-112](pulse_platform/frontend-chat/plat-112.md) | [PLAT-113](pulse_platform/coding-agent-bridge/plat-113.md) | [PLAT-114](pulse_platform/coding-agent-bridge/plat-114.md) | [PLAT-115](pulse_platform/pulse-governance/plat-115.md) |
| [PLAT-116](pulse_platform/coding-agent-bridge/plat-116.md) | [PLAT-117](pulse_platform/coding-agent-bridge/plat-117.md) | [PLAT-118](pulse_platform/security-sandbox/plat-118.md) | [PLAT-119](pulse_platform/pulse-governance/plat-119.md) |
| [PLAT-120](pulse_platform/integrations/plat-120.md) | [PLAT-121](pulse_platform/integrations/plat-121.md) | [PLAT-122](pulse_platform/integrations/plat-122.md) | [PLAT-123](pulse_platform/step-execution/plat-123.md) |
| [PLAT-124](pulse_platform/browser-automation/plat-124.md) | [PLAT-125](pulse_platform/step-execution/plat-125.md) | [PLAT-126](pulse_platform/step-execution/plat-126.md) | [PLAT-127](pulse_platform/coding-agent-bridge/plat-127.md) |
| [PLAT-128](pulse_platform/learnings-knowledge/plat-128.md) | [PLAT-129](pulse_platform/learnings-knowledge/plat-129.md) | [PLAT-130](pulse_platform/scheduler-runs/plat-130.md) | [PLAT-131](pulse_platform/frontend-chat/plat-131.md) |
| [PLAT-132](pulse_platform/integrations/plat-132.md) | [PLAT-133](pulse_platform/evaluation/plat-133.md) |  |  |
| [PLAT-134](pulse_platform/security-sandbox/plat-134.md) | [PLAT-135](pulse_platform/security-sandbox/plat-135.md) | [PLAT-136](pulse_platform/cost-telemetry/plat-136.md) | [PLAT-137](pulse_platform/pulse-governance/plat-137.md) |
| [PLAT-138](pulse_platform/pulse-governance/plat-138.md) | [PLAT-139](pulse_platform/coding-agent-bridge/plat-139.md) | [PLAT-140](pulse_platform/frontend-chat/plat-140.md) | [PLAT-141](pulse_platform/coding-agent-bridge/plat-141.md) |
| [PLAT-142](pulse_platform/pulse-governance/plat-142.md) | [PLAT-143](pulse_platform/frontend-chat/plat-143.md) | [PLAT-144](pulse_platform/scheduler-runs/plat-144.md) | [PLAT-145](pulse_platform/scheduler-runs/plat-145.md) |
| [PLAT-146](pulse_platform/scheduler-runs/plat-146.md) | [PLAT-147](pulse_platform/pulse-governance/plat-147.md) | [PLAT-148](pulse_platform/pulse-governance/plat-148.md) | [PLAT-149](pulse_platform/coding-agent-bridge/plat-149.md) |
| [PLAT-150](pulse_platform/coding-agent-bridge/plat-150.md) | [PLAT-151](pulse_platform/step-execution/plat-151.md) | [PLAT-152](pulse_platform/coding-agent-bridge/plat-152.md) | [PLAT-153](pulse_platform/coding-agent-bridge/plat-153.md) |
| [PLAT-154](pulse_platform/pulse-governance/plat-154.md) | [PLAT-155](pulse_platform/pulse-governance/plat-155.md) | [PLAT-156](pulse_platform/pulse-governance/plat-156.md) | |
| [PLAT-158](pulse_platform/pulse-governance/plat-158.md) | [PLAT-159](pulse_platform/frontend-chat/plat-159.md) | [PLAT-160](pulse_platform/coding-agent-bridge/plat-160.md) | [PLAT-161](pulse_platform/frontend-chat/plat-161.md) |
| [PLAT-164](pulse_platform/coding-agent-bridge/plat-164.md) | [PLAT-166](pulse_platform/cost-telemetry/plat-166.md) | [PLAT-167](pulse_platform/cost-telemetry/plat-167.md) | [PLAT-168](pulse_platform/learnings-knowledge/plat-168.md) |
| [PLAT-162](pulse_platform/step-execution/plat-162.md) | [PLAT-163](pulse_platform/pulse-governance/plat-163.md) | [PLAT-165](pulse_platform/scheduler-runs/plat-165.md) | [PLAT-169](pulse_platform/frontend-chat/plat-169.md) |
| [PLAT-170](pulse_platform/step-execution/plat-170.md) | [PLAT-171](pulse_platform/coding-agent-bridge/plat-171.md) | [PLAT-172](pulse_platform/step-execution/plat-172.md) | [PLAT-173](pulse_platform/learnings-knowledge/plat-173.md) |
| [PLAT-174](pulse_platform/step-execution/plat-174.md) | [PLAT-175](pulse_platform/step-execution/plat-175.md) | [PLAT-176](pulse_platform/scheduler-runs/plat-176.md) | [PLAT-177](pulse_platform/coding-agent-bridge/plat-177.md) |
| [PLAT-178](pulse_platform/chat-reliability/plat-178.md) | [PLAT-179](pulse_platform/coding-agent-bridge/plat-179.md) | [PLAT-180](pulse_platform/coding-agent-bridge/plat-180.md) | |
| [PLAT-182](pulse_platform/scheduler-runs/plat-182.md) | [PLAT-183](pulse_platform/coding-agent-bridge/plat-183.md) | [PLAT-184](pulse_platform/cost-telemetry/plat-184.md) | [PLAT-185](pulse_platform/security-sandbox/plat-185.md) |
| [PLAT-186](pulse_platform/coding-agent-bridge/plat-186.md) | [PLAT-187](pulse_platform/coding-agent-bridge/plat-187.md) | [PLAT-188](pulse_platform/coding-agent-bridge/plat-188.md) | [PLAT-189](pulse_platform/evaluation/plat-189.md) |
| [PLAT-190](pulse_platform/learnings-knowledge/plat-190.md) | [PLAT-191](pulse_platform/scheduler-runs/plat-191.md) | [PLAT-192](pulse_platform/step-execution/plat-192.md) | [PLAT-193](pulse_platform/coding-agent-bridge/plat-193.md) |
| [PLAT-194](pulse_platform/scheduler-runs/plat-194.md) | [PLAT-195](pulse_platform/step-execution/plat-195.md) | [PLAT-196](pulse_platform/pulse-governance/plat-196.md) | [PLAT-197](pulse_platform/plans-contracts/plat-197.md) |
| [PLAT-198](pulse_platform/pulse-governance/plat-198.md) | [PLAT-199](pulse_platform/pulse-governance/plat-199.md) | [PLAT-200](pulse_platform/browser-automation/plat-200.md) | [PLAT-201](pulse_platform/step-execution/plat-201.md) |
| [PLAT-202](pulse_platform/step-execution/plat-202.md) | [PLAT-203](pulse_platform/cost-telemetry/plat-203.md) | [PLAT-204](pulse_platform/browser-automation/plat-204.md) | [PLAT-205](pulse_platform/plans-contracts/plat-205.md) |
| [PLAT-206](pulse_platform/pulse-governance/plat-206.md) | [PLAT-207](pulse_platform/human-decisions/plat-207.md) | [PLAT-208](pulse_platform/human-decisions/plat-208.md) | [PLAT-209](pulse_platform/coding-agent-bridge/plat-209.md) |
| [PLAT-210](pulse_platform/scheduler-runs/plat-210.md) | [PLAT-211](pulse_platform/step-execution/plat-211.md) | [PLAT-212](pulse_platform/plans-contracts/plat-212.md) | [PLAT-213](pulse_platform/pulse-governance/plat-213.md) |
| [PLAT-214](pulse_platform/pulse-governance/plat-214.md) | [PLAT-215](pulse_platform/browser-automation/plat-215.md) | [PLAT-216](pulse_platform/step-execution/plat-216.md) | [PLAT-217](pulse_platform/pulse-governance/plat-217.md) |
| [PLAT-218](pulse_platform/human-decisions/plat-218.md) | [PLAT-219](pulse_platform/scheduler-runs/plat-219.md) | [PLAT-220](pulse_platform/pulse-governance/plat-220.md) | [PLAT-221](pulse_platform/step-execution/plat-221.md); SQLite backlog reconciled 2026-09-05 — verified stale records resolved; see [audit](../audits/platform-backlog-reconciliation-2026-09-05.md), deployed verification not claimed |
| [PLAT-222](pulse_platform/pulse-governance/plat-222.md) | [PLAT-223](pulse_platform/learnings-knowledge/plat-223.md) | [PLAT-224](pulse_platform/browser-automation/plat-224.md) | [PLAT-225](pulse_platform/coding-agent-bridge/plat-225.md) |
| [PLAT-226](pulse_platform/cost-telemetry/plat-226.md) | [PLAT-227](pulse_platform/evaluation/plat-227.md) | [PLAT-228](pulse_platform/learnings-knowledge/plat-228.md) | [PLAT-229](pulse_platform/evaluation/plat-229.md) |
| [PLAT-230](pulse_platform/plans-contracts/plat-230.md) | [PLAT-231](pulse_platform/browser-automation/plat-231.md) | [PLAT-232](pulse_platform/browser-automation/plat-232.md) | [PLAT-233](pulse_platform/browser-automation/plat-233.md) |
| [PLAT-234](pulse_platform/coding-agent-bridge/plat-234.md) | [PLAT-235](pulse_platform/browser-automation/plat-235.md) | [PLAT-236](pulse_platform/evaluation/plat-236.md) | [PLAT-237](pulse_platform/browser-automation/plat-237.md) |
| [PLAT-238](pulse_platform/step-execution/plat-238.md) | [PLAT-239](pulse_platform/pulse-governance/plat-239.md) | [PLAT-240](pulse_platform/pulse-governance/plat-240.md) | [PLAT-241](pulse_platform/scheduler-runs/plat-241.md) |
| [PLAT-242](pulse_platform/scheduler-runs/plat-242.md) | [PLAT-243](pulse_platform/evaluation/plat-243.md) | [PLAT-244](pulse_platform/security-sandbox/plat-244.md) | [PLAT-245](pulse_platform/pulse-governance/plat-245.md) |
| [PLAT-246](pulse_platform/learnings-knowledge/plat-246.md) | [PLAT-247](pulse_platform/integrations/plat-247.md) | [PLAT-248](pulse_platform/browser-automation/plat-248.md) | [PLAT-249](pulse_platform/browser-automation/plat-249.md) |
| [PLAT-250](pulse_platform/frontend-chat/plat-250.md) | [PLAT-251](pulse_platform/frontend-chat/plat-251.md) | [PLAT-252](pulse_platform/frontend-chat/plat-252.md) | [PLAT-253](pulse_platform/frontend-chat/plat-253.md) |
| [PLAT-254](pulse_platform/frontend-chat/plat-254.md) | [PLAT-255](pulse_platform/evaluation/plat-255.md) | [PLAT-256](pulse_platform/frontend-chat/plat-256.md) | [PLAT-257](pulse_platform/learnings-knowledge/plat-257.md) |
| [PLAT-258](pulse_platform/pulse-governance/plat-258.md) | [PLAT-259](pulse_platform/step-execution/plat-259.md) | [PLAT-260](pulse_platform/pulse-governance/plat-260.md) | [PLAT-261](pulse_platform/security-sandbox/plat-261.md) |
| [PLAT-262](pulse_platform/security-sandbox/plat-262.md) | [PLAT-263](pulse_platform/learnings-knowledge/plat-263.md) | [PLAT-264](pulse_platform/integrations/plat-264.md) | [PLAT-265](pulse_platform/learnings-knowledge/plat-265.md) |
| [PLAT-266](pulse_platform/security-sandbox/plat-266.md) | [PLAT-267](pulse_platform/security-sandbox/plat-267.md) | [PLAT-268](pulse_platform/frontend-chat/plat-268.md) | [PLAT-269](pulse_platform/step-execution/plat-269.md) |
| [PLAT-270](pulse_platform/pulse-governance/plat-270.md) | [PLAT-271](pulse_platform/pulse-governance/plat-271.md) | [PLAT-272](pulse_platform/security-sandbox/plat-272.md) | [PLAT-273](pulse_platform/coding-agent-bridge/plat-273.md) |
| [PLAT-274](pulse_platform/coding-agent-bridge/plat-274.md) | [PLAT-275](pulse_platform/coding-agent-bridge/plat-275.md) | [PLAT-276](pulse_platform/security-sandbox/plat-276.md) | [PLAT-277](pulse_platform/frontend-chat/plat-277.md) |
| [PLAT-278](pulse_platform/frontend-chat/plat-278.md) | [PLAT-279](pulse_platform/frontend-chat/plat-279.md) | [PLAT-280](pulse_platform/step-execution/plat-280.md) | [PLAT-281](pulse_platform/security-sandbox/plat-281.md) |
| [PLAT-282](pulse_platform/evaluation/plat-282.md) | [PLAT-283](pulse_platform/security-sandbox/plat-283.md) | [PLAT-284](pulse_platform/security-sandbox/plat-284.md) | [PLAT-285](pulse_platform/plans-contracts/plat-285.md) |
| [PLAT-286](pulse_platform/step-execution/plat-286.md) | [PLAT-287](pulse_platform/step-execution/plat-287.md) | [PLAT-326](pulse_platform/pulse-governance/plat-326.md) | [PLAT-327](pulse_platform/evaluation/plat-327.md) |
| [PLAT-328](pulse_platform/step-execution/plat-328.md) |  |  |  |

## Explicitly not platform issues

The following remain workflow-owned or evidence-state items even when they are
currently blocked:

- a collector catches an exception and ignores the loaded variables;
- a step hardcodes an incorrect log group or database probe result;
- a plan's editable Folder Guard configuration omits a required folder;
- a fix is waiting for the next producing run;
- an upstream service has not produced data;
- a strategy requires a user decision or deployment approval;
- a workflow writes through direct `sqlite3` despite having a documented,
  reachable sanctioned mutation tool.

If the configuration is not editable because the platform exposes no sanctioned
edit path, that missing edit path is the platform defect—not the workflow's
desired configuration.

## Lifecycle behavior required

Until the platform lifecycle is implemented, these records should be treated as
one of two states:

- **platform_open:** confirmed on the current binary and owned outside the
  workflow;
- **platform_reverify:** a platform change exists, and the next applicable real
  run must prove the observation is gone.

They must remain visible to the operator but must not consume normal workflow
Fixer capacity, count as recurring workflow-strategy evidence, or occupy the
reviewer's new-finding cap. A new observation should link to the platform key
above rather than filing another independent platform issue.

After a platform fix ships:

1. move every linked workflow finding to awaiting verification;
2. run the smallest real or E2E producer that exercises the boundary;
3. close all linked findings together when it passes;
4. reopen the single platform issue with the new evidence when it fails.

## Recommended implementation order

1. PLAT-001 human-input propagation — implementation complete; real full-run reverify pending.
2. PLAT-002 canonical tool-error precedence — implementation and focused tests complete; persisted runtime evidence pending.
3. PLAT-003 DB capability exposure — uniform workflow-step read/write is
   implemented and focused capability tests pass; source workflows need
   post-build reverify.
4. PLAT-004 scheduler completion barrier — runtime verified by the full
   2026-08-04 RTS scheduled run; retain regression coverage.
5. Reverify the corrected PLAT-020 same-session schedule-to-chat handoff first:
   the session ID must remain stable and the message must reach the retained or
   resumed tmux exactly once.
6. Reverify completed PLAT-009, PLAT-010, PLAT-018, PLAT-019, and PLAT-021 on
   the next applicable RTS Pulse pass/UI open.
7. Add the platform issue/link lifecycle so the backlog stops duplicating them.
8. Reverify PLAT-006 and the remaining Tectonicus half of PLAT-014; PLAT-013 is
   runtime verified on RTS Latency.
9. Reverify PLAT-015 and PLAT-016 with a producing Social Media evaluation;
   PLAT-017 now has a current-binary Tectonic reproduction and can proceed to
   durable terminal-event/reconciliation design.
10. Repair/query-test the remaining P1 observability and media defects.
11. Address the P2 configuration and changelog completeness gaps.
12. Reverify PLAT-027 and PLAT-028 together on the next Social Media CDP run:
    the asynchronous child must stay visible and its browser action must not
    forward the tab ID as an element selector.

The first four are ordered ahead of cost and UI completeness because they can
change what a workflow does while still allowing it to report success.

# Pulse platform ticket categories

Ticket files live in per-category subdirectories of
[`pulse_platform/`](pulse_platform/) (refactored 2026-09-14 from one flat
directory; filenames and `PLAT-NNN` ids are unchanged, only the directory
changed — `git log --follow` traces each file back).

The full filename → category mapping is
[pulse_platform_mapping.tsv](pulse_platform_mapping.tsv).

## Filing rule for new tickets

First search the [workstream register](pulse_platform_issue_register.md) and
existing tickets. Add evidence or follow-up acceptance to an existing ticket
when the root cause and outcome are the same. File a new `plat-NNN.md` only
for an independently actionable defect with its own fix boundary and
acceptance. Put it in exactly one category directory — the subsystem that owns
the defect. When a ticket genuinely spans two subsystems, pick the owner of
the fix and name the runner-up in the ticket body. Then:

1. Link it from the complete ticket index in
   [pulse_platform_issue_register.md](pulse_platform_issue_register.md) as
   `pulse_platform/<category>/plat-NNN.md`; add it to the relevant workstream's
   recent anchors only if it changes the current focus.
2. Start the file with the index header:
   `[← Pulse platform issue index](../../pulse_platform_issue_register.md)`.
3. Link other tickets relatively: `(plat-MMM.md)` for the same category,
   `(../<category>/plat-MMM.md)` across categories.

`TestEveryPulsePlatformTicketIsLinkedFromTheRegister`
(`agent_go/cmd/server/pulse_register_integrity_test.go`) enforces the
register ↔ file invariant in both directions.

## Categories (365 unique tickets, 2026-09-29)

| Directory | Tickets | What belongs here |
|---|---|---|
| `pulse-governance/` | 50 | Pulse reviews, Gate, Fixer, finding identity/lifecycle/dedup, Review+Fix dispatch, verification, review modules, finalizer, focus rotation, goal metrics |
| `coding-agent-bridge/` | 52 | CLI adapters (Claude/Codex/Pi/Cursor), retained turns, tmux sessions, tool-call event identity, transcripts, live-input, live-attach, MCP bridge behavior |
| `step-execution/` | 43 | Step execution models (message_sequence/scripted/todo/routing/branch), step_config, step tool surface and DB/filesystem grants, workspace tools, schema migration |
| `frontend-chat/` | 35 | Chat UI presentation, activity monitor, report pane and reports, execution logs, decision cards, plan/goal views, deploy chunk issues |
| `chat-reliability/` | 6 | Durable chat history, user ownership and storage, resume/restore continuity, native transcript reconciliation, chat migration |
| `scheduler-runs/` | 33 | Scheduling, cron, occurrences, fire decisions, leases, terminal-state reconciliation, run-folder identity, retention, schedule history |
| `security-sandbox/` | 29 | Landlock/sandbox, Folder Guard, secrets, read-only tier, session isolation, MCP management boundaries, access control |
| `learnings-knowledge/` | 21 | Reflection turn, learnings/KB contracts and locks, skill and prompt guidance, guidance tests |
| `cost-telemetry/` | 17 | Cost ledger, attribution, rate cards, usage telemetry, Pulse-vs-workflow cost |
| `evaluation/` | 16 | Eval harness, pre-validation, validation schemas, evaluation plans |
| `plans-contracts/` | 17 | Plan mutations, changelog coverage, contract upgrades, upgrade preflight |
| `integrations/` | 13 | Webhooks, Slack/email/WhatsApp notifications, Gmail/GWS, MCP catalog, media tools, voice/STT |
| `human-decisions/` | 14 | Human input, operator decisions, approvals, attribution, human-decided branches |
| `performance/` | 5 | Latency, contention, and runtime resource use |
| `browser-automation/` | 14 | agent_browser, CDP tabs, snapshots, managed browser |

Non-ticket files staying at the top of `pulse_platform/`: the two
`baseline-*.json` snapshots.

## Borderline calls

Tickets that span two subsystems, with the runner-up category recorded so the
ambiguity is documented rather than lost. Format: ticket → chosen (runner-up).

- PLAT-002 → coding-agent-bridge (step-execution)
- PLAT-011 → cost-telemetry (frontend-chat)
- PLAT-019 → cost-telemetry (pulse-governance)
- PLAT-020 → coding-agent-bridge (scheduler-runs)
- PLAT-025 → step-execution (security-sandbox)
- PLAT-030 → coding-agent-bridge (frontend-chat)
- PLAT-036 → cost-telemetry (frontend-chat)
- PLAT-043 → step-execution (security-sandbox)
- PLAT-045 → pulse-governance (human-decisions)
- PLAT-049 → plans-contracts (pulse-governance)
- PLAT-053 → coding-agent-bridge (step-execution)
- PLAT-060 → step-execution (pulse-governance)
- PLAT-062 → step-execution (security-sandbox)
- PLAT-064 → frontend-chat (scheduler-runs)
- PLAT-067 → scheduler-runs (coding-agent-bridge)
- PLAT-069 → cost-telemetry (pulse-governance)
- PLAT-075 → evaluation (scheduler-runs)
- PLAT-077 → human-decisions (pulse-governance)
- PLAT-078 → security-sandbox (coding-agent-bridge)
- PLAT-082 → step-execution (scheduler-runs)
- PLAT-084 → pulse-governance (scheduler-runs)
- PLAT-091 → evaluation (scheduler-runs)
- PLAT-094 → pulse-governance (coding-agent-bridge)
- PLAT-100 → coding-agent-bridge (scheduler-runs)
- PLAT-104 → frontend-chat (coding-agent-bridge)
- PLAT-107 → frontend-chat (coding-agent-bridge)
- PLAT-111 → frontend-chat (cost-telemetry)
- PLAT-113 → coding-agent-bridge (scheduler-runs)
- PLAT-114 → coding-agent-bridge (pulse-governance)
- PLAT-117 → coding-agent-bridge (frontend-chat)
- PLAT-119 → pulse-governance (learnings-knowledge)
- PLAT-125 → step-execution (security-sandbox)
- PLAT-130 → scheduler-runs (frontend-chat)
- PLAT-132 → integrations (coding-agent-bridge)
- PLAT-134 → security-sandbox (coding-agent-bridge)
- PLAT-136 → cost-telemetry (frontend-chat)
- PLAT-139 → coding-agent-bridge (scheduler-runs)
- PLAT-140 → frontend-chat (coding-agent-bridge)
- PLAT-142 → pulse-governance (coding-agent-bridge)
- PLAT-146 → scheduler-runs (human-decisions)
- PLAT-147 → pulse-governance (integrations)
- PLAT-148 → pulse-governance (coding-agent-bridge)
- PLAT-158 → pulse-governance (scheduler-runs)
- PLAT-164 → coding-agent-bridge (pulse-governance)
- PLAT-168 → learnings-knowledge (step-execution)
- PLAT-169 → frontend-chat (security-sandbox)
- PLAT-170 → step-execution (security-sandbox)
- PLAT-171 → coding-agent-bridge (scheduler-runs)
- PLAT-175 → step-execution (security-sandbox)
- PLAT-176 → scheduler-runs (step-execution)
- PLAT-178 → chat-reliability (coding-agent-bridge)
- PLAT-182 → scheduler-runs (step-execution)
- PLAT-184 → cost-telemetry (pulse-governance)
- PLAT-185 → security-sandbox (step-execution)
- PLAT-188 → coding-agent-bridge (security-sandbox)
- PLAT-189 → evaluation (step-execution)
- PLAT-211 → step-execution (pulse-governance)
- PLAT-218 → human-decisions (step-execution)
- PLAT-221 → step-execution (security-sandbox)
- PLAT-223 → learnings-knowledge (step-execution)
- PLAT-228 → learnings-knowledge (pulse-governance)
- PLAT-234 → coding-agent-bridge (learnings-knowledge)
- PLAT-241 → scheduler-runs (evaluation)
- PLAT-244 → security-sandbox (integrations)
- PLAT-248 → browser-automation (learnings-knowledge)
- PLAT-249 → browser-automation (security-sandbox)
- PLAT-253 → frontend-chat (human-decisions)
- PLAT-254 → frontend-chat (scheduler-runs)
- PLAT-255 → evaluation (learnings-knowledge)
- PLAT-257 → learnings-knowledge (security-sandbox)
- PLAT-260 → pulse-governance (learnings-knowledge)
- PLAT-267 → security-sandbox (scheduler-runs)
- PLAT-268 → frontend-chat (coding-agent-bridge)
- PLAT-270 → pulse-governance (integrations)
- PLAT-279 → frontend-chat (learnings-knowledge)
- PLAT-283 → security-sandbox (step-execution)
- PLAT-284 → security-sandbox (scheduler-runs)
- PLAT-285, PLAT-290 → plans-contracts (learnings-knowledge)
- PLAT-292 → frontend-chat (security-sandbox)
- PLAT-294 → step-execution (evaluation)
- PLAT-295 → human-decisions (step-execution)
- PLAT-296 → security-sandbox (coding-agent-bridge)
- PLAT-298 → step-execution (learnings-knowledge)
- PLAT-299 → frontend-chat (integrations)
- PLAT-304 → security-sandbox (scheduler-runs)
- PLAT-310 → learnings-knowledge (security-sandbox)
- PLAT-311 → pulse-governance (frontend-chat)
- PLAT-312 → security-sandbox (integrations)
- PLAT-313 → coding-agent-bridge (frontend-chat)
- PLAT-315 → pulse-governance (frontend-chat)
- PLAT-316 → frontend-chat (pulse-governance)
- PLAT-318 → frontend-chat (plans-contracts)

## Notes

- There is no `plat-073.md`: `pulse-governance/plat-073-remaining-board.md` is
  the PLAT-073 artifact (the remaining `external_action_required` board).
- Numbers 079, 157, 181 and 302 have no files. PLAT-157/181/302 were
  browser-ownership tickets consolidated into PLAT-322, which is now the
  canonical browser contract.
- PLAT-285 and PLAT-290 have near-identical titles and bodies (see the
  renumbering note in PLAT-285); both are kept as-is, pending an owner
  decision to merge or differentiate.
- The duplicate chat-reliability PLAT-339 pointer was removed on 2026-09-29.
  PLAT-339 belongs to the security-sandbox Crew-invocation ticket; the chat
  Stop/resume work is PLAT-340.

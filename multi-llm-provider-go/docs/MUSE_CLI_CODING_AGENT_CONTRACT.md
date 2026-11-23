# Muse Coding Agent Contract Specification

Recon date: 2026-09-10. CLI: `muse` (Muse Code) `1.1.1`, `~/.local/bin/muse`.
Status: adapter implemented. Claims below are marked **verified** when backed
by a local or live test and **TBD** where a provider run is still required.

> Name warning: `copilot` on PATH is AWS ECS Copilot, not this agent.
> Provider id: `muse-cli`. CLIName: `muse`.

---

## Dual-Transport Model

Like Codex, Muse ships both transports, so the adapter should plan for both
with tmux as the default product path:

1. **Stateful tmux transport (default path, verified).** Bare `muse [PROMPT]`
   runs the interactive TUI; no subcommand means interactive mode. Tmux
   is wrapped in tmux for builder sessions and transcript streaming.
2. **Structured transport (`muse exec --json`, verified).**
   `muse exec --provider echo --json "say the word pineapple"` exits 0 and
   streams MSP wire-schema JSONL: `run.output.delta` (`payload.text`) while
   running, terminal `run.terminal.completed` (`payload.text`,
   `payload.terminal`) at the end. `muse schema` exports the wire schema
   (JSON Schema / TypeScript) — pin the parser against it.

## Session Registry & Lifecycle

* Sidecar transcript (**verified**): `$XDG_DATA_HOME/muse/sessions/YYYY/MM/DD/<session-uuid>/session.jsonl`,
  plus `subagent/<id>/session.jsonl` children. Respects `XDG_DATA_HOME`
  (confirmed by redirecting it to `/tmp`). This is the final-answer and
  token-usage source for tmux mode — never pane-scrape for the final reply.
* `muse export --session <id|path> [--last] [--out <file>] [--redacted]`
  dumps one session as a self-contained JSON document (messages, tool
  calls/results, approvals, model ids, fork/subagent lineage).
* Suggested tmux registry prefix (following the other providers):
  `mlp-muse-cli-int`.

## Tmux turn lifetime (2026-09-12)

Submitted turns and waits for a previously active persistent turn use the
caller's context deadline/cancellation. There is no adapter-level five-minute
turn cutoff or 90-second cutoff for a busy reused terminal. Previously these
cutoffs returned retryable timeouts while Muse kept executing the original
turn, causing repeated waits and risking resubmission after it finished.
Fresh terminal startup and prompt intake still have bounded readiness checks.

Completion requires idle chrome, pane stability, a quiet transcript, and no
running-tool indicator in the latest rendered block. A silent shell/MCP call
is not completion merely because no text arrived for five seconds. Live-input
steering remains available during tools. P0 coverage uses an isolated real
tmux pane with a frozen running-tool display, eventual completion, and caller
cancellation; the echo-provider resume test covers native session continuity.

Remaining gap: this is a TUI/transcript heuristic, not an authoritative Muse
turn-completion API. A permanently hung native call with no caller deadline
requires cancellation; this change does not add an independent stall watchdog.

## Resume (verified at CLI level, context carryover TBD)

* Interactive: `muse resume --last | <session-uuid>` (bare `resume` opens a picker).
* Headless: `muse exec --session-id <uuid> "<next prompt>"` reuses the
  session — verified: second run exited 0 and appended to the same
  `session.jsonl` (116 lines after two echo turns).
* Context carryover is **TBD**: the echo provider cannot prove the model
  sees prior turns. Needs one Meta-provider two-turn run.
* Contract: `SupportsNativeResume: true` once proven; surface the session id
  (`stream.id`) in `GenerationInfo.Additional` and consume it via
  `NativeResumeOption`, like the other four providers.

## Auth

* `muse login` (Meta account, browser code approval). `META_API_KEY` env
  **always takes priority** over the account login.
* `muse auth set [--provider <PROVIDER>] --api-key-stdin` (key via stdin,
  never argv). `muse exec --api-key-stdin` for one-shot runs.
* Same shape as Claude Code: saved login or explicit key, no third-party
  key forwarding. `APIKeyEnvVars: ["META_API_KEY"]` (TBD: exact provider
  token names for `--provider meta`).

## MCP Bridge & System Prompt

* MCP servers are read from `$XDG_CONFIG_HOME/muse/settings.json` with
  `{"schema_version":1,"mcpServers":{"<name>":{"url":"<streamable-http>"}}}`.
  Each adapter launch now uses its own private configuration directory. It
  copies login/trust files and user preferences, replaces inherited MCP servers
  and `PreToolUse` hooks with the exact launch configuration, and removes the
  private directory on cleanup. Concurrent sessions do not modify shared user
  settings or inherit stale bridge URLs. Both transports use this isolation.
  An unavailable required MCP server still fails startup explicitly.
* **Accepted best-effort restrictions (2026-09-11, Muse Code
  1.1.1-R2514.1):** AgentWorks configures a native allowlist of `web_search`,
  adds `--disable-shell --disable-write`, and disables automatic native subagent
  delegation and workflow triggering. A Node.js `PreToolUse` hook denies
  unlisted calls that enter it. Missing Node.js fails launch before the policy
  is installed. MCP tools, `tool_search` discovery, and the internal
  `submit_reminder_decision` verdict sink remain allowed; blocking that sink
  caused observer retries. The routing prompt directs questions and task work
  to platform tools. These controls reduce native-tool use but do not form a
  complete security boundary.
* **Verified protections:** the live P0 test attempts native file reads and a
  shell write against disposable files, then requires an actual MCP round trip.
  The final successful live run returned explicit hook denials for both
  `read_file` and `bash` in both transports, and no shell file was created.
  Earlier tmux runs declined the shell attempt; those observations establish
  absence of the write, not an exercised shell-hook denial. The test inspects actual
  call/result pairs in native transcripts, not tool names mentioned in prompts.
* **Confirmed gaps:** native tool definitions remain visible to the model.
  `write_todos` executed successfully without a `PreToolUse` event in both
  transports; later runs sometimes declined to call it. Other internal
  session controls have not been fully certified. File/shell protection does
  not establish that every memory, goal, cron, reminder, workflow, or session
  operation is blocked. Hook execution failures and behavior after future Muse
  upgrades are also not certified. No removal of all native tools is claimed.
* A named `run.toolset=["web_search"]` removes mounted MCP tools as well and
  still adds `write_todos`; concrete MCP tool names are rejected as unknown
  native names. The adapter therefore removes an inherited named toolset when
  installing this policy, keeping MCP discoverable and gating native execution
  with the hook instead.
* Native question widgets may still appear. The existing tmux recovery silently
  chooses an explicitly recommended answer and handles multi-page review;
  unsupported or ambiguous questions still require user input. Restriction
  changes do not add automatic-selection announcements to the UI.
* `SupportsBridgeOnlyTools` remains **false**. `ToolRestrictionGaps` explicitly
  records the accepted Muse exception, which requires the P0
  `best_effort_tool_restrictions` live certification. It does not certify strict
  `bridge_only_tools` containment. Other providers retain the strict P0
  requirement; clearing a capability flag cannot waive it.
* `TestMuseCLIRealMCPBridge` remains a reachability proof.
  `TestMuseCLIRealBestEffortToolRestrictions` is the separate restriction proof.
  A skipped live test is not evidence of certification. To rerun with a logged-in
  Muse CLI, Node.js, and tmux available:

  ```sh
  go test ./pkg/adapters/musecli -run '^TestMuseCLIRealBestEffortToolRestrictions$' \
    -v -count=1 -timeout=11m -args -coding-cli-p0-live
  ```

  Unit coverage checks hook decisions, nested JSON arguments, the Node.js
  prerequisite, explicit empty policies, isolated concurrent bridge mounts,
  inherited toolset removal, and restriction flags. Model refusal to attempt
  `write_todos` is logged as untested in that run; successful execution is
  reported as the known gap, not mislabeled as strict containment.
* Platform image inspection is not a Muse-native exception to that policy.
  Host applications may project their registered `read_image` executor as an
  additional MCP bridge tool. Muse calls that mounted tool directly while its
  native allowlist remains `web_search` only; the platform tool independently
  selects a supported image-analysis backend (for example Codex CLI).
* No `--system-prompt` / `--append-system-prompt` / `--instruction` flags
  exist (grep over `muse --help` + `muse exec --help` is empty).
* System prompt goes through project rules: `muse init [--dry-run] [--force]`
  scaffolds `AGENTS.md` ("Muse Code reads this file as project rules when it
  runs in this directory") — same convention as Codex/Pi. Adapter projection
  target: `<workdir>/AGENTS.md`. `WorkingDirInstructionFile: "AGENTS.md"`.
  User-level rules path **TBD** (personal rules exist — see
  `--no-foreign-personal-context` — but the file location is unconfirmed).
* Skills are first-class: `muse skills list|inspect|enable|disable|install|import|update|uninstall`
  with `--source all|user|project|built-in|plugin` and
  `muse skills import --from claude|codex`. Project skill dirs (**verified**
  2026-09-10, one run, distinct names per dir): `<workdir>/.agents/skills/<name>/SKILL.md`
  and `<workdir>/.claude/skills/<name>/SKILL.md` (Claude compat) are both
  discovered with activation `on` by default — no `enable` step needed.
  Ruled out: `<workdir>/.muse/skills/` and `<workdir>/skills/` (never listed).
  Hard requirement: trusted workspace (`--trust-workspace` or saved trust);
  untrusted workspaces skip project skills entirely with diagnostic
  `project-skills-untrusted`. Adapter projection target:
  `<workdir>/.agents/skills/<name>/SKILL.md`.
* `UsesNativeSystemPrompt` **TBD**.

## Trust & Approval Gates

* Workspace is **untrusted by default** (observed: "workspace is untrusted",
  "Agent delegation: auto unavailable"). Contract: `RequiresWorkspaceTrust: true`.
* Per-run gates: `--trust-workspace`, `--approval-mode untrusted|on-request|never`,
  `--disable-approval`, `--disable-sandbox`, `--sandbox-network <MODE>`,
  `--yolo`, `--permission-profile <ID>`.

## Adapter Constraints (verified — will bite if ignored)

1. **Never pass `--no-session-log`.** `exec` hard-fails:
   `local session messaging disabled: session logging is required`.
   Same for `--session-id` without a writable session store.
2. **Resolve the transcript via `XDG_DATA_HOME`** (`$XDG_DATA_HOME/muse/sessions/...`),
   not a hardcoded `~/.local/share/muse` — honors the env override.

## Exec Lane (implemented 2026-09-10, echo-verified)

`pkg/adapters/musecli/musecli_exec.go`: `GenerateContent` shells
`muse exec --json --provider <p> --trust-workspace [--api-key-stdin]
[--model <m>] <prompt>`, parses MSP wire JSONL (`run.output.delta` →
content chunks + accumulation, `run.terminal.completed` → authoritative
final text), forwards deltas to `opts.StreamChan` (caller-owned, never
closed here), attaches `{provider: muse-cli, transport: structured,
native_session_id: stream.id}`. System messages fold into a header (no
exec system-prompt flag exists); `--model` skipped for placeholder and
echo runs; key via stdin avoids argv leak. `MUSE_CLI_EXEC_PROVIDER`
overrides the provider (tests use `echo`). Always-green proof:
`TestMuseExecLaneEchoFinalText/StreamsDeltas` (XDG-isolated, no auth).
Sidecar readers (`musecli_transcript.go`, implemented 2026-09-10):
log path globbed as `$XDG_DATA_HOME/muse/sessions/*/*/*/<id>/session.jsonl`;
usage sums `goal_usage_attribution` quantities per run (echo reports honest
zeros); messages map intake prompts + `assistant_message_committed` texts;
exec lane attaches `resp.Usage` best-effort (proven by the echo test
asserting non-nil). Contract now `SurfacesTokenUsage: true`,
`TokenUsageSource: "transcript-file"`, `AdapterReadsTranscript: true`
(coherence test green); usage magnitudes on a paid model still TBD.

## Structured Resume (implemented 2026-09-10, echo-verified)

`WithMuseResumeSessionID` (adapter-local option + provider wrapper +
`nativeResumeRegistry` entry; contract `SupportsNativeResume: true`,
drift test green). Exec lane passes `--session-id`; each turn attaches
`{structured, stream.id}` so the next turn resumes it. Proof:
`TestMuseExecLaneStructuredMultiTurn` — two echo turns, same session id
both turns, both prompts in one on-disk log (cert registered, red list
unchanged at 12 since `multi_turn`/tmux is still open). Echo cannot prove
the model saw turn one — one Meta turn pair still owed. Full restore
wiring (`mcpagent.Agent` field + `server.go` switch) deferred with the
tmux lane.

## Token & Cost Tracking (TBD)

`readMuseTranscriptUsage` / `readMuseTranscriptMessages` do not exist yet.
Next probe: grep a real (Meta-provider) `session.jsonl` for usage/model
fields, following `docs/COSTS_AND_CONVERSATION_HISTORY.md` ("Adding a new
tmux provider"). Until then `TokenUsageSource` is undecided.

## Testing

* Contract entry goes in [coding_agent_contract.go](../coding_agent_contract.go)
  (`Transport: CodingAgentTransportTmux`); expect
  `TestActiveCodingAgentProvidersSatisfyP0Contract` to go red — that
  failure list is the implementation todo.
* Live proofs gate on `-coding-cli-p0-live`, mirroring the other four
  providers; credential-free P0 is the tmux lifecycle matrix.
* P0 order: tmux smoke (`fresh_launch`) → usage/message readers with
  synthetic `session.jsonl` fixtures → bridge-only proof → structured
  lane (`exec --json` argv pinned by unit test) → multi-turn resume on a
  real model.

### First-option answers in the native question widget

The tmux adapter automatically selects the first option in Muse's native
`Request user input` widget, whether or not another option is labelled
`(Recommended)`. It follows the Cursor readiness pattern: consecutive captures,
validates the active dialog immediately before sending keys, and waits for the
dialog to change before another submission. The current cursor is observed
rather than assumed to be on the first choice. Multi-question forms advance
through each question and submit the final review. Muse 1.3 animates the tool
heading through multiple glyphs; the detector and question identity must stay
stable across those frames.

Unrecognized dialogs and auth/trust prompts remain manual.
`WithAutoSelectRecommended(false)` retains the legacy name and restores manual
`user_input_required` behavior for callers that need it. Automatic selections
do not emit UI announcements. Context cancellation stops further key delivery.

Live regression: `TestMuseCLIRealAutoFirstOptionThreeQuestionsP0`; parser and
cancellation/duplicate checks: `TestMuseRecommendedQuestion`,
`TestMuseRecommendedReview`, and `TestMuseAutoAnswerDisabledCanceledAndDuplicate`.

Live input and retained-turn polling service the same native controls, including
questions that appear after `GenerateContent` has returned. Before each typing
attempt, live delivery resolves eligible questions and review screens. Selector
state is shared per persistent session to serialize key delivery; the selector
does not retain or write to generation stream channels.
Killing the session disables further automatic answers.

Live regression: `TestMuseCLIRealLiveInputQuestionsP0` covers a three-page form
created after live delivery, plus a preexisting dialog with its cursor on the
wrong option. These are real adapter/tmux tests, not HTTP or desktop UI tests.

### Restoring long native conversations

A newly created tmux terminal can resume an existing conversation. When a native
session ID is supplied, startup waits for stable prompt/status chrome using the
same readiness check as a retained terminal. It must not require the `Muse Code`
welcome banner: replayed history can scroll that banner out of the visible pane.
Fresh conversations retain the banner check. Both paths retain question handling,
auth/trust gate detection, terminal liveness, and pane stability checks.

`TestMuseResumeHandleRoundTripTmuxP0` uses the real Muse CLI's credential-free echo
provider to create history longer than the pane, serialize the session handle,
kill the terminal, restore via launch-only, and send a follow-up. It asserts that
the restored pane has no welcome banner and keeps the same native ID and working
directory. Run it with `go test ./pkg/adapters/musecli -run
'^TestMuseResumeHandleRoundTripTmuxP0$' -count=1`.

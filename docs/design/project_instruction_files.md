# Project instruction files, skills and the reader role

Status: merged to `main` 2026-09-30 (not deployed). Ticket:
PLAT-371. Supersedes the
per-adapter "write CLAUDE.md/AGENTS.md, delete on cleanup" behavior that
PLAT-296 and
[isolated workflow testing](../getting-started/isolated-workflow-testing.md)
describe for shared folders.

## Where each kind of chat runs

| Run | CLI working directory |
|---|---|
| Crew chats, schedules, triggers, bots | private runtime per user/project/chat/provider/mode, with `project/` linked to the real Crew folder |
| Code chats, schedules, triggers, bots | the project's own folder (shared by every session in it) |
| Workflow Run/Builder chats, schedules and bots | private per-session/mode runtime outside the docs tree, with `project/` linked to the real workflow ([design](workflow_shared_folder_plan.md)) |
| Workflow step execution agents | private session-stable temporary runtime with `output/` linked to this invocation's real step artifact folder |

## Workflow step outputs

Execution steps and step orchestrators use the same isolated session directory
across turns. `output/` links to their resolved `STEP_OUTPUT_DIR`, scoped to the
current iteration/group/step (including nested and message-sequence overrides).
Native deliverables written under `output/` are real artifacts immediately;
bridge paths and environment variables keep their existing contracts. Native
tool modes are unchanged. Generated instructions, projected skills and CLI
configuration stay in the private runtime. Reviews and learning agents do not
inherit an output link from a parent's environment.

The target must match the dedicated tool session and its write guard. The
native CLI policy takes the step's workspace grants rather than the parent's
chat grants; any admitted DB/cache/KB/subtree capabilities stay intact. Private
CLI homes remain per step. Wrong or obstructed links fail launch. Cleaning the
runtime removes its directory link without deleting the actual output folder.

## Crew linked runtimes

Crew's Run and Builder prompts, generated instructions, projected skills and
configuration live in different private runtime directories outside workspace
documents. `project/` links the whole real Crew directory, so a new file, atomic
save, rename or delete immediately affects authoritative project data without a
copy/sync layer. Native tools use `project/<path>` or `cd project`; bridge tools
keep their existing real-project-relative paths without this prefix.

The project link is checked at every launch; a replacement link, file or directory
fails launch rather than being overwritten or silently adopted. Runtime cleanup
does not remove the linked project. The real project's instruction files and
native CLI folders are never used as projection destinations.

Landlock must grant the real target separately: Builder can read/write it, Run
can only read it. Both can write their own runtime. Run's final policy removes
all workspace write grants other than that runtime. A link into an ungranted
folder remains denied. Isolation of instructions is not a replacement for these
kernel permissions or the bridge folder guard.

## Rules for projection destinations

Workflow chats also use linked private runtimes. Their existing separate prompts
and skill bundles are preserved; workspace bridge paths remain unchanged. Run
always isolates, including when the transitional Builder rollback is enabled.
Read-only turns lose real-workflow and attached-folder CLI write grants. The
backend may still execute the workflow's authorized actions and persist results.
The added link preserves existing runtime identities and compatible native resume.

1. **Never delete what we did not create.** A project's own `AGENTS.md`,
   `.claude/`, `.cursor/`, `.pi/`, `.codex/`, `.agents/` are read, never replaced.
2. **One project-instructions file: `AGENTS.md`.** Claude Code and Codex read it
   natively. We no longer write `CLAUDE.md`.
3. **The session prompt is a marked block**, added after the user's text:
   `<!-- BEGIN agentworks-session-instructions created=true|false -->` …
   `<!-- END agentworks-session-instructions -->`. `created=true` means we created
   the file. Implementation: `multi-llm-provider-go/pkg/projectfile`.
4. **Sessions are counted per file.** Each session start adds or refreshes the block
   (so a changed prompt is picked up); each end releases one hold; the last release
   removes only the block, and the file too when we created it and nothing else is
   left. Release is idempotent (lease tokens), so cleanup running twice is safe.
   Edits the user makes while a session runs are kept.
5. **Crash leftovers** are stripped at the next start (`StripStale`), never while a
   live session holds the file.
6. **Uniquely named files we fully own** (Cursor's `.cursor/rules/mlp-system.mdc`) use
   an owned lease: counted, refreshed, removed by the last session, prior content
   restored.
7. **Projected skills** carry `.agentworks-managed` in each skill folder. Cleanup
   removes only folders with the marker. A same-named folder without the marker (an
   earlier version's, or the project's own) is taken over and marked, so skill
   updates keep reaching existing projects; differently named skills are untouched.
8. **Generated configuration** (`hooks.json`, `mcp.json`, `cli.json`, ...) is removed
   only when its content is recognisably ours.

Per CLI: Claude → `AGENTS.md` block; Codex, Muse → `AGENTS.md` block; Pi →
`.pi/APPEND_SYSTEM.md` block; Cursor → `.cursor/rules/mlp-system.mdc` and its `mcp.json`, `cli.json`, `hooks.json` and hook script are owned leases (counted, always restoring; a project's own `.cursor` files are put back), and the temporary `.git` marker is shared and removed only by the last session;
agy → no prompt file (hooks are counted per workspace). Codex can also take the
prompt through `-c model_instructions_file` and Claude through
`--system-prompt-file`, but `mcpagent` turns on project-instruction-only for Claude,
Codex and Muse, so the block is the carrier. For Claude that needs a binary that reads
`AGENTS.md` (2.1.284+; 2.1.233 does not): an older or unreadable-version binary gets the
prompt through `--system-prompt-file` instead, and deploys keep exactly one managed copy
of each CLI (see PLAT-371).

## The project's own instructions (PLAT-692)

Code and Crew projects may keep a `PROJECT_INSTRUCTIONS.md` at the project root. The profile resolver
(`agent_profile_runtime.go`) reads it every turn and the query path adds it as the LAST instruction section
(`pkg/projectinstructions`), so it lands at the end of the session block in each CLI's carrier, after every
platform section agent_go assembles (mcpagent's runtime tool routing/tool manifest still follow it). It is capped
at 32 KB with a truncation note, and text that would close the managed block early is neutralised. It is an
ordinary project file: not in the managed projection guard, editable in Identity → General and by the agent. Its
hash joins the session fingerprint, so a retained CLI relaunches with resume when it changes. Crew readers get it
but cannot edit it.

## Owner and reader

A Crew has two roles. The owner (Builder) has full tools. A reader (Run: a
non-owner chatting in the Crew, or a guest call into the owner's Crew) is read-only.

- **Separate prompts and skills.** Builder uses the Crew profile and feature
  bundle plus `crew-builder`. Run uses `prompts/run.md` and `crew-run`, keeps
  domain/project skills, and drops the platform authoring bundle and authoring
  feature extensions. Project memory guidance becomes read-only.
- **The reader role is a block in front of each message**
  (`crew_session_mode.go`), on the normal turn and on live input. It is stripped
  from chat history, submission records and native transcripts; the UI shows what the
  user typed. An owner's message is unchanged.
- **Tools and guards enforce it** (reader-denied tools are not registered; folder
  guards block writes).
- **Refusals repeat it.** In a read-only session a refused write, a call to a mutating
  tool the reader was never given, and a shell command blocked by the sandbox all say
  the session is read-only and to offer the change to the owner with
  `submit_crew_suggestion` (a workflow run: `submit_workflow_suggestion`). Hidden tools
  stay out of the catalog; the bridge answers a call to one with the message instead of
  "not found".
- The notice follows whether the TURN is read-only, not who the caller is: a Slack or WhatsApp
  channel route with a read grant runs as the Crew's owner but read-only, and gets it too
  (worded for a shared channel).
- **No terminal for read-only access.** Typing in the native terminal skips the notice, so a
  caller looking at their own read-only session gets no terminal (403 from the main-terminal
  route, refused on every terminal route); owners and editors keep it.
- There is no new mode toggle. Trusted access selects the mode; a pinned Run
  request or a read-only bot route can downgrade an owner's turn, never promote
  a reader. Guest function calls always use Run.
- Code has no reader prompt; its shared participants are governed by the Code share
  rules.

## Session ids

Crew and Code take theirs from the conversation registry: `work:project:<id>` /
`code:project:<id>` for the main chat, `product-<uuid>` for extra chats (a
`:suffix` conversation key), and a reader's chat is stored under their own account.
Workflows: see the [plan](workflow_shared_folder_plan.md) (native CLI session keyed
by session id and mode).

Crew native resume also checks the private runtime directory, including Codex's
project-directory override. An old shared-folder native session or a different
mode cannot override the new working directory: it starts fresh using saved
application history. Same-mode private sessions keep native resume across a
restart, day-folder rollover and compatible profile definition changes.

## Tests

`pkg/projectfile` unit tests (user's file, overlap, edits during a session, stale
blocks, leases, owned files); `skillproject` (adoption, user skills);
`mcpagent` cleanup tests (user's `.claude/.cursor/.pi/.codex/.agents` survive every
provider start); server tests for the session block, its stripping, and the refusal
hint. Real-CLI and server-level runs are recorded in PLAT-371.

Linked Crew tests additionally cover mode-specific prompt/skills, stable private
runtime identities for all six CLI providers, projection for Claude/Codex/Cursor/
Pi/Muse, resume boundaries, preserving real project instructions, and Linux
Landlock read/write/create/rename/delete permissions through the link. Live
authenticated qualification of every CLI in both modes is required before deploy.

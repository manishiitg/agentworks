# Project instruction files, skills and the reader role

Status: merged to `main` 2026-09-30 (not deployed). Ticket:
[PLAT-371](../bugs/pulse_platform/security-sandbox/plat-371.md). Supersedes the
per-adapter "write CLAUDE.md/AGENTS.md, delete on cleanup" behavior that
[PLAT-296](../bugs/pulse_platform/security-sandbox/plat-296.md) and
[isolated workflow testing](../getting-started/isolated-workflow-testing.md)
describe for shared folders.

## Where each kind of chat runs

| Run | CLI working directory |
|---|---|
| Crew and Code chats, schedules, triggers, bots | the project's own folder (shared by every session in it) |
| Workflow Builder chat and workflow schedule | private per-session runtime folder outside the docs tree ([PLAT-296](../bugs/pulse_platform/security-sandbox/plat-296.md)); planned to move to the shared folder ([plan](workflow_shared_folder_plan.md)) |
| Workflow step agents | fresh temporary folder |

## Rules for a shared project folder

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
Codex and Muse, so the block is the carrier.

## Owner and reader

A Crew has two roles. The owner (Builder) has full tools. A reader (Run: a
non-owner chatting in the Crew, or a guest call into the owner's Crew) is read-only.

- **One prompt** for everyone in the folder (the Crew profile prompt plus a short
  paragraph explaining the `[AGENTWORKS SESSION]` block).
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
- The owner does not need a way to chat in Run mode.
- Code has no reader prompt; its shared participants are governed by the Code share
  rules.

## Session ids

Crew and Code take theirs from the conversation registry: `work:project:<id>` /
`code:project:<id>` for the main chat, `product-<uuid>` for extra chats (a
`:suffix` conversation key), and a reader's chat is stored under their own account.
Workflows: see the [plan](workflow_shared_folder_plan.md) (native CLI session keyed
by session id and mode).

## Tests

`pkg/projectfile` unit tests (user's file, overlap, edits during a session, stale
blocks, leases, owned files); `skillproject` (adoption, user skills);
`mcpagent` cleanup tests (user's `.claude/.cursor/.pi/.codex/.agents` survive every
provider start); server tests for the session block, its stripping, and the refusal
hint. Real-CLI and server-level runs are recorded in PLAT-371.

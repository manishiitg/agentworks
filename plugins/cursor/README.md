# AgentWorks for Cursor (PLAT-865)

This package formats turn-boundary messages for one explicitly attached Cursor
conversation. It requires the shared PLAT-861 local bridge and its `cursor_hook`
transport. It does not implement a daemon, sign in, hold credentials, poll a
mailbox, type into the app, or claim that writing stdout admits a message.
The shared CLI accepts native Cursor hooks and derives helpers from the installed
process and CURSOR_CONVERSATION_ID. Linux CLI identity includes the actual bundled
Node/index.js and kernel birth data. macOS desktop identity uses the native app
image and kernel birth data, with a separate UUID for every conversation; live
GUI delivery remains unverified. macOS CLI and desktop shell helpers are unsupported. The shared desktop app
process plus a shell UUID cannot prove the calling conversation; PLAT-861 must
provide the missing own-session fence before helpers can be enabled. Desktop MCP remains
disabled because a shared MCP child does not prove the calling conversation.

## Prerequisites and installation

Use Python 3 and an AgentWorks CLI that includes the shared local bridge. Sign in
with `agentworks login`, then start `agentworks bridge serve` in a terminal. The
CLI owns its private connection config; never put tokens in this plugin.
Existing connections may share account mailbox authority; this package does not
create isolated per-agent credentials.

The person can load this directory with Cursor CLI's `--plugin-dir` option, or
install it locally in the desktop app through its documented local plugin
directory. Approve workspace, plugin, hooks and MCP trust prompts personally.
The MCP manifest intentionally registers no server until PLAT-861 supports the
messaging-only Cursor child. This package must not enable the full-account
`agentworks mcp serve` connection. Broader tool access is a separate person opt-in.
The attached hook/helper route uses only the local socket. Loading a plugin does
not attach a chat.

```sh
cursor-agent --plugin-dir /absolute/path/to/plugins/cursor --workspace /absolute/project/path
```

CLI 2026.10.01-e373342 has call-site guards that check user/project hooks before
invoking `stop` and `beforeSubmitPrompt`; plugin-only hooks may be skipped. If the
hooks do not run, the person should add these project hook entries to
`.cursor/hooks.json`, preserving all other entries (do not install globally by
default). Replace the script path with the actual absolute plugin path:

```json
{
  "version": 1,
  "hooks": {
    "beforeSubmitPrompt": [{"command": "python3 /absolute/plugin/path/scripts/cursor_hook.py", "timeout": 20}],
    "stop": [{"command": "python3 /absolute/plugin/path/scripts/cursor_hook.py", "timeout": 20, "loop_limit": 5}],
    "sessionStart": [{"command": "python3 /absolute/plugin/path/scripts/cursor_hook.py", "timeout": 20}],
    "sessionEnd": [{"command": "python3 /absolute/plugin/path/scripts/cursor_hook.py", "timeout": 20}]
  }
}
```

Install exactly one of plugin hooks or project hooks. Duplicate registration is
unsupported: competing stop hooks can overwrite `followup_message`. Cursor merges
responses with the last follow-up winning; a returned offer is not acceptance.

## Person-controlled attachment

The person submits `/agentworks attach [NAME]`, `/agentworks detach`,
`/agentworks agents` or `/agentworks status` directly. The shared hook handler
consumes these controls before model input and verifies the native UUID and
installed process. Attach uses existing login and bridge configuration; it does
not grant sender rules. Status/agents query only this attachment. Detach first
snapshots its ID and sends that exact fence. Loading the plugin never attaches.
Queued messages and controls containing an agent-delivery header are refused.
Ordinary prompts pass through unchanged. Bridge failures block control submission
with a generic message, without printing credentials or raw errors.

Unattached conversations return no output and make no network request. Hook
queries use only the private Unix socket. `stop` drains only on `completed` with
integer `loop_count` 0–4. Aborts, errors and exhausted loops leave offers pending.
`sessionEnd` marks the exact attachment offline, fencing old operations while preserving its name. If the daemon is
unavailable during close, revoke the connection in AgentWorks; do not claim the
close was fenced until the server confirms it. A model with unrestricted local shell access can forge a beforeSubmitPrompt
payload beneath the same provider process (PLAT-861 issue861-90). Literal command,
queued/header and process checks do not authenticate human intent against that
case. Hooks and discovery are same-user routing checks, not OS-account isolation.

## Delivery and limitations

The bridge must own current sender/connection checks, catch-up, ordering,
reconnect, restart deduplication, rotation and a durable offer journal. The
adapter snapshots attachment identity before draining and passes that ID to the
bridge. It checks individual messages independently, so a malformed or stale
item cannot discard valid neighbors. Drains get a ten-second socket timeout
within a twenty-second hook budget; the attachment lookup has three seconds.
If a drain times out or a batch cannot be fully rendered, the hook returns an
explicit mailbox-recovery prompt. It does not automatically replay an uncertain
offer. The receiving agent uses `cursor_hook.py recover --session SESSION
--attachment-id ATTACHMENT` to read through the shared bridge and deduplicates
message IDs before acting. Further requested pages use `--after next_cursor`.
No adapter body journal, automatic retry loop or admission receipt is introduced.

A hook
offer remains `stored`; the receiving agent explicitly acknowledges `admitted`
after reading it and `processed` after handling it. The messaging skill describes
acknowledgements and replies through the bridge, retaining the original inbox.
Every shared Cursor helper action requires `--attachment-id` from the original offer, forward
it to the bridge before any operation, and reject a mismatching response. They
cannot follow a moved address or substitute the newest attachment for an old one.

Cursor's stop hook submits a new message only when a turn ends. Busy tools finish
first. No documented exact-session idle input API was established in this
research; `--resume` starts a new CLI run and is not a live-session wake contract.
Idle delivery therefore requires the shared bridge to notify the person. This
package implements neither idle notification nor tmux. tmux remains off by
default and any future fallback must be CLI-only, opt-in, exact-session, idle and
empty-composer only. There is no Cursor desktop typing path.

## Messaging skills

The manifest explicitly loads `skills/agent-messages` for the shared messaging
contract and `skills/agentworks-messaging` for Cursor-specific attachment-fenced
ack, reply and recovery helpers. The shared copy is generated from
`plugins/shared/skills/agent-messages/SKILL.md`; update the source through its
shared owner and run `python3 scripts/sync-agent-messaging-skills.py cursor`.
Never edit the generated copy. `--check cursor` detects drift. The shared CLI
fallback applies only when the installed CLI supports this provider and can
verify this exact session.

## Evidence and checks

Research on 2026-10-11: desktop 3.21.18 (installed Info.plist), CLI
2026.10.01-e373342 (`cursor-agent --version` and help). Installed hook code sets
CURSOR_PLUGIN_ROOT, skips stop at loop_limit, and queues followup_message as a
userMessageAction. These establish implementation contracts, not a successful
live delivery. Run `python3 plugins/cursor/scripts/check_adapter.py` for the
package and shared-CLI delegation check. The remote Go `TestCursor` suite covers real Node ancestry, UUID ambiguity, mixed batches and original-lease recovery. Live acceptance must additionally
prove busy-turn delivery, idle notification, draft preservation, project and
conversation isolation, permission revocation and restart behavior with the real
shared bridge. Keep the ticket open until that evidence exists.

Official references:

- [Cursor hooks](https://cursor.com/docs/hooks): conversation ID, stop follow-up,
  loop guard, prompt-submit blocking, lifecycle and response merging.
- [Cursor plugins](https://cursor.com/docs/plugins): local plugin loading.
- [Plugin reference](https://prod.cursor.com/docs/reference/plugins): manifest,
  component discovery, MCP and CURSOR_PLUGIN_ROOT.
- [CLI parameters](https://cursor.com/docs/cli/reference/parameters): local
  plugin directory and exact chat resume.

## Native CLI hook probe

With an existing Cursor login or API-key environment, run:

```sh
python3 plugins/cursor/scripts/check_native_hook.py
```

Use `--interactive` for the terminal UI path; print mode in CLI2026.10.01
can complete without invoking stop or prompt hooks. The interactive probe owns
only a new disposable CLI process. It stops after the second completed hook even
when no owned transcript is available, reporting that narrower result without a
full pass. It verifies assistant markers from that
workspace's native transcript, never from terminal echoes, and refuses to read
transcripts outside the test workspace.

For a shared process-binding check, prepare with `--agentworks-cli /scratch/agentworks`.
It runs the real hook and a deliberately mismatching read-only session pin in the
isolated cache. A refused pin proves context derivation, not an attachment or receipt.

The probe creates a temporary project, asks for a baseline response, and checks
that a completed stop hook causes a second model response containing its unique
marker. It uses ask mode without force/yolo flags. It does not grant workspace trust by
default. If trust is required, prepare a fixed workspace with `--prepare PATH`,
let the person review the script and `.cursor/hooks.json`, then run with
`--workspace PATH`. Use `--trust-workspace` only when the person has explicitly
approved that exact prepared workspace and its hooks; it cannot apply to an
automatically created project. Its report
contains marker checks, hook session IDs, process ancestry and a redacted CLI
failure hint; it prints no credentials or raw model output. It does not install a global plugin,
attach any chat, create a connection, record a delivery receipt or restart a
service. Passing proves the real Cursor hook/model path, not the AgentWorks
lease, mailbox, permission, reply or desktop delivery path.

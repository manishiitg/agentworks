# AgentWorks for Cursor (PLAT-865)

This package formats turn-boundary messages for one explicitly attached Cursor
conversation. It requires the shared PLAT-861 local bridge and its `cursor_hook`
transport. It does not implement a daemon, sign in, hold credentials, poll a
mailbox, type into the app, or claim that writing stdout admits a message.
The shared bridge on main supports Cursor hook receipts and attachment fencing;
Cursor-specific narrow MCP/process binding is still pending under PLAT-861.
Current main requires an exact provider host key for attached operations; this
candidate does not supply it, and shared hook/MCP commands admit only Claude and
Codex. The package is not ready for real attachment or delivery against main.

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
hooks do not run, the person should add these three project hook entries to
`.cursor/hooks.json`, preserving all other entries (do not install globally by
default). Replace the script path with the actual absolute plugin path:

```json
{
  "version": 1,
  "hooks": {
    "beforeSubmitPrompt": [{"command": "python3 /absolute/plugin/path/scripts/cursor_hook.py", "timeout": 20}],
    "stop": [{"command": "python3 /absolute/plugin/path/scripts/cursor_hook.py", "timeout": 20, "loop_limit": 5}],
    "sessionEnd": [{"command": "python3 /absolute/plugin/path/scripts/cursor_hook.py", "timeout": 20}]
  }
}
```

Install exactly one of plugin hooks or project hooks. Duplicate registration is
unsupported: competing stop hooks can overwrite `followup_message`. Cursor merges
responses with the last follow-up winning; a returned offer is not acceptance.

## Person-controlled attachment

Shared person commands are `/agentworks attach [NAME]`, `/agentworks detach`
and `/agentworks agents`. Cursor support for that shared command handler and
exact process binding is pending. Do not use another provider to bypass it.

This preview refuses `/agentworks attach` with a message explaining the missing
shared Cursor binding. It cannot create an attachment, connection or sender rule.
The prompt-submit hook consumes controls before model input. `/agentworks status`
and `/agentworks agents` query only a verified attachment; `/agentworks detach`
first snapshots that attachment and includes its ID in the detach request.
Queued messages and controls containing an agent-delivery header are refused.
Ordinary prompts pass through unchanged. Bridge failures block control submission
with a generic message, without printing credentials or raw errors.

Unattached conversations return no output and make no network request. Hook
queries use only the private Unix socket. `stop` drains only on `completed` with
integer `loop_count` 0–4. Aborts, errors and exhausted loops leave offers pending.
`sessionEnd` asks the bridge to detach the exact conversation. If the daemon is
unavailable during close, revoke the connection in AgentWorks; do not claim the
close was fenced until the server confirms it. Hook input is a same-user local
boundary, not protection against another program running under your OS account.

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
Both helper commands require `--attachment-id` from the original offer, forward
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
adapter's socket/formatting regression check. Live acceptance must additionally
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

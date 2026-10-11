# AgentWorks for Cursor (PLAT-865)

This package formats turn-boundary messages for one explicitly attached Cursor
conversation. It requires the shared PLAT-861 local bridge and its `cursor_hook`
transport. It does not implement a daemon, sign in, hold credentials, poll a
mailbox, type into the app, or claim that writing stdout admits a message.

## Prerequisites and installation

Use Python 3 and an AgentWorks CLI that includes the shared local bridge. Sign in
with `agentworks login`, then start `agentworks bridge serve` in a terminal. The
CLI owns its private connection config; never put tokens in this plugin.
Existing connections may share account mailbox authority; this package does not
create isolated per-agent credentials.

The person can load this directory with Cursor CLI's `--plugin-dir` option, or
install it locally in the desktop app through its documented local plugin
directory. Approve workspace, plugin, hooks and MCP trust prompts personally.
The default MCP entry uses the CLI's existing config. Configure it separately if
the person uses a dedicated connection. Loading a plugin does not attach a chat.

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
    "beforeSubmitPrompt": [{"command": "python3 /absolute/plugin/path/scripts/cursor_hook.py", "timeout": 5}],
    "stop": [{"command": "python3 /absolute/plugin/path/scripts/cursor_hook.py", "timeout": 5, "loop_limit": 5}],
    "sessionEnd": [{"command": "python3 /absolute/plugin/path/scripts/cursor_hook.py", "timeout": 5}]
  }
}
```

Install either plugin hooks or project hooks; avoid duplicate registrations. The
shared offer journal prevents automatic duplicate offers if both accidentally
run, but competing stop hooks can overwrite `followup_message`. Cursor merges
responses with the last follow-up winning; a returned offer is not acceptance.

## Person-controlled attachment

In the exact conversation, submit this literal control prompt, replacing the
values with your name, purpose, connection config and approved sender name:

```text
/agentworks attach --name reviewer --purpose "Review incoming work" --config /absolute/path/connection.json --allow-sender worker
/agentworks status
/agentworks detach
```

Repeat `--allow-sender` for each approved sender. The prompt-submit hook obtains
the conversation ID from Cursor, consumes these controls before model input, and
shows a status message to the person. A config path is required; the adapter never
reads the credential file. Ordinary prompts pass through unchanged. Malformed
controls and bridge failures block control submission with a generic message,
without printing credentials, private paths, sender content or raw errors.

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
adapter checks attachment identity before formatting the returned JSON. A hook
offer remains `stored`; the receiving agent explicitly acknowledges `admitted`
after reading it and `processed` after handling it. The messaging skill describes
acknowledgements and replies through the bridge, retaining the original inbox.

Cursor's stop hook submits a new message only when a turn ends. Busy tools finish
first. No documented exact-session idle input API was established in this
research; `--resume` starts a new CLI run and is not a live-session wake contract.
Idle delivery therefore requires the shared bridge to notify the person. This
package implements neither idle notification nor tmux. tmux remains off by
default and any future fallback must be CLI-only, opt-in, exact-session, idle and
empty-composer only. There is no Cursor desktop typing path.

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
- [Plugin reference](https://prod.cursor.com/docs/plugins/reference): manifest,
  component discovery, MCP and CURSOR_PLUGIN_ROOT.
- [CLI parameters](https://cursor.com/docs/cli/reference/parameters): local
  plugin directory and exact chat resume.

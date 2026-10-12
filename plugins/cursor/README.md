# AgentWorks for Cursor

This package connects Cursor CLI chats to Bullpen through the AgentWorks local
bridge. macOS and Linux CLI are the supported target for package 0.2.0. Desktop
is a separate stage: a shared desktop MCP process still needs a per-call
conversation fence and an exact-chat idle input route. CLI evidence does not
prove desktop messaging.

Use a current AgentWorks CLI build that supports `plugin mcp cursor`, Python 3,
and Cursor CLI. The investigated client is 2026.10.01-e373342; desktop 3.21.18
was inspected separately. Loading a plugin does not sign in, approve workspace
or MCP trust, attach a chat, or enable terminal delivery.

## Install in the project you use

The small repository is generated from the canonical packages in
[AgentWorks](https://github.com/manishiitg/agentworks). Keep this directory for
updates; do not edit the generated mirror as a separate source.

```sh
git clone --depth 1 https://github.com/manishiitg/agentworks-plugins.git "$HOME/.local/share/agentworks-plugins"
python3 "$HOME/.local/share/agentworks-plugins/plugins/cursor/scripts/setup_project.py" --project "/absolute/project/path"
cursor-agent --plugin-dir "$HOME/.local/share/agentworks-plugins/plugins/cursor" --workspace "/absolute/project/path"
```

Review the exact `.cursor/hooks.json` and messaging MCP in Cursor yourself.
The setup script merges the four native hook events, preserves unrelated
entries, and refuses a duplicate or old AgentWorks hook path. It changes only
that project's hook file. It does not approve trust, create a login, or attach.
The package's own hook set is empty, so project hooks are registered once.
This avoids the investigated CLI's plugin-only prompt/stop call-site guards.
Do not also register the same native template in user-level hooks.

The native marketplace is also available through:

```sh
cursor-agent plugin marketplace add https://github.com/manishiitg/agentworks-plugins.git
```

A marketplace index alone does not load or approve a plugin. `--plugin-dir` is
the explicit CLI loading path above. Cursor desktop can discover the package,
but its messaging mode remains unsupported; do not expect CLI results there.

## Connect and use the chat

In a separate terminal, sign in to the intended AgentWorks server:

```sh
agentworks --server https://example.com login --name work
```

In the reviewed Cursor CLI chat, type:

```text
/agentworks attach cursor-review
/agentworks agents
```

Use the returned name/address. The native hook consumes the control prompt;
ordinary prompts pass through unchanged. After the verified session is observed,
you may also ask the agent to attach its own detected CLI chat. Never substitute
a UUID or process override when verification fails.

`/agentworks detach`, `share GROUP`, `unshare`, `groups`, and `group NAME` use
the same bridge controls and account/group rules as the other clients. A group
share does not authorize another person's private inbox or usage data.

The `agentworks-messages` MCP exposes only session-bound messaging tools. It is
not the full-account HTTP MCP connection for workflows or Crews. The agent
acknowledges `admitted` after reading an offer and `processed` after handling it,
and replies explicitly in its original inbox with a stable submission ID.
Terminal display, hook stdout, a send result or a notification is not admission.

## Delivery, restart and updates

Completed-turn hooks offer pending messages after busy work finishes. This
milestone does not wake an idle Cursor CLI. The installed CLI's persist command
uses vendor-owned tmux, but a reviewed guarded input transport is not built yet.
Do not configure an invented opt-in variable or treat a new `--resume` process
as a live message. Explicit mailbox recovery is the last fallback. No inbox
polling loop, automatic uncertain replay or desktop typing path is supplied.

Update the retained plugin directory and project registration:

```sh
git -C "$HOME/.local/share/agentworks-plugins" pull --ff-only
python3 "$HOME/.local/share/agentworks-plugins/plugins/cursor/scripts/setup_project.py" --project "/absolute/project/path"
```

Review and reload the package in Cursor. `agentworks update` updates the CLI
from the connected server; it must publish a Cursor-capable build. CLI
stop/start/update retain existing approvals and restore still-live sessions.
A closed/revoked lease is not revived. A process/session identity failure must
remain a plain refusal; do not change sandbox or hook trust to bypass it.

## Troubleshooting and evidence

If attach reaches the model, check the project path and hook registration,
then restart Cursor CLI. If messaging MCP cannot start, check the AgentWorks
build and approved MCP loading. Do not enable a full-account MCP to hide a
messaging identity failure.

The adapter checks are `python3 plugins/cursor/scripts/check_adapter.py` and
the shared native process/session/lease regressions. Go/frontend checks run on
GitHub. These do not replace the owner's real native acceptance.
See the [owner live test plan](../../docs/cursor-mac-live-test.md) in the main
repository for two-chat identity, receipts, drafts, groups, reconnect and revoke.
In the small repository, use the canonical
[live test plan](https://github.com/manishiitg/agentworks/blob/main/docs/cursor-mac-live-test.md).

Official contracts: [hooks](https://cursor.com/docs/hooks),
[plugins](https://cursor.com/docs/plugins), and
[CLI parameters](https://cursor.com/docs/cli/reference/parameters).

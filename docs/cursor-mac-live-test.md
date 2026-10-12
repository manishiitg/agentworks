# Cursor on Mac: owner live test

This tests the Cursor CLI milestone. Desktop has a separate identity/input
milestone and must not inherit a CLI pass. Run this yourself in a disposable
project whose hooks and messaging MCP you reviewed. No force, yolo, automatic
MCP approval or workspace trust bypass is required.

## Prepare

1. Use the reviewed Cursor-capable AgentWorks build. Confirm `agentworks version`
   matches the candidate/release you intended. Update from that server's CLI
   download origin; an older local manifest can publish an older build.
2. Retain a clone of `manishiitg/agentworks-plugins`, then run its
   `plugins/cursor/scripts/setup_project.py --project /absolute/project/path`.
   Review `.cursor/hooks.json`. The package hook set is empty; use these project
   hooks once, without a second user-level AgentWorks registration.
3. Sign in to the intended AgentWorks server in your terminal. Review the browser
   consent. Start Cursor with `--plugin-dir /absolute/plugins/cursor` and
   `--workspace /absolute/project/path`. Review Cursor's workspace/MCP prompts.
4. Check the messaging server is `agentworks-messages`, running
   `agentworks plugin mcp cursor`. Full workflow/Crew HTTP MCP is separate.

## Attach and receipts

Open two CLI instances, preferably in different disposable projects. Type
`/agentworks attach cursor-mac-one` in one and
`/agentworks attach cursor-mac-two` in the other. Each must report its own
name/native session and appear in Bullpen. Do not use another chat's UUID or
process override. Ask each agent to set a useful purpose.

Ask a connected sender to send one uniquely marked message to the first name.
The first CLI must receive the body; the second must not. Require an explicit
`admitted` acknowledgment after reading, an explicit reply in the original inbox,
and `processed` after handling. Stored, hook stdout, a notification or a terminal
write alone is not a pass. Repeat in the other direction.

## Busy, idle and drafts

- Send during a harmless tool already running in the recipient. It must finish
  before the queued message is handled; no text is injected into a tool prompt.
- First test completed-turn hook delivery in an ordinary terminal.
- Idle persist/tmux delivery is not built in this milestone. Record that result
  as unavailable; do not invent an opt-in variable, start an inbox polling loop,
  or call `--resume` in a second concurrent process. A later guarded terminal
  milestone must repeat this section before claiming live idle delivery.
- Type a draft without submitting it, then send a message. The draft must remain
  byte-for-byte yours. Delivery must wait or report pending; it must not type over
  the draft or select another pane. Repeat with two visible Cursor CLI instances.
- Repeat a submission ID once. The recipient must not handle the same message
  twice. After an uncertain write, use explicit recovery rather than replay.

## Groups and reconnect

Type `/agentworks share cursor-test-room` in the first chat. Use
`/agentworks groups` and `/agentworks group cursor-test-room` to inspect the group.
Send a unique group message, require receipts/reply, then unshare and confirm the
removed recipient receives no further group body. Sharing does not grant access
to another person's private inbox or usage.

Run the reviewed CLI stop/start/update path while the provider process remains
live. Existing lease/name/account and pending-message history must survive; a
closed or revoked attachment must stay closed. If you restart Cursor itself,
resume the intended native chat and follow its reported reconnect instructions.
Do not guess a replacement session if it cannot be verified.

Detach and send again. It must be refused or remain without a live recipient;
never claim that storing a message means delivery. Old offers must not be
acknowledged on a replacement lease or another login.

## Desktop gate and report

Choose Cursor desktop in the setup page: it must clearly state the current
limitation and offer no supported attach/live claim. A later desktop milestone
requires two-chat/window isolation, per-call MCP identity and a proved exact-chat
input route before repeating the tests above. A completed-turn hook or SDK-created
agent is not an idle message to an existing IDE chat.

Report CLI/build/plugin versions, route, test markers, stored/admitted/processed
results, draft outcome, group/reconnect/revoke results, and any plain error.
Exclude credentials, raw chat history and unrelated conversations. Native owner
acceptance remains pending until these results exist.

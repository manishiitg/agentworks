# Native coding runtime events (PLAT-872)

The server observes the native session already owned by a chat. Opening a
terminal or browser is not required. These are observations of real CLI work;
they do not start a model, retry a run, declare business success or add access.

The shared contract lives in the provider library's `llmtypes.NativeRecord`.
The six adapters expose only fields their stored records actually contain:

| Provider | Native source | Observations | Explicit limits |
| --- | --- | --- | --- |
| Claude Code | Selected session JSONL | Replies, exact helper identities, turn roots, tool state, question observation, compaction, model usage snapshots | Native dollars are estimates; stopped is not proof of a server restart |
| Codex | Selected rollout JSONL | Committed items, native turn end/error, tools, files, question observation, compaction, cumulative usage | Account plans, credits and limits are excluded |
| Cursor | Selected session SQLite root | Reply blobs, prompt boundaries, tool starts | Native blob records do not establish timestamps, usage or turn completion |
| Pi | Selected runtime JSONL | Replies, tools, compaction, per-message usage | No fabricated native helper lifecycle or authoritative end signal |
| Muse | Selected runtime journal | Committed replies, task records, tools, questions, compaction, usage/status observations | Native questions retain the existing answer adapter; status is not permission to retry |
| Agy | Selected conversation SQLite steps | Committed reply steps, tool state, error/notice observations | No fabricated native usage, model, timestamp or turn completion |

An initial baseline supplies identity context without importing old chat.
If no checkpoint exists, observation starts from that baseline; it does not
repair historical replies. Saved owned checkpoints are restored at server
startup even when no browser is open and no new event arrives.
Subsequent complete records advance a private durable cursor only after
acceptance. Source replacement, truncation, malformed rows and unknown schema
kinds are reported as coverage gaps. Live SQLite readers use normal read-only
locking and see WAL changes; they never use `immutable=1` on an active database.

## Replies and helpers

Claude's task-notification turn marker and `parentUuid` chain identify a native
continuation. A transcript-only stopped notification changes helper state and
starts no conversational turn. A stopped notification can name several tasks.

The foreground provider and observer share one publication claim per exact
native session/root UUID. A foreground turn that is still waiting keeps its
normal aggregation. After foreground ownership ends, the observer can save a
late continuation as an ordinary durable assistant message. Failed or restarted
foreground turns release unfinished claims. A saved foreground completion keeps
its claim. No helper notification is represented as a person typing a prompt.

Native helpers and background commands appear in the existing work list as
**native_helper** and **native_command**. A committed `backgroundTaskId` proves
a background command even when its initial input did not request background
execution. To-do items and merely proposed tasks do not count as running work.
These are separate from platform delegated work. They do not enter the execution
registry, deploy drain, or platform Stop route. The UI shows the server's state.
The normal journal and saved conversation checkpoints restore replies on reload.
The existing background-agent chat/MCP tool exposes the same native work under
`native_work`, separately from delegated platform executions.

This observer currently publishes late replies into the owning in-app chat.
Delivery of a late observer-owned reply to a *previously returned* Ask, Slack,
WhatsApp or MCP request is not established by that chat save. Audit such channel
delivery separately; do not label it verified from a native finish event alone.
Existing live foreground channel delivery is unchanged.

## Manual server audit

Run `python3 scripts/native-events-audit.py --targets /private/targets.json
--output /private/native-events-report.json` when investigating or accepting a
candidate. Keep real server names, SSH arguments, instance IDs and paths in the
private deployments repository. Example target structure:

For SSM running as root, set a private target's `command_prefix` to run the
probe as the application's service user (for example `sudo -u service env
HOME=/srv/service/home`). The probe uses deployed tools plus that user's
local binary directory; version discovery does not sign in to an account.

```json
[
  {"name":"example","transport":"ssh","ssh":["ssh","operator@example.com"],"state_root":"/srv/app/state","log_path":"/srv/app/logs/agent.log","release_path":"/srv/app/current"},
  {"name":"example-ssm","transport":"ssm","region":"example-region","instance_id":"example-instance","state_root":"/srv/app/state","log_path":"/srv/app/logs/agent.log"}
]
```

The report contains provider versions found on the probe's PATH, owned runtime
counts, observed event kinds, parse/identity gaps and continuation roots without
saved reply evidence. It reads a bounded log tail. It does not copy transcripts,
tool arguments, replies, reasoning, account limits or secrets. Unreachable is
**UNVERIFIED**. Installed, signed in, replay-tested and live-tested are distinct.
No recent native records is not a pass. This is a manual command, not a service.

## Candidate acceptance

Use one candidate across the agreed test accounts. Run focused provider format,
ownership/restart and durable-journal checks on the build host. Record replay
and live evidence separately in PLAT-872. The short human check is:

1. Ordinary reply and a tool result arrive without opening Terminal.
2. A helper completing after its parent reply produces one follow-up.
3. Two helpers, including a batched stopped notification, have correct states.
4. Reload/reconnect retains each answer once; a typed follow-up is still sent.
5. A real pending question remains answerable through the existing choice card.

Do not rerun the whole manual matrix for a small fix: recheck its affected
journey and the automated regression. Missing provider account access is a
named coverage gap, not a simulated pass.

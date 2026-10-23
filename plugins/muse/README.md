# AgentWorks for Muse

**Update the AgentWorks CLI before approving this plugin.** Muse support includes
`agentworks plugin hook muse` and `agentworks plugin mcp muse`. Older CLI releases
reject both commands; approving the plugin with those releases can prevent Muse
from starting. The observed startup error (exit 1) is:

```text
Required MCP server `agentworks` failed during startup: the transport closed unexpectedly.
```

See the diagnostic details below. This release uses a native idle Monitor and
retains turn-boundary hooks as a fallback. Update the CLI and plugin together;
the CLI must also expose `the agentworks tool (action=monitor)`, `the agentworks tool (action=receive)` and
`agentworks bridge message watch`.

This native Muse plugin packages an AgentWorks MCP connection, messaging skill,
person command and UserPromptSubmit, Stop and SessionEnd hooks. It uses the shared
`agentworks plugin hook muse` boundary. It does not store credentials, read a
mailbox in a polling loop or implement another bridge.

## Requirements and installation

Native idle wake is tested with Muse 1.4.6. Plugin/hook setup is also verified
with Muse 1.4.4; use a compatible release with native Monitor support. You need
Python 3 and an AgentWorks CLI supporting
both `agentworks plugin hook muse` and `agentworks plugin mcp muse` on PATH. The
common bridge is owned by PLAT-861; this provider package alone does not supply
those commands. A CLI that accepts only Claude/Codex will refuse Muse attachment
and cannot start this package's MCP child. Sign in to the intended AgentWorks
server with the CLI's normal login before attaching. The MCP child uses the
exact provider process/session and exposes only messaging tools.

The person adds the GitHub marketplace, installs the Muse package and reviews
its capabilities:

```sh
muse plugins marketplace add agentworks https://github.com/manishiitg/agentworks-plugins.git
muse plugins install agentworks-muse@agentworks
muse plugins inspect agentworks-muse --json
```

For an installation-only preview, stop after inspection. With a compatible
AgentWorks CLI installed, approve with `muse plugins approve agentworks-muse --json`
after reviewing and restart Muse. Installation alone does not approve hooks or MCP execution.

The same GitHub repository serves each provider's catalog. Muse reads
`.agents/plugins/marketplace.json`, which contains separate `agentworks-muse` and
`agentworks-codex` entries. Install `agentworks-muse` in Muse; the packages use
different provider commands. Claude Code reads `.claude-plugin/marketplace.json`
and uses `agentworks@agentworks`. Existing Codex metadata remains unchanged.

To update an older plugin to **0.1.8**, use the refresh/remove/reinstall commands
below, then inspect its version, approve it and restart Muse before attaching.

After any plugin update, Muse shows the new hooks and tool server as "review needed". Review them with
`muse plugins inspect agentworks-muse`, approve with `muse plugins approve agentworks-muse`, restart Muse
and **start a new chat**: a resumed older chat keeps the old tool names in its history and keeps failing.
`agentworks update` does the marketplace refresh and reinstall for you when a newer version is on offer,
and prints these steps; it never approves anything.

Muse 1.4.4 keeps an installed marketplace plugin bound to its original snapshot.
After refreshing the marketplace, direct reinstall refuses the different source
and `plugins update` still reads the old snapshot. To upgrade on this release:

```sh
muse plugins marketplace update agentworks
muse plugins remove agentworks-muse
muse plugins install agentworks-muse@agentworks
muse plugins inspect agentworks-muse --json
```

Review the new capabilities before approving them and restarting Muse. The
remove command above does not request `--delete-data`.

For local development, install from the repository root:

```sh
muse plugins validate plugins/muse --json
muse plugins install plugins/muse --scope user --json
muse plugins inspect agentworks-muse --json
muse plugins approve agentworks-muse --json
```

Approve only after reviewing the hook commands and MCP connection. Restart Muse
to load newly approved hooks. Nothing in this package approves its own trust or
attaches a conversation automatically.

In the intended conversation, type one of these person commands:

```text
/agentworks attach
/agentworks attach implementation
/agentworks agents
/agentworks detach
```

Attach connects or reconnects the exact conversation in one step. The person
submitting it supplies approval for all senders in their AgentWorks account.
A name is optional; the attached agent can describe its purpose separately.
Sender allow flags and separate confirmation/resume commands are not supported.

The shared UserPromptSubmit handler consumes the literal person command before
model execution and binds Muse's hook-provided UUID to its process/start time.
The command template refuses to perform attachment if interception fails. Muse's
native namespaced command is `/agentworks-muse:agentworks`. Session close retains
the identity offline; the person reconnects with `/agentworks attach`.

### Muse slash menu entries

Muse shows several AgentWorks entries in its slash menu:

| Entry | Who uses it | What it does |
|---|---|---|
| `/agentworks-muse:agentworks` | The person | The only entry the person needs: `attach [NAME] [--on LOGIN]`, `detach`, `agents`, `share` (publish to the network), `unshare` (see above). |
| `/agentworks-muse:messaging` | The Muse agent | Skill for reading and replying to AgentWorks messages; the person does not run it. |
| `/agentworks-muse:monitor` | The Muse agent | Skill for starting the idle wake-up Monitor; the person does not run it. |
| `/agentworks` (user skill) | — | The general AgentWorks tools guide loaded from Claude (`~/.claude/skills/agentworks`); it is not the attach command, and typing `/agentworks attach` there does nothing. |

To connect Muse, type `/agentworks-muse:agentworks attach`.

`agentworks attach [NAME]` in a person terminal selects a live chat; `muse` as
NAME names that chat, rather than selecting a provider. A missing Muse plugin
uses the marketplace commands above after explicit installation consent. No
installer approves hooks or workspace trust. A Muse chat becomes discoverable
after an approved native hook observes its UUID and exact process. Discovery
records contain only UUID, PID/start-time fence and cwd, are mode0600, and never
register an identity, read transcripts, start the bridge or store credentials.
A native prompt establishes the active UUID after a chat switch; a late Stop
from the old chat cannot replace it. The MCP child resolves that UUID on every
call, so an old lease is never inferred from the process alone. Process restart,
death or one UUID observed on multiple live processes prevents selection. If the chat is absent, install/review the plugin, restart Muse and
submit `/agentworks-muse:agentworks attach` directly in that chat. These records are inside the
documented same-OS-user trust boundary: another process running as the same
user can plant metadata; they are not isolation against that user.

## Recognizing an unavailable MCP connection

On older CLI releases, `agentworks plugin mcp muse` exits with
`Error: local messaging child supports Claude and Codex`. A real Muse 1.4.4
headless run with that command configured as a required MCP server exits with
status 1 and reports:

```text
Required MCP server `agentworks` failed during startup: the transport closed unexpectedly.
```

That diagnostic was observed with a temporary per-run MCP configuration, without
approving an installed plugin. The exact approved-plugin TUI presentation has
not been verified. [Muse documents](https://dev.meta.ai/docs/muse-code/extending)
that required MCP startup failure aborts the run; optional servers are skipped
with a warning. In an interactive session that starts, `/mcp` shows the live
server/tool inventory. Installed capabilities alone do not prove a connection.
If an approved preview prevents startup, `muse plugins disable agentworks-muse`
keeps the package installed while excluding it from future runs.

## Delivery

The bridge holds the authenticated server stream and durable offer journal. Hooks
use its local socket through the CLI. Unattached/offline hooks return no output;
they do not launch the bridge or make server requests. Explicit attach is the
person-controlled exception. The Muse shim rejects malformed/sessionless inputs
and skips Stop when `stop_hook_active` is set. SessionEnd marks the attachment
offline through the bridge but returns no output because Muse treats this event
as observational.

Lifecycle hooks allow 1.25 seconds for the CLI and have a 1.5-second native
deadline. Ordinary timeout/failure returns quietly; explicit AgentWorks commands
still report when their connection has not finished. Process creation can itself
stall under load, so the native deadline also bounds the wrapper. Muse may add
process-termination overhead to its recorded duration. A timeout does not
acknowledge or delete a message; it remains available for later delivery.
The existing local bridge log records start, elapsed milliseconds and the
classified outcome, without prompt text or message content. A start without a
completion plus a native timeout identifies an interrupted wrapper, not a proven
bridge lock or network failure.

Stop returns `decision: block` with the offered sender content in `reason`.
UserPromptSubmit adds context through the shared handler. Neither writes terminal
input, so an unsent composer draft is not touched. An offer is still stored until
the receiving agent acknowledges admission; processing is a separate explicit
acknowledgement. Replies retain the original inbox. Account authorization, revocation,
restart replay and attachment replacement are enforced by the common bridge and
server, rather than by this shim.

Muse's documented peer messaging is limited to eligible retained interactive
sessions for the same user on the same machine. The installed 1.4.4-R5419.1
`muse session-message list --json` probe returned
`external_agent_ingress_closed` on 2026-10-11. Turning on Session messaging is not
proof of external ingress; availability also depends on the rollout gate. A read-only probe of the newer installed 1.4.6-R6000.1 on 2026-10-12 returned
`{"status":"unavailable"}` and “external agent ingress is unavailable”. The
Monitor below supplies idle wake without external session-message ingress or
terminal typing.

Muse's native Monitor uses one persistent, credential-free watch on the local
bridge. After the hook completes the person's attach, it supplies trusted
setup context and its `agentworks-muse-monitor` skill tells Muse to call
`the agentworks tool (action=monitor)` and start the native **monitor** tool with the returned
arguments (`persistent: true`, `wake_delay_ms: 0`, `show_lines: true`). Only the successful Muse attach continues into a model turn to arm its consumer;
failed/queued commands remain blocked and other providers retain their handling.
A normal prompt also supplies pending setup after restart. Native tool approval
remains the person's choice.

The helper returns an absolute installed CLI command, native UUID, approved
attachment pins and a private metadata subscription. The approved MCP broker
rechecks the native UUID and PID/start-time binding before it opens the existing
private Unix bridge stream. The watch command reads a 0600 FIFO under a fixed
0700 provider-owned area, with a read-only exclusive reader lock. A kernel lock
release ends the broker on reader death even while idle. This avoids the native
command sandbox's Unix-socket restriction without changing network permissions.
The child neither logs in nor connects to a socket; it rejects mismatching pins
and stale subscriptions with no live broker lock. It refuses a bash
listener: the native Monitor's `MUSE_TOOL_USE_ID` and exact-session stdout spool
must exist. This spool guard prevents accidental wrong-tool startup; same-OS-user
runtime/filesystem changes are still outside the isolation boundary.

The watch holds one private Unix stream driven by the bridge's existing remote
SSE connection and change notifications, with no timed inbox reads. It emits one
bounded metadata hint per authorized new message, with no body or receipt, and
persists its hint marker before writing. A duplicate reader is refused. Detach,
changed lease/session/host, host death, revocation or bridge shutdown closes it.
The roster reports `muse_monitor` only while that consumer is connected. A
reported route or Monitor line does not prove admission.

On a hint Muse calls `the agentworks tool (action=receive)` to obtain a bounded authorized body
batch through its exact-session MCP, handles the peer data, optionally replies
in the original inbox and acknowledges `delivered` once received. This does not mean the work is finished.
The watch never marks a body offered or clears it. Individual ambiguous hints do not replay
on restart. A new consumer emits one aggregate pending notice for unread bodies; lifecycle hooks and bounded mailbox recovery remain available.
If Monitor is unavailable or refused, report that limit and keep hook delivery;
do not create a model polling loop.

On 2026-10-11, the official [stable release channel](https://api.meta.ai/muse-code/channels/muse-stable)
advertised 1.4.4-R5419.1 and the [canary channel](https://api.meta.ai/muse-code/channels/muse-canary)
advertised 1.4.1-R4503.1. Neither advertised a newer build to test for outside
ingress. This establishes the checked channels, not the behavior of an
unavailable future release.

Sources: [Muse extensions](https://dev.meta.ai/docs/muse-code/extending),
[session messaging](https://dev.meta.ai/docs/muse-code/session-messaging), installed
plugin help and the native plugin contract bundled in Muse 1.4.4-R5419.1.

## Verification

```sh
python3 plugins/muse/tests/check_hooks.py
muse skills validate plugins/muse/skills/messaging --json
muse plugins validate plugins/muse --json
python3 plugins/muse/tests/prove_muse_hooks.py /absolute/path/to/muse-binary
python3 plugins/muse/tests/prove_marketplace.py /absolute/path/to/muse-binary
python3 plugins/muse/tests/prove_idle_monitor.py /absolute/path/to/muse-binary
```

The native proof installs the package only into temporary XDG storage, runs
Muse's explicit `hook test` fixture runner for Stop, prompt context and SessionEnd
without approving runtime capabilities, then uses per-run user hooks and the echo provider to
verify a real Stop continuation, the loop guard, and a single session UUID across
prompt/Stop/close. It does not install or approve this plugin in the person's
settings, and its shared CLI response is a fixture. The hook fixture alone does not establish authenticated AgentWorks delivery
or an idle native wake. The additional idle Monitor proof uses the actual Muse
TUI and installed cache in a temporary profile, with a local fake model provider.
It asserts model-visible prompt/Stop context, private Unix access from the approved
MCP sidecar, private FIFO and reader lock access
under the unchanged native sandbox, and a fresh model request after an idle hint.
Its fake credential and plugin approval exist only in that disposable profile.
The shared real-server test separately exercises the stream, receipt and reply
path; an actual person-approved Muse/account exchange remains a distinct check.

GitHub CI also runs Codex 0.162.1 marketplace add/list and installs the unchanged
Codex package from the same catalog in its disposable runner profile. It checks
the installed version and byte-identical cached files; no model session starts.
The Muse entry has no Codex manifest and must not be installed in Codex.

The marketplace proof uses an isolated Git fixture containing the real Claude,
Codex and Muse catalogs/packages, then runs Muse's marketplace discovery,
installation and inspection. It asserts the installed Muse manifest, three
cached lifecycle scripts and messaging-only MCP command, then changes the Git
fixture and verifies marketplace refresh plus remove/reinstall loads the new
package digest. Pass a GitHub source
as a second argument to exercise the published Git snapshot instead. Neither
path approves runtime capabilities or modifies the person's Muse profile.

Muse 1.4.4 resolves each native manifest hook script to an absolute `source_path`
under the installed plugin cache. A logging `python3` shim records the executed
argv and working directory for all three native hook fixtures. The proof asserts
that Muse runs the absolute cached script while retaining the hostile project
as its working directory. That project contains competing
`hooks/stop.py`, `hooks/prompt_submit.py`, `hooks/session_end.py` and
`hooks/runtime.py`. The plugin responses arrive and the project scripts never
run. This verifies execution paths in the native fixture runner; approved
interactive execution remains a separate check. Keep the native manifest paths
relative: this release rejects literal `${MUSE_PLUGIN_ROOT}` and `${PLUGIN_ROOT}`
paths as missing capability files instead of expanding them; the proof also
checks both rejected manifests in isolated copies.

After person approval, use `muse plugins hook test agentworks-muse:stop --fixture
<fixture.json> --json` with a fixture of the form `{"event":"Stop","stdin":
{"hook_event_name":"Stop","session_id":"<exact UUID>","stop_hook_active":false}}`.
Then verify actual
Stop continuation and prompt context in Muse, separate sessions, busy tools,
drafts, account authorization/revocation and restart behavior against the shared bridge.
Do not claim admission from hook test success or mark the integration complete
from manifest validation alone.

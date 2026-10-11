# AgentWorks for Muse

**Packaging preview:** do not install or approve this plugin for messaging until
the shared AgentWorks CLI supports Muse. Current shared releases reject
`agentworks plugin hook muse` and `agentworks plugin mcp muse`; installation and
inspection can be tested now, but attachment and live delivery cannot work yet.

This native Muse plugin packages an AgentWorks MCP connection, messaging skill,
person command and UserPromptSubmit, Stop and SessionEnd hooks. It uses the shared
`agentworks plugin hook muse` boundary. It does not store credentials, read a
mailbox in a polling loop or implement another bridge.

## Requirements and installation

Use Muse 1.4.4 or a compatible release, Python 3 and an AgentWorks CLI supporting
both `agentworks plugin hook muse` and `agentworks plugin mcp muse` on PATH. The
common bridge is owned by PLAT-861; this provider package alone does not supply
those commands. A CLI that accepts only Claude/Codex will refuse Muse attachment
and cannot start this package's MCP child. Sign in to the intended AgentWorks
server with the CLI's normal login before attaching. The MCP child uses the
exact provider process/session and exposes only messaging tools.

The person adds the GitHub marketplace, installs the Muse package and reviews
its capabilities:

```sh
muse plugins marketplace add agentworks https://github.com/manishiitg/agentworks.git
muse plugins install agentworks-muse@agentworks
muse plugins inspect agentworks-muse --json
```

For an installation-only preview, stop after inspection. Once shared Muse
support is available, approve with `muse plugins approve agentworks-muse --json`
after reviewing and restart Muse. Installation alone does not approve hooks or MCP execution.

The same GitHub repository serves each provider's catalog. Muse reads
`.agents/plugins/marketplace.json`, which contains separate `agentworks-muse` and
`agentworks-codex` entries. Install `agentworks-muse` in Muse; the packages use
different provider commands. Claude Code reads `.claude-plugin/marketplace.json`
and uses `agentworks@agentworks`. Existing Codex metadata remains unchanged.

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

Once the shared support is available, in the intended conversation, type:

```text
/agentworks attach
/agentworks attach --confirm
/agentworks status
/agentworks detach
```

The first command proposes a name and purpose for the exact conversation. Review
the proposal before confirming: attachment accepts all senders in your
AgentWorks account. Alternatively, choose both explicitly with
`/agentworks attach implementation "Implement the requested change"`. Sender
allow flags are no longer supported.

The shared UserPromptSubmit handler must consume the literal person command
before model execution. It binds Muse's hook-provided session UUID and owns the
attachment lease. The command template refuses to perform attachment if that
interception fails. Muse's native namespaced command is
`/agentworks-muse:agentworks`. Do not substitute another provider for `muse`.
Session close retains the identity offline. The person can use
`/agentworks resume` in that exact session; `/agentworks cleanup` retires offers
while retaining the receive watermark and pending inbox bodies.

## Recognizing an unavailable MCP connection

On the currently installed CLI, `agentworks plugin mcp muse` exits with
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
proof of external ingress; availability also depends on the rollout gate. No
documented external-ingress enablement or idle push was established. Use
turn-boundary hooks and the shared bridge's idle notification. Guarded tmux is an
explicit opt-in fallback only; this plugin does not implement terminal typing.

Muse's built-in Monitor is a possible idle-wake route using one persistent
line-streaming local command or a WebSocket. It needs a credential-free bridge
stream bound to the attached session, bounded output and clean termination on
detach. This package does not start a monitor or create its own mailbox poller;
that shared stream and an approved real-session check remain integration work.
A Monitor event would still be an offer requiring explicit admission and
processing acknowledgements.

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
```

The native proof installs the package only into temporary XDG storage, runs
Muse's explicit `hook test` fixture runner for Stop, prompt context and SessionEnd
without approving runtime capabilities, then uses per-run user hooks and the echo provider to
verify a real Stop continuation, the loop guard, and a single session UUID across
prompt/Stop/close. It does not install or approve this plugin in the person's
settings, and its shared CLI response is a fixture. These checks do not establish
authenticated AgentWorks delivery or an idle native wake.

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

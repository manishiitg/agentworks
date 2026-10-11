# AgentWorks for Muse

This native Muse plugin packages an AgentWorks MCP connection, messaging skill,
person command and UserPromptSubmit, Stop and SessionEnd hooks. It uses the shared
`agentworks plugin hook muse` boundary. It does not store credentials, read a
mailbox in a polling loop or implement another bridge.

## Requirements and installation

Use Muse 1.4.4 or a compatible release, Python 3 and an AgentWorks CLI supporting
`agentworks plugin hook muse` on PATH. The common bridge is owned by PLAT-861;
this provider package alone does not supply that command. Sign in to AgentWorks
with the CLI's normal person-controlled login before attaching.

From the repository root, the person installs and reviews the capabilities:

```sh
muse plugins validate plugins/muse --json
muse plugins install plugins/muse --scope user --json
muse plugins inspect agentworks-muse --json
muse plugins approve agentworks-muse --json
```

Approve only after reviewing the hook commands and MCP connection. Restart Muse
to load newly approved hooks. Nothing in this package approves its own trust or
attaches a conversation automatically.

In the intended conversation, type:

```text
/agentworks attach implementation "Implement the requested change" --allow-sender reviewer
/agentworks status
/agentworks detach
```

The shared UserPromptSubmit handler must consume the literal person command
before model execution. It binds Muse's hook-provided session UUID, requires
explicit approved senders (or `--allow-none`), and owns the attachment lease. The
command template refuses to perform attachment if that interception fails. For
the native namespaced command the bridge must also accept
`/agentworks-muse:agentworks`. Do not substitute another provider for `muse`.

## Delivery

The bridge holds the authenticated server stream and durable offer journal. Hooks
use its local socket through the CLI. Unattached/offline hooks return no output;
they do not launch the bridge or make server requests. Explicit attach is the
person-controlled exception. The Muse shim rejects malformed/sessionless inputs
and skips Stop when `stop_hook_active` is set. SessionEnd asks the bridge to detach
but returns no output because Muse treats this event as observational.

Stop returns `decision: block` with the offered sender content in `reason`.
UserPromptSubmit adds context through the shared handler. Neither writes terminal
input, so an unsent composer draft is not touched. An offer is still stored until
the receiving agent acknowledges admission; processing is a separate explicit
acknowledgement. Replies retain the original inbox. Sender approval, revocation,
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

Sources: [Muse extensions](https://dev.meta.ai/docs/muse-code/extending),
[session messaging](https://dev.meta.ai/docs/muse-code/session-messaging), installed
plugin help and the native plugin contract bundled in Muse 1.4.4-R5419.1.

## Verification

```sh
python3 plugins/muse/tests/check_hooks.py
muse skills validate plugins/muse/skills/messaging --json
muse plugins validate plugins/muse --json
python3 plugins/muse/tests/prove_muse_hooks.py /absolute/path/to/muse-binary
```

The native proof installs the package only into temporary XDG storage, runs
Muse's explicit `hook test` fixture runner for Stop and prompt context without
approving runtime capabilities, then uses per-run user hooks and the echo provider to
verify a real Stop continuation, the loop guard, and a single session UUID across
prompt/Stop/close. It does not install or approve this plugin in the person's
settings, and its shared CLI response is a fixture. These checks do not establish
authenticated AgentWorks delivery or an idle native wake.
After person approval, use `muse plugins hook test agentworks-muse:stop --fixture
<fixture.json> --json` with a fixture of the form `{"event":"Stop","stdin":
{"hook_event_name":"Stop","session_id":"<exact UUID>","stop_hook_active":false}}`.
Then verify actual
Stop continuation and prompt context in Muse, separate sessions, busy tools,
drafts, allowlist/revocation and restart behavior against the shared bridge.
Do not claim admission from hook test success or mark the integration complete
from manifest validation alone.

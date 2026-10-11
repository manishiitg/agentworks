# AgentWorks for Pi (PLAT-868)

Pi extension for an explicitly attached, exact existing conversation. The package
uses the common AgentWorks local bridge, not a second mailbox client. It contains
no server URL, token, credential store, remote poller or terminal input driver.

**Integration status:** Pi 1.1.0 native behavior is verified with the real CLI
against a private bridge fixture. Public main's shared bridge accepts `pi` and
`pi_extension`, with exact attachment fencing. Pi installer selection and the
authenticated bridge/server release matrix still need PLAT-861 integration.
This provider candidate has not been installed or deployed in production.

## Install and attach

After reviewing the package, the person installs the provider directory from a
local AgentWorks checkout (replace the example path):

```sh
pi install /absolute/path/to/agentworks/plugins/pi --local
```

Pi's project trust prompt must be approved by the person. This package never
changes trust settings or passes trust bypass flags. A global install requires
the person's explicit choice (`pi install` without `--local`). Local installs
reference the source directory rather than copying it, so keep that checkout.
This package is not published to npm; installing the repository root as a Pi
package does not select this subdirectory.

Sign in through AgentWorks' CLI using your chosen connection config and start the
shared local bridge with `agentworks bridge serve`. Explicit attach/reconnect
reports a missing bridge with that command. Restart/reload Pi after installing,
then use these commands in the **existing** Pi TUI conversation:

```text
/agentworks attach reviewer
/agentworks status
/agentworks detach
```

Attach asks for purpose, approved registered sender names and the absolute path
to your existing CLI connection config, then confirms the exact native session
ID and explicit sharing. The binding uses Pi's native session ID; this extension
does not supply a separate terminal host key. A blank sender list permits none. The model has no
attach/detach tool. RPC, print and JSON modes refuse attach/detach because this
extension requires the person's TUI confirmation. A model with operating-system
shell access is not isolated from local CLI credentials by this tool boundary.

`/agentworks reconnect` rechecks the active receiver's exact lease and reconnects
the extension. Initial discovery (or explicit reconnect after the receiver stops)
looks up the current lease for the same native session ID. It never signs in, creates a lease or starts the daemon. Unattached
sessions make at most a local status request, produce no startup output and
start no listener/timer. A missing daemon is silent on startup. Attached sessions
wait on the private Unix socket; only the bridge holds authenticated server SSE.

The default socket is `Library/Caches/agentworks/bridge/bridge.sock` under the
person's home on macOS, or `$XDG_CACHE_HOME/agentworks/bridge/bridge.sock` on Linux
(`~/.cache` when unset). `AGENTWORKS_BRIDGE_ROOT` selects an explicit alternate
root. The directory and socket must be owned by the current user and have no
group/other permissions. Windows needs a shared bridge IPC implementation;
this package does not provide a Windows Unix-socket substitute.

## Native delivery and receipts

Incoming messages use Pi's `sendUserMessage` with `deliverAs: "followUp"` and
`expandPromptTemplates: false`. Idle sessions start a native turn; busy sessions
receive the message through Pi's own follow-up queue after the active run's tool
work. No editor writes, simulated Enter, tmux, approval relay or channel launch
flags are used. The person's unsent editor draft remains intact.

Each envelope includes the stable message ID, sender and original inbox. Sender
content is untrusted and is never interpreted as a person's permission grant.

- A successful local request or void `sendUserMessage` call does not acknowledge
  delivery. `admitted` is sent only after Pi emits the exact corresponding native
  user `message_start` event. Each complete frame has a random marker and body
  digest. Wrapped input and combined batches retain admission when each frame
  is intact; a marker alone or a changed frame never proves admission. If another
  extension consumes/changes a frame or native submit fails, admission remains
  unconfirmed and the person receives a recovery notice when Pi settles.
- Pi stores a body-free custom journal entry scoped to the native session and
  attachment. The same offered ID is not submitted again within this runtime;
  accepted IDs reconstructed from the session journal are not resubmitted on
  reconnect/reload. The shared bridge owns durable offers before admission and
  uncertain offers; the extension does not promise exactly-once effects. Startup
  reconciles pending frames (and legacy batch hashes) against saved native user
  messages before allowing explicit processed/reply tools. It never resubmits them.
- Receipt retries run independently of `next` and Pi's native event hooks.
  Final 409 receipt refusals are journaled per ID; transient errors retry while
  later delivery continues. Admission and processing calls are serialized so
  processing first confirms any pending admission, then removes its retry without
  a later downgrade. A timer retries late/transient receipts independently of the
  bridge wait. A changed attachment stops the receiver; a known unoffered ID is final.
- `agentworks_processed(message_id)` is explicit agent acknowledgement after
  handling. `agent_end` retries native admission receipts; it never infers that
  a task has been handled and never starts a continuation loop.
- `agentworks_reply(message_id, message, submission_id)` explicitly replies
  through the bridge's original inbox and attached identity. Preserve the same
  submission ID after an uncertain result. A generated retry ID is returned on
  success or included in the error when a reply is unconfirmed. No reply is sent
  merely because a turn ends.

Detach, session replacement and quit stop the listener; replacement/quit also
request bridge detach. Reload retains the exact lease for the replacement
extension runtime. The extension never follows an account-owned name to a new
native session. Messages already handed to Pi's native queue are owned by Pi;
detach does not clear the person's queue or composer. An interrupted native
submit requires explicit recovery from the shared journal/mailbox, rather than
blind reinjection. Active `next`, status, receipts, replies and detach carry the exact attachment
ID. Invalid offers produce a body-free warning instead of disappearing silently.

A private `pi-recovery` directory beside the bridge socket holds only uncertainty
markers (hashed session/lease names and message IDs, never message bodies). Before
requesting an offer, the extension synchronizes a marker to disk; Pi's custom
journal can still be buffered in a conversation with no saved messages. A crash
between bridge release and native journaling therefore leaves a notice on restart
of the same session/lease. Saved native evidence clears known markers. An unknown
interrupted request produces a notice: explicitly read the mailbox to recover it;
the notice retires previous-process markers, without implying admission. Obsolete
lease markers are cleaned on reattach/detach. There is no automatic reinjection. A session with no saved conversation cannot be
resumed after a crash, so use an existing saved Pi conversation when attaching.

Optional full AgentWorks tool access can be added by the person through Pi's
built-in MCP support, for example:

```sh
pi mcp add --local agentworks -- agentworks --config /absolute/path/connection.json mcp serve
```

Approve the connection and project trust normally. It supplies tools; it does
not enable push or attach this conversation. Shared connections still share
mailbox authority unless PLAT-861 provisions narrower connections.

## Shared bridge requirements (PLAT-861)

The adapter is based on the inspected `LocalBridgeRequest`/`LocalBridgeReply`
candidate. It sends JSON `POST /bridge` over the private Unix socket:

| Action | Required fields and behavior |
|---|---|
| `status` | `provider: pi`, exact `session_id`, active `attachment_id` when known; return current `attached` and `attachment`. Local only. |
| `attach` | Person-confirmed `agent_name`, `purpose`, `allowed_senders`, absolute `config_path`, exact `session_id`. |
| `next` | exact `attachment_id`, `transport: pi_extension`; bounded 25-second wait on local journal, oldest-first authorized offers. |
| `ack` | Exact `attachment_id`, offered `message_id`, `delivery: admitted` or `processed`; never downgrade a processed receipt. |
| `reply` | Exact `attachment_id`, offered `message_id`, body and stable `submission_id`; retain the original inbox. |
| `detach` | Exact `session_id` and `attachment_id`; fence the old receiver. |

The response attachment must include `attachment_id`, `provider: pi` and matching
`native_session_id`. Each offer carries matching `attachment_id`, `message_id`,
`inbox_id`, sender, body and sequence. The bridge must enforce current lease,
approved sender and revoked connection checks before offering/acknowledging.
The server receipt transport enum must accept `pi_extension` only for provider
`pi`. The shared installer must select this directory for `agentworks attach pi`.
These shared changes belong to PLAT-861 and are not silently implemented here.

## Verification

Run only the provider probes locally; they do not build AgentWorks or frontend:

```sh
node --test plugins/pi/test/native.test.mjs
python3 plugins/pi/test/tui-probe.py
```

Set `PI_BIN` when Pi is not on PATH. The deterministic test provider is loaded
explicitly and excluded from the package manifest. Probes launch the actual Pi
CLI with an isolated config/session, no remote model or production credentials,
and a private local bridge fixture. They cover native idle delivery, real busy
tool execution, queue-vs-admission receipts, explicit processing/reply, duplicate
ID suppression, wrong lease refusal, revoke, private socket permissions and a
real TUI unsent draft. Regression probes also cover permanent per-ID receipt
refusals, lost responses followed by processing, several busy batches in Pi's
all-at-once queue mode, a second extension wrapping/changing input, reattachment
fences, a killed process before the offer response, and native transcript
reconciliation after lost custom receipt entries. The fixture proves Pi behavior
and provider requests, not server authorization.

Remaining release evidence: shared installer selection, actual account
attachment and approved sender enforcement, connection revocation, pending
catch-up across actual bridge/server restart and authenticated recovery,
multiple simultaneous sessions, busy detach/revoke, person-approved project
installation, candidate review and authenticated end-to-end messaging.

Provider references: [Pi extensions](https://pi.dev/docs/latest/extensions),
[package installation](https://pi.dev/docs/latest/packages),
[MCP setup](https://pi.dev/docs/latest/mcp), and the installed 1.1.0 declarations
and `AgentSession.sendUserMessage` implementation. The installed API returns
`void` from `ExtensionAPI.sendUserMessage`; native events supply the receipt
proof used here.

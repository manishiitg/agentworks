# AgentWorks for Pi (PLAT-868)

This package connects an exact existing Pi conversation to the common AgentWorks
local bridge. The bridge owns credentials, child connections, same-account
sender authorization, lease fencing and the durable offer journal. Pi holds no
server credentials and runs no remote mailbox poller.

## Person setup

The person runs `agentworks attach [NAME]` in a terminal. The CLI discovers live
SDK sessions, lets the person select one, installs a missing Pi package only
with the person's approval from its bundled source or exact CLI commit (the
source is shown before approval), starts the shared bridge and activates the selected
session after its authorized lease exists. NAME is an agent identity, not a
provider argument. Use the existing CLI connection; no new login is inferred.
A missing package requires Pi reload and a fresh person attach. Provider trust
prompts remain the person's choice.

For manual project installation from a reviewed local checkout:

```sh
pi install /absolute/path/to/agentworks/plugins/pi --local
```

Local packages reference that checkout, so retain it. Do not install the repository
root as a Pi package. Global installation is a separate person choice. This
adapter never writes trust settings or bypasses provider approvals.

In the existing Pi TUI, type one of these literal commands:

```text
/agentworks attach [NAME]
/agentworks detach
/agentworks agents
/agentworks help
```

Typed attach is the approval. Purpose is set by the receiving agent after the
bridge's single setup offer; there are no config, purpose or sender prompts.
The input hook checks interactive/TUI source before any side effects. Registered
slash commands are deliberately avoided because Pi dispatches them before its
input source hook. RPC, print and extension-generated text cannot attach/detach.
The hook consumes a one-use command/session nonce held only in SDK memory; parent
PID alone cannot distinguish a person from a model shell that execs the CLI. A
non-exec shell shim for agentworks on PATH breaks the direct-parent check; use
the native CLI binary. No attach/detach tool is exposed to the model. Model shell access is not a
security boundary against the person's own local OS credentials.

## Exact process and native delivery

An SDK-owned, private `pi-sessions/PID.json` record binds the actual native UUID,
PID/start-time SHA256, current working directory and a private activation socket.
The terminal validates process identity, record/socket ownership and permissions;
it never guesses a session from a project, recent file or selected agent name.
Activation checks the exact host/session/lease against bridge status before
starting one consumer. It cannot create an attachment or child connection,
change identity, admit messages, accept sender bodies or revive an offline lease.
Repeated activation preserves the same consumer. Session switch/end cancels it
and removes discovery; end marks the retained identity offline. A new process
requires person attach. Reload of the same process may restore its existing lease.

Unattached startup performs at most one quiet local status lookup. It creates only local
SDK discovery/activation metadata, without starting delivery timers or remote
listeners. The default bridge root is the system user cache under
`agentworks/bridge`; `AGENTWORKS_BRIDGE_ROOT` selects an alternate private root.
Directory/socket must belong to the current user with no group/other access.
Windows needs a supported shared IPC adapter; unsupported startup fails closed.

Attached input uses `sendUserMessage` with `deliverAs: 'followUp'` and
`expandPromptTemplates: false`. Busy sessions use Pi's own queue after tool work;
idle sessions start a native turn. The person's unsent draft stays intact.
Each complete frame contains a random marker, sender, message ID and original
inbox. Wrapped or merged batches retain local evidence only when the whole frame
matches its digest. A surviving marker with altered text proves nothing.
Already submitted input belongs to Pi; this adapter never clears the person's
queue. Detach or changed leases stop further offers and tools.

## Receipts and recovery

Stored, offered, native display and receiver admission are distinct. A native
`message_start` only records intact-frame evidence. It never sends an admission
receipt. The receiving agent explicitly uses the registered schemas:

- `agent_message_ack(message_id, delivery)`: admitted on receipt, processed after
  handling. Processing completes a pending admission the receiver already requested;
  display alone never creates admission intent.
- `agent_message_reply(message_id, message, submission_id)`: optional reply in
  the original inbox; keep the same submission ID after uncertain results.
- `agent_message_peers()`: discover authorized names for this attached identity.
- `agent_message_set_purpose(purpose)`: one line, up to 80 characters, at most
  once per minute. Set once after setup, then update when the person asks.
- `agent_message_send(target, message, submission_id)`: contact only a peer the
  person authorized; acceptance means stored, not admitted.
- `agent_message_read(inbox_id?, after?)`: explicit bounded recovery, never a
  watch loop. Server validation retains account and lease boundaries.

Active status, offers, tools and shutdown carry the exact host and attachment.
Receiver-requested receipt retries run independently of offers. Final per-message
refusals do not block later input; attachment-wide refusal stops delivery.
Successful processing cancels pending admission retry to avoid downgrade.

Before next releases an offer, a private fsynced uncertainty marker covers the
bridge-response-to-native-journal crash window. Recovery reconciles saved native
frames, reports ambiguity and retires old markers only after notice. It never
blindly reinjects or converts recovery into admission. Native custom evidence and
explicit admission intent are separate. Atomic metadata uses stable process birth
identity; fresh/live artifacts are preserved and stale temporary/malformed files
are bounded housekeeping. An idle empty reply need not write the marker twice.

The bridge owns the persisted setup/guidance cadence: setup once per person
attach, full guidance once per session and after 24 hours, short provenance/help
on each offer. The manifest loads the generated canonical messaging skill;
edit its shared source and run the sync script, never edit the generated copy.

## Verification and release limits

Run the five actual CLI regressions with `node --test plugins/pi/test/native.test.mjs`
and the PTY editor probe with `python3 plugins/pi/test/tui-probe.py`. They use an
isolated real Pi 1.1.0 session, deterministic receiver tools and a private fixture
bridge with no production credentials. Coverage includes busy merged batches,
changed-frame refusal, native display without admission, receipt retry/refusal,
lease-only activation, detach/revoke and process-crash recovery. Shared Go checks
run remotely through `scripts/verify-remote.sh` before main.

These fixtures do not prove authenticated production rollout. Same-account
server enforcement, real connection revocation, bridge/server restarts and
multiple simultaneous sessions still need authenticated release evidence.
No local running binary, listener or installed plugin is replaced by this task.

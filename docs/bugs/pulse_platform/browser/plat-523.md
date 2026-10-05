# PLAT-523 — Browser dead-session recovery can misclassify unrelated errors

State: open. Date: 2026-10-05. Priority: P3. Source: owner-supplied browser review.

## Verified review

The local `isDeadSession` classifier in `executor.go` matches any error
containing CDP response channel closed, No such file or directory or
ProcessSingleton. It excludes Failed to save errors, but does not corroborate
other matches with daemon/socket/process health before calling
`killSessionRuntimeFully` and retrying the command.

`client.go` builds errors from API error, stdout and stderr, so a file-operation
failure or incidental stderr can satisfy the broad match. The classifier also
controls failed-open removal from the tracker. Direct-CDP automatic recovery
has an additional active-owner/recording guard; this reduces shared disruption
but does not make the text classifier a reliable health check.

Source confirms the risk. This review did not reproduce a healthy-browser loss
from a benign error or claim the runtime currently has structured error codes.

## Remaining

Limit recovery to structured transport failures when available, or corroborate
with the correct execution host's socket/daemon health. Distinguish launch
singleton collisions from action/file errors. Preserve the original failure and
a healthy session; do not retry mutations solely because error text resembles
a dead runtime. Verify a healthy browser survives missing-file errors and a real
crash still follows the supported recovery path.

No runtime fix or deployment is claimed by this ticket.

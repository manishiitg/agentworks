[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-451 — A slot launch mismatch falls through to the app's tmux

| Coordination | Value |
|---|---|
| State | open; confirmed by selector test and Linux source trace |
| Priority | P1 |
| Date | 2026-10-04 |
| Owner | security-sandbox |

## Finding

PLAT-442 commit `892c86cfe` documents that a launch script naming one slot
and a working folder naming another is refused. `ExecConfig.SlotForLaunch`
logs the mismatch and returns an empty string. `slottmux.run` interprets the
same empty string as an ordinary non-slot launch and calls `passthrough`.
That invokes the system tmux with the app account's identity/environment.
A mismatch therefore does not stop the launch; it removes the slot routing.
Landlock is a separate layer and does not make this OS identity equivalent.

## Evidence and scope

Reviewed AgentWorks `a04b393c9`. The existing
`TestSlotForLaunchFollowsTheScriptsRunFolder` passes the row "script says A,
folder is B's tree: refused, not run as either" while asserting only that the
selector returns empty. Source trace: `workspace/slots/config.go:161-163` →
`workspace/cmd/slottmux/main_linux.go:328` → `344-345` → `passthrough` (`:30`).
The Muse branch also passes through for the same empty result.

Workspace slot tests passed on macOS. The Linux tmux launch itself was not
executed during review, so this is a verified control-flow finding, not a live
OS-account exploit demonstration. PLAT-449 supplies a reachable source of
script/folder disagreement through mutable owner metadata.

## Left

Represent mismatch as an explicit error separate from "no slot requested",
and return a nonzero status before executing tmux. Test the frontend's decision
through execution, not only the selector's empty-string return. Normal
explicit app-account launches must still work. Deploy the corrected root-owned
tmux frontend with the matching application/provider release.

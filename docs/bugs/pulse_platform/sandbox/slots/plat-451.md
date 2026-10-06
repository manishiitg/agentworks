[← sandbox / slots](index.md)

# PLAT-451 — A slot launch mismatch falls through to the app's tmux

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | sandbox |
| Area | slots |
| Summary | fixed on main (not deployed). |

| Coordination | Value |
|---|---|
| State | fixed on main, not deployed; the root-owned slottmux frontend must be deployed with the matching release |
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

## Done (fixed on main)

- `workspace/slots`: `SlotForLaunch` returns `(slot, error)`; a script/folder
  disagreement is a `*SlotMismatchError` (`errors.Is(err, ErrSlotMismatch)`),
  separate from "no slot requested" (`"", nil`). `SlotForDir` is a folder rule
  with no mismatch notion and is unchanged.
- `workspace/cmd/slottmux`: on a mismatch it prints
  `[SLOT_EXPLICIT_MISMATCH] launch refused ...` to stderr and exits 126 before
  anything is executed, ahead of the forget/Muse/passthrough/slot branches. The
  file is now `main.go` (no build tag) with `passthroughFn`/`asSlotFn`/
  `runAsSlotFn` hooks so the decision is testable on macOS. App-account
  launches, hosts without slots and every other tmux command are unchanged.
- Provider `e5790ce` (`internal/slotfs`): when the application declares a user
  and a slot for a launch, the canary covers that user, and the host table says
  another slot, has none, or (no table) the name is not a slot name, the launch
  is refused with `slotfs.ErrLaunchBlocked` (`*LaunchBlockedError`) instead of
  falling back to the app account (or silently using the table's other slot). A
  declared app account, a user the canary does not cover, and a user named
  without a slot that the host does not hold one for keep today's "no slot".
  `[SLOT_FALLBACK]` logging for path-inferred slots is unchanged. Propagation:
  `slotfs.CreateTemp`, `MkdirTemp`, `WrapCmd` (all return the error),
  `shelllaunch.CommandWithEnv` / `CommandWithScopedEnv` (so `CommandWithFinalEnv`
  and every adapter that calls them), `clisandbox.LandlockArgs`, and the plain
  compatibility launch in `clisandbox.PrepareCodexCommandScoped`.
- Tests: `TestSlotForLaunchFollowsTheScriptsRunFolder` now asserts the error
  (mismatch rows: A script/B tree, B script/A tree, A script/slot B state
  folder, A script/slot B run folder); slottmux `TestRunDecision...` (hooks,
  runs everywhere) and `TestBuiltFrontendRefusesAMismatchBeforeTmux` (builds and
  executes the binary; Linux only, ran in a golang:1.26 Linux container with no
  tmux, so the app-account row proves it reached the exec of the system tmux);
  provider `TestExplicitMismatchIsARefusalNotNoSlot` and the end-to-end launch
  tests in `shelllaunch` and `clisandbox`. The 13-row identity table and the
  workspace table are unchanged and green.

## Left

- Deploy: the root-owned slottmux frontend (`workspace/cmd/slottmux`) and the
  application/provider release (`ae8e204` -> `e5790ce` pinned in `agent_go/go.mod`)
  go together. An old frontend with a new provider is safe (the provider refuses
  first); a new frontend with an old provider still lets the provider
  fall back to the app account for a declared-but-unconfirmed slot.
- Provider helpers that cannot return an error (`slotfs.SlotOf`, `IsSlotLaunch`,
  `Mode`, `TempDir`, and the test-only `claudeInteractiveShellCommand` /
  `codexInteractiveShellCommand` wrappers around `shelllaunch.Command`) still
  report "no slot" for a blocked launch; they are always preceded by the
  checked launch functions above, but a new launch path must call
  `slotfs.CheckLaunch` first.
- `agent_go` was not rebuilt here (its replace points at the main checkouts);
  the provider change is additive API.

## Register notes

[PLAT-451](plat-451.md), P1, fixed on main (not
deployed). The slot selector logs a refusal but returned the same value as a
non-slot launch; slottmux then passed it to the app account's tmux. A mismatch is
now an explicit error in the selector, slottmux and the provider; the root-owned
slottmux must be deployed with the matching release.

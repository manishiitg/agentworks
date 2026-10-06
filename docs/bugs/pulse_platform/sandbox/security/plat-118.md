[← platform / security-sandbox](index.md)

# PLAT-118 — Linux `execute_shell_command` is unusable on rootless AppArmor-hardened hosts because the platform unconditionally requires a privileged mount namespace

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | sandbox |
| Area | security |
| Summary | P1 — every `execute_shell_command` call fails before the user's |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented; core runtime verified` — project-scoped Video Studio shell is fixed on EC2; nested `BlockedWritePaths` and provider-auth acceptance remain open |
| Last synchronized | `2026-08-17` |

- **Priority:** P1 — every `execute_shell_command` call fails before the user's
  command starts, including `echo` and `whoami`. Products that depend on the
  bridge shell cannot inspect files, render media, or call providers such as
  fal.ai. The failure is total on affected Linux deployments, but does not
  affect macOS or Linux hosts that grant mount-namespace capability.
- **Owner:** shared workspace shell sandbox (`workspace/security/isolator.go`),
  shell execution boundary (`workspace/handlers/shell.go` and
  `agent_go/pkg/workspace/execute_shell_command.go`), and Linux deployment
  preflight/health reporting.

## Symptom

In the public Video Studio deployment, Claude Code correctly reaches the
AgentWorks MCP bridge and calls `execute_shell_command`, but every call returns
a tool failure before the requested program runs. A request to test fal.ai
therefore reports that the shell is unavailable; it provides no evidence about
the fal.ai credential itself.

The smallest user-visible reproduction is:

```json
{"command":"whoami"}
```

The bridge returns `exit_code=1` with no command output. `echo test`, file
inspection, and provider test scripts fail identically.

## Confirmed platform call chain

`agent_go/pkg/workspace/execute_shell_command.go` resolves and validates the
Folder Guard, then the workspace service constructs a `security.Isolator`.
`Isolator.ExecuteIsolated` selects its backend only by operating system:

```go
if runtime.GOOS == "darwin" {
    return iso.executeIsolatedMacOS(ctx, command, args)
}
return iso.executeIsolatedLinux(ctx, command, args)
```

The Linux backend then unconditionally executes:

```go
exec.CommandContext(ctx, "unshare", "-m", "--propagation", "private", "sh", scriptPath)
```

There is no capability probe, supported rootless backend, or actionable startup
failure. Consequently this is shared platform behavior: any AgentWorks product
using `execute_shell_command` on the same host policy fails, not only Video
Studio.

## Live evidence — Video Studio EC2, 2026-08-17

The deployed instance is a non-root installation running as `video-studio` on
an AWS Linux kernel with Landlock and AppArmor enabled:

```text
kernel=6.17.0-1019-aws
kernel.apparmor_restrict_unprivileged_userns=1
```

Running the exact namespace primitive used by the platform as the service user
produces:

```text
$ /usr/bin/unshare -m --propagation private sh -c 'echo platform-shell-ok'
unshare: unshare failed: Operation not permitted
exit=1
```

`kernel.unprivileged_userns_clone=1` and a nonzero
`/proc/sys/user/max_user_namespaces` do not make this operation legal: the
AppArmor policy still prevents the unprivileged namespace setup. Adding
`--user --map-root-user` was also tested and fails while writing
`/proc/self/uid_map`.

The same host reports `landlock` in `/sys/kernel/security/lsm`, so a rootless
kernel-enforced filesystem sandbox is available without changing the host-wide
AppArmor policy.

## Root cause

The platform treats "Linux" as equivalent to "the service may create a mount
namespace." That capability is not guaranteed for a non-root process and is
intentionally denied by common hardened distributions. The deployment is
rootless by design, while the selected Linux sandbox backend requires a
privilege the service does not have.

The bug has two layers:

1. **Execution:** Linux has no rootless sandbox backend, even when the kernel
   exposes Landlock.
2. **Diagnosis:** startup succeeds and all services report healthy; the
   incompatibility is discovered only after an agent calls the shell, and the
   bridge reduces the cause to a generic tool failure.

## Required repair

### 1. Add a rootless Linux sandbox backend

Implement Landlock as the preferred backend when its required ABI and access
rights are available. Apply the restriction in a dedicated child launcher
before `exec`, with `no_new_privs`, so the Go server process itself is not
restricted and the policy is inherited irreversibly by the command and all of
its descendants.

The backend must preserve the existing `Isolator` contract:

- `ReadPaths` are readable but not writable;
- `WritePaths` are readable and writable;
- `BlockedPaths` deny both reads and writes and take precedence;
- `BlockedWritePaths` remain readable but reject writes;
- `WorkDir` is usable without granting its parent tree accidentally;
- symlink/canonical-path escapes remain denied;
- only the minimal system paths required to load and execute programs are
  readable/executable;
- the safe environment and secret-injection boundary remain unchanged.

Network policy must remain explicit. Landlock filesystem rules must not
silently change current connectivity, and any Landlock network rules should be
applied only from the platform's declared `AllowNetwork` policy.

### 2. Detect capabilities instead of assuming them

Select a Linux backend from verified runtime capabilities, not only `GOOS`:

1. use Landlock when the necessary ABI is present;
2. use mount-namespace isolation only when a harmless preflight proves it is
   permitted for the service identity;
3. otherwise fail closed with a typed `SANDBOX_UNAVAILABLE` error.

Never fall back to executing the command directly.

### 3. Make deployment health truthful

The Linux deployment preflight must run a sandboxed no-op using the same
service identity and backend that real shell calls use. Service health should
report the shell capability separately from HTTP process liveness. The UI/tool
error should state which sandbox backend failed and why, without exposing
secrets or host paths.

## Independent code review (2026-08-17)

Every code claim above was checked against the source rather than taken from the
report. All of them hold:

| Claim | Verified |
|---|---|
| `ExecuteIsolated` selects a backend only from `GOOS` | `workspace/security/isolator.go:139-145`, verbatim |
| Linux is an unconditional `unshare -m --propagation private` | `workspace/security/isolator.go:169`, verbatim |
| No capability probe exists | `unshare` appears only in `isolator.go` — four comments and one `exec`, nothing that tests permission |
| No Landlock support anywhere | zero matches across `workspace/` and `agent_go/` |
| Shared platform behaviour, not Video Studio-specific | four call sites: `workspace/handlers/shell.go:159`, `agent_go/cmd/family-server/browser_backend.go:220`, `agent_go/cmd/family-server/shell_tool.go:107` and `:196` |

Three additions the report does not cover.

**The diagnosis layer may be less broken than stated — check before rebuilding
it.** `ExecuteIsolated` returns an error only for *setup* failure (writing the
mount script). The `unshare` denial happens later, at `cmd.Run()`, so
`unshare: unshare failed: Operation not permitted` lands in `stderrBuf` and
should reach the caller through the normal exit-code path. The report says the
bridge returned `exit_code=1` with no output, which suggests that stderr is
being dropped somewhere between the handler and the tool result. Establish which
of the two it is first: if the stderr already surfaces, "make deployment health
truthful" is a much smaller change than section 3 implies; if it does not, that
is a separate reporting defect that deserves its own line rather than being
absorbed into the sandbox work.

**An unsandboxed execution path already exists.** `workspace/handlers/shell.go`
runs `exec.CommandContext(ctx, "sh", "-c", fullCommand)` with no isolator in the
`else` branch taken when no Folder Guard is supplied. That is not the
capability-triggered fallback the non-fixes section forbids, but anyone
implementing "fail closed" must know it is there — otherwise fail-closed looks
already-guaranteed when it is only guaranteed for guarded calls.

**Keep the `--user` finding prominent.** The current call creates a mount
namespace with `-m` and no `--user`. The live evidence records that
`--user --map-root-user` was also tested and fails while writing
`/proc/self/uid_map`. That single line forecloses the obvious "just add
`--user`" patch, and it should stay visible to whoever picks this up.

### On the proposed repair

The shape is right — Landlock preferred, namespace only when a preflight proves
it, otherwise typed `SANDBOX_UNAVAILABLE`, never unsandboxed. Two cautions:

- **Landlock is not a translation of the current contract.** It has no
  equivalent of `BlockedWritePaths` *precedence*; the same effect is produced by
  simply never granting write on that subtree. The rule set has to be rewritten
  in Landlock's own terms and re-argued against the acceptance list, not mapped
  across mechanically. The ticket also says "when the necessary ABI is present"
  without pinning which: filesystem rules need ABI 1+, network rules ABI 4+, and
  the two should be probed separately so a kernel with filesystem-only support
  is still usable.
- **The test boundary is the strongest section of this ticket** and should be
  treated as binding. It already states the rule this register keeps relearning
  — exercise the product path, never construct a successful backend result. That
  rule was violated again on the same day this ticket was written: PLAT-117's
  first fix passed its tests while not matching any record that occurs in
  production, because the test built the record shape the fix expected. A
  Landlock backend is exactly the kind of change where a constructed-success
  test would certify nothing.

### Not attempted here

No code change is proposed. This is Linux-deployment behaviour that cannot be
meaningfully verified from a macOS workstation, and the ticket's own acceptance
criteria require the hardened EC2 host. Reviewing it from here without that host
would produce precisely the constructed-evidence result the test boundary warns
against.

## Implementation and live verification — 2026-08-17

The repair now ships a dedicated `video-studio-landlock-runner`. The workspace
service writes a mode-0600 policy file, starts the launcher with the existing
safe environment, and the launcher applies `no_new_privs` plus a Landlock
filesystem ruleset before replacing itself with the requested command. The
server process is never restricted. Linux selection is now capability-based:
Landlock first, a mount namespace only after a successful `unshare` preflight,
and otherwise a typed `SANDBOX_UNAVAILABLE` setup error. There is no
capability-triggered unsandboxed fallback.

`/health` now exposes `shell_sandbox` separately from HTTP liveness and caches
the result of a real sandboxed `/bin/true` launcher preflight. The rootless
deployment builds and installs the launcher alongside the workspace binary.

The focused Linux regression test calls the real `ExecuteShellCommand` HTTP
handler and real child launcher. On the hardened EC2 kernel, as uid 999
`video-studio`, it proved:

- an allowed file is readable and an allowed output file is writable;
- a read-only path rejects writes;
- an ungranted path is unreadable; and
- a symlink inside an allowed path cannot escape to the ungranted file.

The deployed release
`/var/lib/video-studio/video-studio/releases/8dcda96a6-20260817145736` then
passed the authenticated production `/api/execute` route with the exact
project-scoped Video Studio guard for
`_users/default/Chats/Video Studio/projects/newtest-6570e26d`:

```text
shell_sandbox.available=true
shell_sandbox.backend=landlock
shell_sandbox.detail="filesystem ABI 7; launcher preflight passed"
pwd=/data/video-studio/docs/_users/default/Chats/Video Studio/projects/newtest-6570e26d
whoami=video-studio
project write/create/remove=pass
parent-directory escape=blocked
public HTTPS gateway=reachable (HTTP 303 auth redirect)
exit_code=0
```

All three rootless user services (`video-studio-agent`,
`video-studio-workspace`, and `video-studio-gateway`) were active after the
release switch. Local macOS security/handler tests and Linux amd64 cross-builds
also pass.

Two acceptance items remain deliberately open rather than being claimed:

1. Landlock rules are additive, so a writable parent plus a nested
   `BlockedWritePaths` exception cannot preserve the current precedence
   contract exactly. Such a policy fails closed with `SANDBOX_UNAVAILABLE` on
   this host instead of weakening the deny. The deployed Video Studio project
   guard does not use that shape.
2. DNS/TLS and the public gateway were verified from the guarded child, but a
   fal.ai authentication-only call was not made. No paid provider request was
   issued and no credential was printed.

### Review follow-up after the fix landed (2026-08-17)

Re-checked the shipped implementation against this review's concerns. All of the
code claims in the section above verify:

- backend selection is capability-based, not `GOOS`-based
  (`workspace/security/isolator_linux.go:22-36`): Landlock when the ABI probe
  succeeds, mount namespace only after `mountNamespaceAvailable()` actually runs
  `unshare -m --propagation private true`, otherwise a typed
  `SANDBOX_UNAVAILABLE`. There is no capability-triggered unsandboxed path;
- both probes are real rather than assumed — `landlockABI()` issues a genuine
  `LANDLOCK_CREATE_RULESET_VERSION` syscall, and the namespace probe executes;
- `/health` exposes `shell_sandbox` via `security.CurrentSandboxCapability()`
  (`workspace/server.go:105`), separately from HTTP liveness;
- the rootless deploy script cross-builds and installs the launcher
  (`deploy/aws-ec2/deploy-rootless.sh:32`);
- the focused test drives the real handler and the real launcher, with
  read/write/blocked/symlink-escape cases — the product path this ticket's own
  test boundary demands, not a constructed backend result.

The review predicted that Landlock could not express `BlockedWritePaths`
precedence, because its rules are additive and a narrower rule cannot revoke a
broader write grant. That is exactly the limit the implementation hit, and it
was handled the right way: such a policy fails closed with `SANDBOX_UNAVAILABLE`
rather than silently weakening the deny. Recorded as open rather than claimed.

**Limits of this follow-up.** The EC2 runtime evidence above was not
independently reproduced here — it cannot be, from a macOS workstation. What was
verified locally is that the Linux path cross-builds and vets cleanly
(`GOOS=linux GOARCH=amd64`), and that the macOS security/handler suites still
pass. The runtime claims are specific and falsifiable (release id, backend,
`filesystem ABI 7`, service uid, per-case results), which is the right shape for
a claim someone else must be able to check — but they remain Codex's evidence,
not this reviewer's.

## Explicit non-fixes

- Do not run the workspace service as root.
- Do not grant `CAP_SYS_ADMIN` to the service or `unshare` binary.
- Do not disable AppArmor or set
  `kernel.apparmor_restrict_unprivileged_userns=0` host-wide.
- Do not bypass `Isolator` or rely only on command-string/path validation.
- Do not silently run unsandboxed when capability detection fails.

Those options either violate the deployment's rootless contract or weaken the
entire host to make one platform primitive work.

## Acceptance

- On a Linux host with
  `kernel.apparmor_restrict_unprivileged_userns=1`, running as an ordinary
  non-root service user, `execute_shell_command({"command":"echo test"})`
  succeeds through a kernel-enforced sandbox.
- An allowed project file can be read; an allowed output file can be created.
- A read-only path rejects modification while remaining readable.
- A blocked path and a symlink escape are unreadable and unwritable.
- Deployment credentials, other projects, and the service user's private files
  are inaccessible from the child command.
- A command that requires declared network access can reach its provider; a
  network-denied command cannot establish an outbound connection.
- If neither Landlock nor a proven namespace backend is available, startup or
  the shell capability check fails closed with `SANDBOX_UNAVAILABLE`; the user
  never receives a misleading provider-key diagnosis.
- macOS `sandbox-exec` behavior and existing Folder Guard tests remain green.
- The deployed Video Studio instance passes, in order: `echo`, project
  read/write, blocked-path denial, and a fal.ai authentication-only probe that
  does not print the credential.

## Test boundary

Focused tests must exercise the product path, not construct a successful
backend result directly:

1. start the workspace service as a non-root user under an AppArmor policy that
   rejects unprivileged user namespaces;
2. call the real `/api/execute` route with an enabled Folder Guard;
3. assert the allowed/denied filesystem and network cases above;
4. force backend capability absence and assert the typed fail-closed error;
5. deploy the resulting binaries rootlessly and repeat the live Video Studio
   shell probe before moving this ticket to `runtime_reverify` or `done`.

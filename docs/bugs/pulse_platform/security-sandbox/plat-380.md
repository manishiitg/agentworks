[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-380 — Relay builder shell fails inspecting the host Google CLI store

| Coordination | Value |
|---|---|
| State | implemented and verified; deployment pending |
| Date | 2026-10-03 |
| Owner | security-sandbox |

## Evidence

The user reported that an invoice Relay builder could read workflow metadata but
failed before creating a plan. Read-only inspection of Excellence's workspace
log found three failed `execute_shell_command` calls in `Workflow/test` at
2026-10-03 16:03:11–16:03:28 +02:00, including `pwd` and `echo hi`:

```
SANDBOX_UNAVAILABLE: inspect Landlock path: stat /srv/agents/home/.config/agentworks/gog: permission denied
```

Each returned exit code 125 in 23–33 ms, before the requested command ran. The
PDF/base64 INPUT is unrelated to this admission failure. The release inspected was
`agents-6ca99b48-20261003145227` (builder `6ca99b48b`, mcpagent `e9af395a6`,
provider `e38d33f25`). No service, permissions, credentials or workspace files
were changed during inspection.

## Cause

`workspace/security/isolator.go` automatically appended `gogconfig.TerminalHome`
to read/write paths whenever the profile was not strict. Its value came from the
service's HOME/config. Relay uses the shared workflow Builder runner, so it took
that legacy branch even when running as a user's Linux slot. Slot identity alone
was missing from the Google-store policy. Crew/Code's stricter profiles already
excluded that automatic host grant. Plan creation does not need Gmail.

## Implemented

- Add one shared `hostGogRestricted` predicate: strict profile OR user slot.
  Use it for both automatic Google-store grants and environment construction in
  every runner backend. There is no Relay-specific sandbox or alternate executor.
- A slot command never creates/adds the host Google store and never inherits its
  `GOG_HOME`, `GOG_KEYRING_BACKEND` or `GOG_KEYRING_PASSWORD`. Explicit per-call
  variables and session credentials still arrive in the slot request.
- Local trusted CLI access keeps its existing behavior. Per-connection Google
  tools and their authorization code are unchanged. No credential-directory
  permissions are loosened and no production service or workflow is altered.

## Verification

- Local policy/environment regression covers non-strict Relay slots, strict
  profiles and trusted local shells. Existing macOS sandbox test still reads and
  refreshes a synthetic Google-store file for trusted shells.
- Linux serialized-request test verifies no host GOG/keyring variables reach a
  slot while the existing per-call session token, secrets and inputs remain.
- Opt-in Linux integration ran on Excellence through the shipped Landlock
  launcher and slotctl, in a throwaway folder under an existing slot's run area.
  The old predicate reproduced the exact reported exit 125 / `inspect Landlock
  path: stat .../gog: permission denied`, using a private service-owned fixture.
  The corrected predicate ran `pwd` as the slot, saved valid JSON in
  `planning/plan.json`, preserved INPUT/session environment and denied a
  readable-but-ungranted host credential fixture. All three Linux tests passed.
- Tests used no real email credentials, mailbox calls or live Relay files.

## Remaining

Deploy the updated workspace service in the normal Excellence release. Then
retry the invoice Relay's builder message externally. The deployed service was
not restarted or changed by this fix's integration test.

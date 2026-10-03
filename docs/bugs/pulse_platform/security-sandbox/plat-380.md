[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-380 — Relay builder shell fails inspecting the host Google CLI store

| Coordination | Value |
|---|---|
| State | confirmed on Excellence; open |
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
PDF/base64 INPUT is unrelated to this admission failure. The active release is
`agents-6ca99b48-20261003145227` (builder `6ca99b48b`, mcpagent `e9af395a6`,
provider `e38d33f25`). No service, permissions, credentials or workspace files
were changed during inspection.

## Source / remaining

`workspace/security/isolator.go` appends `gogconfig.TerminalHome` to read/write
paths whenever the execution is not strict. `workspace/gogconfig/config.go`
resolves that path from the service's HOME/config and enables the grant unless
explicitly disabled. This same automatic shared-store grant remains on main
at `46b8cd40e`; the two PLAT-378 UI fixes do not repair it.

Correct the Google CLI grant/environment policy for per-user sandbox execution
without making the service account's shared credential directory readable by
user slots. Retain per-connection Google access through the existing authorized
Google tool. Verify a Relay builder can run `pwd`, create its plan and use only
Google connections it is allowed to use. The Downloads boundary fix (PLAT-373)
is a different failure and does not explain this error.

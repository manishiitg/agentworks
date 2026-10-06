[← sandbox / paths](index.md)

# PLAT-376 — Admin-managed provider terminal failed with "enter working directory: chdir"

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | sandbox |
| Area | paths |
| Summary | **fixed** on `main`, RTS deploy pending: a `global:` provider binding was confined like a personal account and started with no working folder. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; RTS deploy pending |
| Severity | P1 (admins could not inspect or check the server's Cursor account) |
| Date | 2026-10-03 |
| Owner | security-sandbox |
| Related | personal-account terminal confinement (`provider_setup_confine.go`, `docs/DECISIONS.md` 2026-10-01) |

## Problem

On RTS, Providers → Cursor → Admin-managed account → open the terminal printed
`SANDBOX_UNAVAILABLE: enter working directory: chdir : no such file or directory`.
The RTS log shows `cursor-cli inspect for server account (HOME service HOME)`.

Only personal accounts are meant to be confined to their private home; the
server's own account (admin-managed) keeps its service home. The start code
decided "personal" with `bindingID != provider`, but the admin-managed account's
binding is `global:cursor-cli`, so it counted as personal and was started under
the Landlock launcher with no working folder.

## Fix

`providerSetupIsPersonalBinding` treats a `global:` binding as the server
account, so it is not confined. Personal accounts are unchanged.
Test: `TestProviderSetupConfinesOnlyPersonalAccounts`.

## Register notes

[PLAT-376](plat-376.md), P1, **fixed** on `main`,
RTS deploy pending: a `global:` provider binding was confined like a personal
account and started with no working folder.

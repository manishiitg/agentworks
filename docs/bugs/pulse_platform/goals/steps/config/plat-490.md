[← goals / steps / config](index.md)

# PLAT-490 — A selected secret with no value stopped the whole run

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | steps/config |
| Summary | fixed on main: a selected secret with no value no longer refuses a run; the workflow shows a banner naming it. |

| Coordination | Value |
|---|---|
| State | fixed on main; needs a restart |
| Date | 2026-10-05 |
| Owner | step-execution |

## Source

Both LinkedIn schedules failed on 2026-10-04/05 in seconds with `Secret "IMGBB_API_KEY" does not exist` (403): the
workflow selected two secrets that no longer exist anywhere, and the admission check refused the whole run.

## Done

- A selected secret that has no stored value no longer refuses the run (`validateVaultSecretSelection`). The run
  starts; `$SECRET_<NAME>` is empty. A secret that exists but the person's groups may not use is still refused.
- The workflow manifest response carries `missing_secrets`, and the workflow shows an amber banner above the chat
  naming them ("Add the value in Integrations → Connections → Secrets, or remove the selection").
- The two stale selections on the LinkedIn workflow were removed (the current plan used neither).

## Left

- The banner has no button; it only names the secrets and where to add them.

## Register notes

[PLAT-490](plat-490.md), fixed on main: a selected secret with no value no longer refuses a run;
the workflow shows a banner naming it.

[← coding-agents / accounts](index.md)

# PLAT-713: Private CLI sign-ins leak into the shared server account

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | coding-agents |
| Area | accounts |
| Summary | A person's private CLI sign-in (Cursor 2026-10-05, Codex before 2026-10-06) landed in the service HOME and became the shared server account everyone used |

## What happened

## Fix

## Left

## Seen (Excellence)

- Codex: a personal login sat in `/srv/agents/home/.codex/auth.json` and was linked into ~10 sandboxes; removed by the 2026-10-06 repair (`state/repairs/ankita-codex-20261006T125437Z`). The shared account then had no login until the owner signed it in on 2026-10-08.
- Cursor: 2026-10-05 12:37:17 CEST a member added a private Cursor account (`6dd2cae1…`, HOME under `provider-connections/<id>/home`) and started its sign-in; at 12:38:39 `/srv/agents/home/.config/cursor/auth.json` (the service HOME) was written and the private home has no auth.json. With no "Who can use it" setting the shared Cursor account defaults to everyone, so all members ran on that login (likely also the `cursor-cli [quota_exhausted]` failures of 2026-10-07).

## Done (2026-10-08, owner request)

Quarantined the Cursor file and its 3 sandbox links to `state/repairs/cursor-shared-login-20261008T065149Z`; set `available_to.cursor-cli = admins` in provider-account-settings.json (applies at the next restart or admin save). The shared Cursor account is signed out until the owner signs in a company account.

## Left

- Find where cursor-agent writes its login (it seems to ignore the per-account HOME/XDG we pass, e.g. resolving the home from the passwd entry) and pin it to the account's own folder; check Claude, Muse, Agy and Pi the same way.
- Refuse/flag a shared-account login file that appears without an admin sign-in of that shared account.

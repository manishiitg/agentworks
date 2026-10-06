[← sandbox / paths](index.md)

# PLAT-457 — A symlink in a user's own tree into `config/` or `Workflow/<id>` is followed by the workspace service

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | sandbox |
| Area | paths |
| Summary | open. |

| Coordination | Value |
|---|---|
| State | open (found by reading and a throwaway test while closing the same hole for `Crew/<id>`, PLAT-442 step 4); `Crew/<id>` is closed, `Workflow/<id>` and `config/` are not |
| Priority | P1 |
| Date | 2026-10-04 |
| Owner | security-sandbox |

## Finding

`utils.IsValidFilePath` (workspace module) refuses a path whose symlinks carry it from one `_users/<id>/` tree into another. A link that resolves into a
shared top-level folder is allowed. The proxy's per-folder gates (`Workflow/<id>` read/write by access record, `config/` and `_system/` admin-only)
classify the NAME of the path, which for a link in the caller's own tree is the caller's own `Chats/...`. A user who can create a symlink in their own tree
can name `Chats/<link>` and the workspace service (the app account) reads or writes the target.

Throwaway test on the unchanged code (`workspace/utils`, deleted after the run): with `_users/alice/Chats/wf-link.md -> <docs>/Workflow/shared/readme.md` and
`_users/alice/Chats/cfg-link -> <docs>/config`, `ResolveUserPath(docs, "Chats/wf-link.md", "alice")` and `ResolveUserPath(docs, "Chats/cfg-link/users.json",
"alice")` both resolve without error. Who can create such a link: any account that can run a shell in its own tree (a Code's CLI as the person's slot, a
workspace shell tool). Not reproduced end to end through the proxy against a private Goal.

## Done

For the shared Crew root only (PLAT-442 step 4): a link whose target is inside a `Crew/<id>` tree must have been made inside that same tree, or the path is
refused; a link to the bare `Crew/` root is refused (`sharedCrewTree` in `workspace/utils/path.go`, `TestIsValidFilePathSharedCrewTreesAreClosedToLinksFromElsewhere`).
Without it, moving Crews out of `_users/<owner>/` (which that rule protects) would have opened every Crew to anyone who can make a link.

## Left

- The same rule for `Workflow/<id>` (a link made anywhere else must not read or write a workflow it was not made in) and a flat refusal of links into `config/`
  and `_system/`. Needs the workflow tree rule to allow the legitimate links workflows make today (inside the workflow, or into `skills/`); check them first.
- A regression test through the proxy with a private Goal and a link in another user's tree.

## Register notes

[PLAT-457](plat-457.md), P1, open. The workspace service's link guard only closes links into other users' trees; a link into
`config/` or `Workflow/<id>` resolves and bypasses the proxy's path gates. `Crew/<id>` is closed (PLAT-442 step 4), the others are not.

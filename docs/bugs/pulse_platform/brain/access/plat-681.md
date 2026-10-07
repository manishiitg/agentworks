[← brain / access](index.md)

# PLAT-681: Brain: say why a project cannot use a folder

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | brain |
| Area | access |
| Summary | A project blocked from a folder by its output readers got NOT_FOUND, so agents took real folders for empty shells |

## What happened

## Fix

## Left

## What happened

RTS, 2026-10-07: the rtslatency workflow (readers yoav, laxmi) got NOT_FOUND on RTS/Engineering and RTS/Company even by folder ID; only RTS/Latency worked. Brain limits a project to folders every non-admin person who sees its output can read; the two readers had Reader on RTS/Latency (from the import) and nothing on the new folders. The agent concluded the folders were "not fully wired" and proposed retargeting the steps.

## Fix

When the project's own principal could use the folder, the error now names the folder, the people who cannot read it, and the two ways out (give them Reader, or remove them from the project). Read-only projects asked to write get "This project has Read-only Brain access". Otherwise the uniform NOT_FOUND stays.

## Done on RTS

The owner granted every RTS user Reader on the whole Brain (root folder), 2026-10-07.

## Open decision

Whether a project's Brain access should be user-level only (drop the output-reader limit); asked 2026-10-07.

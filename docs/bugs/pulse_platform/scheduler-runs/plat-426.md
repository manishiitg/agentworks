[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-426 — Build a release once and deploy it everywhere (deploys take 5–7 minutes per product because each server builds the same source)

| Coordination | Value |
|---|---|
| State | open (planned; owner asked for it 2026-10-04) |
| Severity | P3 (speed and consistency: the same commit is compiled up to six times, and each build can differ) |
| Date | 2026-10-04 |
| Owner | scheduler-runs (deploy tooling; see PLAT-405) |
| Related | PLAT-405 (deploy behaviour and unification) |

## Problem

Every deploy clones the three repositories on the target server and builds there (Go with cgo for native STT, the AgentWorks CLI downloads, the frontend). Measured on 2026-10-04:
Excellence 4 min 57 s, 4 min 57 s, 5 min 0 s, 5 min 5 s; RTS 7 min 3 s and 6 min 54 s (RTS has 2 cores, the Hetzner box 16).
Excellence, Confida, SparkQuill and Dominion share one Hetzner box, so a release of all four builds the same source four times. The server also needs the full build toolchain and network access to clone and install.

## Facts that make one build possible

Hetzner box and RTS are both x86_64, Ubuntu 24.04, glibc 2.39. A binary built on one runs on the other.

## Plan

1. `deploy/build-release.sh <commit>`: build once on the Hetzner box (16 cores) from the three repositories at their pinned revisions into `/srv/_builds/<sha>/` (binaries, slot tools, launcher, frontend dist, CLI downloads, catalogs), with a manifest: revisions, arch, glibc, build time, file hashes.
2. The existing activation steps (drain, env and units from the runtime profile, restart, verify, drift report) take a prebuilt build instead of building: `build-and-activate.sh --prebuilt /srv/_builds/<sha>` for the products on the Hetzner box (copy, no compile).
3. RTS: `deploy.sh rts` ships the same build as a tarball (checked against the manifest hash, arch and glibc), then runs its activation steps.
4. Refuse a build whose manifest does not match the target (arch, glibc, missing file); keep the previous release for rollback as today.
5. Optional later: build in CI per commit so a deploy never waits for a build.

## Acceptance

One build per commit; `./deploy.sh excellence` with a prebuilt release finishes in about a minute; all four Hetzner products and RTS run byte-identical binaries for a commit (checked by the manifest hashes in the drift report).

## Done

Nothing yet.

# PLAT-469 — The early selected-secret check refuses chats that select a project secret (Relay 403 after the first Vault install)

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | sandbox |
| Area | secrets |
| Summary | open, P1, owned by the Vault maintainer; GitHub issue #266. |

Status: open, P1; owner: the Vault maintainer (separate laptop). GitHub issue: https://github.com/manishiitg/agentworks/issues/266 (full write-up, reproduction, audit list and tests). Found 2026-10-04 on a multi-user deployment right after its first Vault install.

## Finding

`validateVaultSecretSelection` at `agent_go/cmd/server/server.go` ~3968 runs in `handleQuery` before the workflow's project secrets are loaded (`loadSelectedSecrets` at ~4464 and ~4604). A project secret named in `SelectedGlobalSecrets` is therefore treated as a missing shared secret and the chat is refused with 403 "A selected secret is unavailable or your group no longer has access". Reproduced from the log and the Relay's manifest on a deployment with no shared secrets; not reproduced locally. The other call sites listed in the issue may share the pattern (not verified).

## Left

Fix and tests as in the issue; the owner's related Vault decisions (Platform group auto-grants every shared secret and platform MCP, revoke persists, no-Vault servers behave as before, a missing secret is named) are in the issue and not built. Deploy impact: Confida and RTS stay undeployed with the new Vault code until this is fixed.

## Register notes

[PLAT-469](plat-469.md), open, P1, owned by the Vault maintainer; GitHub issue #266. `validateVaultSecretSelection` runs before the project secrets are loaded, so a project secret is treated as a missing shared secret (403). Blocks the Vault rollout to Confida and RTS.

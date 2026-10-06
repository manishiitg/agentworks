[← brain / learnings-knowledge](index.md)

# PLAT-310 — Attach multiple workflow knowledge bases with read-only shell access

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | brain |
| Area | learnings-knowledge |
| Summary | Consumers declare `knowledgebase_sources` by stable workflow ID, unique alias and read access. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Implemented and deployed to RTS; verification scope below |
| Last synchronized | 2026-09-12 |
| Implementation commits | 4d7725c22, 4d92b5abe, 78f48f76c |

## Delivered behavior and verification

Consumers declare `knowledgebase_sources` by stable workflow ID, unique alias and read access. Multiple same-host sources are supported. Agents read canonical source KB files through their ordinary shell/file tools without copying the knowledge. Attachments are non-transitive and do not expose the source workflow's unrelated files.

Source resolution rejects inaccessible workflows, self-references, duplicate aliases/source IDs and unsupported access. Eligible builders, execution agents and reviewers receive read-only source knowledge; source files remain managed by their owning workflow.

Users can inspect/add/remove attachments in Setup → attached folders, with source visibility in the folder and KB views. Builder configuration tools support the same manifest field. The implementation and UI regression tests are included in main and the RTS release. A fresh cross-workflow shell read/write-denial acceptance run was not performed during this deployment verification. See [shared KB contract](../../../../workflow/shared_knowledgebase_sources.md).

## Deployment receipt

Included in RTS app release `bb7ac6d17ec43750e74a5c92d73ef067f66c69bf`
(`bb7ac6d-20260912135437`), with provider `570ede69fb85beef251ddca9792a2e5ad0dfe95d`.
All three services were active and the public health endpoint was healthy after deployment.
Deployment health is distinct from the feature-specific acceptance scope above.

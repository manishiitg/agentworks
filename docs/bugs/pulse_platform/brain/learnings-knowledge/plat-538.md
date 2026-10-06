# PLAT-538: move RTS knowledge to Brain, then retire per-workflow KB sharing

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | brain |
| Area | learnings-knowledge |
| Summary | open: Brain is on main (PR 268), not deployed; migrate RTS knowledge in dependency order before the old `knowledgebase_sources` path is removed. |

**State:** open. P2. PR 268 (Brain, product id `knowledgebase`) is on main (`8221d81e4`), not deployed. The old sharing path (`knowledgebase_sources` on a workflow manifest) still exists.

**Why a ticket:** deploying Brain migrates nothing. Retiring the old path before RTS is migrated would silently remove knowledge from running workflows.

**RTS today (2026-10-05, read through the MCP):** each workflow has its own `knowledgebase/` folder; attachments, all read-only: rtsaws reads rtslatency; automationtesting reads rtslatency and rtsaws; rtsprreviweer reads rtslatency, automationtesting and rtssprinttracking. rtslatency and rtssprinttracking read none. Backups already cover `knowledgebase` (rtslatency to GitHub, automationtesting to S3).

**Order:** deploy with the product on for the owner only; migrate (preview, import, rebind consumers, cutover; pause writers and schedules first) rtslatency and rtssprinttracking, then rtsaws, then automationtesting, then rtsprreviweer; check that no manifest lists `knowledgebase_sources`; only then remove the old path.

**Removal change must:** log or refuse loudly when a workflow still has `knowledgebase_sources`; never drop it silently. Scan Excellence, Confida and Dominion for `knowledgebase_sources` before the removal.

**Known follow-ups from the PR 268 review (not blockers):** default MCP consent includes Brain write (role-bounded); the backup token is stored with the host key, not in Vault; a Builder chat getting the Brain tools has no test.

**Decision 2026-10-06 (owner, option B):** shared and org knowledge (person, company, projects, positioning patterns, org-wide decisions) goes to shared Brain folders; facts used only by one workflow stay in its local `knowledgebase/`. Such projects are not cut over (cutover makes a project shared-only and denies the local folder): import the shared notes, set `brain_access=read`, remove the local copies after a checked import, keep the rest local. A full cutover stays possible per project once delivery from Brain is guaranteed. Design: `docs/design/workflow_knowledge_layers.md`, PLAT-556.

**Left:** everything above.

**Pilot, 2026-10-05 (rtslatency):** Brain folders `RTS` and `RTS/Latency` created; grants set (admin Owner, yoav and laxmi Reader, the workflow's owner and readers). `migration_preview` then `migration_import` ran through the MCP: 6 files imported (`context/context.md` and five notes, all read back through Brain), `notes/_index.json` skipped (unsupported), source files untouched. **Not done:** cutover. Three workflows still read this one through the old path and must be rebound first: `automationtesting` (alias `rtslatency`), `rtsaws` (`rtslatency`), `rtsprreviweer` (`latency`); the schedules must be paused around cutover. Next: do the same for rtssprinttracking, then rtsaws, automationtesting, rtsprreviweer.

**Fixed 2026-10-06 (found doing the pilot):** the cutover compared the whole manifest with the preview, but requires schedules to be paused first, and pausing them edits the manifest, so the documented order could never succeed. It now compares only the knowledge configuration recorded at preview and saves against the current version (a concurrent edit is still refused); the migration test pins it. Also: a migrated project may now be set to Read & write (open), and in the open modes a binding alias is a shortcut to that folder instead of being ignored.

**Upwork, option B (2026-10-06): prepared, not run.** No sanctioned path to the owner's local Brain was available: the local backend (127.0.0.1:18743) requires auth, the `agentworks` CLI is connected to another server, and no token was extracted. `brain_access` is server-managed (`workflow_manifest.go` keeps the prior value on manifest writes), so it was not set by file edit. Also, `migration_preview` imports the whole local `knowledgebase/` into one empty folder; it cannot take a subset, so option B needs per-note writes, not the migration importer.
- Shared notes (local copies still in `Workflow/upwork/knowledgebase/notes/`, `graph.json` links already removed): `person-manish-prakash.md` → `Org/People`; `project-agentworks.md`, `project-conductor.md` → `Org/Projects`; `pattern-agent-orchestration.md`, `pattern-browser-automation.md`, `pattern-multi-model-orchestration.md`, `pattern-security-isolation.md`, `pattern-workflow-runtime.md` → `Org/Patterns`.
- Steps: (1) create the three folders and grant the owner Owner and the Upwork workflow Reader; (2) write each note through the Brain MCP `update_knowledgebase` note action and read it back; (3) set Upwork `brain_access=read` through the access builder; (4) repoint references to `brain:Org/<folder>/<note>`: the plan names `project-agentworks` 6 times and step config twice (no `person-*` or `pattern-*` names in the plan); notes `upwork-positioning.md`, `profile-change-log.md` and `learnings/_global/references/cover-letter-and-output-integrity.md` link `project-agentworks` / `person-manish-prakash`; `bid-pick-job` Inputs say "the relevant project/person notes"; (5) only after the read-back, move the local copies to `archive/` and drop them from `notes/_index.json`; (6) needs guaranteed delivery of `brain:` references (PLAT-556 change 1) before steps rely on them.

## Register notes

[PLAT-538](plat-538.md), P2, open: Brain is on main (PR 268), not deployed; migrate RTS knowledge in dependency order before the old `knowledgebase_sources` path is removed.

**2026-10-06: RTS follows option B, done for rtslatency.** Owner chose option B for RTS and that the structure is the agents' call (the workflow only has Brain on or off). The rtslatency Builder (asked through the MCP Builder chat) organized its shared knowledge in Brain under `RTS/Latency` by topic (architecture, infrastructure incl. log mapping, voice pipeline, engineering/sprint-release, platform context, readme index); `context.md` stays local; its old local notes are MOVED stubs (the Builder could not delete files); its weekly knowledge refresh and learnings now point at Brain. No cutover. The Builders of rtsaws, automationtesting and rtsprreviweer switched their prompts and learnings to Brain; the `rtslatency`/`latency` attachments were then removed through the MCP (bind with replace, Read & write kept, unbind), because Builder sessions started through the MCP are refused project setup. Left: automationtesting still attaches rtsaws, rtsprreviweer still attaches autotest and sprint (same treatment next); the rtsprreviweer Builder also realigned its Notion server id; the Builder could only create folders under `RTS/Latency` because the workflows' readers (yoav, laxmi) can only read there (giving the RTS team Reader on `RTS` is an owner decision); the first weekly refresh writing to Brain is the live test.

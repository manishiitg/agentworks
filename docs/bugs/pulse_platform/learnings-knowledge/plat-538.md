# PLAT-538: move RTS knowledge to Brain, then retire per-workflow KB sharing

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

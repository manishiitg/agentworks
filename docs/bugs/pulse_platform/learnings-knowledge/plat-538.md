# PLAT-538: move RTS knowledge to Brain, then retire per-workflow KB sharing

**State:** open. P2. PR 268 (Brain, product id `knowledgebase`) is on main (`8221d81e4`), not deployed. The old sharing path (`knowledgebase_sources` on a workflow manifest) still exists.

**Why a ticket:** deploying Brain migrates nothing. Retiring the old path before RTS is migrated would silently remove knowledge from running workflows.

**RTS today (2026-10-05, read through the MCP):** each workflow has its own `knowledgebase/` folder; attachments, all read-only: rtsaws reads rtslatency; automationtesting reads rtslatency and rtsaws; rtsprreviweer reads rtslatency, automationtesting and rtssprinttracking. rtslatency and rtssprinttracking read none. Backups already cover `knowledgebase` (rtslatency to GitHub, automationtesting to S3).

**Order:** deploy with the product on for the owner only; migrate (preview, import, rebind consumers, cutover; pause writers and schedules first) rtslatency and rtssprinttracking, then rtsaws, then automationtesting, then rtsprreviweer; check that no manifest lists `knowledgebase_sources`; only then remove the old path.

**Removal change must:** log or refuse loudly when a workflow still has `knowledgebase_sources`; never drop it silently. Scan Excellence, Confida and Dominion for `knowledgebase_sources` before the removal.

**Known follow-ups from the PR 268 review (not blockers):** default MCP consent includes Brain write (role-bounded); the backup token is stored with the host key, not in Vault; a Builder chat getting the Brain tools has no test.

**Left:** everything above.

**Pilot, 2026-10-05 (rtslatency):** Brain folders `RTS` and `RTS/Latency` created; grants set (admin Owner, yoav and laxmi Reader, the workflow's owner and readers). `migration_preview` then `migration_import` ran through the MCP: 6 files imported (`context/context.md` and five notes, all read back through Brain), `notes/_index.json` skipped (unsupported), source files untouched. **Not done:** cutover. Three workflows still read this one through the old path and must be rebound first: `automationtesting` (alias `rtslatency`), `rtsaws` (`rtslatency`), `rtsprreviweer` (`latency`); the schedules must be paused around cutover. Next: do the same for rtssprinttracking, then rtsaws, automationtesting, rtsprreviweer.

# PLAT-547: Brain refused every OAuth MCP connection as "expired or revoked"

**State:** fixed on main, not deployed. P1: it blocks the main way agents reach Brain.

**Found:** 2026-10-05, RTS, after reconnecting the MCP with the Brain scopes: the Brain tools were listed, but `browse_knowledgebase` returned `FORBIDDEN: This connection is expired or revoked.`

**Cause:** Brain checked a connection only in the local access-token store (`knowledgebaseExecute` and the principal's recheck). OAuth MCP connections (Claude Code and every standard client) live in the OAuth store under an `oauth-<family>` ID, so they never matched. PR 268's tests issued local tokens, so this never showed.

**Fix:** both checks use `activeExternalGrantClaims`, which verifies a local token or an OAuth grant in the right store, and the claims must belong to the same user (`knowledgebase_runtime.go`, `knowledgebase_routes.go`).

**Left:** deploy RTS and check it live (browse and read through the OAuth connection); there is no OAuth grant helper in the Go tests, so the live check is the proof.

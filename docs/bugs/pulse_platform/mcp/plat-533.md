# PLAT-533: the external MCP returns stored webhook secrets in workflow manifests

**State:** fixed on main (not deployed). P3.

**Found:** 2026-10-05, RTS, signed in through the MCP: `list_workflows` and `get_workflow` returned each webhook's `encrypted_secret` (ciphertext) inside the manifest. Owners and readers of the workflow could see it; it is encrypted with the host key and bound to the workflow and trigger, so it was not usable by a client, but only the server ever needs it.

**Fix:** `externalWorkflowView` (`agent_go/cmd/server/external_tools.go`) copies the manifest and clears `encrypted_secret` on every webhook before `list_workflows` and `get_workflow` return it. The server's own manifest is untouched. One test pins it.

**Left:** deploy. Other external tools that return manifests were checked by grep (`list_schedules` returns a reduced shape); the in-app routes are unchanged.

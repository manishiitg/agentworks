# PLAT-502: webhook/scheduled run folders are owner-only, so a code step running as a slot cannot write its outputs

**State:** fixed on main (not deployed, not yet verified with a live webhook run). P1 for slotted hosts (RTS and Confida show it; Excellence had no recent runs to compare).

**Found:** 2026-10-05, RTS, workflow `rtsprreviweer`, triggered by SDE Private calling the SDE Crew. The first step `pr-eligibility-gate` finishes its logic, then fails with `PermissionError: [Errno 13] Permission denied: '.../runs/iteration-744-hook/default/execution/pr-eligibility-gate/route_selection.json'` (iteration-743 the first time).

**Cause (from the host, not yet proven by a run):**
- Until 10-01 hook and scheduled run folders were `drwxrws---` (group `slotshared` read/write). Since 10-01/10-02 every new one is `drwx--S---` (group no access), and the folders the workspace service creates inside them are `drwxr-s---` (group read only). Confida shows the same since 10-02 (`cfshared`).
- `allocateImmutableRunFolder` (`agent_go/cmd/server/schedule_run_folder.go`) creates `runs/iteration-N-<kind>` with mode 0700 and has since 09-14. The older group access came from the one-time permission pass when slots were provisioned (10-01), not from the code. Folders created after it get only what the server and workspace service request (0700, and 0755 under their umask = 0750).
- A step that runs a script through the shell tool runs as the owner's slot account, a member of `slotshared`, so it can neither enter the run folder nor write in `execution/<step>/`.

**Fix options (none applied):**
1. Server: after allocating, set the run folder to `2770` (group `slotshared`/`cfshared` already inherited via setgid); and make the workspace service create folders under a run folder group-writable on slotted hosts (it uses literal 0755 under a 0027 umask, so a mode change alone is not enough).
2. Stopgap on a host: `chmod -R g+rwX` on a workflow's `runs/`. Helps existing folders only; every new webhook run is created owner-only again.

**Done (option 1):** `allocateImmutableRunFolder` sets the new run folder `2770` when slots are on; the workspace service's folder creation in `workspace/handlers/documents.go` asks for `0775` instead of `0755` (slots-on umask 007 gives `0770`; hosts without slots keep their modes). The workspace service's own umask is 007 with slots on (`workspace/slots/umask_linux.go`), which is why 0755 became 0750.

**Left:** deploy RTS and check with a real webhook run on RTS (new `iteration-N-hook` folder and its `default/execution/<step>` folders must show group rwx, and `pr-eligibility-gate` must write `route_selection.json`) (the `rtsprreviweer` hook is the repro); then Confida.

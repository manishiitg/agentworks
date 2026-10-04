# PLAT-443 — Script-tool result reader follows symlinks outside its output folder

Status: open. Reproduced in an isolated local worktree on main f38701175.
Priority: P1.
Related: PLAT-432, PLAT-441, PLAT-118.

## Finding

`step_based_workflow/scripted_route_tools.go:readScriptedRouteResult` uses
`os.Stat` and `os.ReadFile` directly in the agent server. Both follow symlinks.
A script can create `STEP_OUTPUT_DIR/route_result.json` as a link to another
server-readable JSON file. The server reads and returns that target even though
it is outside the script's assigned output directory. Child-process sandbox
restrictions do not constrain this parent-process read. This also affects named
script tools in ordinary workflows.

The comment claiming the path is the route's own output folder does not establish
where the file resolves. The stat-before-read size check also does not bound the
read if the file grows or changes between calls.

## Evidence

A temporary Go reproduction ran beside the existing step tests:

1. Write a fake `{"fixture_secret":"outside-output-dir"}` JSON file outside
   an assigned route-output directory.
2. Symlink `route-output/route_result.json` to that file.
3. Call `readScriptedRouteResult(route-output)`.
4. It returns the external JSON with no error.

No real credentials or production files were accessed. This reproduces the
unconfined server read, not a live production sandbox exploit. Existing focused
Relay/route/strict-script tests and server ingress tests pass separately.

## Needed

Reuse the platform's confined workspace-file reader, enforcing the route's
allowed output path, symlink confinement and a bounded read. Include a regression
for a leaf symlink and a symlinked ancestor, with a regular JSON file still
returned successfully. Do not deploy the affected tool path as ready until this
boundary is fixed and verified.

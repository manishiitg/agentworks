# PLAT-443 — Script-tool result reader follows symlinks outside its output folder

Status: fixed on `main`; not deployed. Reproduced in an isolated local worktree on main f38701175.
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

## Fix (2026-10-04)

`readScriptedRouteResult(confineRoot, outputDir)`: resolves the workflow folder and
the output folder with `EvalSymlinks` and refuses an output folder that resolves
outside the workflow (a symlinked ancestor); `Lstat`s the file and accepts only a
regular file (a leaf symlink, even to another file inside the workflow, a
directory or any special file is refused); opens it and requires `os.SameFile`
with the checked file; reads through a `LimitReader` of the cap plus one byte, so
growth between check and read cannot exceed the bound. An unusable file is logged
and the run summary is kept, as before. Tests: regular file returned, leaf
symlink outside, link to another workflow file, symlinked ancestor outside, a
directory named route_result.json.

Not done: this is a purpose-built reader, not a shared confined workspace-file
reader (none exists in the codebase); consolidating the several `EvalSymlinks`
checks into one helper is a separate cleanup.

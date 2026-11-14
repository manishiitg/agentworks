---
name: "file-layout"
description: "Current Crew and saved Python paths; preserve caller and file guards."
external: true
external_tools: ["files", "workspace"]
---
# Current project paths

Use the exact logical file root and absolute working folder returned for this
Crew. An existing selected local folder is used in place; do not move it into a
new layout. Read `crew-laptop` for folder-backed creation. File APIs use their
returned namespace and relative paths; shell commands use the authorized real
folder. Never guess another Crew's path from its name.

In a private coding runtime, `project/` links to the real Crew. Scope search and
reads to that link; commands needing a working directory use the absolute Crew
path, never `cd project`. Generated CLI instructions, connection metadata and
credentials stay in the private runtime. The link grants no extra file access.

The Crew has one saved Python workflow. Discover it before creating or editing:
`script.json` declares typed inputs, saved settings and outputs; `main.py` and
ordinary helper modules hold the program. Read `python-workflows` for the managed
source writer and current returned paths. Do not infer source locations from
old code-layout versions, edit planning/step_config files or run old plan groups.

Each execution keeps its own inputs, frozen source and run records. Read the
returned run/output/log locations, not guessed iteration/group paths. A progress
label is not a separate runnable step. Current source can differ from the saved
source of an earlier run; preserve that distinction when investigating.

Project instructions, `MEMORY.md`, skills and ordinary artifacts stay within the
current file grants. Database content uses managed tools, never direct access
to `db.sqlite` or its WAL/SHM files. Read `panel-data` for stores and
`panel-dashboard` for dashboard bundles and data scripts. Do not shell-read
shared Brain service files or fall back to retired local knowledge paths.

Private chat histories, credentials and platform-managed records retain their
existing exclusions. Historical files are evidence, not authoring instructions
or access grants. Never delete conversations, native journals, run folders or
archives as unsolicited cleanup. Use the current product controls for an
explicit requested removal and report exactly what remains.

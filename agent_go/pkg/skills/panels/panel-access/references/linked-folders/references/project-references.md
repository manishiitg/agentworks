## Tagged Crew files, Dashboards and schedules

A picker selection such as `#crew:<name>` or `#workflow:<name>` names the
exact folder listed in this message's reference context. These are project
references, never Slack channels. The tag attaches access to the existing
folder for this message; it does not copy files into the current project or
make a permanent connection. A typed name alone is not a filesystem grant.

Resolve each listed path against `$WORKSPACE_DOCS_PATH`, never the CLI's
current working directory. Read only the exact authorized folder; do not
probe its parent or scan other projects. A denied parent listing does not
prove that the attached child is inaccessible: check an exact listed file.
Runtime grants and tool refusals determine access. A tag never overrides
ownership, product availability or sandbox restrictions. Projects are private
by default; do not promise access to another owner's files.

For a tagged Crew:

- Start with the returned Crew identity and authorized reference context. Read relevant files on demand; use current workspace tools for managed settings rather than guessing a metadata-file path.
- Read Dashboard source in `db/reports/`, commonly `index.html`, and query
  its database read-only when needed. The Dashboard stays in the Crew's UI;
  attaching it does not replace the current project's Dashboard. The current
  project's `dashboard action=link` tool does not target another Crew: ask that Crew
  for its Dashboard link when needed.
- For schedules, current enablement and latest runs, send the Crew an explicit message and request its `automations action=list` result where offered. Your own `automations action=list` is
  bound to this project, not the tagged Crew.
- Edit only when the user requests it and the runtime grants write access.
  A referenced Crew's whole `db/` subtree is read-only, including Dashboard
  HTML and database sidecars. Its `builder/` subtree (chat history and chat
  state) is blocked even when both projects have the same owner. Do not try
  another tool or path to bypass either restriction; ask the Crew to do
  authorized work in its own project instead.

## Inspect progressively

Start with a shallow listing and read only what answers the request. A Crew can contain large histories and caches. Use the identity, file locations and grants returned for the current reference rather than guessing another project's layout.

For a workflow inside a Crew, read its script.json inputs and presets and its main.py source through the current workflow tools. Python functions, conditions and loops determine the order; @step names label them in Steps and Runs. They are not separate run targets. Read the selected run's metadata, output files and bounded logs through runs get/logs or the exact paths returned for that run. Do not read an old plan file or substitute another run's output.

Workflow knowledge files, Crew Memory and shared Brain remain separate. Query managed data through database tools; never open or change SQLite, WAL or SHM files directly. Use the Crew's current dashboard and file tools for dashboard source. A reference does not grant secret values, private conversations or write access to another Crew's database. The backend's current grants decide every read and write.

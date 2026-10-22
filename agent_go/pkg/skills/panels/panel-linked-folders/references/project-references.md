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

- Start with `product.json` and `workflow.json` for identity, capabilities,
  schedule definitions and durable references. Then read relevant `code/`
  or other files on demand.
- Read Dashboard source in `db/reports/`, commonly `index.html`, and query
  its database read-only when needed. The Dashboard stays in the Crew's UI;
  attaching it does not replace the current project's Dashboard. The current
  project's `get_report_link` tool does not target another Crew: ask that Crew
  for its Dashboard link when needed.
- Schedule definitions live in `workflow.json`. For current enablement, next
  run or latest run status, send the Crew an explicit message and request its
  `list_project_schedules` result. Your own `list_project_schedules` tool is
  bound to this project, not the tagged Crew.
- Edit only when the user requests it and the runtime grants write access.
  A referenced Crew's whole `db/` subtree is read-only, including Dashboard
  HTML and database sidecars. Its `builder/` subtree (chat history and chat
  state) is blocked even when both projects have the same owner. Do not try
  another tool or path to bypass either restriction; ask the Crew to do
  authorized work in its own project instead.

## Inspect progressively

Start with a shallow listing and read only what answers the request. Workflow
folders can contain large run histories and caches.

| Path | Meaning |
| --- | --- |
| `workflow.json` | Authoritative workflow identity, version, capabilities, schedules, and durable selections. |
| `instructions.md` | Human-authored operating instructions, when present. |
| `code/` | Current step implementations and reusable workflow code. |
| `knowledgebase/` | Curated source context, notes, and indexes. |
| `learnings/` | Accumulated global or step-specific lessons. |
| `db/db.sqlite` | Managed workflow data. Query read-only; never edit SQLite, WAL, or SHM files. |
| `db/reports/` | Dashboard source, commonly `index.html`, plus Dashboard assets. |
| `runs/run_index.json` and `runs/iteration-*` | Run metadata, logs, and per-step execution outputs. Select the relevant/latest run instead of crawling all runs. |
| `reports/` | Published or user-scoped Dashboard artifacts, when present. |

For workflow references, folders such as `builder/`, `planning/`, `config/`, `costs/`, `evaluation/`,
`pulse/`, `scores/`, `soul/`, `backup/`, `versions/`, hidden provider folders,
and `tool_output_folder/` are platform/runtime state. Inspect them only when the
user's question specifically requires diagnostics, history, or architecture
and the runtime permits it. A referenced Crew's `builder/` stays blocked.

For an attached workflow database, use a read-only shell query such as
`sqlite3 -readonly "$root/db/db.sqlite" ...`. The project project's
`query_workflow_db` tool targets the current project project, not an attached
workflow. Treat files as evidence that may change while the source workflow is
running, and state when a conclusion depends on a particular run or snapshot.

Do not search files for credentials. Workflow secrets remain behind the
Secrets system and are not granted by read-only folder access.

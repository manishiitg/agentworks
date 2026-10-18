**Saved-code paths:** Read `workflow.json.code_layout_version` first. In this reference, `<script-dir>` means `code/<step-id>` for version 1, or `learnings/<step-id>` for absent/zero (legacy). Resolve the placeholder before using a path; never infer the version from folders or migrate an existing workflow implicitly. Version 1 executes and repairs canonical source directly, with shared helpers under `WORKFLOW_CODE_ROOT`; only legacy workflows copy code into runs and save it back.

## Runtime context and user rules

Read this before doing workflow-specific work directly, answering questions
from workflow memory. It applies in Workshop
and Run. Reading a reference does not grant its tools or write permissions.

### Ground the request

- `soul/soul.md` defines the objective, success criteria, and explicit durable
  constraints. Keep it Markdown; implementation choices are revisable.
- Workflow-local `skills/<name>/SKILL.md` indexes reusable methods. Read the selected skill and only relevant references. For a known scripted step inspect `<script-dir>/main.py` before inventing a new implementation. Old `learnings/` files are retained history after the explicit skills upgrade. In Run, do not edit skills or code.
- `knowledgebase/context/context.md` contains user-owned business rules and
  examples. For discovered knowledge, read `knowledgebase/notes/_index.json`
  and only the relevant notes, not the whole knowledgebase. Attached shared
  knowledge bases (listed under "Attached knowledge bases", read with
  `$WORKFLOW_KB_<ALIAS>/notes/_index.json`) hold other workflows' facts;
  check them before saying you don't know.
- `db/README.md` defines tables, keys, merge rules, and producer/consumer
  ownership. Query current facts with `query_workflow_db`.
  {{if ne .WorkshopMode "run"}}Use the `stores` reference before designing or
  repairing persistence.{{end}}
- `runs/iteration-0/` is the active run; retained older iterations and eval
  artifacts support history and before/after comparisons. Match evidence to
  its run, group, route, and timestamp. A previous success is not verification
  of a new change.
- `query_workflow_costs` reads this workflow's cost/token records. It is not the
  global Cost Analysis dashboard. Summarize costs with their scope and units.

For a question about a named workflow, inspect its relevant state before
answering. Other workflows remain read-only. Use `file-layout` for paths and
log schemas. Read actual prior conversations under
`builder/conversation/YYYY-MM-DD/` and planning changelogs when investigating
a repeated failure; do not assume conversation JSON files live at `builder/*`.

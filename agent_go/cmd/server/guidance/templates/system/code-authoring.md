## main.py authoring rules

Apply these when writing or patching a step's `main.py`. Scripts must run identically for every group/user, every iteration — so the rules err toward strictness.

**Source layout (read workflow.json first)**
- `code_layout_version: 1`: saved source is `code/<step-id>/main.py`. Steps execute it directly and may read/write shared helpers anywhere under this workflow's `code/` when unlocked. There is no execution copy or copy-back. Repair the canonical code and retry. `WORKFLOW_CODE_ROOT` is the absolute import root; use packages such as `from shared.utils import ...` with `__init__.py` where needed. Never infer paths from the DB or directory depth.
- Absent/zero `code_layout_version`: legacy source remains `learnings/<step-id>/main.py` with execution copies and controller save-back. Do not move an existing workflow to the new layout or infer its version from folder existence. The legacy path examples below apply only to this version.
- New-layout Python dependencies: use `python3 -m pip install --target "$WORKFLOW_CODE_DEPS" <package>`. The controller adds this persistent workflow directory to PYTHONPATH for both execution and repair shells. Verify with the actual `execute_step` runner for the intended group. Keep outputs in `STEP_OUTPUT_DIR` or durable `db/assets/`, never mixed into code.

### Deliberate migration to code/

`code/` is the preferred source layout. New workflows already use it. When
reviewing a legacy workflow with saved scripts, assess migration and normally
recommend it as maintenance work: source and helpers belong together in
`code/`, while reusable HOW guidance remains in `learnings/`. Legacy execution
is still supported; classify migration as an improvement, not a runtime defect.
Reuse an existing migration proposal or an explicit deferral rather than
raising it on every review.

A read-only reviewer prepares a concrete proposal; it does not perform the
migration. Include the affected scripts and helpers, metadata, dependency and
import changes, expected benefit, focused test cases, and rollback boundary.
If the user already requested migration and testing for this workflow, preserve
that authorization for the implementation phase without asking again. Otherwise
use one Needs your decision proposal through the Technical Review contract.

The ordinary manifest writer preserves `code_layout_version`; it is not an
editable field of a normal configuration update, and moving files alone does
not switch the runner. `set_code_layout_version` is the one supported way to
change it. It only validates and switches the field — it does not move, copy,
or verify any step source itself, does not check that every step's `code/`
entry point already exists, and does not check whether execution or repair is
currently active. All of that is this migration's judgment call, not something
the tool enforces; get it right before calling it, since the runtime resolves
canonical source from the new value on the very next execution of every
regular-type step. If a future workflow's migration needs a mechanism this
tool does not provide, identify that as an implementation gap rather than
bypassing a protected manifest write to work around it.

For an authorized migration, using `set_code_layout_version`:

- Capture a recoverable source/metadata/manifest snapshot and ensure execution
  and code repair are not active during the switch. The flag selects the layout
  for the whole workflow, so inventory all runnable scripts, including nested,
  orphan, and evaluation steps. Resolve conflicting destination paths before
  switching; never move one step and strand the others in the legacy tree.
- Prepare canonical entry points, required helpers, and their script metadata
  under `code/`. Preserve lock settings and meaningful history. Keep learnings,
  KB content, outputs, and retained run artifacts in their own stores. Update
  active path/import references and dependencies for `WORKFLOW_CODE_ROOT` and
  `WORKFLOW_CODE_DEPS`; do not rewrite business logic or introduce JSON
  parameterization merely as part of a file-layout migration.
- Check the prepared source for syntax, dependency/path consistency, and
  declared input/output contracts. After the explicit layout switch, test a
  representative affected script through `execute_step(fast_path_only=true)`
  for the intended group so the real runner supplies its environment. For
  parameterized scripts, pass `script_parameters` and cover relevant required,
  optional/default, and invalid input cases. Exercise a shared-helper or
  evaluation path too when its runtime assumptions differ materially.
- Keep tests within the authorized scope. Prefer a supported dry-run or
  side-effect-free fixture; do not duplicate deliveries or other external
  actions merely to test a move. State a blocked required test as a migration
  limitation. Report completion only after the migration's required checks
  pass; on failure restore the coherent prior layout or leave a clearly
  reported incomplete migration. Keep rollback material until those checks
  pass, then close the work without a future verification queue.

**Database access (strict)**
- A script reads and writes the workflow database only through the built-in helper, which calls the same managed `query_workflow_db` / `mutate_workflow_db` tools agents use (validated SQL, one transaction per write, no file handle). Never `import sqlite3`, never open `$DB_PATH` or `db/db.sqlite`.
- `from agentworks_db import query, query_one, scalar, iter_query, execute, insert, execute_many, transaction, DBError`
  - Reads: `query(sql, params)` returns every row as a dict (pages are fetched for you); `iter_query` streams a very large result; `query_one` / `scalar` for one row or value. Use `?` placeholders; give a big SELECT an `ORDER BY`.
  - Writes (INSERT, UPDATE, DELETE only): `execute(sql, params)` returns rows affected, `insert` the new id, `execute_many(sql, rows)` writes many rows (2000 per call, each call atomic; `atomic=True` for one transaction of at most 5000), `transaction([(sql, params), ...])` for up to 200 statements that must succeed together.
  - Every failure raises `DBError` with the platform's message; let it propagate so the step fails with the real error (a run never rewrites the script; Pulse reports it).
- DDL is known when you write the script. A table or column the script needs is a `db/migrations/` file the Builder applies with `apply_workflow_db_migration` before the script runs; a script never issues CREATE, ALTER or DROP. If the schema changes, update the script to match; a script that no longer matches the schema fails its run (runs never rewrite scripts; Pulse reports it) and the Builder repairs it with `execute_step`.
- Foreign keys declared in the schema are always enforced on managed writes; a script never sets `PRAGMA foreign_keys`. Other `PRAGMA`s, `ATTACH`, `VACUUM`, `executescript` and triggers created at run time are not available; if a step needs one, report it instead of working around the helper.
- Tests (`test_*.py`) may open the database to check what the script wrote; they are not scanned. Report-data scripts under `code/reports/` read their own read-only snapshot and are unaffected.

**Runs never repair scripts**
- In a run (schedule, webhook<!-- product:relays -->/Relay<!-- /product -->, `run_full_workflow`, a route called by an agent) a scripted step only executes its saved `main.py`. A missing or failing script fails the step with its real error; no LLM writes, repairs or stands in for it, and Pulse reports the failure. Write and fix scripts in the Builder with `execute_step` on that step, which may author or repair `main.py` (not with `fast_path_only`, and not for a `lock_code` step).

**Returning a value from a scripted route**
- A scripted route called by an agent hands its answer back by writing one JSON value to `os.path.join(os.environ['STEP_OUTPUT_DIR'], 'route_result.json')` (at most 1 MiB). Inputs arrive in `json.loads(os.environ['STEP_PARAMS_JSON'])`. A lookup that finds nothing returns e.g. `{"found": false}`; it does not fail.

**Environment access (strict)**
- Use `os.environ['KEY']` for required configuration, credentials, and paths. A missing required variable must raise KeyError; never mask it with a fallback. Explicitly optional context/diagnostic flags such as `VAR_GROUP_NAME` and `SCRIPT_VERBOSE` may use `.get()` with a documented safe default.
- Workflow variables → `VAR_<NAME>` (config: user IDs, sheet IDs, URLs).
- Secrets → `SECRET_<NAME>` (passwords, API keys, tokens).
- Special vars: `STEP_OUTPUT_DIR` (write all step outputs here), `STEP_EXECUTION_DIR` (parent execution folder; never a write target or a substitute for controller-resolved context dependencies), `DB_PATH` (backward compatibility only, for scripts written before contract 1.0.45: never use it in a new or repaired script, see **Database access** below), `MCP_API_URL`, `MCP_API_TOKEN`, `VAR_GROUP_NAME` (use `.get('VAR_GROUP_NAME', '')` — this one is optional).
- A parameterized scripted step reads its non-secret, per-call values from `json.loads(os.environ['STEP_PARAMS_JSON'])`. Its plan-level `script_parameters` declaration is the only public contract: do not add a competing CLI flag or free-form instruction path. `context_dependencies` remain positional `sys.argv` inputs.
- NO hardcoded user IDs, account numbers, URLs, paths, or credentials. Every dynamic value flows from env or sys.argv.
- **The step description shows RESOLVED current-run values.** Those are for context only. NEVER copy any name, ID, or literal value from the description into the script — or into any `export` you issue manually. The same script runs for every group/user; a copied value from one run breaks the others.

**Input/output**
- Input data arrives via `sys.argv[1]`, `sys.argv[2]`, ... — these are the resolved `context_dependencies`. Read them.
- NEVER use `/tmp` or any other host path outside the workflow, in code or in a step description: the sandbox does not allow `/tmp`. Scratch goes to `$TMPDIR` (private, removed after the command), step output to `$STEP_OUTPUT_DIR`, durable files under the workflow (`code/`, `db/assets/`).
- NEVER construct paths to sibling step folders (e.g. `execution/login-step/output.json`). The controller resolves correct per-group paths and passes them as sys.argv. If you need data not in sys.argv, add it as a `context_dependency` in `plan.json` — do not hardcode.
- Write output files to `os.environ['STEP_OUTPUT_DIR']` with the exact filenames and structure the validation_schema requires. `STEP_OUTPUT_DIR` is **volatile** (per-run, wiped on re-run). A durable **file** that later steps, runs, or the builder must reach — a download, generated PDF/CSV/image/zip, any format — goes under `db/assets/` (write it via the workspace root, e.g. `os.path.join(os.path.dirname(os.environ['DB_PATH']), 'assets', name)`), with a reference row in `db.sqlite`. Use `db/assets/` for durable output files. Version 1 source and shared helpers belong in `code/`; other paths require explicit Folder Guard grants.
- Check `python3 -m pip --version` before installing a Python dependency. Missing pip/venv is a server prerequisite failure; report it rather than repeatedly rewriting the test. For version 1, install with `python3 -m pip install --target "$WORKFLOW_CODE_DEPS" package`; the controller includes that persistent directory in `PYTHONPATH`. Do not choose a separate venv interpreter for a saved script: the runner uses `python3`. For legacy workflows verify the actual runner dependency path before installing. Never disable host package protections. Package caches persist under `.sandbox-cache/`. A saved `main.py` browser suite may use Python Playwright directly. For browser tests, first read `references/playwright-scripted.md`: JS/TS uses `@agentworks/playwright`; Python uses the `agentworks-playwright` helper/pytest fixture, while the saved-step runner remains Python. Anything needing root or `apt` must be installed by the server operator.

**Data authenticity — no fabrication**
- Every value written to output files MUST trace to a real MCP tool call, API response, or input file. No hardcoded rows, no invented records.
- If the script writes output without making any external calls or reading real input, it will be rejected.

**Deliberate refusal — fail-closed guards must exit code 2, not 1**
- In a run, any non-zero exit simply fails the step. The distinction below matters when you run the step yourself with `execute_step`, where a failing script is handed back to you as repair context.
- Any non-zero exit is treated as a bug by default: the failure is handed back to you as repair context so you fix the script. That is correct for a real error, but wrong for a guard that deliberately detected an unsafe condition (stale data, a write that would overwrite history it could not verify, a precondition that isn't met) and refused to proceed on purpose.
- Use `sys.exit(2)` — not `sys.exit(1)` or any other code — for that second case. Exit code 2 is reserved and means "this refusal is terminal, do not attempt an agentic workaround." The step fails outright instead of falling back to a relearn turn, and the refusal is never handed to an agent as "here is an error, fix it."
- Before exiting, print exactly one explicit refusal record on its own stdout line: `AGENTWORKS_REFUSAL: ` followed by JSON with non-empty string fields `reason`, `blocked_action`, and `resolution`. Use `json.dumps` to escape values correctly. For example:
  ```python
  print("AGENTWORKS_REFUSAL: " + json.dumps({
      "reason": "Existing balance history could not be verified",
      "blocked_action": "Overwrite balance rows",
      "resolution": "Restore database access and verify existing rows before retrying"
  }), flush=True)
  sys.exit(2)
  ```
- The platform displays this as the script's reported refusal, not proof the guard was correct. Exit code 2 without a complete record still stops automatic repair conservatively, but the notification states that the reason is unverified and includes captured output. Existing plain-text refusals remain stopped; malformed records never enable an automatic workaround.
- Get this distinction right: `sys.exit(1)` on a guard that should be terminal lets an agentic retry read your own refusal, agree it was correct, and then perform the exact write the guard existed to prevent — which has happened live. `sys.exit(2)` on an ordinary bug wrongly aborts the step instead of letting the normal repair loop fix the script.

**Non-fatal concerns — say what went wrong without failing the step**
- When the script completes but something consequential looks wrong — a source returned nothing or far less than usual, data was stale or conflicting, an optional API failed, a result works against the workflow's goal — print one line per problem to stdout, unconditionally (not behind `VERBOSE`), as the last lines before exiting 0:
  ```python
  print(f"CONCERNS: prospect source returned 0 rows for group {group} (usual 40-60); outreach queue is empty")
  ```
- Plain text, one line each, with the affected artifact and the evidence. Do not classify or file it anywhere else. Pulse reads these lines from the step's retained output and decides whether each is a real issue. Print nothing when everything is normal. Use `CONCERNS:` only for non-fatal problems; a real error still exits non-zero, and a deliberate refusal still uses exit code 2.

**Calling platform tools from main.py — which errors to retry**
- A retry helper must sort errors into two kinds, never "retry everything". Retrying an error that cannot go away only delays the failure and hides a platform defect. A helper that retried every error six times turned a platform bug into a random 30-second delay.
- Retry only failures that can pass on a second attempt: connection refused or reset, timeouts, and HTTP 429, 502, 503 or 504. Bound it to about 3 attempts with capped exponential backoff (for example 1s, 2s, 4s), and print each failed attempt's error.
- Fail on the first attempt for errors a retry cannot change:
  - validation and SQL errors (HTTP 400, `no such column`, `statements must contain 1-20 operations`) — fix the call instead;
  - HTTP 401/403;
  - any tool-boundary error: `requires an authenticated session`, `session permissions changed`, `session ownership changed`, `session origin changed`, `caller identity conflicts`, `caller does not own this tool session`.
- The script cannot cause a tool-boundary error, and it also cannot cause `workflow database context is ambiguous`, `workflow database context is unavailable`, or `custom tool ... is not registered for session`. These are platform issues. Print the error verbatim and exit 1. Do not work around it by retrying, rewriting the script, or bypassing the bridge. A repair agent that meets one reports it as a platform issue in its repair report and leaves the script's logic alone.
- Make every write safe to repeat before letting the helper retry it: use a natural key with `INSERT ... ON CONFLICT DO NOTHING` / `DO UPDATE`, or check-then-insert inside one transaction. Give external sends (email, Slack, posts) a stable idempotency key. If a write cannot be made safe to repeat, do not retry it.
- State the retry policy — what is retried, how often, and why the writes are safe to repeat — in a comment next to the helper.

**Logging**
- `VERBOSE = os.environ.get('SCRIPT_VERBOSE', '') == '1'`. Guard debug prints with `if VERBOSE:`. Log state before and after each major action. Stdout is the ONLY debugging channel available to the fix loop.

**Robustness across groups**
- The same script runs for every group/user with different data. Use `.get()` with safe defaults for optional *data* fields (never required configuration), handle empty lists, `None` values, date-as-string-vs-number variants, missing optional files.
- Print diagnostic context BEFORE raising. The error output is how the next fix pass understands what broke.
- If the same script keeps failing for specific groups, branch on `os.environ.get('VAR_GROUP_NAME', '')` rather than forcing one code path.

**Code documentation — builder and repair agents**
- Apply these requirements when creating or repairing scripts and shared helpers. Keep documentation proportional to the code; a simple test should remain simple.
- Start each entry point with a concise module docstring explaining its purpose, required environment-variable names and input arguments, outputs/side effects, and how it is executed through the platform. Document names and formats, never actual credentials or sensitive values.
- Give shared helpers and non-obvious functions docstrings describing their inputs, return values, side effects, and important failure behavior. Use clear names and small functions; do not comment every obvious assignment or repeat the code in prose.
- Explain the reasoning behind non-obvious assertions, selectors, retries, timeouts, ordering constraints, and workarounds in nearby comments. Identify observed limitations honestly; do not invent explanations for failures.
- During repair, update affected docstrings and comments together with the implementation. Remove stale explanations. Explain a workaround's reason and when it can be removed; keep the repair history in the run/repair report rather than accumulating dated change logs in the source.
- Before finishing, check that the documented inputs, outputs, shared-helper behavior, and failure conditions agree with the changed code and the verification actually performed.

**Patching discipline**
- In builder chat, edit the saved source selected by the manifest: `code/{step-id}/main.py` for version 1, `learnings/{step-id}/main.py` for legacy. During controller-managed authoring/repair, use the explicit working directory supplied in that turn. Version 1 edits canonical source in place; only legacy execution copies are saved back by the controller. Never patch a stale run copy from builder chat.
- Prefer a targeted edit — your own Edit tool when native tools are on, otherwise `diff_patch_workspace_file` — preserves working code and reduces regressions. Full rewrite (cat-heredoc) only when restructuring large portions. It accepts a unified diff or your native `*** Begin Patch` / `*** Update File:` format (context-located hunks, no line numbers), and one Begin Patch may change several files: all are checked, then all written or none.
- Never edit a large file with an ad-hoc script that finds markers and splices the text (`t.find(...)`, slicing, `str.replace` on the whole file): a marker that is not found cuts off everything after it. A real `shared/pw_lib.py` lost its helper functions that way. When a patch is refused, read the reported file region and fix the patch. After any edit to a shared helper, confirm every name its importers use still exists before running the step.
- Version 1 helpers may live anywhere in the workflow's `code/` tree; import shared packages through `WORKFLOW_CODE_ROOT`, already included in `PYTHONPATH`. Unlocked steps may repair shared helpers in place. Keep outputs in `STEP_OUTPUT_DIR` or durable `db/assets/`, not in source folders.
- Report data scripts live at `code/reports/<name>.py` and run only through a Dashboard's `window.report.run`, not as steps: args in `$REPORT_ARGS`, exactly one JSON value on stdout (logs to stderr), read-only `$DB_PATH` snapshot, writes only in `$REPORT_CACHE_DIR`, 60 s cap, no upstream side effects. They may import shared helpers from `code/`. Contract and caching rules: `reporting-policy.md` "Live data from a script".
- For legacy workflows, keep helpers adjacent to main.py in `learnings/{step-id}/`; the controller copies them with the entry point. Do not infer source/import roots from `__file__.parents[...]` or `DB_PATH`, or assume `learnings/_global/scripts` is on the runtime import path.
- Test with `execute_step` for the intended group so the controller supplies the runtime environment and input arguments. When the step declares `script_parameters`, pass its test values in `execute_step(script_parameters={...})`; the controller validates them and supplies the exact same `STEP_PARAMS_JSON` contract used by scripted specialist routes. A generic builder shell is not a step execution: do not invent `STEP_OUTPUT_DIR` or bypass managed DB access to simulate it. Missing declared `VAR_*` in a builder shell is a platform environment issue to report.
- Shell commands must be POSIX-compatible unless explicitly wrapped in `bash -lc`; `set -o pipefail` requires Bash. Tool JSON does not expand shell variables: resolve `os.environ['VAR_BASE_URL']` before passing the URL to `agent_browser`.
- Distinguish a failed assertion from a script that could not run. Inspect the actual page/result and diagnostics before repairing an assertion; do not weaken expected behavior merely to obtain a pass.

**Never hand-escape prose into a string literal**

Text you are inserting into generated code — an HTML fragment, a log line, a
sentence for the user — has usually already been escaped once on its way to you
through the tool call. Escaping it again is what produces a backslash before the
quote, and a backslash before a quote does not escape it: the quote still closes
the string.

A real failure: `'page\\'s Current work counts...'` — an apostrophe in the word
"page's" written as `\\'`, which Python reads as a literal backslash followed by
a string-terminating quote. `SyntaxError: unterminated string literal`. It was
the third attempt at the same file (`migrate3.py`), because each retry rewrote
the same prose the same way.

Keep text out of code instead of trying to quote it correctly:

- Write the text to its own file and have the script read it. Prose in a data
  file cannot break the parser that reads the code.
- If it must be inline, use a triple-quoted block and pick a delimiter the text
  does not contain. Do not add backslashes by hand.
- When writing the file through the shell, use a QUOTED heredoc delimiter —
  `<< 'PYEOF'` and not `<< PYEOF`. Quoting the delimiter stops the shell
  expanding or re-escaping anything in the body, which is the other half of the
  same problem.
- Prefer a targeted edit (your own Edit tool, or `diff_patch_workspace_file`
  without native tools) over a full heredoc rewrite. A targeted
  patch touches fewer lines, so there is less text to get wrong.

If a generated script fails to parse twice, stop rewriting it the same way. The
quoting is the bug, not the logic — move the text out of the code.

**Output format — HTML vs JSON vs Markdown**

- **JSON** — for structured data consumed by downstream steps or db writes. Always use JSON for `context_output` files other steps read.
- **Markdown (`.md`) — the default for human-readable output**: reports, analyses, summaries. It renders richly in the file viewer (headings, tables, lists), gets clickable workspace file links that HTML doesn't, and is simpler and more robust to author than self-contained HTML.
- **HTML** — only when you genuinely need a rich/branded layout markdown cannot express. For an actual dashboard, use workflow-owned documents under `db/reports/` with `window.report`; `index.html` is the default and optional `views.json` controls only toolbar metadata.
- Do NOT make HTML copies of Markdown stores (`soul.md`, learnings, KB) — those stay Markdown and are read as Markdown.

Before writing a `.html` output file, call `read_skill(skills=[{"name":"builder-reference","path":"references/html-output.md"}])` — it has the full layout baseline, dark-mode styles, inline chart pattern, and quality checklist.


For Slack messages and cross-step thread continuation, load `read_skill(skills=[{"name":"builder-reference","path":"references/slack-bot-routing.md"}])`. Use the backend-owned `send_slack_message` tool through this bridge with `MCP_AUTH`, a stable idempotency key, and an opaque `thread_ref`. Persist the reference as declared context output and pass it through a downstream context dependency. Never expose bot/app tokens to workflow code.

## MCP Bridge — HTTP API for Tool Calls

This skill covers the full mechanics of the MCP HTTP bridge: the env vars
that are pre-set, the curl pattern for calling MCP tools, filesystem
rules, and the workflow loop. The short version lives in the
`system-tools` skill; this is the deep reference.

The bridge is active whenever the session is in agentic or scripted mode
(code-execution infrastructure on). In direct tool-call mode the bridge
is not used — call tools as LLM functions instead.

## Tool access — HTTP, not direct

Through the bridge, MCP tools are reachable via authenticated HTTP, not
as direct LLM tool calls. Discover currently authorized tools with `search_tools`, then use
`get_api_spec(tool_name="<returned-name>")` for the schema and route. If a
keyword search misses, enumerate a group or server instead of guessing names.

Provider-native file and shell availability is determined by this session's
runtime contract. With Native agent tools on, the CLI's own tools run in a
sandbox limited to the folders granted to the chat, and protected files (a
workflow's planning/, its raw database, instruction files) stay refused; with
them off, files and commands go through bridge tools. Invoke only declared and
authorized tools.

## Environment variables (pre-set)

| Variable | Purpose |
|---|---|
| `$MCP_MCP`, `$MCP_CUSTOM`, `$MCP_VIRTUAL` | Short endpoint bases for MCP, custom, and virtual tools |
| `$MCP_AUTH` | Authorization header value; use `-H "$MCP_AUTH"` |
| `$MCP_API_URL` + `$MCP_API_TOKEN` | Full endpoint + bearer token fallback |
| `$STEP_OUTPUT_DIR` | Write primary outputs here. The folder exists — do not `mkdir` it |
| `$STEP_EXECUTION_DIR` | Parent of `$STEP_OUTPUT_DIR`. Use only as a fallback when reaching a sibling step's folder and `sys.argv` wasn't used |
| `$DB_PATH` | Saved scripted/application compatibility only: absolute path to `db/db.sqlite`. Agentic steps use `query_workflow_db` / `mutate_workflow_db` and do not receive this path. |
| `$VAR_<NAME>` | Workflow config values (e.g. `$VAR_PAN`, `$VAR_SHEET_URL`). Reference always; never hardcode the literal value |
| `$SECRET_<NAME>` | Credentials (e.g. `$SECRET_API_KEY`). Never echo to stdout, never write to files |
| `$VAR_GROUP_NAME` | Current group (may be empty when no group is active). The only var where empty/absent is acceptable |

**Missing vars must fail loudly.** In bash: `"${VAR_PAN:?missing}"` or
`set -u`. In Python: `os.environ['VAR_PAN']` — never `.get()` with a default.

## Calling an MCP tool

Real MCP-server tools live at `$MCP_MCP/{server}/{tool}`. Authenticate with
`-H "$MCP_AUTH"`. Use any shell that fits — curl, jq, node, python,
whatever. Shell-first; Python is optional.

```bash
payload='{"arg1":"value1"}'
curl --fail-with-body -sS --json "$payload" -H "$MCP_AUTH" "$MCP_MCP/{server_name}/{tool_name}"
```

The literal form above is only for fixed, simple values. For SQL, multiline
text, JSON paths, or any argument containing quotes, keep the value in a shell
variable and let `jq` encode the request:

```bash
sql="SELECT json_extract(data, '$.field') FROM events"
payload="$(jq -cn --arg sql "$sql" '{sql:$sql}')"
curl --fail-with-body -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/query_workflow_db"
```

Never put SQL containing single quotes inside an outer single-quoted JSON
literal. Shell single quotes do not nest, so the inner quotes are removed even
when the command text appears to contain the correct SQL.

`$MCP_AUTH` is already the complete `Authorization: Bearer ...` header. Never
prepend another header or Bearer prefix. `--json` already selects POST and
Content-Type, so do not add `-X POST`, another Content-Type header, or `--data`.
Keep the call unpiped so curl's nonzero HTTP-failure status reaches
`execute_shell_command`.

## Calling a custom / built-in tool

Built-in categories such as `human_tools`, `workflow`, `workspace_advanced`,
`workspace_browser`, `auto_improvement`, and `knowledgebase_tools` are not MCP
servers from the workflow's selected server list. Call their tools through
`$MCP_CUSTOM/{tool_name}` with no category segment in the URL.

```bash
payload='{"message_for_user":"Workflow completed successfully.","slack_title":"Workflow complete","slack_color":"success","slack_fields":[{"label":"Status","value":"Passed"}]}'
curl --fail-with-body -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/notify_user"
```

`notify_user` owns Slack delivery and renders Block Kit in the backend. For structured
summaries, use `slack_title`, `slack_color`, `slack_fields`, `slack_sections`, and
`slack_footer`; never read a webhook secret or post to a webhook with shell commands.

Do not call `notify_user` as `$MCP_MCP/human_tools/notify_user`; that treats
`human_tools` as a real MCP server and can be blocked by the session MCP
server allowlist.

**Response envelope:** `{"success": true|false, "result": ..., "error": "..."}`.
Always check `success` before treating `result` as authoritative.
Custom Go tools commonly return JSON text inside the envelope's `result`
string. Decode that string exactly once; do not guess that the outer envelope
is the tool payload and do not repeat the tool call while rediscovering its
shape:

For `query_workflow_db`, the decoded payload contains `columns` and `rows`.
Direct/native and MCP tool calls already unwrap this HTTP envelope and return
that payload. The envelope appears when calling raw HTTP with curl; it is not
a different SQL result schema. Do not unwrap a direct payload a second time,
and do not interpret HTTP/tool errors as zero query rows.

```bash
response="$(curl --fail-with-body -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/get_pulse_state")"
jq -e '.success == true' >/dev/null <<<"$response"
result="$(jq -r '.result' <<<"$response")"
jq '.' <<<"$result"
```

For a large result, save or filter the already-decoded `result` variable. Never
repeat the same read merely to apply a different local `jq` expression.

When passing returned guidance to a background task, extract it from the decoded
result, not the HTTP envelope. A missing `.guidance` produces `null`; even `jq -e`
does not stop a shell script unless its failure is handled. Stop before launching
the task if any read or validation fails:

```bash
set -euo pipefail
response="$(curl --fail-with-body -sS --json "$guide_payload" -H "$MCP_AUTH" "$MCP_CUSTOM/get_workflow_command_guidance")"
instruction="$(jq -er '
  if .success != true then error("guidance request failed") else .result end
  | if type == "string" then fromjson else . end
  | .guidance
  | if type != "string" then error("missing guidance")
    elif test("\\S") and (ascii_downcase != "null") then .
    else error("empty guidance") end
' <<<"$response")"
payload="$(jq -cn --arg name "$task_name" --arg instruction "$instruction" '{name:$name,instruction:$instruction}')"
curl --fail-with-body -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/run_in_background"
```

For retries, backoff, or structured logging, write a small helper in the
language of your choice. For reusable helpers saved to `main.py`
(`scripted` mode only), see the `code-authoring` reference doc.

## Workflow

1. Inspect the `<available_tools>` JSON block. Pick the tool you need.
2. Call `get_api_spec(tool_name="<returned-name>")` to get the full
   parameter shape if you haven't called this tool before.
3. Use `execute_shell_command` to write and run the code that calls the
   tool via the HTTP bridge.
4. Parse `success`/`result`/`error` from the response. Retry only transient
   failures (connection errors, timeouts, 429/502/503/504). Stop on hard
   failures with a clear message: validation/SQL errors and tool-boundary errors
   such as `caller does not own this tool session`. The full list is in
   "Calling platform tools from main.py" in `code-authoring.md`.

## Single-call discipline (agentic mode)

The goal of agentic mode is to **minimize tool calls**. Ideally
the entire step runs in a **single `execute_shell_command` call** — one
script that does API calls, data processing, and output writing in one
shot. After a step runs, review whether the agent used multiple tool
calls where a single script would have sufficed.

This is the agentic-mode efficiency target. For multi-call workflows
where each call's output drives the next, that's fine; for independent
operations that could batch, consolidate.

## Variable handling

When writing code that uses workflow variables, **never hardcode the
literal value** — account IDs, URLs, credentials, etc. — even if you can
see the value in the step description. The execution agent's saved
learnings get auto-templated, so hardcoded values become stale placeholders
(`{{"{{"}}VARIABLE_NAME{{"}}"}}`) that fail at runtime when the value changes.

Pattern for code accepting variables:

```python
import os, sys, argparse

parser = argparse.ArgumentParser()
parser.add_argument("--pan", default=os.environ["VAR_PAN"])
parser.add_argument("--sheet-url", default=os.environ["VAR_SHEET_URL"])
args = parser.parse_args()
```

Bash equivalent:

```bash
PAN="${VAR_PAN:?missing}"
SHEET_URL="${VAR_SHEET_URL:?missing}"
```

## Common mistakes

- Calling provider-native `Bash` / `Read` / `Write` tools — disabled; use
  `execute_shell_command` instead.
- Constructing absolute MCP tool URLs without `$MCP_API_URL` — breaks when
  the bridge host changes.
- Echoing `$SECRET_*` values to stdout for debugging — credentials leak
  into logs.
- Using `os.environ.get('VAR', 'default')` — silent fallback masks missing
  config. Use `os.environ['VAR']` and let it raise.
- Splitting a step into 10 tool calls when one script would do — wasted
  tokens; consolidate into a single `execute_shell_command`.
- Hardcoding variable values inline — fails the next time the value
  changes. Always pass via `sys.argv` / `argparse` / env vars.


For Slack messages and cross-step thread continuation, load `read_skill(skills=[{"name":"builder-reference","path":"references/slack-bot-routing.md"}])`. Use the backend-owned `send_slack_message` tool through this bridge with `MCP_AUTH`, a stable idempotency key, and an opaque `thread_ref`. Persist the reference as declared context output and pass it through a downstream context dependency. Never expose bot/app tokens to workflow code.

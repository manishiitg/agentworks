[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-423 — User-authored Python functions as named agent tools

| Coordination | Value |
|---|---|
| State | fixed on main; deployment pending |
| Date | 2026-10-04 |
| Owner | coding-agent-bridge |

## Requirement and gap

The original Relay requirement explicitly included user-created tools implemented
as Python scripts. Graph script nodes and generic shell access did not implement
that requirement: an authored agent could not select a named function with its
own description and input schema. Custom database lookups/actions need that
tool-calling contract inside a normal agent turn.

## Implementation

- `code/tools/<name>/tool.json` contains description, object JSON parameters
  schema and optional timeout_seconds (1–300, default 60). `main.py` implements
  synchronous `run(input)`, returning JSON-compatible data.
- Existing `update_step_config(enabled_custom_tools=["python_tools:<name>"])`
  validates saved definitions/source. Selections are explicit per tool;
  traversal, wildcards and platform tool collisions are rejected. Schema
  external refs cannot load service files or URLs.
- Shared execution-only agent factory appends immutable direct ToolDefinitions
  before agent construction. Native API schema/tool calling and existing CLI
  bridge discovery use the shared registry. No separate agent/executor loop,
  tool-management API or permission store was added.
- Each function captures the existing shell executor after step env/session
  injection and explicit guard wrapping. Source paths are read grants, preserved
  on every sequence-item guard refresh. Existing sandbox, slot, secrets, network
  policy, cancellation and managed workflow DB restrictions remain authoritative.
- Arguments are validated before execution and encoded as data, not shell code.
  Python prints go to stderr, the returned value is serialized as strict JSON,
  and Python/schema/timeout/sandbox failures reach the existing agent tool loop.
  No automatic side-effect retry or tool-result-to-node-output substitution.
- Publish validates declarations in workflow defaults, plan and step config
  against the exact snapshot. Source/metadata are already included in the shared
  release file hash. Earlier versions use their frozen source.
- Relay product.yaml continues owning builder prompt/skills/tools. Its prompt,
  relay-builder skill and product implementation doc now explain named Python
  tools and how to test them.

## Verification

- Real Python tests: Unicode/nested JSON, logging separation, shell quoting and
  injection-resistant arguments, no bytecode writes, schema/type/extra-field
  rejection before sandbox, exceptions, missing run(), non-JSON/NaN returns,
  cancellation, invalid/truncated shell response and unavailable sandbox.
- Shared agent loop integration: a local HTTP model fixture receives the named
  tool schema, requests a call, the real Python function queries a temporary
  SQLite customer database, and the tool JSON returns to the model before the
  agent produces its final response. No live provider request is made.
- Step binding tests: same identity, environment and guard, no shared pool
  mutation, platform collision rejection, read-only source grant retained for
  every message sequence item.
- Release tests: missing source/definition blocks publish, workflow defaults
  checked, v1/v2 preserve different tool implementations.
- Relay, step-config and shared execution-tool regression tests pass.

## Remaining and limits

Deploy the normal backend/product release to make this available on Excellence.
Tests used owned worktrees and temporary fixtures; no running app was restarted.
There is no new tool editor pane; authoring uses Builder chat and existing file
tools. Publish checks source presence/schema but does not import user Python or
verify dependencies/connectivity; test those through the step sandbox.
Arguments are capped at 64 KiB; return values use the existing bounded shell
output and incomplete JSON fails explicitly. Existing crash-resume and Relay
JSON repair gaps are separate. PLAT-411's tool errors remain open.

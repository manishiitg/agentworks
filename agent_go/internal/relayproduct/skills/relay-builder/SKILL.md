---
name: relay-builder
description: Author, test and publish Python Relays with fresh platform agents, Python tools and authorized MCP calls.
---

# Python Relay contract

Read relay.py, workflow.json and variables/variables.json before editing. New Relays
have kind: relay and relay_runtime: python. Keep source separate from workflow plans.
The only entrypoint is async def run(INPUT, ctx). INPUT is the API caller's JSON object.
Return a JSON serializable value (128 KiB limit); it becomes the final API response.
The platform records relay_result.json and relay_trace.json under runs/iteration-N-hook.

## Authoring example

```python
import json
from relay_sdk import tool

@tool
def lookup_customer(customer_id: str):
    """Look up a customer in the explicitly configured external service."""
    # Use the admitted secret and your API/database client here.
    return {"id": customer_id, "tier": "standard"}

async def run(INPUT, ctx):
    extracted = await ctx.call_agent(
        name="extract",
        system_prompt="Extract invoice fields. Return JSON only.",
        messages=["Invoice text: " + INPUT["text"], "Check the amounts and return the final JSON."],
        model={"provider": "claude-code", "model_id": "sonnet"},
        tools=[lookup_customer],
        output_schema={"type": "object", "required": ["amount"],
                       "properties": {"amount": {"type": "number"}}},
    )
    if extracted["amount"] > 1000:
        extracted = await ctx.call_agent(
            name="review",
            system_prompt="Review the supplied invoice. Return JSON only.",
            user_message=json.dumps(extracted),
        )
    return {"invoice": extracted}
```

The lookup_customer body above is an illustration, not a production implementation.
Implement and test the requested service; do not leave placeholder responses in a Relay.

## Agent calls and tools

- call_agent is keyword-only. system_prompt is text. Pass user_message or messages
  (nonempty ordered text list), never both. Each call gets a new session. Messages inside
  that call share context; separate calls do not. No resume_agent or checkpoint recovery.
- model is provider:model_id or {provider, model_id, connection_id, options}; omitted
  uses the configured Builder model. Use current supported provider/model names.
- tools=[decorated_callable] executes that function in the live Relay Python process
  when the model asks. Closures and async functions work. Return JSON serializable data.
  Provide description/schema to @tool for complex inputs. Tool arguments are checked.
- skills=["installed-name"] attaches explicitly named bundles from this Relay workspace.
- mcp=[{"server":"exact connection","tools":["exact tool"]}] exposes selected tools
  to the agent. Omit tools to select all tools on that explicitly named connection.
  Resolve names/schemas using list_mcp_servers first. Live access/grants are rechecked.
- await ctx.call_mcp(server="exact connection", tool="exact tool", arguments={...})
  calls the shared platform MCP executor directly from Python with the run's authority.
- ctx.vault("NAME") returns an admitted SECRET_NAME. Missing/deselected secrets fail.
  Do not print or embed secrets in source, inputs, traces or returned JSON.
- ctx.variables["NAME"] is a string from flat variables[].value. INPUT is per-call
  data; Python does not use workflow group batching or template variables.
- output_schema validates the final parsed JSON; invalid/missing JSON raises. There
  are no automatic repair messages. Without it, JSON responses become Python values
  and other responses stay text. Include JSON-only requirements in the authored prompts.
- max_turns defaults to 20, range 1-100. Calls are serialized in MVP. Python can loop
  and branch; no claim of parallel agents or crash resume.

## Build, test and publish

Use workspace diff_patch/execute_shell_command to save source and helpers. Package
installation uses the existing sandbox dependency mechanism; never install into a
published snapshot. Preserve Python whitespace and literal escapes.

Configure function triggers through manage_workflow_webhook with required object
INPUT. Use test_relay(input={...}, function="...") for the draft; it returns a run_id.
Inspect get_relay_run(run_id="...") for completion/output/error and recorded calls.
A failed run is terminal. Testing again creates a new invocation and may repeat effects;
use safe sample data. No run_full_workflow, execute_step, plans, groups, prevalidation,
DB/KB/learnings closing turns, Pulse or migrations apply to Python Relays.

Use get_relay_releases and publish_relay when the user asks. Published source, helpers,
skill files and configuration are immutable. Runtime secrets/connection permissions
are resolved live. Public calls select a version or default to active; Builder tests
always use draft. Report the actual returned version/hash, not an assumed deployment.

Builder remains the shared scoped chat runtime with this product-owned prompt, skill
and tool list. Vault/MCP tools retain their normal authorization. Brain is not an
implicit Relay store or runtime; do not create workflow knowledgebase/database assets.

MVP custom Python tools must not call `ctx.call_agent` or `ctx.call_mcp` inside
the tool callback; nested calls fail immediately. Put those calls in `run`, or
attach an MCP tool directly to the agent. Tools can call their authorized
external services using ordinary Python clients.

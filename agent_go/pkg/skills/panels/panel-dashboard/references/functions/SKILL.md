# Functions

Use the listed `functions` actions in chat or MCP. List before choosing or changing a function. A function says what work is available; a schedule says when to call it; a webhook brings an outside event. They keep the same project access and caller identity.

Call with `project`, `function` and `input`. Keep the returned `call_id`; read `functions action=status` until it settles. Answer a pending question with `action=reply` only when you are its caller. Current project members may read shared results. A saved or accepted call is not proof of completion.

Builders use `action=define` to create or replace a `definition` (name, description, action, fixed input and caller input schema); `action=delete` removes it. Crew actions send instructions to the function's named tab, or use a fresh chat when declared. Goal actions run their saved plan/step/routes with declared non-secret variables. Flows use their published version and required INPUT object. Preserve stable identity and caller restrictions when changing a function.

Read [calls and authoring](references/calls.md) for schemas, results, retry IDs and existing saved step bindings.

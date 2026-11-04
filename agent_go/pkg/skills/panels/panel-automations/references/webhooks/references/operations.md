# HTTP setup and evidence

Use webhooks for outside HTTP events; functions expose reusable work. Workflow/script callers select names from the executable’s shared Inputs; Crew message functions retain their own caller schema. List first, then use the offered schema and current project permissions. MCP uses project and trigger; the bound Builder uses its captured project.

Keep the existing canonical URL when editing. HTTP hooks live in webhooks.json; functions and explicit saved step bindings live in functions.json. Use typed tools, not raw file or shell edits. Bearer and GitHub HMAC checks remain; encrypted secret values never belong in logs, messages or committed source. A secret name is safe to report.

A workflow or saved-script hook runs its saved work with validated input and payload mappings; a Crew chat hook sends its saved message. A Crew-owned workflow/script records its outcomes in the saved Crew chat while retaining its own run identity. Define inputs once on the workflow/script and expose explicit names for envelope requests; presets are optional saved values, not groups. Fixed and mapped route selections must agree. Input overrides use declared non-secret variables. Do not use a webhook to bypass a disabled product or publish step.

An HTTP acceptance receipt is not completion. Preserve the delivery ID on retry. Inspect deliveries/runs or status and the actual output/error. A requested internal test runs real work through the receiver, but does not prove public DNS or gateway delivery. Never rotate a secret or test a hook just to inspect it.

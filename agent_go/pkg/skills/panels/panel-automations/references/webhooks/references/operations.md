# HTTP setup and evidence

Use webhooks for outside HTTP events; functions expose reusable work and keep their own declared input contract. List first, then use the offered schema and current project permissions. MCP uses project and trigger; the bound Builder uses its captured project.

Keep the existing canonical URL when editing. HTTP hooks live in webhooks.json; functions and explicit saved step bindings live in functions.json. Use typed tools, not raw file or shell edits. Bearer and GitHub HMAC checks remain; encrypted secret values never belong in logs, messages or committed source. A secret name is safe to report.

A Goal hook runs its saved route/step with validated group, variable and payload mappings; a Crew hook sends its saved message to main or its own continuing conversation. Fixed and mapped route selections must agree. Input overrides use declared non-secret variables. Flows keep published function execution and release verification; do not configure a Goal-plan webhook to bypass that.

An HTTP acceptance receipt is not completion. Preserve the delivery ID on retry. Inspect deliveries/runs or status and the actual output/error. A requested internal test runs real work through the receiver, but does not prove public DNS or gateway delivery. Never rotate a secret or test a hook just to inspect it.

# One invocation, one saved-script run

A direct workflow start executes one saved Python program. Inputs resolve from declared defaults, an optional saved input preset and explicit typed values. Saved settings supply input values that the program can use to choose a task path; they do not create another workflow or automatically fan out. `@step` labels describe progress inside the program and cannot be started or debugged independently.

Start several input sets as separate invocations only when requested. Each invocation keeps its own saved input values, frozen source, run identity, outputs and records. Scheduling and webhooks use the same script entry and current access; manage them through their own offered tools. See the schedules reference for their current contract.

Read the returned run record and logs before deciding whether more work is needed. A start receipt is not a completed result. A failed run may already have written records, called an app or sent a message; do not automatically repeat it. Stop only the selected live execution when the person asks, using the current execution control and returned identity. A Python progress label is not an execution identity. For a correction, edit the saved source and run the requested work again; use a continuation operation only when the current schema offers it and its saved-source/input contract matches the request.

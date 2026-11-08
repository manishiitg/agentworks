# Build your first workflow

Start with a small, real job: a recurring research brief, support summary or finance check. A workflow belongs to a Crew. Keep its conversation there and use the workflow's **Steps** and **Runs** views to inspect the saved work and its results.

## Describe the job in Crew chat

Open a Crew, choose its agent, and explain the inputs, permitted sources and result you need. For example:

> Review the open support conversations, group them by urgency and cite the source messages. Save a brief for me to review. Do not send replies.

Use the Crew's existing connections and files. If a source needs a sign-in, follow the connection instructions it returns. Creating a workflow does not create an account or grant access to another Crew.

The agent saves the workflow and explains what each step does. Underneath, a workflow is `script.json` plus `main.py`; the `python-workflows` skill covers writing it. Python functions, conditions and loops decide the order. Named `@step` functions appear in Steps and Runs, and `agent()` calls handle work that needs an AI agent.

## Review its inputs and result

Give changing values names and types, with sensible defaults. A saved preset is a named set of those values. Secrets stay in Vault; a workflow declares the secret names it needs and uses the account already selected for its Crew.

Keep the expected result concrete: a file, a checked calculation, or records with source evidence. An agent finishing its turn is not enough; the workflow checks what it produced. If evidence is missing, the result should say so.

Discuss any decision that needs a person in Crew chat before running work that depends on it. A workflow never pauses for a question. It either has the required values or stops with a clear reason.

## Run and inspect

Ask the Crew to run the workflow, or start it from its workflow page. Choose a preset or provide the inputs for that run.

Open Runs to see the outcome, named steps, checks, files, logs and available usage. Reading a failed run does not repeat it. Starting another run repeats real effects, including app calls and sends, so check the saved result before retrying an uncertain action.

Fix a correctness or access problem using that evidence, then run the corrected work. The workflow's agent may make one correction turn for an invalid output in the same conversation; it must preserve completed external actions.

## Add a schedule when wanted

Once the result is useful, request its cadence. A schedule can start the workflow with a saved preset and inputs, or message an existing Crew agent when the job needs the conversation's context. The agent can run the whole workflow as part of that work. A single step label is not a separate schedule target.

Read [schedules](../workflow/workflow_scheduling.md), [run results](../workflow/workflow_monitoring.md) and [CLI/MCP access](agentworks-cli-mcp.md) for those paths.

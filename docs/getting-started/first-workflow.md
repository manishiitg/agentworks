# Build Your First Workflow

The first workflow should be small enough to review but real enough to produce evidence. A recurring research brief, support summary, lead review, or finance check is a better first test than a large autonomous process.

## 1. Create the Automation

1. Open **Automation** mode.
2. Open **Select Automation** and choose **+ Add Automation**.
3. Enter an **Automation Name**.
4. Select an automation provider under **Agent LLM Configuration**.
5. Set the required **Workflow Folder**.
6. Choose **Save Automation**.

The workflow folder becomes the durable home for the workflow definition, run artifacts, reports, learnings, and knowledge used in later runs.

## 2. Define the Operating Contract

In **Chat**, describe:

- the business outcome to achieve;
- the inputs and systems the workflow may use;
- the expected output and evidence;
- the run cadence;
- decisions that require human approval;
- the metric or review standard that determines success.

Keep the first contract explicit. For example: "Every weekday, review open support conversations, group them by urgency, cite the source messages, draft responses, and ask for approval before sending anything."

## 3. Review the Plan and Access

Open **Plan** before the first run. Confirm that each step has the right worker and only the access it needs:

- coding CLI or model provider;
- MCP servers and tools;
- browser mode;
- global or workflow-scoped secrets;
- human approval boundaries.

See [Workflow Builder Commands and Tools](../workflow/workflow_builder_commands_and_tools.md), [MCP Bridge Layer](../core/mcp_bridge_layer.md), [Browser](../core/browser.md), and [Secrets](../core/secrets.md) for detailed configuration.

## 4. Run and Observe

Start the workflow and use **Tree** for step progress or **Terminal** for the live agent session. A useful first run should leave an inspectable record rather than only a chat response.

After the run, inspect:

- the generated report and files;
- logs and per-step execution status;
- model and token cost;
- any requested human input;
- run evidence about reliability or missing data.

See [Workflow Monitoring](../workflow/workflow_monitoring.md), [Cost and Log Measurement](../workflow/cost_and_log_measurement.md), and [Human Feedback](../workflow/human_feedback_system.md).

## 5. Improve the Next Run

Fix correctness and access problems first. Then review proposed changes to instructions, scripts, evaluation criteria, or reusable skills. Approve only changes supported by run evidence and keep a human review step around consequential actions.

Use [Self-Improvement and Reporting](../workflow/self_improvement_and_reporting.md), [Auto Improvement Framework](../workflow/auto_improvement_framework.md), [Evaluation System](../workflow/evaluation_system.md), and [Learning Architecture](../workflow/learning_architecture.md) to extend the loop.

## First-Run Completion Check

The workflow is ready to schedule when:

- the result matches the stated output contract;
- source evidence is available for review;
- retries and approval boundaries are clear;
- cost is acceptable for the planned cadence;
- the next run can reuse saved workflow context instead of starting from zero.

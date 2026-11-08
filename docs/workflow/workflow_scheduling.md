# Schedules

A Crew's Automations contains its workflows and scheduled work. There are two useful jobs for a schedule:

| What should happen | Schedule target |
| --- | --- |
| Repeat a defined workflow with known inputs | Start the workflow with a saved preset and input values. |
| Continue work that needs an agent's conversation and context | Message an existing Crew agent. That agent can run the whole workflow when needed. |

Read the current entries before changing one. Use the same schedule ID to update its requested fields, and read back the saved cadence, timezone, enabled state and target. Do not create a duplicate timer or start it just to inspect it.

## Starting a workflow

Select the workflow inside its Crew and supply its preset or declared inputs. Python decides branches from those values; there is no route selection or individual step target. A missing required input stops the run clearly.

Keep a timezone explicit when the person's request depends on local time. Treat the saved cadence and next-run information as server state, not something the browser should calculate independently.

## Messaging an agent

Select an existing Crew agent and describe the work in the message. Use the agent's actual returned identity, not a guessed chat or a new conversation. The Crew's current caller, account and tool permissions still apply. Sending a message does not prove the work finished; inspect its answer and any workflow run it starts.

Connecting a computer or selecting an account does not make it available to every scheduled job. Follow the existing Crew laptop and connection rules for that agent turn; workflow execution keeps its own resources and grants.

## Results

A scheduled workflow produces the same run record as a manual run. Inspect the result, checks and evidence in Runs. A scheduled message appears in its target Crew conversation; any workflow it starts has its own run record.

Runs and app calls are real. A second run can repeat writes or sends, so check an uncertain start before retrying it. Use existing scheduler history to diagnose queued, running, failed or completed work. No automatic owner-summary email or daily digest is implied; the run record is the summary, and business notifications belong explicitly to the job that sends them.

Use the current `automations` tools in Crew chat, or the schedule actions offered by the external tool catalog. For Python authoring and caller input mappings, read `python-workflows`.

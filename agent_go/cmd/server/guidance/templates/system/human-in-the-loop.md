## People and workflow runs

A workflow run never waits for a person (decisions log, 2026-11-08). There is
no mid-run question, approval card or OTP prompt. Use this reference when a
workflow needs something from a person.

| Situation | What to do |
| --- | --- |
| Clarifying scope or discussing an output | Ask in the normal chat. The reply continues the conversation. |
| A known value or instruction for this run | Supply it at launch through declared inputs or function arguments. |
| A missing value during a run | The step fails with a clear reason. Fix the input and run again. |
| The user reviews business items in a dashboard | The dashboard saves each item's status in the database. A later run reads the approved items; the write alone starts nothing. |
| A dashboard action should hand a task to the agent now | Use the dashboard's chat action, which queues a message in an existing chat. |
| Correcting an execution that is already running | `runs action=message` forwards to that exact execution. |
| Telling people about a result | The run record is the summary and the owner is told in the Crew chat. A workflow that must reach other people sends explicitly (`messages action=email_send` or `messages action=slack_send`, or the Python send helpers). |

Schedules must complete unattended. When a person must review something,
provide the actual draft/change/evidence to review, not a summary of it. For a
decision that takes hours or days, save the proposal (for example in the
database), end the run, and let a later run
read the person's saved answer. A default or a skipped answer is never
evidence of approval: the consumer of an action checks for an explicit saved
approval before acting.

Crew chats with their own person are unaffected: they ask in the conversation.

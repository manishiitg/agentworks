# Crew suggestions

Anyone can use another user's Crew in Run mode, but only its owner can change
it. Suggestions let everyone else ask for a change.

## For someone using a Crew

Tell the Crew what you would like changed, for example "can the daily report
also go to #qa?". It offers to send that to the owner as a suggestion
(`submit_crew_suggestion`): your request in your words, an optional reason, and
optionally the function or schedule it is about. Nothing about the Crew
changes, and you do not see other people's suggestions.

## For the owner

The Crew's right pane has a **Suggestions** view (lightbulb icon), with a
badge counting pending suggestions. Each shows who sent it, what they asked
for, and why. **Accept** or **Decline** records your decision and can carry a
note; accepting changes nothing by itself. Make the change in the Crew's chat;
the view's Ask AI button asks the Crew to help with the accepted ones.

## Rules

- Only the Crew's owner reads, accepts or declines its suggestions.
- A suggestion cannot be edited or overwritten afterwards; send a new one.
- The Crew and its owner come from the session, never from the model.
- Workflows have the same feature in Run mode (`submit_workflow_suggestion`),
  reviewed in the workflow's decisions panel.

Suggestions are stored with the Crew's decisions in its `db/db.sqlite`. The
decisions API checks access on every route: for a Crew, the owner only; for a
workflow, readers can list and owners/editors can answer.

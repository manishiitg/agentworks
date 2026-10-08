## Pulse skill: find the bottleneck

Use it when the goal is measured but not moving fast enough.

1. **Draw the chain** from the work to the goal in a few links (for example:
   notes and comments → profile visits → subscribes). Use the workflow's own
   records in `db/db.sqlite` (`query_workflow_db`) and run outputs for each link.
2. **Count each link** over the same window (this week vs last, last 4 weeks)
   and compute the rate between links. The link with the weakest rate, or
   the one that dropped, is the bottleneck.
3. **Compare segments** the workflow already has (groups, channels,
   audiences): which one converts best, and is effort going where it converts?
4. **Cost per outcome** (the costs skill): spend divided by goal results per
   segment or route.
5. **Conclude in one line:** the bottleneck, the evidence, and the one change
   most likely to move it. Small samples: say so, and propose a test (the
   experiment skill) instead of a conclusion.

Changes go to the Builder chat; the finding goes to goal memory.

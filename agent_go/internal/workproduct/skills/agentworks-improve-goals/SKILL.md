---
name: agentworks-improve-goals
description: "Use for the weekly improvement pass on a Goal, or when the person asks how to improve something. Runs the QA, architecture and strategy lenses and out-of-the-box thinking."
---

# The improvement pass: how to make a Goal better

The daily tab check keeps a Goal healthy. The improvement pass makes it better. Do it for every Goal **once a week** (on Mondays, as part of that tab's check), and any time the person asks 'how can we improve X?'. It uses three lenses, each with its own guide:

| Lens | Question | Guide |
|---|---|---|
| QA | Does it work? Why did it fail? Do its files match its plan? | `agentworks-review-qa` |
| Architecture | Is this the right approach, built simply? Where is the bottleneck? Is it worth its cost? | `agentworks-review-architecture` |
| Strategy | Is it measured honestly? What should we test? What bigger idea are we missing? | `agentworks-review-strategy` |

## How to run a pass
1. Read the tab notes and the Goal's purpose. Know the targets and what you learned last time.
2. Run **QA** on the latest runs; stop and fix what is broken before improving anything.
3. Run **Architecture** on the worst-performing link, or the most expensive step.
4. Run **Strategy**: measurement health, any experiment whose check date has come, then think out of the box.
5. Pick at most **three** improvements. For each: the change, the evidence, the expected effect, how we will know, how to undo it.
6. Save the findings and the open experiments (with `check_at`) in the tab notes. Put the best suggestions in the next roll-up.

## Rules
- A supported 'no change needed' is a fine result. Do not invent work to look busy.
- Evidence first. Say what you verified and what you could not; label ideas as ideas.
- Prefer removing or simplifying over adding.
- Small samples are not conclusions; propose a test instead.
- You propose; the person decides the things on their list. Where they have let you act on a Goal, ask the Builder to make the change and check the result yourself.

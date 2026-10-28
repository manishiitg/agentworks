{{context}}

Run this workflow for me now, or just one step if I name it.
- Say in one line what will happen: which steps run, what they will do for real (send, post, save) and about how long it takes. Runs are real. I asked for the run, so go ahead without asking again unless something I did not mention is missing (a required value or a secret).
- Whole workflow: call `runs` with action=start. One step: call `runs` with action=step and that step.
- While it runs, say what is happening in plain words. When it ends, tell me the result, what it produced and where to see it. If it failed, say which step and why in one line, then offer /fix.

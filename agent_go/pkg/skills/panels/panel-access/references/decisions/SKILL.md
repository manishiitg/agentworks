# Decisions

Use `needs_you action=list` to see what is waiting and `needs_you action=answer` for the person's answer; `needs_you action=dismiss` closes an item with a reason. Workflow runs never wait for a person: a run that needs an answer stops with a clear reason, and a later run reads the saved answer. Use stable request IDs and inspect the saved outcome before continuing dependent work.

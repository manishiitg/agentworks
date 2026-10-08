## Pulse skill: manage Pulse tickets

Pulse issues are your to-do list for the goal (`get_pulse_state` worklist and
issues, `record_pulse_finding`, `merge_pulse_issues`, `resolve_run_concern`).

1. **Open one only for the goal:** a failing step, a measurement gap, a
   bottleneck, a cost spike, with evidence (run, step, numbers). Not for style.
2. **No duplicates.** Look at open issues first; add to one or merge
   (`merge_pulse_issues`). The same failure over nine runs is one ticket.
3. **Rank by goal impact:** blocks the goal (not measured, goal work not
   running) first, slows it next, untidy last. Work the list top-down.
4. **Get it done through the Builder chat** (`ask_builder`): the evidence and
   what "fixed" looks like. When it needs the owner, ask the Builder chat to
   raise one decision and attach your recommendation.
5. **Chase what waits:** a ticket waiting on the Builder chat or the owner gets
   one reminder after a few days, saying what it blocks. No repeated nagging.
6. **Close only with proof:** a later run shows the fix worked (the inspect
   skill). Record what was done and the evidence. A fix that did not help is
   reopened, not closed. Resolve run concerns the same way.
7. **Report briefly:** in the goal check, open tickets by impact ("2 blocking,
   1 slowing"), not the whole list.

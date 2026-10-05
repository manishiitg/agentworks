# Crew monitor retains a completed chat as running

## Local observation — 2026-10-05

Company CA appeared as running in the desktop Global Monitor after its chat
had finished. The workflow-trigger conversation completed at 09:09:36 IST.
The stuck item was the separate main Crew chat, whose last structured
completion at 09:30:55 promoted another input execution. Its active-session
snapshot still reported a busy foreground and a running promoted execution,
with no cancel handle, background agents, or busy native terminal. Therefore
the monitor was displaying backend lifecycle state, rather than merely an
old label for the completed workflow-trigger run.

## Completion watcher hardening

The retained-turn observer previously reset its inspection timer on every
terminal output chunk. Continuous TUI repaint bytes could prevent the timer
from firing, starving both provider-confirmed completion and the existing
stuck-turn backstop. A scheduled check now keeps its deadline despite further
output. Completion-capable providers are consulted after the initial bounded
observation delay and at the existing throttled interval, even when cosmetic
output continues. Their native completion contract remains authoritative;
output silence alone does not prove completion, and providers with ungated
transcript readers remain excluded.

Regression coverage emits continuous terminal repaint output and verifies
that a provider-confirmed final response settles the execution. A second case
verifies the existing backstop still fails an uncompleted execution under
continuous output. The existing genuine-new-output test continues to keep
active work running. This does not change conversation destinations or
permissions. The existing local runtime was not restarted during this fix.

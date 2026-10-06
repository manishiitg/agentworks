[← chat / reliability](index.md)

# PLAT-604: Lost auto-notifications are never reported

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | chat |
| Area | reliability |
| Summary | Pending completions are dropped silently after 8 failed synthetic turns, and a restart loses every wait unannounced |

## What happened

Review 2026-10-06 (code): after 8 failed synthetic turns (`background_agents.go:1776-1780`) pending completions are
dropped and neither the model nor the user is told; one only resurfaces if another completion triggers the retry sweep.
The registry is in memory (`background_agents.go:557`), so a server restart loses every wait and nothing tells the
model afterwards. Unverified: whether a synthetic turn after the 1h tmux reaper closed an idle CLI restarts it or fails.

## Fix (not built)

Tell the chat when a completion is dropped or a wait is lost on restart; persist pending waits or list them on start.
Live proof: a 70-minute trigger (reaper), and a restart while a trigger waits.


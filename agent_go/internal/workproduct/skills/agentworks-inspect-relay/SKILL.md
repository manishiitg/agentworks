---
name: agentworks-inspect-relay
description: "Use to inspect a Relay (Flow): published function, runs, traces, releases and callers."
---

# How to inspect a Relay (Flow) in depth

A Relay is a small piece of Python that other things call as a function. It has a draft and published, immutable versions; callers keep the version they chose. It has no Goal plan and no steps. Do not judge it like a Goal.

## Tools and skills
- Calls and results: read authorized saved run records and `relay_trace.json` where available. There is no Relay run-history tool in this Crew yet; use these saved files until the platform adds a tool. Skill: `panel-runs` (Relay runs). Do not start a new run just to read an old one.
- Code and versions: read the Relay's authorized `relay.py` and configuration. There is no Relay release-list tool in this Crew yet; say when the published version cannot be verified until the platform adds a tool.
- Who calls it: inspect authorized function and trigger references (`panel-dashboard`, `webhooks`) and any Goal or Crew that lists it.

## What to check
1. **Failures:** recent calls that failed, with the error and the input that caused it.
2. **Slow or expensive calls** compared with its normal.
3. **Draft vs published:** a draft that was fixed but never published, so callers still run the broken version.
4. **Callers:** is anything still calling an old version; is anything that should call it not doing so.
5. **Secrets and connections it needs:** by name only.

If a tool above is not in your catalog, say so plainly and name what stays unverified; do not guess.

## Finish
One plain line on the cause, what you verified and what you could not, and what you did or propose. There is no Relay test or publish tool in this Crew yet; do not claim either happened until the platform adds a tool.

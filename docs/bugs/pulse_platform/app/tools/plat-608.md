[← app / tools](index.md)

# PLAT-608: product.yaml is the only tool registry; Brain tools renamed to brain_*

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | app |
| Area | tools |
| Summary | Tools are registered and authorized through several paths besides product.yaml; Brain tools keep their knowledgebase names |

## What happened

## Fix

## Left

## Why

Owner, 2026-10-06: "we use product.yml to register tools right ... and there should be no other path to register", and rename the Brain tools to brain. Two bugs today came from the other paths: PLAT-600 (the Builder's Brain project tools were swapped in by server.go when the request was workflow_phase, but their authority check ran after handleQuery rewrote the mode to multi-agent, so they always refused) and the need to add a new Brain tool by hand to four copied name lists (external builder, feature catalog, test mode, step execution policy).

## Plan

1. Decide product, phase and role once at admission and store it on the request; registration and every permission check read that value instead of re-deriving it from request fields that change during the request.
2. product.yaml is the only registry: phase tool variants (such as the Builder's project-scoped Brain tools) are declared there, and copied name lists are derived from the tool owner's definitions.
3. Rename the six Brain tools to `brain_*` (browse, read, update, backup, skills, access), keeping the `*_knowledgebase` names as aliases for a transition; update prompts, docs and the AgentWorks skill. Product ID, URLs and token scopes stay `knowledgebase` for now.

## Progress

- Survey of every registration path: running.

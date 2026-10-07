[← brain / agents](index.md)

# PLAT-647: Built-in brain skill for steps and chats with Brain access

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | brain |
| Area | agents |
| Summary | Agents with Brain tools only saw the tool descriptions; a built-in brain skill now teaches how to use Brain well |

## What happened

## Fix

## Left

## Why

Owner, 2026-10-07: "do workflows skills know how to use brain". The Builder had Brain guidance (builder.md, step-description.md, stores.md), but a step or Crew/Code chat with Brain tools saw only the tool descriptions: nothing about browsing before writing, following a folder's readme, updating with expected_version and a stable request_id, one subject per note, Timeline lines, or company skills. (Vault, by contrast, is covered in integration-discovery, secret-management, workflow-tools, the Crew/Code prompts and the vault-access skill.)

## Done

`pkg/skills/builtin_brain_skill.go` registers a built-in `brain` skill (about 30 lines). It is attached to every workflow step whose Brain access is not none (the same condition that gives it the Brain tools; `effectiveStepSkills`), and to Crew and Code chats through the `knowledgebase` profile feature. Not deployed.

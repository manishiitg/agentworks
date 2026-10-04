[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-438 — Message-sequence prompts advertised knowledge-base read the sandbox denied

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | step-execution |

## What happened

social-media (twitter-automation) run 2026-10-04: `review-critic` (and
`review-profile-health`) ran `cat knowledgebase/context/context.md` through the
bridge shell and got "Operation not permitted". Their `knowledgebase_access` was
`none`, so the folder guard and sandbox correctly refused; the Builder has since
granted the critic read access. But the step prompt listed "Knowledgebase
(PERSISTENT, READ)" and told it to read `knowledgebase/context`:
`buildMessageSequenceTemplateVars` hardcoded `KbAccess = read` for every
message-sequence step, ignoring the step's `knowledgebase_access`.

## Done

- `messageSequencePromptKBAccess`: the prompt uses the same rule as
  `setupMessageSequenceFolderGuard` (`resolveKnowledgebaseAccess` of the step
  config; read-write only when the item's effective write access includes KB and
  the step allows writes). A step with access `none` no longer sees the KB row or
  the read instructions.
- Regular-step and Agent-orchestrator prompts already used the step config.
- Tests: `TestMessageSequencePromptKBAccessMatchesTheGuard`; the template-vars
  fixture now grants KB read-write on the step, as the real path requires.

## Left

- Not deployed; the owner's local app needs a restart to pick it up.
- `review-profile-health` still has `knowledgebase_access: none`; whether it should
  read the KB is a workflow decision, not a platform bug.

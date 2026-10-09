# Fallback persistence and orphan-decision cleanup

Local implementation follow-up for G38/PUL-5D98671B (PLAT-011/060 related)
and G24/PUL-C004DFAF (PLAT-021/045/077 related). Not deployed. No live
workflow configurations, decisions, or issue tracking rows were changed.

## Fallbacks

Review follow-up: published primary references retain their matching resolved
provider, model, and options in runtime. The published reference stays on disk.
Missing/mismatched resolution rejects the update before any write; regression
tests cover usable primaries, rejection, and old-runtime isolation.

`update_workflow_config(update_tier_fallbacks=...)` now validates the entire
patch against the fresh workflow.json, requires explicit routing mode and
configured primary tiers, and writes the fallbacks durably before replacing
the runtime tier configuration. Empty arrays intentionally clear fallbacks;
invalid entries no longer silently clear or partially apply a list.
Unrelated manifest fields are retained. Manifest persistence errors propagate
to the caller rather than being logged behind a successful response.

Provider-profile mode is explicitly rejected, not silently converted. The
operator's model-routing choice and any separate set_workflow_llm_config
permission problem remain outside this fix. The server A report is not closed
solely from this local change.

## Duplicate decisions

New `dismiss_duplicate_human_input_request` is registered in human_tools and
the shared workshop/run human-tool surface. It requires workspace_path,
input_id, keep_input_id, and reason. Both requests must be pending with
identical source, question, context, options, free-text setting, run, evidence, and approval
contract. The discarded ID must have no human_input_id reference in typed
finding details or finding events. Malformed finding metadata fails closed.

The check, dismissal, and audit event are in one transaction. The retained
request is untouched. No answer, consumption, or permission is fabricated.
The duplicate_dismissed event retains the kept ID, reason, and session.
This is not general agent authority to dismiss unique or linked user questions.

## Verification

Focused local tests cover fallback persistence/clearing, profile rejection,
invalid entries, missing primaries, denied writes, retained unrelated fields,
registered duplicate-tool execution, linked/mismatched/non-pending rejection,
retained pending state, absent fabricated answers, and audit history.
Existing human-tool and human-input tests also pass.

The separate image-path report remains open for bridge-specific reproduction.

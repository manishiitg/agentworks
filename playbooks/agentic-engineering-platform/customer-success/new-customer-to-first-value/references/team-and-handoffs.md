# New Customer to First Value: team and handoffs

| Slot | Crew template | Output | Minimum source |
| --- | --- | --- | --- |
| Onboarding, required | `customer-onboarding-coordinator` | `onboarding-milestone-register/v1` | Authorized customer handoff, account owner, first-value goal |
| Adoption, required | `product-adoption-analyst` | `first-value-readout/v1` | Validated register, product event definition and coverage |
| Health, optional | `customer-health-coordinator` | `customer-health-brief/v1` | Validated readout, approved support and renewal scope |

Reuse an existing Crew only after inspecting selected skills, setup evidence, owner, and source access. Distinct Crew IDs are the default for the two required responsibilities. A small team may consolidate only when one Crew has both verified capabilities and compatible access; Builder must still preserve the two validated step outputs.

## First manual route

1. Onboarding reads one authorized new-customer handoff, confirms account and tenant IDs, and emits an owned milestone register. Its goal names the exact event that would demonstrate first value. Unknown, pending, blocked and complete milestones remain distinct. If the source is Signed Deal to Onboarding Handoff, first validate that Playbook's Sales artifact and receiving acceptance pair; a `needs_resolution` review cannot enter this route.
2. Validate the register with `python3 skills/agentworks-playbook-new-customer-to-first-value/scripts/validate_customer_success_artifact.py onboarding path/to/onboarding-milestone-register.json`. For the signed-deal route append `--accepted-handoff path/to/onboarding-acceptance.json`; include `purchased_scope` and `first_value_goal` in the register and cite the exact accepted artifact ID in its handoff source URI. Use the actual installed skill path. A failed validator stops Adoption.
3. Adoption receives only the validated register and its own authorized product event source. Match account and tenant IDs, event definition, timezone and window. Exclude internal/test and duplicate events. Emit a first-value readout. Validate it with `adoption path/to/first-value-readout.json --onboarding path/to/onboarding-milestone-register.json`.
4. If Health is selected, pass the validated readout and register, plus only its authorized support and renewal scope. Validate `health path/to/customer-health-brief.json --onboarding ... --adoption ...`.
5. Show the owner the actual result, source coverage, blocked milestones and proposed next action. Save run IDs, artifact paths, validator results and an action ledger. Customer outreach or CRM writes are separate decisions.

The validator checks structure, references, account/tenant and rule binding, milestone links, source coverage, and observed-state consistency. It cannot verify a source claim or prove the product event happened; the human reviewer must inspect the actual authorized records. The example fixtures are fictional and cannot complete customer setup.

## State meanings

- `reached`: the agreed event is present in the authorized source inside the window and linked to the customer.
- `not_observed`: the complete source coverage for the specified window shows no qualifying event.
- `unknown`: the source, mapping, window, or instrumentation is incomplete. Do not call this a failed customer.

On repeats, preserve the same stable account and milestone IDs. Report changes, late events, and corrected records. Never create a second customer reminder solely because a webhook retried.

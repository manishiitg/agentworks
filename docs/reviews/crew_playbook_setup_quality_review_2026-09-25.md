# Crew and Playbook setup quality review

Date: 2026-09-25. Reviewed checkout: `deploy/crew-templates-20260925`, based on `5b3a57a5c`, with the local corrections listed below.

## Verdict

The product direction is coherent and the implementation has useful foundations. It is suitable for a supervised pilot after the local fixes are released. It is not yet a verified, self-service system for configuring and operating a team of agents.

The biggest remaining issue is the distance between the authored Playbook instructions and runtime guarantees. Builder is told to verify connections, validate handoffs, record evidence, and test recurrence. The package and runtime do not yet make all of those conditions mandatory or prove they happened.

This review inspected the Crew catalog, installation and identity receipts, chat setup status, Workflow Playbook installation, Builder creation/attachment/trigger tools, Crew execution, typed functions, Website Growth package, and the unified design document. It used code inspection and targeted automated tests. It did not run an authenticated customer onboarding session or inspect the current rendered UI. Local changes in this review have not been deployed.

Current branch update, 2026-09-26: Website Growth Loop v0.4.0 now ships executable validators and good/rejected fixtures for the strategist → search → optional content brief → page draft chain. The library validator also checks that all 44 Playbook slots resolve to installed Crew templates and that handoff graphs have matching versioned outputs, valid required edges and no cycles. Builder still must insert and test blocking steps; the generic Crew runner does not enforce these contracts automatically. Findings below remain the original review record where they describe the older checkout.

Later library update, 2026-09-26: Website Growth Loop v0.5.0 extends the authored route through approved page publication, distribution and traffic readout with exact-ID checks and rejected fixtures. The generated catalog now has 11 Website Growth Crews. These are template and contract changes; runtime wiring and customer source verification remain open for a real installation. Counts in the original verification section below describe that earlier review run.

## Findings, highest priority first

### 1. High — Builder Crew creation missed the admin-only Work restriction. Fixed locally.

`CreateCrewProject` did not check the user's entitlement to the Work product before creating it. A regression test with `AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES=work` reproduced a non-admin creating a Crew and writing seven files. This matters directly to the admin-only Dominion pilot.

The service now checks current user identity, disabled state, and Work access before any creation writes. It also requires write/owner access to the creating Workflow. Builder mutation tools now recheck current Workflow write access on every invocation; previously their shared authorization check also accepted readers. Read-only discovery remains available to authorized readers.

Evidence: [creation service](../../agent_go/cmd/server/crew_creation.go), [creation regressions](../../agent_go/cmd/server/crew_creation_test.go), [Builder tools](../../agent_go/cmd/server/crew_builder_tools.go), [revoked-write regression](../../agent_go/cmd/server/crew_builder_tools_test.go).

### 2. High — Installing a Playbook could overwrite customer setup progress. Fixed locally, with a remaining transaction limitation.

The package installer wrote authored `SETUP.json` over the Workflow's saved checklist on reinstall/update. This could erase completed checks and evidence while the customer's Crews continued to exist.

Reinstalling the same ID/version now preserves the saved checklist byte for byte. A version change or unrecognized prior checklist archives the exact previous content under `playbooks/setup-history/` and links it from the new checklist. New-version checks start pending so old evidence can be revalidated. Failure to read or archive the old state stops checklist replacement. The source digest continues to describe the authored package.

This is not a transactional installer or an automatic migration of readiness between versions. Other package files and the manifest are still separate writes; see finding 7.

Evidence: [installer and preservation helper](../../agent_go/cmd/server/playbook_routes.go), [reinstall/update regression](../../agent_go/cmd/server/playbook_routes_test.go).

### 3. High — Website Growth handoff contracts are not shipped as enforced contracts. Open.

The package names `growth-priority-brief/v1`, `search-opportunity-list/v1`, and other outputs. Its handoff guide says invalid artifacts stop the route before the next Crew runs. The generic Crew-step executor saves `FinalResponse` and marks the step complete after a successful Crew run. It does not validate those named artifact schemas. Input parsing also falls back to a string when JSON decoding fails.

Builder can add a deterministic validation step, but the package currently ships prose field descriptions rather than executable schemas and positive/negative output fixtures. A successful Crew invocation therefore does not establish a valid business handoff.

Typed Crew functions already support input/result schema validation. The design document incorrectly described them as future work; that statement is corrected locally. Website Growth still needs to define and wire its contracts through that mechanism or through explicit tested Workflow validators.

Required next step: ship versioned artifact schemas and fixtures; reject invalid output before invoking the consumer; retain producer run ID, consumer run ID, artifact path, and validation result. Demonstrate this with a two-Crew test where malformed output prevents the second call.

Evidence: [handoff promise](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/references/team-and-handoffs.md), [Crew executor](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_crew.go), [typed functions](../../agent_go/cmd/server/crew_functions.go), [function documentation](../crew-calls.md).

### 4. High — “Setup complete” is recorded progress, not verified operational readiness. Partially fixed locally.

Two concrete display/validation defects are fixed: a failed checklist read no longer retains an earlier “Setup complete”; a saved Crew checklist cannot remove a required canonical check and still be accepted.

The larger limitation remains. Crew completion depends on saved check IDs. Website Growth completion additionally requires a nonempty evidence string, but the parser does not verify the linked artifact, current account access, current Crew binding, successful run, or evidence freshness. A disconnected account can leave a historically completed checklist looking ready. Installation also records Playbook status as `draft`; the readiness parser does not implement the proposed persisted lifecycle.

Required next step: keep the simple chat-driven setup experience, backed by a service that records evidence type, resource/run ID, template version, verifier, checked time, and invalidation reason. Recheck required capabilities before a run and block affected steps with a specific explanation. Optional capabilities should remain optional when a documented fallback works.

Evidence: [Crew status component](../../frontend/src/products/work/WorkTemplateSetup.tsx), [Crew parser](../../frontend/src/products/work/crewTemplates.ts), [Automation progress parser](../../frontend/src/components/playbooks/websiteGrowthSetupProgress.ts), [installation status](../../agent_go/cmd/server/playbook_routes.go).

### 5. Medium — Measurement expects an artifact no preceding slot produces. Open.

The optional edge from Content Brief Writer to Traffic & Engagement Analyst declares `shipped-change/v1`. The content slot produces `content-brief/v1`. The package explicitly says a brief is not a published page, and provides no publication/change-record producer for this edge.

Required next step: add a reviewed publication/change-record step, or let the owner supply an actual change log. Measurement should consume the actual URL, shipped timestamp, change description, metric source, and comparable date window. Until that exists, omit this edge and let measurement independently establish the baseline. Do not treat a generated brief as shipped growth work.

Evidence: [slots and handoffs](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/playbook.json), [handoff guide](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/references/team-and-handoffs.md).

### 6. Medium — Individual Crew installation and Automation proposal setup follow different contracts. Open.

The agreed design says choosing an Agent Playbook attaches a proposal and chat applies the reviewed identity/skills. Current Crew creation immediately writes the template files, selects skills, and sets role/purpose. Adding a supporting template also applies its files and selections immediately. Automation Playbook selection copies guidance and leaves team creation to Builder.

This is a product consistency gap; selecting a Crew template does not itself activate recurrence or attach external credentials. We should either implement the proposed common lifecycle or clearly describe individual Crew selection as installing a starter pack with setup still pending. The design and UI should tell the same story.

Evidence: [agreed flows](../design/unified_agent_automation_playbooks.md), [Crew creation and supporting packs](../../frontend/src/products/work/workSessions.ts).

### 7. Medium — Installation is vulnerable to partial failures and concurrent changes. Open; inferred from write ordering.

Workflow installation reads a manifest, writes many package files, then replaces the manifest without an expected revision. Concurrent Builder edits or a second install can be lost; failure between writes can leave a partially updated package. UI Crew creation generates a new UUID on each attempt and writes runtime/template files before the product receipt. An interrupted attempt can leave orphaned files, and retrying is a new creation.

Builder Crew creation has stronger idempotency/recovery behavior. Bring UI creation and Playbook application through an equivalent backend operation with a durable operation ID, expected revision, resumable progress, and explicit completion receipt. Preserve customer edits independently of authored package files. These concurrent-failure cases were identified by inspection, not stress-tested in this review.

Evidence: [Workflow installer](../../agent_go/cmd/server/playbook_routes.go), [UI project creation](../../frontend/src/platform/chat/productProjects.ts), [supporting template selections](../../frontend/src/products/work/workSessions.ts).

### 8. Medium — The catalog UI can browse more templates, but the underlying catalog is still split. Open.

The Crew picker has search, categories, and batches of 12. However, Crew metadata/content is bundled into frontend TypeScript, the Builder loads a generated Website Growth-only catalog, and Workflow Playbooks use a separate catalog. Finance templates visible in the UI are not supported by the Website Growth-specific Builder template loader.

Before expanding to 100+ templates, establish one versioned catalog contract used by frontend and Builder: kind, category, outcome, package version/digest, input requirements, capabilities, checklist definition, output contracts, and availability. Load full package content on selection. This is principally a consistency and maintenance problem; this review did not benchmark 100-template performance.

Evidence: [Crew catalog](../../frontend/src/products/work/crewTemplates.ts), [picker](../../frontend/src/products/work/CreateWorkProjectDialog.tsx), [Builder catalog loader](../../agent_go/cmd/server/crew_website_growth_templates.go), [catalog generator](../../frontend/scripts/sync-website-growth-crew-catalog.mjs).

### 9. Medium — Content checks do not yet establish agent output quality. Open.

The Playbook validator checks v1 package shape, sections, links, and syntax. It does not validate the new slot/handoff graph against output contracts. Passing catalog and package checks therefore cannot catch the measurement mismatch above or prove a specialist produces a useful output.

Required next step: add graph/schema validation and a small representative evaluation set per specialist. For Website Growth, include a new site with no analytics, a site with conflicting audience/offer, incomplete source evidence, revoked analytics access, and an existing suitable Crew that should be reused. Score source traceability, useful prioritization, honest uncertainty, and correct blocking behavior.

Evidence: [package validator](../../playbooks/scripts/validate_playbooks.py), [Website Growth package](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/).

## What is already good

- The Automation Playbook is a proposal. Selection does not provision a team or enable a recurring schedule.
- Website Growth has distinct specialist roles, minimum inputs, first-result descriptions, and a baseline-first route for a new site without useful analytics history.
- Installed Crew packs contain procedures and checklists, with template/version receipts in identity. Supporting packs can extend an existing Crew.
- Builder creation has idempotency receipts, capability preflight, and retry handling. It uses secret references rather than copying credentials into template content.
- Crew execution has caller bindings, timeout handling, run records, and usage attribution. Typed functions already provide a reusable foundation for structured calls.
- The setup entry point follows the intended simple interaction: a status indicator opens chat; the agent saves progress. A separate wizard is not necessary to fix the remaining backend gaps.

## Verification and limits

Completed during this review:

- Targeted server tests for Crew creation, Builder mutations, Playbook installation/search/catalog loading, and Website Growth templates passed.
- Seven focused frontend test files passed: 35 tests, including the new stale-status and missing-check regressions.
- Frontend TypeScript build checking (`npx tsc -b`) passed.
- Targeted typed Crew function tests and Workflow Crew-step executor tests passed.
- Generated catalog check verified all 10 Website Growth Crew agents.
- Package validator passed for 24 Playbook packages.
- `git diff --check` passed.

This review does not establish authenticated end-to-end onboarding, live MCP connection health, real specialist output quality, or recurring execution quality. No deployment was performed as part of this review.

## Recommended implementation order

1. Release the tested authorization and setup-preservation fixes.
2. Implement one complete Website Growth vertical slice: existing/new Crew selection, required capability probes, two validated artifacts, visible blockers, and a manual run with linked evidence.
3. Make readiness a verified state and use it to guard recurring runs. Demonstrate revocation, retry, and recovery behavior.
4. Resolve measurement's missing producer and the individual Crew proposal/apply mismatch.
5. Unify the catalog and harden installation before expanding the template inventory.

The release acceptance case should be concrete: a new company's authorized website goes from proposal to a reviewed two-Crew result; bad output stops the handoff; missing analytics takes the baseline-first route; retry creates no duplicate Crew; completed evidence survives reinstall; recurrence remains paused until the owner chooses it.

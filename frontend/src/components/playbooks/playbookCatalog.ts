export type PlaybookCatalogItem = {
  id: string
  title: string
  description: string
  version: string
  changelog?: PlaybookChangelogEntry[]
  category: string
  order: number
  inputCount: number
  toolCount: number
  teamScope?: 'small_team'
  setupPrompt?: string
  setupInputs?: PlaybookSetupInput[]
  requiredCapabilities?: string[]
  recommendedTools?: PlaybookRecommendedTool[]
  pulseFocus?: PlaybookPulseFocus[]
  outputs?: string[]
  agentSlots?: PlaybookAgentSlot[]
  handoffs?: PlaybookHandoff[]
  setupChecks?: string[]
}

export type PlaybookAgentSlot = { id: string; agent_playbook_id: string; accepts?: string[]; required: boolean; output: string }
export type PlaybookHandoff = { id: string; from: string; to: string; artifact_type: string; required: boolean }

export type PlaybookChangelogEntry = {
  version: string
  summary: string
}

export type PlaybookSetupInput = {
  id: string
  label: string
  required: boolean
  default?: unknown
}

export type PlaybookRecommendedTool = {
  id: string
  name: string
  type: string
  purpose: string
  capability: string
  optional: boolean
}

export type PlaybookPulseFocus = {
  module: 'strategic_review'
  label: string
  focus_areas: string[]
  review_when: string[]
}

// Read-only catalog projection of the first-party playbook manifests. The API
// slice will replace this projection when installation records are introduced.
export const PLAYBOOK_CATALOG: readonly PlaybookCatalogItem[] = [
  { id: 'campaign-signal-to-reviewed-experiment', title: 'Campaign Signal to Reviewed Experiment', description: 'Explain a campaign conversion change from matched spend and CRM evidence, then prepare one bounded experiment plan with an optional sourced competitor context.', version: '0.1.0', category: 'Marketing', order: 1, inputCount: 6, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'performance', agent_playbook_id: 'campaign-performance-analyst', required: true, output: 'campaign-performance-brief/v1' },
    { id: 'competitor', agent_playbook_id: 'competitor-intelligence-analyst', required: false, output: 'competitor-context/v1' },
    { id: 'experiment', agent_playbook_id: 'growth-experiment-planner', required: true, output: 'growth-experiment-plan/v1' },
  ], handoffs: [
    { id: 'performance-to-experiment', from: 'performance', to: 'experiment', artifact_type: 'campaign-performance-brief/v1', required: true },
    { id: 'competitor-to-experiment', from: 'competitor', to: 'experiment', artifact_type: 'competitor-context/v1', required: false },
  ], setupChecks: ['goal_owner', 'campaign_scope', 'metric_policy', 'baseline_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'meeting-decision-to-owned-follow-through', title: 'Meeting Decision to Owned Follow-through', description: 'Turn authorized meeting notes into confirmed, deduplicated actions and a source-observed project status report, with optional leadership review.', version: '0.1.0', category: 'Operations', order: 1, inputCount: 6, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'meeting', agent_playbook_id: 'meeting-actions-coordinator', required: true, output: 'meeting-action-register/v1' },
    { id: 'status', agent_playbook_id: 'project-status-reporter', required: true, output: 'project-action-status/v1' },
    { id: 'review', agent_playbook_id: 'chief-of-staff', required: false, output: 'operations-review-brief/v1' },
  ], handoffs: [
    { id: 'meeting-to-status', from: 'meeting', to: 'status', artifact_type: 'meeting-action-register/v1', required: true },
    { id: 'status-to-review', from: 'status', to: 'review', artifact_type: 'project-action-status/v1', required: false },
  ], setupChecks: ['goal_owner', 'meeting_scope', 'owner_policy', 'status_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'vendor-evaluation-to-purchase-decision', title: 'Vendor Evaluation to Purchase Decision', description: 'Compare exact SaaS vendor plans against requirements and cost, then prepare a current, owner-reviewed purchase decision.', version: '0.1.0', category: 'Operations', order: 2, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'research', agent_playbook_id: 'vendor-researcher', required: true, output: 'vendor-comparison/v1' },
    { id: 'payables', agent_playbook_id: 'spend-payables-coordinator', required: true, output: 'vendor-purchase-review/v1' },
  ], handoffs: [
    { id: 'research-to-payables', from: 'research', to: 'payables', artifact_type: 'vendor-comparison/v1', required: true },
  ], setupChecks: ['goal_owner', 'request_scope', 'requirements_rule', 'commercial_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'order-exception-to-reviewed-update', title: 'Order Exception to Reviewed Update', description: 'Investigate a cross-system order exception and prepare a policy-safe, unsent customer update for the exact support case.', version: '0.1.1', category: 'Operations', order: 3, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'operations', agent_playbook_id: 'order-operations-coordinator', required: true, output: 'order-exception/v1' },
    { id: 'support', agent_playbook_id: 'support-reply-drafter', required: true, output: 'order-customer-update-review/v1' },
  ], handoffs: [
    { id: 'operations-to-support', from: 'operations', to: 'support', artifact_type: 'order-exception/v1', required: true },
  ], setupChecks: ['goal_owner', 'order_scope', 'case_scope', 'state_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'finding-to-verified-remediation', title: 'Finding to Verified Remediation', description: 'Connect an authorized, validated security finding to owned remediation and independent retest of the affected deployed asset.', version: '0.1.0', category: 'Security', order: 1, inputCount: 6, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'finding', agent_playbook_id: 'security-findings-analyst', required: true, output: 'security-finding/v1' },
    { id: 'remediation', agent_playbook_id: 'security-remediation-coordinator', required: true, output: 'security-remediation-ledger/v1' },
  ], handoffs: [
    { id: 'finding-to-remediation', from: 'finding', to: 'remediation', artifact_type: 'security-finding/v1', required: true },
  ], setupChecks: ['scope_owner', 'finding_identity', 'severity_policy', 'remediation_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'access-exception-to-verified-fix', title: 'Access Exception to Verified Fix', description: 'Connect an authorized permission mismatch to an owned fix, deployed build, independent same-cell retest, and closure decision.', version: '0.1.0', category: 'Security', order: 2, inputCount: 6, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'access', agent_playbook_id: 'access-review-analyst', required: true, output: 'access-review-matrix/v1' },
    { id: 'remediation', agent_playbook_id: 'security-remediation-coordinator', required: true, output: 'access-remediation-ledger/v1' },
  ], handoffs: [
    { id: 'access-to-remediation', from: 'access', to: 'remediation', artifact_type: 'access-review-matrix/v1', required: true },
  ], setupChecks: ['scope_owner', 'policy_matrix', 'isolated_identity', 'direct_observation', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'release-candidate-to-reviewed-gate', title: 'Release Candidate to Reviewed Gate', description: 'Join required journey attempts and optional flake investigation to an exact-build release quality decision with separate status publication evidence.', version: '0.1.0', category: 'QA', order: 1, inputCount: 6, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'journey', agent_playbook_id: 'browser-journey-qa-analyst', required: true, output: 'journey-result/v1' },
    { id: 'flake', agent_playbook_id: 'flaky-test-investigator', required: false, output: 'flake-investigation/v1' },
    { id: 'gate', agent_playbook_id: 'release-quality-assistant', required: true, output: 'release-quality-brief/v1' },
  ], handoffs: [
    { id: 'journey-to-gate', from: 'journey', to: 'gate', artifact_type: 'journey-result/v1', required: true },
    { id: 'journey-to-flake', from: 'journey', to: 'flake', artifact_type: 'journey-result/v1', required: false },
    { id: 'flake-to-gate', from: 'flake', to: 'gate', artifact_type: 'flake-investigation/v1', required: false },
  ], setupChecks: ['goal_owner', 'candidate_scope', 'suite_policy', 'evidence_rules', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'support-case-to-reviewed-resolution', title: 'Support Case to Reviewed Resolution', description: 'Route an authorized customer case through sourced triage, a grounded unsent reply or owned escalation, and provider-backed follow-through.', version: '0.1.0', category: 'Customer Support', order: 1, inputCount: 6, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'triage', agent_playbook_id: 'support-triage-assistant', required: true, output: 'support-case-triage/v1' },
    { id: 'reply', agent_playbook_id: 'support-reply-drafter', required: true, output: 'support-reply-draft/v1' },
    { id: 'escalation', agent_playbook_id: 'escalation-coordinator', required: false, output: 'support-escalation-brief/v1' },
  ], handoffs: [
    { id: 'triage-to-reply', from: 'triage', to: 'reply', artifact_type: 'support-case-triage/v1', required: true },
    { id: 'triage-to-escalation', from: 'triage', to: 'escalation', artifact_type: 'support-case-triage/v1', required: false },
    { id: 'escalation-to-reply', from: 'escalation', to: 'reply', artifact_type: 'support-escalation-brief/v1', required: false },
  ], setupChecks: ['goal_owner', 'case_scope', 'priority_policy', 'contact_knowledge', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'case-pattern-to-knowledge-review', title: 'Case Pattern to Knowledge Review', description: 'Turn a verified repeated support question into a privacy-safe, owner-reviewed help article draft without claiming publication or deflection.', version: '0.1.0', category: 'Customer Support', order: 2, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'triage', agent_playbook_id: 'support-triage-assistant', required: true, output: 'support-case-pattern/v1' },
    { id: 'knowledge', agent_playbook_id: 'support-knowledge-curator', required: true, output: 'support-knowledge-update/v1' },
  ], handoffs: [
    { id: 'triage-to-knowledge', from: 'triage', to: 'knowledge', artifact_type: 'support-case-pattern/v1', required: true },
  ], setupChecks: ['goal_owner', 'subject_scope', 'source_policy', 'decision_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'order-exception-to-resolution', title: 'Order Exception to Resolution', description: 'Connect an order or fulfillment problem to a return or refund request, then prepare an approved action and verify the resulting store and customer state.', version: '0.1.1', category: 'Shopify', order: 1, inputCount: 5, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'store_operations', agent_playbook_id: 'store-operations-coordinator', required: true, output: 'store-order-exception/v1' },
    { id: 'returns_refunds', agent_playbook_id: 'returns-refunds-coordinator', required: true, output: 'return-resolution-review/v1' },
  ], handoffs: [
    { id: 'order-to-returns', from: 'store_operations', to: 'returns_refunds', artifact_type: 'store-order-exception/v1', required: true },
  ], setupChecks: ['goal_owner', 'store_order_scope', 'policy_money_rules', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'action_ledger', 'activation_choice'] },
  { id: 'storefront-opportunity-to-verified-change', title: 'Storefront Opportunity to Verified Change', description: 'Find a product-page or variant issue, prepare a merchant-reviewed catalog correction, then verify the shipped change and comparable shopper evidence.', version: '0.1.0', category: 'Shopify', order: 2, inputCount: 5, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'growth', agent_playbook_id: 'shopify-growth-analyst', required: true, output: 'shopify-growth-opportunity/v1' },
    { id: 'catalog', agent_playbook_id: 'catalog-merchandising-analyst', required: true, output: 'catalog-change-review/v1' },
  ], handoffs: [
    { id: 'growth-to-catalog', from: 'growth', to: 'catalog', artifact_type: 'shopify-growth-opportunity/v1', required: true },
  ], setupChecks: ['goal_owner', 'store_market_scope', 'metric_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'change_ledger', 'activation_choice'] },
  { id: 'inventory-availability-to-owner-action', title: 'Inventory Availability to Owner Action', description: 'Investigate a Shopify variant availability mismatch, prepare a location-aware merchant action, and verify the resulting inventory or storefront state.', version: '0.1.0', category: 'Shopify', order: 3, inputCount: 5, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'catalog', agent_playbook_id: 'catalog-merchandising-analyst', required: true, output: 'inventory-availability-exception/v1' },
    { id: 'operations', agent_playbook_id: 'store-operations-coordinator', required: true, output: 'inventory-action-review/v1' },
  ], handoffs: [
    { id: 'catalog-to-operations', from: 'catalog', to: 'operations', artifact_type: 'inventory-availability-exception/v1', required: true },
  ], setupChecks: ['goal_owner', 'variant_location_scope', 'inventory_authority', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'action_ledger', 'activation_choice'] },
  { id: 'payment-exception-to-order-decision', title: 'Payment Exception to Order Decision', description: 'Investigate an order payment exception, decide whether fulfillment must wait or can be reviewed for release, and verify any approved action.', version: '0.1.0', category: 'Shopify', order: 4, inputCount: 5, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'payments', agent_playbook_id: 'payment-operations-investigator', required: true, output: 'payment-exception/v1' },
    { id: 'operations', agent_playbook_id: 'store-operations-coordinator', required: true, output: 'payment-order-decision/v1' },
  ], handoffs: [
    { id: 'payment-to-operations', from: 'payments', to: 'operations', artifact_type: 'payment-exception/v1', required: true },
  ], setupChecks: ['goal_owner', 'order_transaction_scope', 'payment_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'action_ledger', 'activation_choice'] },
  { id: 'product-launch-readiness-to-go-no-go', title: 'Product Launch Readiness to Go/No-Go', description: 'Check a Shopify product and market publication, test the buyer journey, prepare a merchant go/no-go decision, and verify any approved launch.', version: '0.1.0', category: 'Shopify', order: 5, inputCount: 5, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'catalog', agent_playbook_id: 'catalog-merchandising-analyst', required: true, output: 'launch-catalog-readiness/v1' },
    { id: 'growth', agent_playbook_id: 'shopify-growth-analyst', required: true, output: 'launch-storefront-decision/v1' },
  ], handoffs: [
    { id: 'catalog-to-growth', from: 'catalog', to: 'growth', artifact_type: 'launch-catalog-readiness/v1', required: true },
  ], setupChecks: ['goal_owner', 'launch_scope', 'catalog_standards', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'launch_ledger', 'activation_choice'] },
  { id: 'checkout-signal-to-reviewed-recovery', title: 'Checkout Signal to Reviewed Recovery', description: 'Turn a Shopify abandoned checkout signal into a contact-policy review and an unsent recovery draft, with separate proof for any later send or order.', version: '0.1.0', category: 'Shopify', order: 6, inputCount: 5, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'growth', agent_playbook_id: 'shopify-growth-analyst', required: true, output: 'checkout-recovery-signal/v1' },
    { id: 'recovery', agent_playbook_id: 'checkout-recovery-coordinator', required: true, output: 'checkout-recovery-review/v1' },
  ], handoffs: [
    { id: 'signal-to-recovery', from: 'growth', to: 'recovery', artifact_type: 'checkout-recovery-signal/v1', required: true },
  ], setupChecks: ['goal_owner', 'checkout_scope', 'contact_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'contact_ledger', 'activation_choice'] },
  { id: 'inventory-risk-to-reviewed-replenishment', title: 'Inventory Risk to Reviewed Replenishment', description: 'Turn a Shopify item and location stock risk into a supplier-aware reorder proposal, then distinguish purchase-order status from actual receipt.', version: '0.1.0', category: 'Shopify', order: 7, inputCount: 5, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'catalog', agent_playbook_id: 'catalog-merchandising-analyst', required: true, output: 'inventory-availability-exception/v1' },
    { id: 'procurement', agent_playbook_id: 'replenishment-planner', required: true, output: 'replenishment-review/v1' },
  ], handoffs: [
    { id: 'inventory-to-replenishment', from: 'catalog', to: 'procurement', artifact_type: 'inventory-availability-exception/v1', required: true },
  ], setupChecks: ['goal_owner', 'item_location_scope', 'inventory_demand_policy', 'supplier_terms', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'incident-to-verified-recovery', title: 'Incident to Verified Recovery', description: 'Coordinate an incident investigation, an owned fix or rollback decision, and evidence that the affected service recovered.', version: '0.1.0', category: 'Engineering', order: 1, inputCount: 5, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'investigation', agent_playbook_id: 'incident-investigator', required: true, output: 'incident-investigation/v1' },
    { id: 'delivery', agent_playbook_id: 'engineering-delivery-coordinator', required: true, output: 'engineering-blocker-ledger/v1' },
  ], handoffs: [
    { id: 'investigation-to-delivery', from: 'investigation', to: 'delivery', artifact_type: 'incident-investigation/v1', required: true },
  ], setupChecks: ['goal_owner', 'incident_scope', 'source_access', 'recovery_rule', 'team_bindings', 'handoff_contract', 'plan_review', 'test_run', 'action_ledger', 'activation_choice'] },
  { id: 'launch-to-qualified-pipeline', title: 'Launch to Qualified Pipeline', description: 'Take an approved B2B offer from a sourced launch brief through observed campaign and lead signals to reviewed qualification and pipeline outcomes.', version: '0.1.0', category: 'GTM', order: 1, inputCount: 6, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'strategy', agent_playbook_id: 'gtm-strategy-analyst', required: true, output: 'gtm-launch-brief/v1' },
    { id: 'launch', agent_playbook_id: 'launch-coordinator', required: true, output: 'launch-signal-register/v1' },
    { id: 'website', agent_playbook_id: 'website-growth-starter', required: false, output: 'growth-priority-brief/v1' },
    { id: 'qualification', agent_playbook_id: 'lead-intake-qualifier', required: true, output: 'lead-qualification-brief/v1' },
    { id: 'followup', agent_playbook_id: 'sales-followup-coordinator', required: true, output: 'sales-followup-draft/v1' },
  ], handoffs: [
    { id: 'strategy-to-launch', from: 'strategy', to: 'launch', artifact_type: 'gtm-launch-brief/v1', required: true },
    { id: 'launch-to-qualification', from: 'launch', to: 'qualification', artifact_type: 'launch-signal-register/v1', required: true },
    { id: 'qualification-to-followup', from: 'qualification', to: 'followup', artifact_type: 'lead-qualification-brief/v1', required: true },
  ], setupChecks: ['goal_owner', 'offer_claims', 'launch_scope', 'metric_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'offer-to-seller-readiness', title: 'Offer to Seller Readiness', description: 'Join an owner-accepted priced SaaS offer to current, approved seller guidance without making unsupported claims or sending outreach.', version: '0.1.0', category: 'GTM', order: 2, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'pricing', agent_playbook_id: 'pricing-packaging-analyst', required: true, output: 'gtm-offer-decision/v1' },
    { id: 'enablement', agent_playbook_id: 'sales-enablement-coordinator', required: true, output: 'gtm-enablement-brief/v1' },
  ], handoffs: [
    { id: 'pricing-to-enablement', from: 'pricing', to: 'enablement', artifact_type: 'gtm-offer-decision/v1', required: true },
  ], setupChecks: ['goal_owner', 'subject_scope', 'source_policy', 'decision_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'launch-signal-to-pipeline-review', title: 'Launch Signal to Pipeline Review', description: 'Reconcile an observed launch campaign with deduplicated form, lead and opportunity records under one attribution policy.', version: '0.1.0', category: 'GTM', order: 3, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'launch', agent_playbook_id: 'launch-coordinator', required: true, output: 'launch-signal-register/v1' },
    { id: 'revenue', agent_playbook_id: 'revenue-operations-analyst', required: true, output: 'gtm-funnel-reconciliation/v1' },
  ], handoffs: [
    { id: 'launch-to-revenue', from: 'launch', to: 'revenue', artifact_type: 'launch-signal-register/v1', required: true },
  ], setupChecks: ['goal_owner', 'subject_scope', 'source_policy', 'decision_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: "basic-browser-setup", title: "Basic Browser Setup", description: "Configure browser access and Playwright, save verified locators, capture video plus console/network evidence, and establish reporting.", version: "0.9.0", changelog: [{"version":"0.9.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.8.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.7.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Browser QA", order: 1, inputCount: 7, toolCount: 4, teamScope: "small_team" },
  { id: "authentication-session-validation", title: "Authentication and Session Validation", description: "Validate login, logout, MFA, recovery, expiry, refresh, and invalid-session behavior with durable evidence.", version: "0.4.0", changelog: [{"version":"0.4.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.3.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.2.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Browser QA", order: 2, inputCount: 6, toolCount: 3, teamScope: "small_team" },
  { id: "role-permission-validation", title: "Role and Permission Validation", description: "Validate page, action, and data permissions across roles, ownership states, and tenant boundaries.", version: "0.4.0", changelog: [{"version":"0.4.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.3.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.2.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Browser QA", order: 3, inputCount: 5, toolCount: 3, teamScope: "small_team" },
  { id: "critical-journey-validation", title: "Critical Journey Validation", description: "Reuse browser setup and locator helpers to run critical journeys, investigate failures, and retain video plus console/network evidence.", version: "0.9.0", changelog: [{"version":"0.9.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.8.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.7.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Browser QA", order: 4, inputCount: 5, toolCount: 6, teamScope: "small_team" },
  { id: "flaky-test-detection-stabilization", title: "Flaky-Test Detection and Stabilization", description: "Detect inconsistent browser-test outcomes through bounded repeated runs and verify reviewed stabilization without hiding failures.", version: "0.4.0", changelog: [{"version":"0.4.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.3.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.2.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Browser QA", order: 5, inputCount: 6, toolCount: 3, teamScope: "small_team" },
  { id: "browser-test-self-healing", title: "Browser Test Self-Healing", description: "Diagnose browser-test failures with preserved video and console/network evidence, verify test-only repairs, obtain review, and update canonical tests and knowledge.", version: "0.6.0", changelog: [{"version":"0.6.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.5.1","summary":"Makes the Report dashboard the primary asynchronous review surface, with Ask in chat for discussion and separate durable approval and application."},{"version":"0.5.0","summary":"Separates repair preparation from application: verified candidates become durable pending reviews, and a later action route validates approval and current source state before applying."},{"version":"0.4.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Browser QA", order: 6, inputCount: 7, toolCount: 5, teamScope: "small_team" },
  { id: "scheduled-regression-synthetic-monitoring", title: "Scheduled Regression and Synthetic Monitoring", description: "Run approved browser journeys on a schedule, retain comparable history, and notify only on actionable changes.", version: "0.4.0", changelog: [{"version":"0.4.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.3.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.2.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Browser QA", order: 7, inputCount: 6, toolCount: 3, teamScope: "small_team" },
  { id: "release-pr-quality-gate", title: "Release and PR Quality Gate", description: "Bind an exact change or build to required Browser QA suites and publish an auditable pass, fail, or needs-review decision.", version: "0.4.0", changelog: [{"version":"0.4.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.3.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.2.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Browser QA", order: 8, inputCount: 6, toolCount: 3, teamScope: "small_team" },
  { id: 'engineering-operations-intelligence', title: 'Engineering Operations Intelligence', description: 'Reconcile one governed team engineering metric, then prepare an owned improvement review with current issue evidence.', version: '0.2.0', changelog: [{ version: '0.2.0', summary: 'Promotes the workflow guide to a chat-led Engineering Operations Analyst to Delivery Coordinator proposal with typed metric/action handoff, pending setup and executable contract checks.' }, { version: '0.1.0', summary: 'Combines engineering data foundation, delivery/quality/reliability intelligence, and recurring operations review into one small-team workflow playbook.' }], category: 'Engineering Operations Intelligence', order: 1, inputCount: 6, toolCount: 7, teamScope: 'small_team', agentSlots: [
    { id: 'analysis', agent_playbook_id: 'engineering-operations-analyst', required: true, output: 'engineering-metric-observation/v1' },
    { id: 'delivery', agent_playbook_id: 'engineering-delivery-coordinator', required: true, output: 'engineering-improvement-review/v1' },
  ], handoffs: [
    { id: 'analysis-to-delivery', from: 'analysis', to: 'delivery', artifact_type: 'engineering-metric-observation/v1', required: true },
  ], setupChecks: ['goal_owner', 'team_scope', 'metric_rule', 'comparison_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'manual_test', 'activation_choice'] },
  { id: 'cost-anomaly-to-verified-savings', title: 'Cost Anomaly to Verified Savings', description: 'Connect a sourced cloud-cost change to a reviewed engineering action and a finance-verified savings outcome, keeping estimates separate from realized billed results.', version: '0.6.0', changelog: [{ version: '0.6.0', summary: 'Adds a chat-led Cost, Delivery and Finance Crew route with exact cost/change/savings artifacts, pending setup and executable verification checks.' }, { version: '0.5.0', summary: 'Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split.' }, { version: '0.4.0', summary: 'Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state.' }, { version: '0.3.0', summary: 'Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval.' }], category: 'FinOps', order: 1, inputCount: 6, toolCount: 7, teamScope: 'small_team', agentSlots: [
    { id: 'cost', agent_playbook_id: 'cloud-cost-analyst', required: true, output: 'cloud-cost-review/v1' },
    { id: 'delivery', agent_playbook_id: 'engineering-delivery-coordinator', required: true, output: 'cloud-change-review/v1' },
    { id: 'finance', agent_playbook_id: 'finance-analyst', required: true, output: 'cloud-savings-readout/v1' },
  ], handoffs: [
    { id: 'cost-to-delivery', from: 'cost', to: 'delivery', artifact_type: 'cloud-cost-review/v1', required: true },
    { id: 'delivery-to-finance', from: 'delivery', to: 'finance', artifact_type: 'cloud-change-review/v1', required: true },
  ], setupChecks: ['goal_owner', 'billing_scope', 'cost_metric_rule', 'change_health_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'manual_test', 'activation_choice'] },
  { id: "browser-performance-validation", title: "Browser Performance Validation", description: "Measure approved browser pages and journeys against customer-defined budgets with comparable samples and durable diagnostics.", version: "0.5.0", changelog: [{"version":"0.5.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.4.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.3.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."},{"version":"0.2.1","summary":"Adds workflow goal, metric, and workflow-boundary guidance plus Technical, Architecture, and Strategic Pulse focus recommendations."}], category: "Performance Engineering", order: 1, inputCount: 6, toolCount: 3, teamScope: "small_team" },
  { id: "api-performance-validation", title: "API Performance Validation", description: "Measure approved API scenarios under bounded load against customer-defined latency, throughput, error, and capacity policies.", version: "0.4.0", changelog: [{"version":"0.4.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.3.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.2.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Performance Engineering", order: 2, inputCount: 7, toolCount: 3, teamScope: "small_team" },
  { id: "ci-deployment-failure-triage", title: "CI and Deployment Failure Triage", description: "Ingest CI and deployment failures, classify likely cause with evidence, perform safe policy actions, and route the result.", version: "0.4.0", changelog: [{"version":"0.4.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.3.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.2.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Reliability Operations", order: 1, inputCount: 5, toolCount: 4, teamScope: "small_team" },
  { id: "incident-investigation-coordination", title: "Incident Investigation and Coordination", description: "Receive reliability errors, correlate incident signals, perform basic evidence-backed RCA, establish impact and severity, and coordinate current status.", version: "0.5.0", changelog: [{"version":"0.5.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.4.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.3.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Reliability Operations", order: 2, inputCount: 5, toolCount: 4, teamScope: "small_team" },
  { id: "governed-remediation-recovery", title: "Governed Remediation and Recovery", description: "Prepare exact operational remediation, validate and approve it, execute through authorized paths, and verify sustained recovery or rollback.", version: "0.4.0", changelog: [{"version":"0.4.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.3.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.2.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Reliability Operations", order: 3, inputCount: 5, toolCount: 4, teamScope: "small_team" },
  { id: "post-incident-review-actions", title: "Post-Incident Review and Actions", description: "Reconstruct one stabilized incident from frozen sources, propose a blameless review, then track accepted improvements to independently verified completion.", version: "0.5.0", changelog: [{"version":"0.5.0","summary":"Adds a chat-led Post-Incident Reviewer to Improvement Follow-Through Coordinator route, review/action evidence and executable completion checks."},{"version":"0.4.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.3.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.2.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Reliability Operations", order: 4, inputCount: 7, toolCount: 5, teamScope: "small_team", agentSlots: [
    { id: 'review', agent_playbook_id: 'post-incident-reviewer', required: true, output: 'post-incident-review/v1' },
    { id: 'follow_through', agent_playbook_id: 'improvement-follow-through-coordinator', required: true, output: 'incident-improvement-register/v1' },
  ], handoffs: [
    { id: 'review-to-follow-through', from: 'review', to: 'follow_through', artifact_type: 'post-incident-review/v1', required: true },
  ], setupChecks: ['goal_owner', 'incident_scope', 'impact_rule', 'review_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'manual_test', 'activation_choice'] },
  { id: "application-security-assessment-remediation", title: "Application Security Assessment and Remediation", description: "Run authorized web, API, code, dependency, secret, and configuration assessment through reviewed remediation and verified closure.", version: "0.5.0", changelog: [{"version":"0.5.0","summary":"Scopes setup to small engineering teams and defaults to one workflow unless access or lifecycle boundaries require a split."},{"version":"0.4.0","summary":"Defaults human approvals to durable asynchronous review: preparation ends pending review and a later action validates the recorded decision and current state."},{"version":"0.3.0","summary":"Adds an explicit inspect-first discovery step, focused customer questions, recorded user direction, and a clear boundary that installation is not approval."}], category: "Security Engineering", order: 1, inputCount: 6, toolCount: 14, teamScope: "small_team" },
  { id: 'growth-data-foundation', title: 'Growth Data Foundation', description: 'Connect and normalize traffic, product, billing, and feedback data with durable customer identity and provenance.', version: '0.2.0', category: 'Growth Analytics', order: 1, inputCount: 6, toolCount: 4, teamScope: 'small_team' },
  { id: 'funnel-conversion-intelligence', title: 'Funnel and Conversion Intelligence', description: 'Turn an authorized signup-to-paid funnel change into a reconciled cohort observation and a bounded, owner-reviewed experiment proposal.', version: '0.2.0', changelog: [{ version: '0.2.0', summary: 'Adds a chat-led Funnel Analyst to Growth Experiment Planner handoff, reconciled cohort and stage-count artifacts, pending setup and executable checks.' }, { version: '0.1.0', summary: 'Initial playbook release.' }], category: 'Growth Analytics', order: 2, inputCount: 7, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'funnel', agent_playbook_id: 'funnel-analyst', required: true, output: 'funnel-observation/v1' },
    { id: 'experiment', agent_playbook_id: 'growth-experiment-planner', required: true, output: 'funnel-experiment-plan/v1' },
  ], handoffs: [
    { id: 'funnel-to-experiment', from: 'funnel', to: 'experiment', artifact_type: 'funnel-observation/v1', required: true },
  ], setupChecks: ['goal_owner', 'funnel_definition', 'identity_source', 'comparison_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'manual_test', 'activation_choice'] },
  { id: 'activation-retention-intelligence', title: 'Activation and Retention Intelligence', description: 'Compare mature SaaS signup cohorts under one activation and retention rule, then propose one owner-reviewed retention experiment without treating correlation as cause.', version: '0.2.0', changelog: [{ version: '0.2.0', summary: 'Adds a chat-led Lifecycle Analyst to Growth Experiment Planner route with maturity-aware cohort evidence, pending setup and executable handoff checks.' }, { version: '0.1.0', summary: 'Initial playbook release.' }], category: 'Growth Analytics', order: 3, inputCount: 7, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'lifecycle', agent_playbook_id: 'lifecycle-analyst', required: true, output: 'cohort-retention-observation/v1' },
    { id: 'experiment', agent_playbook_id: 'growth-experiment-planner', required: true, output: 'retention-experiment-plan/v1' },
  ], handoffs: [
    { id: 'lifecycle-to-experiment', from: 'lifecycle', to: 'experiment', artifact_type: 'cohort-retention-observation/v1', required: true },
  ], setupChecks: ['goal_owner', 'cohort_policy', 'maturity_rule', 'identity_source', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'manual_test', 'activation_choice'] },
  { id: 'growth-experimentation-follow-through', title: 'Growth Experimentation and Follow-Through', description: 'Track one owner-approved growth experiment from exact provider launch evidence to a guarded, reproducible outcome readout; leave underpowered results inconclusive.', version: '0.3.0', changelog: [{ version: '0.3.0', summary: 'Adds a chat-led Experiment Run Coordinator to Growth Outcome Analyst route with frozen policy, launch receipts, inconclusive readouts and executable checks.' }, { version: '0.2.0', summary: 'Accepts findings from SEO and AI Visibility Intelligence in addition to funnel and lifecycle sources.' }, { version: '0.1.0', summary: 'Initial playbook release.' }], category: 'Growth Analytics', order: 4, inputCount: 7, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'run', agent_playbook_id: 'experiment-run-coordinator', required: true, output: 'experiment-execution-record/v1' },
    { id: 'outcome', agent_playbook_id: 'growth-outcome-analyst', required: true, output: 'experiment-outcome-readout/v1' },
  ], handoffs: [
    { id: 'run-to-outcome', from: 'run', to: 'outcome', artifact_type: 'experiment-execution-record/v1', required: true },
  ], setupChecks: ['goal_owner', 'frozen_plan', 'approval_rule', 'provider_scope', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'manual_test', 'activation_choice'] },
  { id: 'seo-intelligence', title: 'SEO Intelligence', description: 'Coordinate a technical SEO analyst and buyer-question mapper to find source-backed search opportunities on an approved site, with review before any page change.', version: '0.2.0', category: 'Growth Analytics', order: 5, inputCount: 6, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'technical_seo', agent_playbook_id: 'seo-analyst', required: true, output: 'seo-issue-list/v1' },
    { id: 'search', agent_playbook_id: 'search-opportunity-mapper', required: true, output: 'seo-opportunity-list/v1' },
  ], handoffs: [
    { id: 'technical-to-search', from: 'technical_seo', to: 'search', artifact_type: 'seo-issue-list/v1', required: true },
  ], setupChecks: ['goal_owner', 'site_scope', 'buyer_metric_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'manual_test', 'action_ledger', 'activation_choice'] },
  { id: 'ai-visibility-intelligence', title: 'AI Visibility Intelligence', description: 'Turn a bounded, repeatable AI-answer citation sample into a sourced buyer-question opportunity, with owner review before any page change.', version: '0.2.0', changelog: [{ version: '0.2.0', summary: 'Adds a chat-led AI Visibility Analyst to Search Opportunity Mapper handoff, sample-level citations, pending setup and executable checks.' }, { version: '0.1.0', summary: 'Initial playbook release.' }], category: 'Growth Analytics', order: 6, inputCount: 6, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'visibility', agent_playbook_id: 'ai-visibility-analyst', required: true, output: 'ai-visibility-snapshot/v1' },
    { id: 'opportunity', agent_playbook_id: 'search-opportunity-mapper', required: true, output: 'ai-citation-opportunity/v1' },
  ], handoffs: [
    { id: 'visibility-to-opportunity', from: 'visibility', to: 'opportunity', artifact_type: 'ai-visibility-snapshot/v1', required: true },
  ], setupChecks: ['goal_owner', 'question_scope', 'sample_method', 'citation_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'manual_test', 'activation_choice'] },
  { id: 'inbound-lead-to-meeting-review', title: 'Inbound Lead-to-Meeting Review', description: 'Qualify inbound enquiries, offer a reviewed booking path, and track provider-confirmed delivery and meetings.', version: '0.2.0', category: 'Sales', order: 1, inputCount: 7, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'qualification', agent_playbook_id: 'lead-intake-qualifier', required: true, output: 'lead-qualification-brief/v1' },
    { id: 'research', agent_playbook_id: 'account-researcher', required: false, output: 'account-research-brief/v1' },
    { id: 'followup', agent_playbook_id: 'sales-followup-coordinator', required: true, output: 'sales-followup-draft/v1' },
  ], handoffs: [
    { id: 'qualification-to-followup', from: 'qualification', to: 'followup', artifact_type: 'lead-qualification-brief/v1', required: true },
    { id: 'qualification-to-research', from: 'qualification', to: 'research', artifact_type: 'lead-qualification-brief/v1', required: false },
    { id: 'research-to-followup', from: 'research', to: 'followup', artifact_type: 'account-research-brief/v1', required: false },
  ], setupChecks: ['goal_owner', 'source_scope', 'policy_metric', 'team_bindings', 'access', 'handoff_contract', 'plan_review', 'test_run', 'action_ledger', 'activation_choice'] },
  { id: 'trial-account-to-reviewed-sales-assist', title: 'Trial Account to Reviewed Sales Assist', description: 'Review one SaaS trial account\'s sourced product use, then prepare a permitted seller assist or an explicit no-contact decision.', version: '0.1.0', category: 'Sales', order: 4, inputCount: 6, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'adoption', agent_playbook_id: 'product-adoption-analyst', required: true, output: 'trial-usage-observation/v1' },
    { id: 'sales', agent_playbook_id: 'sales-followup-coordinator', required: true, output: 'trial-sales-assist/v1' },
  ], handoffs: [
    { id: 'adoption-to-sales', from: 'adoption', to: 'sales', artifact_type: 'trial-usage-observation/v1', required: true },
  ], setupChecks: ['goal_owner', 'trial_scope', 'usage_rule', 'contact_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'new-customer-to-first-value', title: 'New Customer to First Value', description: 'Coordinate onboarding milestones and observed product adoption so a new B2B customer reaches an agreed first result.', version: '0.2.0', category: 'Customer Success', order: 1, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'onboarding', agent_playbook_id: 'customer-onboarding-coordinator', required: true, output: 'onboarding-milestone-register/v1' },
    { id: 'adoption', agent_playbook_id: 'product-adoption-analyst', required: true, output: 'first-value-readout/v1' },
    { id: 'health', agent_playbook_id: 'customer-health-coordinator', required: false, output: 'customer-health-brief/v1' },
  ], handoffs: [
    { id: 'onboarding-to-adoption', from: 'onboarding', to: 'adoption', artifact_type: 'onboarding-milestone-register/v1', required: true },
    { id: 'adoption-to-health', from: 'adoption', to: 'health', artifact_type: 'first-value-readout/v1', required: false },
  ], setupChecks: ['goal_owner', 'account_scope', 'first_value_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'action_ledger', 'activation_choice'] },
  { id: 'signed-deal-to-onboarding-handoff', title: 'Signed Deal to Onboarding Handoff', description: 'Verify one signed SaaS deal and have Customer Success accept its exact scope and first-value promise before onboarding starts.', version: '0.1.0', category: 'Customer Success', order: 2, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'sales', agent_playbook_id: 'deal-follow-through-coordinator', required: true, output: 'sales-cs-handoff/v1' },
    { id: 'onboarding', agent_playbook_id: 'customer-onboarding-coordinator', required: true, output: 'onboarding-acceptance/v1' },
  ], handoffs: [
    { id: 'sales-to-onboarding', from: 'sales', to: 'onboarding', artifact_type: 'sales-cs-handoff/v1', required: true },
  ], setupChecks: ['goal_owner', 'deal_scope', 'agreement_policy', 'first_value_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'renewal-risk-to-owned-decision', title: 'Renewal Risk to Owned Decision', description: 'Join bounded customer-health evidence to current SaaS contract and billing terms for an owner-reviewed renewal decision.', version: '0.1.0', category: 'Customer Success', order: 3, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'health', agent_playbook_id: 'customer-health-coordinator', required: true, output: 'renewal-health-brief/v1' },
    { id: 'renewal', agent_playbook_id: 'renewal-coordinator', required: true, output: 'renewal-decision-register/v1' },
  ], handoffs: [
    { id: 'health-to-renewal', from: 'health', to: 'renewal', artifact_type: 'renewal-health-brief/v1', required: true },
  ], setupChecks: ['goal_owner', 'account_scope', 'contract_policy', 'health_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'finance-operations-review', title: 'Finance Operations Review', description: 'Coordinate billing exceptions and a sourced finance impact review, with optional accounting close and payables specialists.', version: '0.1.0', category: 'Finance', order: 1, inputCount: 6, toolCount: 4, teamScope: 'small_team', agentSlots: [
    { id: 'billing', agent_playbook_id: 'billing-operations-coordinator', required: true, output: 'billing-exception-queue/v1' },
    { id: 'finance', agent_playbook_id: 'finance-analyst', required: true, output: 'finance-impact-readout/v1' },
    { id: 'close', agent_playbook_id: 'revenue-close-analyst', required: false, output: 'revenue-close-exceptions/v1' },
    { id: 'payables', agent_playbook_id: 'spend-payables-coordinator', required: false, output: 'payables-exception-queue/v1' },
  ], handoffs: [
    { id: 'billing-to-finance', from: 'billing', to: 'finance', artifact_type: 'billing-exception-queue/v1', required: true },
  ], setupChecks: ['goal_owner', 'source_scope', 'policy_metric', 'team_bindings', 'access', 'handoff_contract', 'plan_review', 'test_run', 'action_ledger', 'activation_choice'] },
  { id: 'invoice-intake-to-reviewed-payable', title: 'Invoice Intake to Reviewed Payable', description: 'Extract an authorized vendor invoice, reconcile it against current payables, and prepare an owner-reviewed decision without posting or paying it.', version: '0.1.0', category: 'Finance', order: 2, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'intake', agent_playbook_id: 'document-intake-assistant', required: true, output: 'document-intake-record/v1' },
    { id: 'payables', agent_playbook_id: 'spend-payables-coordinator', required: true, output: 'payable-review/v1' },
  ], handoffs: [
    { id: 'intake-to-payables', from: 'intake', to: 'payables', artifact_type: 'document-intake-record/v1', required: true },
  ], setupChecks: ['goal_owner', 'document_scope', 'invoice_schema', 'duplicate_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'refund-request-to-reconciled-outcome', title: 'Refund Request to Reconciled Outcome', description: 'Review a SaaS customer refund against the exact payment and policy, then verify its provider and finance outcome without an implicit money action.', version: '0.1.0', category: 'Finance', order: 3, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'billing', agent_playbook_id: 'billing-operations-coordinator', required: true, output: 'refund-decision/v1' },
    { id: 'close', agent_playbook_id: 'revenue-close-analyst', required: true, output: 'refund-reconciliation/v1' },
  ], handoffs: [
    { id: 'billing-to-close', from: 'billing', to: 'close', artifact_type: 'refund-decision/v1', required: true },
  ], setupChecks: ['goal_owner', 'request_scope', 'policy_amount', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'action_ledger', 'activation_choice'] },
  { id: 'subscription-receivable-to-verified-outcome', title: 'Subscription Receivable to Verified Outcome', description: 'Investigate one overdue or failed-payment subscription invoice, prepare policy-safe follow-up, and verify collection without confusing a payment with a bank deposit.', version: '0.1.0', category: 'Finance', order: 4, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'billing', agent_playbook_id: 'billing-operations-coordinator', required: true, output: 'receivable-review/v1' },
    { id: 'finance', agent_playbook_id: 'finance-analyst', required: true, output: 'receivable-outcome/v1' },
  ], handoffs: [
    { id: 'billing-to-finance', from: 'billing', to: 'finance', artifact_type: 'receivable-review/v1', required: true },
  ], setupChecks: ['goal_owner', 'invoice_scope', 'balance_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'action_ledger', 'activation_choice'] },
  { id: 'dispute-to-reconciled-outcome', title: 'Dispute to Reconciled Outcome', description: 'Review one payment dispute, then verify the actual provider decision and finance treatment without an implicit evidence submission.', version: '0.1.0', category: 'Finance', order: 5, inputCount: 6, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'dispute', agent_playbook_id: 'billing-operations-coordinator', required: true, output: 'dispute-case/v1' },
    { id: 'finance', agent_playbook_id: 'revenue-close-analyst', required: true, output: 'dispute-finance-review/v1' },
  ], handoffs: [
    { id: 'dispute-to-finance', from: 'dispute', to: 'finance', artifact_type: 'dispute-case/v1', required: true },
  ], setupChecks: ['goal_owner', 'case_scope', 'deadline_policy', 'evidence_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'discovery-to-reviewed-proposal', title: 'Discovery to Reviewed Proposal', description: 'Prepare a sourced call brief and an unsent SaaS proposal from approved post-call discovery and current pricing, with distinct seller and commercial review.', version: '0.1.0', category: 'Sales', order: 2, inputCount: 6, toolCount: 6, teamScope: 'small_team', agentSlots: [
    { id: 'briefing', agent_playbook_id: 'sales-call-briefing', required: true, output: 'sales-call-brief/v1' },
    { id: 'proposal', agent_playbook_id: 'proposal-drafter', required: true, output: 'sales-proposal-draft/v1' },
    { id: 'research', agent_playbook_id: 'account-researcher', required: false, output: 'account-research-brief/v1' },
  ], handoffs: [
    { id: 'briefing-to-proposal', from: 'briefing', to: 'proposal', artifact_type: 'sales-call-brief/v1', required: true },
    { id: 'research-to-briefing', from: 'research', to: 'briefing', artifact_type: 'account-research-brief/v1', required: false },
  ], setupChecks: ['goal_owner', 'account_meeting', 'claim_policy', 'pricing_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'pipeline-health-to-owned-action', title: 'Pipeline Health to Owned Action', description: 'Explain a source-backed stale opportunity and prepare a seller-owned next step after rechecking current CRM and contact state.', version: '0.1.0', category: 'Sales', order: 3, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'pipeline', agent_playbook_id: 'pipeline-analyst', required: true, output: 'pipeline-exception-brief/v1' },
    { id: 'deal', agent_playbook_id: 'deal-follow-through-coordinator', required: true, output: 'deal-action-register/v1' },
  ], handoffs: [
    { id: 'pipeline-to-deal', from: 'pipeline', to: 'deal', artifact_type: 'pipeline-exception-brief/v1', required: true },
  ], setupChecks: ['goal_owner', 'pipeline_scope', 'pipeline_policy', 'team_bindings', 'snapshot_access', 'current_state_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'feedback-theme-to-product-decision', title: 'Feedback Theme to Product Decision', description: 'Validate a bounded customer feedback theme, compare it with current product work, and prepare an owner-reviewed decision without silently creating roadmap work.', version: '0.1.0', category: 'Product', order: 1, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'feedback', agent_playbook_id: 'feedback-review-analyst', required: true, output: 'feedback-theme-brief/v1' },
    { id: 'product', agent_playbook_id: 'product-feedback-coordinator', required: true, output: 'product-feedback-decision/v1' },
  ], handoffs: [
    { id: 'feedback-to-product', from: 'feedback', to: 'product', artifact_type: 'feedback-theme-brief/v1', required: true },
  ], setupChecks: ['goal_owner', 'feedback_scope', 'product_policy', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'action_ledger', 'activation_choice'] },
  { id: 'released-feature-to-adoption-decision', title: 'Released Feature to Adoption Decision', description: 'Measure eligible, exposed and using accounts for a shipped feature, then prepare a bounded product owner decision.', version: '0.1.0', category: 'Product', order: 2, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'adoption', agent_playbook_id: 'product-adoption-analyst', required: true, output: 'feature-adoption-observation/v1' },
    { id: 'product', agent_playbook_id: 'product-feedback-coordinator', required: true, output: 'feature-adoption-decision/v1' },
  ], handoffs: [
    { id: 'adoption-to-product', from: 'adoption', to: 'product', artifact_type: 'feature-adoption-observation/v1', required: true },
  ], setupChecks: ['goal_owner', 'release_scope', 'measurement_rule', 'target_sample', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'opportunity-to-reviewed-requirements', title: 'Opportunity to Reviewed Requirements', description: 'Move a bounded customer problem through accepted priority into reviewable product requirements without silently opening delivery work.', version: '0.1.0', category: 'Product', order: 3, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'discovery', agent_playbook_id: 'product-discovery-researcher', required: true, output: 'product-opportunity-brief/v1' },
    { id: 'priority', agent_playbook_id: 'roadmap-prioritization-analyst', required: true, output: 'product-priority-decision/v1' },
    { id: 'requirements', agent_playbook_id: 'product-requirements-coordinator', required: true, output: 'product-requirements-brief/v1' },
  ], handoffs: [
    { id: 'discovery-to-priority', from: 'discovery', to: 'priority', artifact_type: 'product-opportunity-brief/v1', required: true },
    { id: 'priority-to-requirements', from: 'priority', to: 'requirements', artifact_type: 'product-priority-decision/v1', required: true },
  ], setupChecks: ['goal_owner', 'subject_scope', 'source_policy', 'decision_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'approved-requirement-to-release-readiness', title: 'Approved Requirement to Release Readiness', description: 'Join approved product requirements to exact-build QA, support content and measurement evidence for a Product release decision.', version: '0.1.0', category: 'Product', order: 4, inputCount: 6, toolCount: 5, teamScope: 'small_team', agentSlots: [
    { id: 'requirements', agent_playbook_id: 'product-requirements-coordinator', required: true, output: 'product-requirements-brief/v1' },
    { id: 'release', agent_playbook_id: 'product-release-coordinator', required: true, output: 'product-release-readiness/v1' },
  ], handoffs: [
    { id: 'requirements-to-release', from: 'requirements', to: 'release', artifact_type: 'product-requirements-brief/v1', required: true },
  ], setupChecks: ['goal_owner', 'subject_scope', 'source_policy', 'decision_rule', 'team_bindings', 'source_access', 'handoff_contract', 'plan_review', 'test_run', 'activation_choice'] },
  { id: 'website-growth-loop', title: 'Website Growth Loop', description: 'Coordinate a growth strategist and buyer-question specialist to find relevant website traffic opportunities, then track approved changes and measurement.', version: '0.5.0', category: 'Website Growth', order: 1, inputCount: 6, toolCount: 4, teamScope: 'small_team', agentSlots: [
    { id: 'strategist', agent_playbook_id: 'website-growth-starter', required: true, output: 'growth-priority-brief/v1' },
    { id: 'search', agent_playbook_id: 'search-opportunity-mapper', required: true, output: 'search-opportunity-list/v1' },
    { id: 'technical_seo', agent_playbook_id: 'seo-analyst', required: false, output: 'seo-issue-list/v1' },
    { id: 'content', agent_playbook_id: 'content-brief-writer', required: false, output: 'content-brief/v1' },
    { id: 'page', agent_playbook_id: 'content-page-builder', required: false, output: 'reviewable-page-draft/v1' },
    { id: 'publication', agent_playbook_id: 'website-publishing-coordinator', required: false, output: 'shipped-change/v1' },
    { id: 'distribution', agent_playbook_id: 'content-distribution-coordinator', required: false, output: 'distribution-plan/v1' },
    { id: 'measurement', agent_playbook_id: 'traffic-engagement-analyst', required: false, output: 'traffic-readout/v1' },
  ], handoffs: [
    { id: 'strategy-to-search', from: 'strategist', to: 'search', artifact_type: 'growth-priority-brief/v1', required: true },
    { id: 'search-to-content', from: 'search', to: 'content', artifact_type: 'search-opportunity-list/v1', required: false },
    { id: 'content-to-page', from: 'content', to: 'page', artifact_type: 'content-brief/v1', required: false },
    { id: 'page-to-publication', from: 'page', to: 'publication', artifact_type: 'reviewable-page-draft/v1', required: false },
    { id: 'publication-to-distribution', from: 'publication', to: 'distribution', artifact_type: 'shipped-change/v1', required: false },
    { id: 'publication-to-measurement', from: 'publication', to: 'measurement', artifact_type: 'shipped-change/v1', required: false },
    { id: 'distribution-to-measurement', from: 'distribution', to: 'measurement', artifact_type: 'distribution-plan/v1', required: false },
  ], setupChecks: ['goal_owner', 'metric_policy', 'team_bindings', 'site_scope', 'capabilities', 'handoff_contract', 'plan_review', 'test_run', 'action_ledger', 'activation_choice'] },
] as const

const ENGINEERING_PLAYBOOK_SPECIALTIES = new Set([
  'Browser QA', 'Engineering Operations Intelligence', 'FinOps', 'Performance Engineering',
  'QA', 'Reliability Operations', 'Security', 'Security Engineering',
])

// Group related methods in the picker without changing package identities or
// the category stored in an installed Playbook manifest.
export function playbookBrowseCategory(playbook: Pick<PlaybookCatalogItem, 'category'>): string {
  return ENGINEERING_PLAYBOOK_SPECIALTIES.has(playbook.category) ? 'Engineering' : playbook.category
}

export function playbookBrowsePath(playbook: Pick<PlaybookCatalogItem, 'category'>): string {
  const parent = playbookBrowseCategory(playbook)
  return parent === playbook.category ? parent : `${parent} / ${playbook.category}`
}

export const PLAYBOOK_CATEGORIES = [...new Set(PLAYBOOK_CATALOG.map(playbookBrowseCategory))]

export function isNewerPlaybookVersion(candidate: string, installed: string): boolean {
  const parse = (value: string) => value.split('.').map(part => Number.parseInt(part, 10))
  const next = parse(candidate)
  const current = parse(installed)
  if (next.length !== 3 || current.length !== 3 || [...next, ...current].some(Number.isNaN)) return candidate !== installed
  for (let index = 0; index < 3; index += 1) {
    if (next[index] !== current[index]) return next[index] > current[index]
  }
  return false
}

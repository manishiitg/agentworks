import { describe, expect, it } from 'vitest'
import { isNewerPlaybookVersion, playbookBrowseCategory, playbookBrowsePath, PLAYBOOK_CATALOG } from './playbookCatalog'

describe('isNewerPlaybookVersion', () => {
  it('compares semantic versions without treating older catalog data as an update', () => {
    expect(isNewerPlaybookVersion('0.6.1', '0.6.0')).toBe(true)
    expect(isNewerPlaybookVersion('1.0.0', '0.9.9')).toBe(true)
    expect(isNewerPlaybookVersion('0.6.0', '0.6.0')).toBe(false)
    expect(isNewerPlaybookVersion('0.5.9', '0.6.0')).toBe(false)
  })
})

describe('small-team catalog', () => {
  it('groups QA and Security packages within Engineering browse without changing their identities', () => {
    expect(playbookBrowsePath({ category: 'QA' })).toBe('Engineering / QA')
    expect(playbookBrowsePath({ category: 'Security' })).toBe('Engineering / Security')
    expect(playbookBrowseCategory({ category: 'Browser QA' })).toBe('Engineering')
    expect(playbookBrowseCategory({ category: 'Shopify' })).toBe('Shopify')
  })
  it('uses one engineering operations intelligence playbook and scopes every playbook to small teams', () => {
    const intelligence = PLAYBOOK_CATALOG.filter(item => item.category === 'Engineering Operations Intelligence')
    expect(intelligence.map(item => item.id)).toEqual(['engineering-operations-intelligence'])
    expect(intelligence[0].version).toBe('0.2.0')
    expect(intelligence[0].agentSlots?.map(slot => slot.procedure)).toEqual([
      'software-work/references/engineering-operations-analyst.md', 'software-work/references/engineering-delivery-coordinator.md',
    ])
    expect(intelligence[0].handoffs?.[0].artifact_type).toBe('engineering-metric-observation/v1')
    expect(intelligence[0].setupChecks).toHaveLength(10)
    expect(PLAYBOOK_CATALOG).toHaveLength(64)
    expect(PLAYBOOK_CATALOG.every(item => item.teamScope === 'small_team')).toBe(true)
  })

  it('connects the new Product, GTM and Support specialists through reviewed routes', () => {
    const routes = [
      ['opportunity-to-reviewed-requirements', 'Product', ['team-work/references/product-discovery-researcher.md', 'team-work/references/roadmap-prioritization-analyst.md', 'team-work/references/product-requirements-coordinator.md']],
      ['approved-requirement-to-release-readiness', 'Product', ['team-work/references/product-requirements-coordinator.md', 'team-work/references/product-release-coordinator.md']],
      ['offer-to-seller-readiness', 'GTM', ['growth-work/references/pricing-packaging-analyst.md', 'growth-work/references/sales-enablement-coordinator.md']],
      ['launch-signal-to-pipeline-review', 'GTM', ['growth-work/references/launch-coordinator.md', 'growth-work/references/revenue-operations-analyst.md']],
      ['case-pattern-to-knowledge-review', 'Customer Support', ['customer-work/references/support-triage-assistant.md', 'customer-work/references/support-knowledge-curator.md']],
    ] as const
    for (const [id, category, crewIds] of routes) {
      const playbook = PLAYBOOK_CATALOG.find(item => item.id === id)
      expect(playbook?.category).toBe(category)
      expect(playbook?.agentSlots?.map(slot => slot.procedure)).toEqual(crewIds)
      expect(playbook?.handoffs).toHaveLength(crewIds.length - 1)
      expect(playbook?.setupChecks).toHaveLength(10)
    }
  })

  it('offers distinct follow-through routes for Product, GTM, Support and Engineering', () => {
    const routes = [
      ['release-to-observed-adoption', 'Product', ['team-work/references/product-release-coordinator.md', 'customer-work/references/product-adoption-analyst.md']],
      ['accepted-offer-to-launch-readiness', 'GTM', ['growth-work/references/pricing-packaging-analyst.md', 'growth-work/references/gtm-strategy-analyst.md', 'growth-work/references/launch-coordinator.md']],
      ['verified-knowledge-to-case-reply', 'Customer Support', ['customer-work/references/support-knowledge-curator.md', 'customer-work/references/support-reply-drafter.md']],
      ['performance-regression-to-owned-change', 'Engineering', ['software-work/references/performance-investigator.md', 'software-work/references/engineering-delivery-coordinator.md']],
    ] as const
    for (const [id, category, crewIds] of routes) {
      const playbook = PLAYBOOK_CATALOG.find(item => item.id === id)
      expect(playbook?.category).toBe(category)
      expect(playbook?.agentSlots?.map(slot => slot.procedure)).toEqual(crewIds)
      expect(playbook?.handoffs).toHaveLength(crewIds.length - 1)
      expect(playbook?.setupChecks).toHaveLength(10)
    }
  })

  it('reuses Product and Sales Crews for a permission-gated trial assist', () => {
    const trial = PLAYBOOK_CATALOG.find(item => item.id === 'trial-account-to-reviewed-sales-assist')
    expect(trial?.category).toBe('Sales')
    expect(trial?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'customer-work/references/product-adoption-analyst.md', 'sales-work/references/sales-followup-coordinator.md',
    ])
    expect(trial?.handoffs?.[0].artifact_type).toBe('trial-usage-observation/v1')
    expect(trial?.setupChecks).toHaveLength(10)
  })

  it('reuses Product Crews for released feature adoption with a pending setup route', () => {
    const feature = PLAYBOOK_CATALOG.find(item => item.id === 'released-feature-to-adoption-decision')
    expect(feature?.category).toBe('Product')
    expect(feature?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'customer-work/references/product-adoption-analyst.md', 'team-work/references/product-feedback-coordinator.md',
    ])
    expect(feature?.handoffs?.[0].artifact_type).toBe('feature-adoption-observation/v1')
    expect(feature?.setupChecks).toHaveLength(10)
  })

  it('offers exact vendor evaluation to a separate purchase owner', () => {
    const vendor = PLAYBOOK_CATALOG.find(item => item.id === 'vendor-evaluation-to-purchase-decision')
    expect(vendor?.category).toBe('Operations')
    expect(vendor?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'team-work/references/vendor-researcher.md', 'finance-work/references/spend-payables-coordinator.md',
    ])
    expect(vendor?.handoffs?.[0].artifact_type).toBe('vendor-comparison/v1')
    expect(vendor?.setupChecks).toHaveLength(10)
  })

  it('keeps the buyer-question handoff with Search Opportunity Mapper', () => {
    const growth = PLAYBOOK_CATALOG.find(item => item.id === 'website-growth-loop')
    expect(growth?.version).toBe('0.5.0')
    expect(growth?.agentSlots?.find(slot => slot.id === 'search')?.procedure).toBe('growth-work/references/search-opportunity-mapper.md')
    expect(growth?.agentSlots?.find(slot => slot.id === 'search')).not.toHaveProperty('accepts')
    expect(growth?.agentSlots?.find(slot => slot.id === 'technical_seo')?.required).toBe(false)
    expect(growth?.agentSlots?.find(slot => slot.id === 'publication')?.procedure).toBe('growth-work/references/website-publishing-coordinator.md')
    expect(growth?.handoffs?.some(handoff => handoff.artifact_type === 'shipped-change/v1')).toBe(true)
    expect(growth?.setupChecks).toContain('action_ledger')
  })

  it('offers a focused SEO proposal with two Crew roles and pending setup', () => {
    const seo = PLAYBOOK_CATALOG.find(item => item.id === 'seo-intelligence')
    expect(seo?.version).toBe('0.2.0')
    expect(seo?.agentSlots?.map(slot => slot.procedure)).toEqual(['growth-work/references/seo-analyst.md', 'growth-work/references/search-opportunity-mapper.md'])
    expect(seo?.handoffs?.[0].artifact_type).toBe('seo-issue-list/v1')
    expect(seo?.setupChecks).toContain('manual_test')
    expect(seo?.setupChecks).toContain('activation_choice')
  })

  it('offers sampled AI visibility with a separate page opportunity review', () => {
    const ai = PLAYBOOK_CATALOG.find(item => item.id === 'ai-visibility-intelligence')
    expect(ai?.version).toBe('0.2.0')
    expect(ai?.agentSlots?.map(slot => slot.procedure)).toEqual(['growth-work/references/ai-visibility-analyst.md', 'growth-work/references/search-opportunity-mapper.md'])
    expect(ai?.handoffs?.[0].artifact_type).toBe('ai-visibility-snapshot/v1')
    expect(ai?.setupChecks).toContain('sample_method')
    expect(ai?.setupChecks).toContain('manual_test')
  })

  it('offers a signup-to-paid funnel handoff to a reviewed experiment', () => {
    const funnel = PLAYBOOK_CATALOG.find(item => item.id === 'funnel-conversion-intelligence')
    expect(funnel?.version).toBe('0.2.0')
    expect(funnel?.agentSlots?.map(slot => slot.procedure)).toEqual(['growth-work/references/funnel-analyst.md', 'growth-work/references/growth-experiment-planner.md'])
    expect(funnel?.handoffs?.[0].artifact_type).toBe('funnel-observation/v1')
    expect(funnel?.setupChecks).toContain('identity_source')
    expect(funnel?.setupChecks).toContain('manual_test')
  })

  it('offers FinOps with cost, delivery and independent finance verification', () => {
    const finops = PLAYBOOK_CATALOG.find(item => item.id === 'cost-anomaly-to-verified-savings')
    expect(finops?.version).toBe('0.6.0')
    expect(finops?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'software-work/references/cloud-cost-analyst.md', 'software-work/references/engineering-delivery-coordinator.md', 'finance-work/references/finance-analyst.md',
    ])
    expect(finops?.handoffs?.map(handoff => handoff.artifact_type)).toEqual(['cloud-cost-review/v1', 'cloud-change-review/v1'])
    expect(finops?.setupChecks).toContain('manual_test')
  })

  it('exposes the Finance Operations Review team and validated billing handoff', () => {
    const finance = PLAYBOOK_CATALOG.find(item => item.id === 'finance-operations-review')
    expect(finance?.category).toBe('Finance')
    expect(finance?.agentSlots?.filter(slot => slot.required).map(slot => slot.procedure)).toEqual(['finance-work/references/billing-operations-coordinator.md', 'finance-work/references/finance-analyst.md'])
    expect(finance?.handoffs?.find(handoff => handoff.required)?.artifact_type).toBe('billing-exception-queue/v1')
    expect(finance?.setupChecks).toContain('test_run')
  })

  it('exposes invoice intake with a distinct payables owner and validated document handoff', () => {
    const invoice = PLAYBOOK_CATALOG.find(item => item.id === 'invoice-intake-to-reviewed-payable')
    expect(invoice?.category).toBe('Finance')
    expect(invoice?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'team-work/references/document-intake-assistant.md', 'finance-work/references/spend-payables-coordinator.md',
    ])
    expect(invoice?.handoffs?.[0].artifact_type).toBe('document-intake-record/v1')
    expect(invoice?.setupChecks).toContain('duplicate_policy')
    expect(invoice?.setupChecks).toContain('test_run')
  })

  it('exposes refund review with a distinct close owner and validated decision handoff', () => {
    const refund = PLAYBOOK_CATALOG.find(item => item.id === 'refund-request-to-reconciled-outcome')
    expect(refund?.category).toBe('Finance')
    expect(refund?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'finance-work/references/billing-operations-coordinator.md', 'finance-work/references/revenue-close-analyst.md',
    ])
    expect(refund?.handoffs?.[0].artifact_type).toBe('refund-decision/v1')
    expect(refund?.setupChecks).toContain('action_ledger')
    expect(refund?.setupChecks).toContain('test_run')
  })

  it('exposes a dispute review with a separate finance outcome and exact handoff', () => {
    const dispute = PLAYBOOK_CATALOG.find(item => item.id === 'dispute-to-reconciled-outcome')
    expect(dispute?.category).toBe('Finance')
    expect(dispute?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'finance-work/references/billing-operations-coordinator.md', 'finance-work/references/revenue-close-analyst.md',
    ])
    expect(dispute?.handoffs?.[0].artifact_type).toBe('dispute-case/v1')
    expect(dispute?.setupChecks).toContain('deadline_policy')
  })

  it('exposes exact-invoice receivable recovery with a separate finance outcome', () => {
    const recovery = PLAYBOOK_CATALOG.find(item => item.id === 'subscription-receivable-to-verified-outcome')
    expect(recovery?.category).toBe('Finance')
    expect(recovery?.agentSlots?.map(slot => slot.procedure)).toEqual(['finance-work/references/billing-operations-coordinator.md', 'finance-work/references/finance-analyst.md'])
    expect(recovery?.handoffs?.[0].artifact_type).toBe('receivable-review/v1')
    expect(recovery?.setupChecks).toContain('balance_policy')
  })

  it('exposes campaign measurement to a reviewed experiment with optional competitor context', () => {
    const marketing = PLAYBOOK_CATALOG.find(item => item.id === 'campaign-signal-to-reviewed-experiment')
    expect(marketing?.category).toBe('Marketing')
    expect(marketing?.agentSlots?.filter(slot => slot.required).map(slot => slot.procedure)).toEqual([
      'growth-work/references/campaign-performance-analyst.md', 'growth-work/references/growth-experiment-planner.md',
    ])
    expect(marketing?.agentSlots?.find(slot => slot.id === 'competitor')?.required).toBe(false)
    expect(marketing?.handoffs?.find(handoff => handoff.required)?.artifact_type).toBe('campaign-performance-brief/v1')
    expect(marketing?.setupChecks).toContain('baseline_rule')
  })

  it('exposes seven Shopify routes with distinct two-Crew handoffs', () => {
    const shopify = PLAYBOOK_CATALOG.filter(item => item.category === 'Shopify')
    expect(shopify.map(item => item.id)).toEqual(['order-exception-to-resolution', 'storefront-opportunity-to-verified-change', 'inventory-availability-to-owner-action', 'payment-exception-to-order-decision', 'product-launch-readiness-to-go-no-go', 'checkout-signal-to-reviewed-recovery', 'inventory-risk-to-reviewed-replenishment'])
    expect(shopify[0].version).toBe('0.1.1')
    expect(shopify[1].agentSlots?.map(slot => slot.procedure)).toEqual(['store-work/references/shopify-growth-analyst.md', 'store-work/references/catalog-merchandising-analyst.md'])
    expect(shopify[1].handoffs?.[0].artifact_type).toBe('shopify-growth-opportunity/v1')
    expect(shopify[2].handoffs?.[0].artifact_type).toBe('inventory-availability-exception/v1')
    expect(shopify[3].agentSlots?.[0].procedure).toBe('store-work/references/payment-operations-investigator.md')
    expect(shopify[4].handoffs?.[0].artifact_type).toBe('launch-catalog-readiness/v1')
    expect(shopify[5].agentSlots?.map(slot => slot.procedure)).toEqual(['store-work/references/shopify-growth-analyst.md', 'store-work/references/checkout-recovery-coordinator.md'])
    expect(shopify[5].handoffs?.[0].artifact_type).toBe('checkout-recovery-signal/v1')
    expect(shopify[6].agentSlots?.[1].procedure).toBe('store-work/references/replenishment-planner.md')
    expect(shopify[6].handoffs?.[0].artifact_type).toBe('inventory-availability-exception/v1')
  })

  it('exposes the Sales route with required qualification and follow-up and optional research', () => {
    const sales = PLAYBOOK_CATALOG.find(item => item.id === 'inbound-lead-to-meeting-review')
    expect(sales?.category).toBe('Sales')
    expect(sales?.version).toBe('0.2.0')
    expect(sales?.agentSlots?.filter(slot => slot.required).map(slot => slot.procedure)).toEqual(['sales-work/references/lead-intake-qualifier.md', 'sales-work/references/sales-followup-coordinator.md'])
    expect(sales?.agentSlots?.find(slot => slot.id === 'research')?.required).toBe(false)
    expect(sales?.handoffs?.find(handoff => handoff.required)?.artifact_type).toBe('lead-qualification-brief/v1')
    expect(sales?.setupChecks).toContain('action_ledger')
  })

  it('exposes a distinct Sales proposal route with a required call brief handoff', () => {
    const proposal = PLAYBOOK_CATALOG.find(item => item.id === 'discovery-to-reviewed-proposal')
    expect(proposal?.agentSlots?.filter(slot => slot.required).map(slot => slot.procedure)).toEqual([
      'sales-work/references/sales-call-briefing.md', 'sales-work/references/proposal-drafter.md',
    ])
    expect(proposal?.handoffs?.find(handoff => handoff.required)?.artifact_type).toBe('sales-call-brief/v1')
    expect(proposal?.setupChecks).toContain('pricing_policy')
    expect(proposal?.setupChecks).toContain('test_run')
  })

  it('exposes a Support-to-Product feedback decision with a validated theme handoff', () => {
    const feedback = PLAYBOOK_CATALOG.find(item => item.id === 'feedback-theme-to-product-decision')
    expect(feedback?.category).toBe('Product')
    expect(feedback?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'customer-work/references/feedback-review-analyst.md', 'team-work/references/product-feedback-coordinator.md',
    ])
    expect(feedback?.handoffs?.[0].artifact_type).toBe('feedback-theme-brief/v1')
    expect(feedback?.setupChecks).toContain('action_ledger')
  })

  it('exposes a bounded pipeline exception to seller-owned action', () => {
    const pipeline = PLAYBOOK_CATALOG.find(item => item.id === 'pipeline-health-to-owned-action')
    expect(pipeline?.category).toBe('Sales')
    expect(pipeline?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'sales-work/references/pipeline-analyst.md', 'sales-work/references/deal-follow-through-coordinator.md',
    ])
    expect(pipeline?.handoffs?.[0].artifact_type).toBe('pipeline-exception-brief/v1')
    expect(pipeline?.setupChecks).toContain('current_state_access')
  })

  it('exposes the GTM launch route and reuses the Sales qualification handoff', () => {
    const gtm = PLAYBOOK_CATALOG.find(item => item.id === 'launch-to-qualified-pipeline')
    expect(gtm?.category).toBe('GTM')
    expect(gtm?.agentSlots?.filter(slot => slot.required).map(slot => slot.procedure)).toEqual([
      'growth-work/references/gtm-strategy-analyst.md', 'growth-work/references/launch-coordinator.md', 'sales-work/references/lead-intake-qualifier.md', 'sales-work/references/sales-followup-coordinator.md',
    ])
    expect(gtm?.agentSlots?.find(slot => slot.id === 'website')?.required).toBe(false)
    expect(gtm?.handoffs?.map(handoff => handoff.artifact_type)).toEqual([
      'gtm-launch-brief/v1', 'launch-signal-register/v1', 'lead-qualification-brief/v1',
    ])
    expect(gtm?.setupChecks).toContain('test_run')
  })

  it('exposes Customer Success with onboarding and adoption handoffs', () => {
    const success = PLAYBOOK_CATALOG.find(item => item.id === 'new-customer-to-first-value')
    expect(success?.category).toBe('Customer Success')
    expect(success?.agentSlots?.filter(slot => slot.required).map(slot => slot.procedure)).toEqual(['customer-work/references/customer-onboarding-coordinator.md', 'customer-work/references/product-adoption-analyst.md'])
    expect(success?.agentSlots?.find(slot => slot.id === 'health')?.required).toBe(false)
    expect(success?.handoffs?.find(handoff => handoff.required)?.artifact_type).toBe('onboarding-milestone-register/v1')
    expect(success?.setupChecks).toContain('first_value_rule')
  })

  it('exposes the signed Sales to CS handoff before onboarding', () => {
    const handoff = PLAYBOOK_CATALOG.find(item => item.id === 'signed-deal-to-onboarding-handoff')
    expect(handoff?.category).toBe('Customer Success')
    expect(handoff?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'sales-work/references/deal-follow-through-coordinator.md', 'customer-work/references/customer-onboarding-coordinator.md',
    ])
    expect(handoff?.handoffs?.[0].artifact_type).toBe('sales-cs-handoff/v1')
    expect(handoff?.setupChecks).toHaveLength(10)
  })

  it('exposes a health-to-renewal decision with exact notice setup', () => {
    const renewal = PLAYBOOK_CATALOG.find(item => item.id === 'renewal-risk-to-owned-decision')
    expect(renewal?.category).toBe('Customer Success')
    expect(renewal?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'customer-work/references/customer-health-coordinator.md', 'customer-work/references/renewal-coordinator.md',
    ])
    expect(renewal?.handoffs?.[0].artifact_type).toBe('renewal-health-brief/v1')
    expect(renewal?.setupChecks).toContain('contract_policy')
  })

  it('exposes maturity-aware retention as a two-Crew proposal', () => {
    const retention = PLAYBOOK_CATALOG.find(item => item.id === 'activation-retention-intelligence')
    expect(retention?.version).toBe('0.2.0')
    expect(retention?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'customer-work/references/lifecycle-analyst.md', 'growth-work/references/growth-experiment-planner.md',
    ])
    expect(retention?.handoffs?.[0].artifact_type).toBe('cohort-retention-observation/v1')
    expect(retention?.setupChecks).toHaveLength(10)
  })

  it('exposes exact experiment execution and outcome as a two-Crew proposal', () => {
    const experiment = PLAYBOOK_CATALOG.find(item => item.id === 'growth-experimentation-follow-through')
    expect(experiment?.version).toBe('0.3.0')
    expect(experiment?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'growth-work/references/experiment-run-coordinator.md', 'growth-work/references/growth-outcome-analyst.md',
    ])
    expect(experiment?.handoffs?.[0].artifact_type).toBe('experiment-execution-record/v1')
    expect(experiment?.setupChecks).toHaveLength(10)
  })

  it('exposes post-incident review and independent improvement verification', () => {
    const incident = PLAYBOOK_CATALOG.find(item => item.id === 'post-incident-review-actions')
    expect(incident?.version).toBe('0.5.0')
    expect(incident?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'software-work/references/post-incident-reviewer.md', 'software-work/references/improvement-follow-through-coordinator.md',
    ])
    expect(incident?.handoffs?.[0].artifact_type).toBe('post-incident-review/v1')
    expect(incident?.setupChecks).toHaveLength(10)
  })

  it('exposes the Customer Support case route with optional escalation', () => {
    const support = PLAYBOOK_CATALOG.find(item => item.id === 'support-case-to-reviewed-resolution')
    expect(support?.category).toBe('Customer Support')
    expect(support?.agentSlots?.filter(slot => slot.required).map(slot => slot.procedure)).toEqual([
      'customer-work/references/support-triage-assistant.md', 'customer-work/references/support-reply-drafter.md',
    ])
    expect(support?.agentSlots?.find(slot => slot.id === 'escalation')?.required).toBe(false)
    expect(support?.handoffs?.find(handoff => handoff.required)?.artifact_type).toBe('support-case-triage/v1')
    expect(support?.setupChecks).toContain('handoff_contract')
  })

  it('exposes QA release gate composition with optional flake investigation', () => {
    const qa = PLAYBOOK_CATALOG.find(item => item.id === 'release-candidate-to-reviewed-gate')
    expect(qa?.category).toBe('QA')
    expect(qa?.agentSlots?.filter(slot => slot.required).map(slot => slot.procedure)).toEqual([
      'software-work/references/browser-journey-qa-analyst.md', 'software-work/references/release-quality-assistant.md',
    ])
    expect(qa?.agentSlots?.find(slot => slot.id === 'flake')?.required).toBe(false)
    expect(qa?.handoffs?.find(handoff => handoff.required)?.artifact_type).toBe('journey-result/v1')
    expect(qa?.setupChecks).toContain('suite_policy')
  })

  it('exposes the Security finding-to-remediation route', () => {
    const security = PLAYBOOK_CATALOG.find(item => item.id === 'finding-to-verified-remediation')
    expect(security?.category).toBe('Security')
    expect(security?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'software-work/references/security-findings-analyst.md', 'software-work/references/security-remediation-coordinator.md',
    ])
    expect(security?.handoffs?.[0].artifact_type).toBe('security-finding/v1')
    expect(security?.setupChecks).toContain('remediation_rule')
  })

  it('exposes the Security access exception and same-cell fix route', () => {
    const access = PLAYBOOK_CATALOG.find(item => item.id === 'access-exception-to-verified-fix')
    expect(access?.category).toBe('Security')
    expect(access?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'software-work/references/access-review-analyst.md', 'software-work/references/security-remediation-coordinator.md',
    ])
    expect(access?.handoffs?.[0].artifact_type).toBe('access-review-matrix/v1')
    expect(access?.setupChecks).toContain('direct_observation')
  })

  it('exposes Operations meeting actions with required status and optional review', () => {
    const operations = PLAYBOOK_CATALOG.find(item => item.id === 'meeting-decision-to-owned-follow-through')
    expect(operations?.category).toBe('Operations')
    expect(operations?.agentSlots?.filter(slot => slot.required).map(slot => slot.procedure)).toEqual([
      'team-work/references/meeting-actions-coordinator.md', 'team-work/references/project-status-reporter.md',
    ])
    expect(operations?.agentSlots?.find(slot => slot.id === 'review')?.required).toBe(false)
    expect(operations?.handoffs?.find(handoff => handoff.required)?.artifact_type).toBe('meeting-action-register/v1')
    expect(operations?.setupChecks).toContain('owner_policy')
  })

  it('offers a generic order exception to exact-case unsent update handoff', () => {
    const order = PLAYBOOK_CATALOG.find(item => item.id === 'order-exception-to-reviewed-update')
    expect(order?.category).toBe('Operations')
    expect(order?.agentSlots?.map(slot => slot.procedure)).toEqual([
      'team-work/references/order-operations-coordinator.md', 'customer-work/references/support-reply-drafter.md',
    ])
    expect(order?.handoffs?.[0].artifact_type).toBe('order-exception/v1')
    expect(order?.setupChecks).toContain('case_scope')
    expect(order?.setupChecks).toHaveLength(10)
  })
})

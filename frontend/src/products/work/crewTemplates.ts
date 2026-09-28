import financeAnalystSkill from './templates/finance-analyst/SKILL.md?raw'
import financeAnalystSetup from './templates/finance-analyst/TEMPLATE_SETUP.md?raw'
import financeAnalystSetupState from './templates/finance-analyst/TEMPLATE_SETUP.json?raw'
import taxExportSkill from './templates/tax-export/SKILL.md?raw'
import taxExportSetup from './templates/tax-export/SETUP.md?raw'
import taxExportSetupState from './templates/tax-export/TEMPLATE_SETUP.json?raw'
import billingOperationsSkill from './templates/billing-operations-coordinator/SKILL.md?raw'
import billingOperationsSetup from './templates/billing-operations-coordinator/SETUP.md?raw'
import billingOperationsSetupState from './templates/billing-operations-coordinator/TEMPLATE_SETUP.json?raw'
import revenueCloseSkill from './templates/revenue-close-analyst/SKILL.md?raw'
import revenueCloseSetup from './templates/revenue-close-analyst/SETUP.md?raw'
import revenueCloseSetupState from './templates/revenue-close-analyst/TEMPLATE_SETUP.json?raw'
import spendPayablesSkill from './templates/spend-payables-coordinator/SKILL.md?raw'
import spendPayablesSetup from './templates/spend-payables-coordinator/SETUP.md?raw'
import spendPayablesSetupState from './templates/spend-payables-coordinator/TEMPLATE_SETUP.json?raw'
import { billingPacks, type BillingPackId } from './billingPacks'
import websiteGrowthSkill from './templates/website-growth-starter/SKILL.md?raw'
import websiteGrowthSetup from './templates/website-growth-starter/SETUP.md?raw'
import websiteGrowthSetupState from './templates/website-growth-starter/TEMPLATE_SETUP.json?raw'
import { websiteGrowthSpecialists, type WebsiteGrowthSpecialistId } from './websiteGrowthSpecialists'
import { marketingSpecialists, type MarketingSpecialistId } from './marketingSpecialists'
import { salesSpecialists, type SalesSpecialistId } from './salesSpecialists'
import { salesExpansion, type SalesExpansionId } from './salesExpansion'
import { customerSuccessSpecialists, type CustomerSuccessSpecialistId } from './customerSuccessSpecialists'
import { supportSpecialists, type SupportSpecialistId } from './supportSpecialists'
import { productSpecialists, type ProductSpecialistId } from './productSpecialists'
import { operationsSpecialists, type OperationsSpecialistId } from './operationsSpecialists'
import { engineeringSpecialists, type EngineeringSpecialistId } from './engineeringSpecialists'
import { qaSpecialists, type QASpecialistId } from './qaSpecialists'
import { securitySpecialists, type SecuritySpecialistId } from './securitySpecialists'
import { gtmSpecialists, type GTMSpecialistId } from './gtmSpecialists'
import { shopifySpecialists, type ShopifySpecialistId } from './shopifySpecialists'
import { categoryExpansionSpecialists, type CategoryExpansionId } from './categoryExpansionSpecialists'

export type CrewTemplateId = 'finance-analyst' | 'tax-export' | 'billing-operations-coordinator' | 'revenue-close-analyst' | 'spend-payables-coordinator' | BillingPackId | 'website-growth-starter' | WebsiteGrowthSpecialistId | MarketingSpecialistId | SalesSpecialistId | SalesExpansionId | CustomerSuccessSpecialistId | SupportSpecialistId | ProductSpecialistId | OperationsSpecialistId | EngineeringSpecialistId | QASpecialistId | SecuritySpecialistId | GTMSpecialistId | ShopifySpecialistId | CategoryExpansionId

export type CrewTemplateSetupCheck = {
  id: string
  title: string
  instructions: string
  optional?: boolean
}

export type CrewTemplateSetupState = {
  schema_version: 1
  template_id: string
  template_version: number
  checks: CrewTemplateSetupCheck[]
  completed_steps: string[]
}

export type CrewTemplate = {
  id: CrewTemplateId
  version: number
  category: string
  subcategory?: string
  name: string
  icon: string
  role: string
  purpose: string
  firstResult: string
  minimumInput: string
  optionalConnections: string
  exampleRequests: readonly string[]
  selectedSkills: readonly [string, ...string[]]
  setupPath: string
  setupGuidePath: string
  requiredFiles: readonly [string, ...string[]]
  files: Readonly<Record<string, string>>
}

// Browse hierarchy only. Installed IDs, skill paths and setup files retain
// their original specialty identity so existing Crews need no migration.
export function crewTemplateBrowseCategory(template: Pick<CrewTemplate, 'category'>): string {
  return template.category === 'QA' || template.category === 'Security' ? 'Engineering' : template.category
}

export function crewTemplateBrowsePath(template: Pick<CrewTemplate, 'category' | 'subcategory'>): string {
  const parent = crewTemplateBrowseCategory(template)
  return [parent, template.category === parent ? '' : template.category, template.subcategory || ''].filter(Boolean).join(' · ')
}

export const crewTemplates: readonly CrewTemplate[] = [{
  id: 'finance-analyst',
  version: 1,
  category: 'Finance',
  subcategory: 'Planning & performance',
  name: 'Finance Analyst',
  icon: '📊',
  role: 'Finance analyst for this business',
  purpose: 'Analyze authorized finance records, explain revenue and expense changes, and prepare sourced finance briefs with transparent calculations.',
  firstResult: 'A sourced finance brief with calculations and questions to verify.',
  minimumInput: 'Upload a statement or finance export, then provide the reporting period and metric definitions.',
  optionalConnections: 'Spreadsheet skill or accounting MCP for connected data; Slack or email for delivery.',
  exampleRequests: [
    'Prepare a finance brief for last month from this statement. Show your calculations.',
    'What changed in expenses between these two periods, and which items need review?',
  ],
  selectedSkills: ['finance-analyst'],
  setupPath: 'TEMPLATE_SETUP.json',
  setupGuidePath: 'TEMPLATE_SETUP.md',
  requiredFiles: ['skills/finance-analyst/SKILL.md', 'TEMPLATE_SETUP.md', 'TEMPLATE_SETUP.json'],
  files: {
    'skills/finance-analyst/SKILL.md': financeAnalystSkill,
    'TEMPLATE_SETUP.md': financeAnalystSetup,
    'TEMPLATE_SETUP.json': financeAnalystSetupState,
  },
}, {
  id: 'tax-export',
  version: 1,
  category: 'Finance',
  subcategory: 'Tax & compliance',
  name: 'Tax Export Preparer',
  icon: '🧾',
  role: 'Tax records preparer for this business',
  purpose: 'Organize authorized transaction records into a traceable export for review by the business and its tax professional.',
  firstResult: 'A reconciled transaction export with source references and an exceptions list.',
  minimumInput: 'Provide the reporting period, jurisdiction, and authorized transaction records.',
  optionalConnections: 'Accounting MCP or spreadsheet skill for connected records; secure delivery after approval.',
  exampleRequests: [
    'Prepare a transaction export for my accountant for this period, with an exceptions list.',
    'Reconcile these invoices and payments before the tax handoff.',
  ],
  selectedSkills: ['tax-export'],
  setupPath: 'templates/tax-export/TEMPLATE_SETUP.json',
  setupGuidePath: 'templates/tax-export/SETUP.md',
  requiredFiles: ['skills/tax-export/SKILL.md', 'templates/tax-export/SETUP.md', 'templates/tax-export/TEMPLATE_SETUP.json'],
  files: {
    'skills/tax-export/SKILL.md': taxExportSkill,
    'templates/tax-export/SETUP.md': taxExportSetup,
    'templates/tax-export/TEMPLATE_SETUP.json': taxExportSetupState,
  },
}, {
  id: 'billing-operations-coordinator',
  version: 1,
  category: 'Finance',
  subcategory: 'Billing & payments',
  name: 'Billing Operations Coordinator',
  icon: '💳',
  role: 'Subscription billing and payment operations coordinator',
  purpose: 'Review overdue invoices, failed payments, customer refund requests, and disputes; prepare source-linked next actions and customer-safe drafts for approval.',
  firstResult: 'A dated billing exception queue with source IDs, deadlines, proposed next steps, and drafts for review.',
  minimumInput: 'Provide a billing export or authorized account, reporting period, invoice terms, refund policy, and contact rules.',
  optionalConnections: 'Stripe or Paddle if used; a CRM or support inbox for prior contact. Exports work without a live connection.',
  exampleRequests: [
    'Review overdue invoices and failed payments this week. Show what needs follow-up and why.',
    'Prepare a refund request queue with payment IDs, remaining refundable amounts, and approval owners.',
  ],
  selectedSkills: ['billing-operations-coordinator'],
  setupPath: 'templates/billing-operations-coordinator/TEMPLATE_SETUP.json',
  setupGuidePath: 'templates/billing-operations-coordinator/SETUP.md',
  requiredFiles: ['skills/billing-operations-coordinator/SKILL.md', 'templates/billing-operations-coordinator/SETUP.md', 'templates/billing-operations-coordinator/TEMPLATE_SETUP.json'],
  files: {
    'skills/billing-operations-coordinator/SKILL.md': billingOperationsSkill,
    'templates/billing-operations-coordinator/SETUP.md': billingOperationsSetup,
    'templates/billing-operations-coordinator/TEMPLATE_SETUP.json': billingOperationsSetupState,
  },
}, {
  id: 'revenue-close-analyst',
  version: 1,
  category: 'Finance',
  subcategory: 'Revenue accounting',
  name: 'Revenue & Close Analyst',
  icon: '📚',
  role: 'Subscription revenue and accounting close analyst',
  purpose: 'Reconcile subscription billing, credits, payments, and ledger records into a reviewable close memo with unresolved differences.',
  firstResult: 'A period close checklist, reconciliation, and source-linked exception memo for the accountant.',
  minimumInput: 'Provide a fiscal period, entity, currency, accounting policy, and authorized billing and ledger exports.',
  optionalConnections: 'A billing source such as Stripe or Paddle, plus the customer’s ledger or revenue schedule if available. Exports are sufficient.',
  exampleRequests: [
    'Prepare a close review for this period and show differences between billed, collected, and recognized revenue.',
    'Reconcile these subscription invoices, credits, and ledger entries with source IDs.',
  ],
  selectedSkills: ['revenue-close-analyst'],
  setupPath: 'templates/revenue-close-analyst/TEMPLATE_SETUP.json',
  setupGuidePath: 'templates/revenue-close-analyst/SETUP.md',
  requiredFiles: ['skills/revenue-close-analyst/SKILL.md', 'templates/revenue-close-analyst/SETUP.md', 'templates/revenue-close-analyst/TEMPLATE_SETUP.json'],
  files: {
    'skills/revenue-close-analyst/SKILL.md': revenueCloseSkill,
    'templates/revenue-close-analyst/SETUP.md': revenueCloseSetup,
    'templates/revenue-close-analyst/TEMPLATE_SETUP.json': revenueCloseSetupState,
  },
}, {
  id: 'spend-payables-coordinator',
  version: 2,
  category: 'Finance',
  subcategory: 'Payables & spend',
  name: 'Spend & Payables Coordinator',
  icon: '🧮',
  role: 'Vendor purchase, bills, expenses, and spend review coordinator',
  purpose: 'Review proposed purchases, bills and spend against current vendor records, budgets, duplicates and approval policy without executing them.',
  firstResult: 'A source-linked purchase decision or payables queue with exact amount, approver, current state and evidence gaps.',
  minimumInput: 'Provide a validated vendor comparison or bill/expense records, entity, currency, current vendor and spend source, budget and approval policy.',
  optionalConnections: 'The customer’s AP, card, or ledger system, such as BILL or Ramp when connected; exports work first.',
  exampleRequests: [
    'Review bills due this month and flag duplicates, missing approvals, and payment-status gaps.',
    'Summarize software vendor spend and prepare an exceptions queue for the budget owners.',
    'Review this selected vendor plan against current vendors, budget, security policy and purchase approval rules.',
  ],
  selectedSkills: ['spend-payables-coordinator'],
  setupPath: 'templates/spend-payables-coordinator/TEMPLATE_SETUP.json',
  setupGuidePath: 'templates/spend-payables-coordinator/SETUP.md',
  requiredFiles: ['skills/spend-payables-coordinator/SKILL.md', 'templates/spend-payables-coordinator/SETUP.md', 'templates/spend-payables-coordinator/TEMPLATE_SETUP.json'],
  files: {
    'skills/spend-payables-coordinator/SKILL.md': spendPayablesSkill,
    'templates/spend-payables-coordinator/SETUP.md': spendPayablesSetup,
    'templates/spend-payables-coordinator/TEMPLATE_SETUP.json': spendPayablesSetupState,
  },
}, {
  id: 'website-growth-starter',
  version: 1,
  category: 'Website Growth',
  name: 'Website Growth Starter',
  icon: '🌱',
  role: 'Website growth strategist for this business',
  purpose: 'Audit the business website, find evidence-backed opportunities for relevant traffic, and guide a measurable 30-day growth plan.',
  firstResult: 'A sourced website audit and prioritized 30-day traffic growth plan.',
  minimumInput: 'Public website URL or page export, target audience, and primary visitor action.',
  optionalConnections: 'Search Console and analytics for measurement; a repository or CMS for reviewed page edits.',
  exampleRequests: [
    'Audit my newly launched website and propose the first five actions to attract relevant visitors.',
    'Review our homepage and product page. What is blocking discovery, and what should we measure next?',
  ],
  selectedSkills: ['website-growth-starter'],
  setupPath: 'templates/website-growth-starter/TEMPLATE_SETUP.json',
  setupGuidePath: 'templates/website-growth-starter/SETUP.md',
  requiredFiles: ['skills/website-growth-starter/SKILL.md', 'templates/website-growth-starter/SETUP.md', 'templates/website-growth-starter/TEMPLATE_SETUP.json'],
  files: {
    'skills/website-growth-starter/SKILL.md': websiteGrowthSkill,
    'templates/website-growth-starter/SETUP.md': websiteGrowthSetup,
    'templates/website-growth-starter/TEMPLATE_SETUP.json': websiteGrowthSetupState,
  },
}, ...billingPacks, ...websiteGrowthSpecialists, ...marketingSpecialists, ...salesSpecialists, ...salesExpansion, ...customerSuccessSpecialists, ...supportSpecialists, ...productSpecialists, ...operationsSpecialists, ...engineeringSpecialists, ...qaSpecialists, ...securitySpecialists, ...gtmSpecialists, ...shopifySpecialists, ...categoryExpansionSpecialists]

export function parseCrewTemplateSetupState(content: string, template: CrewTemplate): CrewTemplateSetupState | null {
  try {
    const raw = JSON.parse(content) as Partial<CrewTemplateSetupState>
    if (raw.schema_version !== 1 || raw.template_id !== template.id || raw.template_version !== template.version || !Array.isArray(raw.checks) || !Array.isArray(raw.completed_steps)) return null
    if (raw.checks.length < 5 || raw.checks.length > 10) return null
    const ids = new Set<string>()
    for (const check of raw.checks) {
      if (!check || typeof check.id !== 'string' || !check.id || typeof check.title !== 'string' || !check.title || typeof check.instructions !== 'string' || !check.instructions || ids.has(check.id)) return null
      ids.add(check.id)
    }
    // Saved progress cannot remove requirements from the installed template.
    const canonical = JSON.parse(template.files[template.setupPath]) as CrewTemplateSetupState
    if (!Array.isArray(canonical.checks) || canonical.checks.length !== ids.size || canonical.checks.some(check => !ids.has(check.id))) return null
    if (raw.completed_steps.some(id => typeof id !== 'string' || !ids.has(id))) return null
    return raw as CrewTemplateSetupState
  } catch {
    return null
  }
}

export function getCrewTemplate(id: CrewTemplateId): CrewTemplate {
  const template = crewTemplates.find(item => item.id === id)
  if (!template) throw new Error(`Unknown Crew template: ${id}`)
  if (!parseCrewTemplateSetupState(template.files[template.setupPath] || '', template)) {
    throw new Error(`Invalid setup checklist for Crew template: ${id}`)
  }
  if (template.requiredFiles.some(path => !(path in template.files))) {
    throw new Error(`Missing required file in Crew template: ${id}`)
  }
  return template
}

export function matchesCrewTemplateSearch(template: CrewTemplate, search: string): boolean {
  const haystack = `${template.name} ${crewTemplateBrowsePath(template)} ${template.purpose} ${template.firstResult} ${template.minimumInput} ${template.optionalConnections} ${template.exampleRequests.join(' ')}`.toLowerCase()
  return search.trim().toLowerCase().split(/\s+/).filter(Boolean).every(term => haystack.includes(term))
}

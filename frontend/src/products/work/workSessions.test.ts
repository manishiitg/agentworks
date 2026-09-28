import { describe, expect, it, vi } from 'vitest'

const updatePlannerFile = vi.hoisted(() => vi.fn().mockResolvedValue({}))
const getPlannerFileContent = vi.hoisted(() => vi.fn())
const createPlannerFolder = vi.hoisted(() => vi.fn().mockResolvedValue({}))
const deleteAgentProfileProject = vi.hoisted(() => vi.fn().mockResolvedValue({ success: true }))
const listSharedProjects = vi.hoisted(() => vi.fn())
const loadProductProjects = vi.hoisted(() => vi.fn())

vi.mock('../../services/api', () => ({
  agentApi: { createPlannerFolder, deleteAgentProfileProject, getPlannerFileContent, listSharedProjects, updatePlannerFile },
  getApiBaseUrl: () => '',
  getAuthToken: () => null,
}))

vi.mock('../../platform/chat/productProjects', async importOriginal => {
  const actual = await importOriginal<typeof import('../../platform/chat/productProjects')>()
  return { ...actual, loadProductProjects }
})

vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
  ok: true,
  json: async () => ({
    runtime: {
      provider_options: [{
        id: 'muse-cli',
        provider: 'muse-cli',
        model_id: 'muse-spark-1.3-contributor',
        options: { reasoning_effort: 'max' },
        default: true,
      }],
    },
  }),
}))

import { updateProductProjectLLMConfig, updateProductProjectSelections } from '../../platform/chat/productProjects'
import type { SharedProjectSummary } from '../../services/api-types'
import { createWorkSession, deleteWorkSession, installWorkSessionTemplate, loadWorkSessionsIncludingShared, parseSessionManifest, sessionSlug, sharedProjectToWorkSession, updateWorkSessionIdentity, workLLMConfigFromSelection, workLLMSelectionFromConfig, type WorkSession } from './workSessions'

describe('sessionSlug', () => {
  it('slugifies titles and falls back', () => {
    expect(sessionSlug('My Website!')).toBe('my-website')
    expect(sessionSlug('  ')).toBe('workspace')
  })
})

describe('parseSessionManifest', () => {
  const manifest = JSON.stringify({
    schema_version: 1,
    product: 'work',
    id: 'abc',
    title: 'Site',
    description: 'Build it',
    session_id: 'work:project:abc',
    created_at: '2026-09-13T00:00:00Z',
    updated_at: '2026-09-13T01:00:00Z',
    capabilities: {
      selected_servers: ['github'],
      selected_skills: ['code-reviewer'],
      selected_secrets: ['GITHUB_TOKEN'],
      selected_global_secret_names: ['SHARED_API_TOKEN'],
      workflow_context_paths: ['Workflow/reference'],
      llm_config: {
        schema_version: 2,
        mode: 'explicit',
        builder_llm: { provider: 'muse-cli', model_id: 'muse-spark-1.3-contributor' },
      },
    },
  })

  it('parses a valid manifest', () => {
    const session = parseSessionManifest(manifest, 'Chats/Work/projects/site-abc')
    expect(session?.id).toBe('abc')
    expect(session?.workspacePath).toBe('Chats/Work/projects/site-abc')
    expect(session?.sessionId).toBe('work:project:abc')
    expect(session?.llmConfig?.builder_llm?.provider).toBe('muse-cli')
    expect(session?.selectedServers).toEqual(['github'])
    expect(session?.selectedSkills).toEqual(['code-reviewer'])
    expect(session?.selectedSecrets).toEqual(['GITHUB_TOKEN'])
    expect(session?.selectedGlobalSecrets).toEqual(['SHARED_API_TOKEN'])
    expect(session?.secretSelectionInitialized).toBe(true)
    expect(session?.workflowContextPaths).toEqual(['Workflow/reference'])
  })

  it('rejects other products and incomplete manifests', () => {
    expect(parseSessionManifest('not json', 'w')).toBeNull()
    expect(parseSessionManifest(JSON.stringify({ ...JSON.parse(manifest), product: 'video-studio' }), 'w')).toBeNull()
    expect(parseSessionManifest(JSON.stringify({ ...JSON.parse(manifest), session_id: '' }), 'w')).toBeNull()
  })

  it('reads a legacy single-template Crew as one installed pack', () => {
    const legacy = { ...JSON.parse(manifest), template: { id: 'finance-analyst', version: 1 } }
    expect(parseSessionManifest(JSON.stringify(legacy), 'w')?.templates).toEqual([{ id: 'finance-analyst', version: 1 }])
  })
})

describe('createWorkSession', () => {
  it('creates its own project folder without requiring a workspace selection', async () => {
    const session = await createWorkSession('New project', '', '🧭')

    expect(session.workspacePath).toMatch(/^Chats\/Work\/projects\/new-project-/)
    const runtimeCall = updatePlannerFile.mock.calls.find(call => call[0] === `${session.workspacePath}/workflow.json`)
    const productCall = updatePlannerFile.mock.calls.find(call => call[0] === `${session.workspacePath}/product.json`)
    expect(runtimeCall).toBeTruthy()
    expect(productCall).toBeTruthy()
    const runtime = JSON.parse(runtimeCall![1] as string)
    const product = JSON.parse(productCall![1] as string)
    expect(product).not.toHaveProperty('workspace_id')
    expect(product).not.toHaveProperty('capabilities')
    expect(product.identity).toEqual({ name: 'New project', icon: '🧭' })
    expect(session.identity).toEqual({ name: 'New project', icon: '🧭' })
    expect(runtime.capabilities.llm_config.builder_llm).toMatchObject({
      provider: 'muse-cli',
      model_id: 'muse-spark-1.3-contributor',
    })
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.capabilities.selected_skills).toEqual([])
    expect(runtime.capabilities.selected_secrets).toEqual([])
    expect(runtime.capabilities.selected_global_secret_names).toEqual([])
    expect(runtime.workflow_context_paths).toEqual([])
    expect(createPlannerFolder).toHaveBeenCalledWith(
      `${session.workspacePath}/code`,
      expect.stringContaining('Initialize Work project code folder'),
    )
  })

  it('creates a Finance Analyst with its local skill and no active integrations or automations', async () => {
    updatePlannerFile.mockClear()
    const session = await createWorkSession('My Finance Analyst', 'Review the shop’s finances.', '📊', 'finance-analyst')
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const product = JSON.parse(writes.get(`${session.workspacePath}/product.json`)!)
    const runtime = JSON.parse(writes.get(`${session.workspacePath}/workflow.json`)!)

    expect(product.templates).toEqual([{ id: 'finance-analyst', version: 1 }])
    expect(product.identity.role).toBe('Finance analyst for this business')
    expect(product.description).toBe('Review the shop’s finances.')
    expect(runtime.capabilities.selected_skills).toEqual(['finance-analyst'])
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(writes.get(`${session.workspacePath}/skills/finance-analyst/SKILL.md`)).toContain('Show the calculation behind every key figure')
    expect(writes.get(`${session.workspacePath}/TEMPLATE_SETUP.md`)).toContain('These are suggestions')
    const setup = JSON.parse(writes.get(`${session.workspacePath}/TEMPLATE_SETUP.json`)!)
    expect(setup).toMatchObject({ schema_version: 1, template_id: 'finance-analyst', template_version: 1, completed_steps: [] })
    expect(setup.checks).toHaveLength(9)
    expect(session.templates).toEqual([{ id: 'finance-analyst', version: 1 }])
    expect(session.selectedSkills).toEqual(['finance-analyst'])
  })

  it.each([
    ['billing-operations-coordinator', 'Billing Operations Coordinator', 1],
    ['revenue-close-analyst', 'Revenue & Close Analyst', 1],
    ['spend-payables-coordinator', 'Spend & Payables Coordinator', 2],
  ] as const)('creates the %s finance specialist ready for chat setup, without financial actions', async (id, name, version) => {
    updatePlannerFile.mockClear()
    const session = await createWorkSession(name, `Review ${name.toLowerCase()} records.`, undefined, id)
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const runtime = JSON.parse(writes.get(`${session.workspacePath}/workflow.json`)!)
    const setup = JSON.parse(writes.get(`${session.workspacePath}/templates/${id}/TEMPLATE_SETUP.json`)!)

    expect(session.templates).toEqual([{ id, version }])
    expect(runtime.capabilities.selected_skills).toEqual([id])
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(setup).toMatchObject({ template_id: id, completed_steps: [] })
    expect(setup.checks).toHaveLength(9)
    expect(writes.get(`${session.workspacePath}/skills/${id}/SKILL.md`)).toContain('## Setup in chat')
  })

  it('creates an Engineering Operations Analyst with pending metric setup and no active route', async () => {
    updatePlannerFile.mockClear()
    const id = 'engineering-operations-analyst'
    const session = await createWorkSession('Engineering Operations Analyst', 'Review one team metric.', undefined, id)
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const runtime = JSON.parse(writes.get(`${session.workspacePath}/workflow.json`)!)
    const setup = JSON.parse(writes.get(`${session.workspacePath}/templates/${id}/TEMPLATE_SETUP.json`)!)
    expect(session.templates).toEqual([{ id, version: 1 }])
    expect(runtime.capabilities.selected_skills).toEqual([id])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(setup).toMatchObject({ template_id: id, completed_steps: [] })
    expect(setup.checks).toHaveLength(9)
    expect(writes.get(`${session.workspacePath}/skills/${id}/SKILL.md`)).toContain('engineering-metric-observation/v1')
  })

  it.each([
    ['competitor-intelligence-analyst', 'Competitor Intelligence Analyst'],
    ['campaign-performance-analyst', 'Campaign Performance Analyst'],
    ['growth-experiment-planner', 'Growth Experiment Planner'],
  ] as const)('creates the %s Marketing Crew with pending setup and no campaign action', async (id, name) => {
    updatePlannerFile.mockClear()
    const session = await createWorkSession(name, 'Review a source-linked marketing result.', undefined, id)
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const runtime = JSON.parse(writes.get(`${session.workspacePath}/workflow.json`)!)
    const setup = JSON.parse(writes.get(`${session.workspacePath}/templates/${id}/TEMPLATE_SETUP.json`)!)
    expect(session.templates).toEqual([{ id, version: 2 }])
    expect(runtime.capabilities.selected_skills).toEqual([id])
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(setup).toMatchObject({ template_id: id, completed_steps: [] })
    expect(setup.checks).toHaveLength(9)
    expect(writes.get(`${session.workspacePath}/skills/${id}/SKILL.md`)).toContain('## Setup through chat')
  })

  it.each([
    ['lead-intake-qualifier', 'Lead Intake & Qualifier', 1],
    ['account-researcher', 'Account Researcher', 1],
    ['sales-followup-coordinator', 'Sales Follow-up Coordinator', 3],
  ] as const)('creates the %s Sales specialist with its checklist and no outbound route', async (id, name, version) => {
    updatePlannerFile.mockClear()
    const session = await createWorkSession(name, `Review ${name.toLowerCase()} records.`, undefined, id)
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const runtime = JSON.parse(writes.get(`${session.workspacePath}/workflow.json`)!)
    const setup = JSON.parse(writes.get(`${session.workspacePath}/templates/${id}/TEMPLATE_SETUP.json`)!)
    expect(session.templates).toEqual([{ id, version }])
    expect(runtime.capabilities.selected_skills).toEqual([id])
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(setup).toMatchObject({ template_id: id, completed_steps: [] })
    expect(setup.checks).toHaveLength(9)
    expect(writes.get(`${session.workspacePath}/skills/${id}/SKILL.md`)).toContain('## Setup through chat')
  })

  it.each([
    ['customer-onboarding-coordinator', 'Customer Onboarding Coordinator', 1],
    ['product-adoption-analyst', 'Product Adoption Analyst', 3],
    ['customer-health-coordinator', 'Customer Health Coordinator', 1],
    ['renewal-coordinator', 'Renewal Coordinator', 1],
  ] as const)('creates the %s Customer Success Crew with pending setup', async (id, name, version) => {
    updatePlannerFile.mockClear()
    const session = await createWorkSession(name, 'Help customers reach first value.', undefined, id)
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const runtime = JSON.parse(writes.get(`${session.workspacePath}/workflow.json`)!)
    const setup = JSON.parse(writes.get(`${session.workspacePath}/templates/${id}/TEMPLATE_SETUP.json`)!)
    expect(session.templates).toEqual([{ id, version }])
    expect(runtime.capabilities.selected_skills).toEqual([id])
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(setup).toMatchObject({ template_id: id, completed_steps: [] })
    expect(setup.checks).toHaveLength(9)
  })

  it('creates Product Feedback Coordinator with route-scoped pending setup and no issue action', async () => {
    updatePlannerFile.mockClear()
    const id = 'product-feedback-coordinator'
    const session = await createWorkSession('Product Feedback Coordinator', 'Review one product decision.', undefined, id)
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const runtime = JSON.parse(writes.get(`${session.workspacePath}/workflow.json`)!)
    const setup = JSON.parse(writes.get(`${session.workspacePath}/templates/${id}/TEMPLATE_SETUP.json`)!)
    expect(session.templates).toEqual([{ id, version: 3 }])
    expect(runtime.capabilities.selected_skills).toEqual([id])
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(setup).toMatchObject({ template_id: id, template_version: 3, completed_steps: [] })
    expect(setup.checks).toHaveLength(9)
  })

  it.each([
    ['browser-journey-qa-analyst', 'Browser Journey QA Analyst'],
    ['flaky-test-investigator', 'Flaky Test Investigator'],
    ['release-quality-assistant', 'Release Quality Assistant'],
  ] as const)('creates the %s QA Crew without activating a test or status route', async (id, name) => {
    updatePlannerFile.mockClear()
    const session = await createWorkSession(name, 'Review exact QA evidence.', undefined, id)
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const runtime = JSON.parse(writes.get(session.workspacePath + '/workflow.json')!)
    const setup = JSON.parse(writes.get(session.workspacePath + '/templates/' + id + '/TEMPLATE_SETUP.json')!)
    expect(session.templates).toEqual([{ id, version: 2 }])
    expect(runtime.capabilities.selected_skills).toEqual([id])
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(setup).toMatchObject({ template_id: id, completed_steps: [] })
    expect(setup.checks).toHaveLength(9)
  })

  it.each([
    ['security-findings-analyst', 'Security Findings Analyst'],
    ['access-review-analyst', 'Access Review Analyst'],
    ['security-remediation-coordinator', 'Security Remediation Coordinator'],
  ] as const)('creates the %s Security Crew without activating a scan or change', async (id, name) => {
    updatePlannerFile.mockClear()
    const session = await createWorkSession(name, 'Review authorized security evidence.', undefined, id)
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const runtime = JSON.parse(writes.get(session.workspacePath + '/workflow.json')!)
    const setup = JSON.parse(writes.get(session.workspacePath + '/templates/' + id + '/TEMPLATE_SETUP.json')!)
    expect(session.templates).toEqual([{ id, version: 2 }])
    expect(runtime.capabilities.selected_skills).toEqual([id])
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(setup).toMatchObject({ template_id: id, completed_steps: [] })
    expect(setup.checks).toHaveLength(9)
  })

  it.each([
    ['gtm-strategy-analyst', 'GTM Strategy Analyst'],
    ['launch-coordinator', 'Launch Coordinator'],
  ] as const)('creates the %s GTM Crew without activating distribution or outreach', async (id, name) => {
    updatePlannerFile.mockClear()
    const session = await createWorkSession(name, 'Review a sourced GTM plan.', undefined, id)
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const runtime = JSON.parse(writes.get(session.workspacePath + '/workflow.json')!)
    const setup = JSON.parse(writes.get(session.workspacePath + '/templates/' + id + '/TEMPLATE_SETUP.json')!)
    expect(session.templates).toEqual([{ id, version: 2 }])
    expect(runtime.capabilities.selected_skills).toEqual([id])
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(setup).toMatchObject({ template_id: id, completed_steps: [] })
    expect(setup.checks).toHaveLength(9)
  })

  it.each([
    ['support-triage-assistant', 'Support Triage Assistant'],
    ['support-reply-drafter', 'Support Reply Drafter'],
    ['escalation-coordinator', 'Escalation Coordinator'],
    ['feedback-review-analyst', 'Feedback & Review Analyst'],
  ] as const)('creates the %s Customer Support Crew without a case write or message', async (id, name) => {
    updatePlannerFile.mockClear()
    const session = await createWorkSession(name, 'Review a support case.', undefined, id)
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const runtime = JSON.parse(writes.get(session.workspacePath + '/workflow.json')!)
    const setup = JSON.parse(writes.get(session.workspacePath + '/templates/' + id + '/TEMPLATE_SETUP.json')!)
    expect(session.templates).toEqual([{ id, version: 2 }])
    expect(runtime.capabilities.selected_skills).toEqual([id])
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(setup).toMatchObject({ template_id: id, completed_steps: [] })
    expect(setup.checks).toHaveLength(9)
  })

  it.each([
    ['chief-of-staff', 'Chief of Staff'],
    ['meeting-actions-coordinator', 'Meeting Actions Coordinator'],
    ['project-status-reporter', 'Project Status Reporter'],
    ['order-operations-coordinator', 'Order Operations Coordinator'],
    ['vendor-researcher', 'Vendor Researcher'],
    ['document-intake-assistant', 'Document Intake Assistant'],
  ] as const)('creates the %s Operations Crew without activating external actions', async (id, name) => {
    updatePlannerFile.mockClear()
    const session = await createWorkSession(name, 'Review a sourced operations result.', undefined, id)
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const runtime = JSON.parse(writes.get(session.workspacePath + '/workflow.json')!)
    const setup = JSON.parse(writes.get(session.workspacePath + '/templates/' + id + '/TEMPLATE_SETUP.json')!)
    expect(session.templates).toEqual([{ id, version: 2 }])
    expect(runtime.capabilities.selected_skills).toEqual([id])
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(setup).toMatchObject({ template_id: id, completed_steps: [] })
    expect(setup.checks).toHaveLength(9)
  })

  it('creates a Website Growth Starter with a separate setup checklist and no active connections', async () => {
    updatePlannerFile.mockClear()
    const session = await createWorkSession('Our Website Growth Crew', 'Grow relevant website visits.', '🌱', 'website-growth-starter')
    const writes = new Map(updatePlannerFile.mock.calls.map(call => [call[0] as string, call[1] as string]))
    const product = JSON.parse(writes.get(`${session.workspacePath}/product.json`)!)
    const runtime = JSON.parse(writes.get(`${session.workspacePath}/workflow.json`)!)
    const setup = JSON.parse(writes.get(`${session.workspacePath}/templates/website-growth-starter/TEMPLATE_SETUP.json`)!)

    expect(product.templates).toEqual([{ id: 'website-growth-starter', version: 1 }])
    expect(product.identity.role).toBe('Website growth strategist for this business')
    expect(runtime.capabilities.selected_skills).toEqual(['website-growth-starter'])
    expect(runtime.capabilities.selected_servers).toEqual([])
    expect(runtime.schedules).toEqual([])
    expect(runtime.triggers).toEqual([])
    expect(writes.get(`${session.workspacePath}/skills/website-growth-starter/SKILL.md`)).toContain('Website Growth Brief')
    expect(setup).toMatchObject({ schema_version: 1, template_id: 'website-growth-starter', template_version: 1, completed_steps: [] })
    expect(setup.checks).toHaveLength(10)
  })

  it('adds Tax Export to Finance Analyst without changing identity, existing skill, or setup progress', async () => {
    const files = new Map<string, string>()
    updatePlannerFile.mockImplementation(async (path: string, content: string) => { files.set(path, content); return {} })
    getPlannerFileContent.mockImplementation(async (path: string) => {
      const content = files.get(path)
      if (content === undefined) throw { response: { status: 404 } }
      return { data: { content } }
    })
    try {
      const original = await createWorkSession('My Finance Analyst', 'Review the shop’s finances.', '📊', 'finance-analyst')
      const financeSetupPath = `${original.workspacePath}/TEMPLATE_SETUP.json`
      const financeSetup = JSON.parse(files.get(financeSetupPath)!)
      financeSetup.completed_steps = ['identity', 'skill']
      files.set(financeSetupPath, JSON.stringify(financeSetup))
      const updated = await installWorkSessionTemplate(original, 'tax-export')
      const product = JSON.parse(files.get(`${original.workspacePath}/product.json`)!)
      const runtime = JSON.parse(files.get(`${original.workspacePath}/workflow.json`)!)
      expect(updated.templates).toEqual([{ id: 'finance-analyst', version: 1 }, { id: 'tax-export', version: 1 }])
      expect(product.identity).toEqual({ name: 'My Finance Analyst', icon: '📊', role: 'Finance analyst for this business' })
      expect(product.description).toBe('Review the shop’s finances.')
      expect(runtime.capabilities.selected_skills).toEqual(['finance-analyst', 'tax-export'])
      expect(files.get(financeSetupPath)).toBe(JSON.stringify(financeSetup))
      expect(JSON.parse(files.get(`${original.workspacePath}/templates/tax-export/TEMPLATE_SETUP.json`)!).checks).toHaveLength(8)
      await expect(installWorkSessionTemplate(updated, 'tax-export')).rejects.toThrow('already installed')
    } finally {
      updatePlannerFile.mockReset().mockResolvedValue({})
      getPlannerFileContent.mockReset()
    }
  })

  it('adds multiple Billing packs to one Crew without replacing identity or completed setup', async () => {
    const files = new Map<string, string>()
    updatePlannerFile.mockImplementation(async (path: string, content: string) => { files.set(path, content); return {} })
    getPlannerFileContent.mockImplementation(async (path: string) => {
      const content = files.get(path)
      if (content === undefined) throw { response: { status: 404 } }
      return { data: { content } }
    })
    try {
      const original = await createWorkSession('Billing Desk', 'Review customer billing issues.', '💳', 'billing-operations-coordinator')
      const baseSetupPath = `${original.workspacePath}/templates/billing-operations-coordinator/TEMPLATE_SETUP.json`
      const baseSetup = JSON.parse(files.get(baseSetupPath)!)
      baseSetup.completed_steps = ['identity', 'skill']
      files.set(baseSetupPath, JSON.stringify(baseSetup))
      const withInvoices = await installWorkSessionTemplate(original, 'invoice-chasing')
      const withRefunds = await installWorkSessionTemplate(withInvoices, 'refund-review')
      const product = JSON.parse(files.get(`${original.workspacePath}/product.json`)!)
      const runtime = JSON.parse(files.get(`${original.workspacePath}/workflow.json`)!)
      expect(withRefunds.templates).toEqual([
        { id: 'billing-operations-coordinator', version: 1 },
        { id: 'invoice-chasing', version: 1 },
        { id: 'refund-review', version: 1 },
      ])
      expect(product.identity).toEqual({ name: 'Billing Desk', icon: '💳', role: 'Subscription billing and payment operations coordinator' })
      expect(runtime.capabilities.selected_skills).toEqual(['billing-operations-coordinator', 'invoice-chasing', 'refund-review'])
      expect(files.get(baseSetupPath)).toBe(JSON.stringify(baseSetup))
      for (const id of ['invoice-chasing', 'refund-review']) {
        const setup = JSON.parse(files.get(`${original.workspacePath}/templates/${id}/TEMPLATE_SETUP.json`)!)
        expect(setup.completed_steps).toEqual([])
        expect(setup.checks).toHaveLength(9)
      }
    } finally {
      updatePlannerFile.mockReset().mockResolvedValue({})
      getPlannerFileContent.mockReset()
    }
  })
})

describe('deleteWorkSession', () => {
  it('deletes the authenticated durable Crew project through its profile', async () => {
    const session = parseSessionManifest(JSON.stringify({
      schema_version: 1,
      product: 'work',
      id: 'crew-1',
      title: 'Research',
      session_id: 'work:project:crew-1',
    }), 'Chats/Work/projects/research-crew-1')!

    await deleteWorkSession(session)

    expect(deleteAgentProfileProject).toHaveBeenCalledWith('work', 'crew-1')
  })

  it('refuses to delete someone else’s Crew', async () => {
    const session = sharedProjectToWorkSession(sharedRow({ id: 'crew-shared' }))

    await expect(deleteWorkSession(session)).rejects.toThrow('only be deleted by their owner')
    expect(deleteAgentProfileProject).not.toHaveBeenCalledWith('work', 'crew-shared')
  })
})

function sharedRow(overrides: Partial<SharedProjectSummary> = {}): SharedProjectSummary {
  return {
    id: 'crew-shared',
    title: 'Shared Research',
    description: 'Another user’s Crew.',
    icon: '🔬',
    name: 'Researcher',
    owner_id: 'owner-1',
    owner_username: 'ada',
    workspace_path: '_users/owner-1/Chats/Work/projects/crew-shared',
    created_at: '2026-09-20T00:00:00Z',
    updated_at: '2026-09-21T00:00:00Z',
    llm: { provider: 'muse-cli', model_id: 'muse-spark-1.3-contributor', reasoning_effort: 'max' },
    selected_servers: ['github'],
    selected_skills: ['code-reviewer'],
    selected_secrets: ['GITHUB_TOKEN'],
    selected_global_secrets: ['SHARED_API_TOKEN'],
    workflow_context_paths: ['Workflow/reference'],
    triggers: [{ id: 'trig-1', name: 'Nightly', enabled: true }],
    schedules: [{ id: 'sched-1', name: 'Daily', enabled: true }],
    ...overrides,
  }
}

function ownedSession(overrides: Partial<WorkSession> = {}): WorkSession {
  return {
    schemaVersion: 1,
    product: 'work',
    id: 'crew-owned',
    title: 'Owned',
    description: '',
    templates: [],
    sessionId: 'work:project:crew-owned',
    workspacePath: 'Chats/Work/projects/owned-crew-owned',
    createdAt: '',
    updatedAt: '',
    selectedServers: [],
    selectedSkills: [],
    selectedSecrets: [],
    selectedGlobalSecrets: [],
    workflowContextPaths: [],
    selectionConfigInitialized: true,
    secretSelectionInitialized: true,
    runtimeConfigInitialized: true,
    ...overrides,
  }
}

describe('sharedProjectToWorkSession', () => {
  it('maps a shared row to a read-only session stub without the owner’s live session', () => {
    const session = sharedProjectToWorkSession(sharedRow())

    expect(session.id).toBe('crew-shared')
    expect(session.title).toBe('Shared Research')
    expect(session.identity).toEqual({ icon: '🔬', name: 'Researcher' })
    expect(session.sessionId).toBe('')
    expect(session.workspacePath).toBe('_users/owner-1/Chats/Work/projects/crew-shared')
    expect(session.llmConfig?.builder_llm).toMatchObject({ provider: 'muse-cli', model_id: 'muse-spark-1.3-contributor' })
    expect(session.selectedServers).toEqual(['github'])
    expect(session.selectedSkills).toEqual(['code-reviewer'])
    expect(session.selectedSecrets).toEqual(['GITHUB_TOKEN'])
    expect(session.selectedGlobalSecrets).toEqual(['SHARED_API_TOKEN'])
    expect(session.workflowContextPaths).toEqual(['Workflow/reference'])
    expect(session.selectionConfigInitialized).toBe(true)
    expect(session.secretSelectionInitialized).toBe(true)
    expect(session.runtimeConfigInitialized).toBe(true)
    expect(session.shared).toEqual({
      ownerId: 'owner-1',
      ownerUsername: 'ada',
      triggers: [{ id: 'trig-1', name: 'Nightly', enabled: true }],
      schedules: [{ id: 'sched-1', name: 'Daily', enabled: true }],
    })
  })
})

describe('loadWorkSessionsIncludingShared', () => {
  it('lists owned Crews first, then shared Crews', async () => {
    loadProductProjects.mockResolvedValue([ownedSession()])
    listSharedProjects.mockResolvedValue({ projects: [sharedRow()] })

    const sessions = await loadWorkSessionsIncludingShared()

    expect(listSharedProjects).toHaveBeenCalledWith('work')
    expect(sessions.map(session => session.id)).toEqual(['crew-owned', 'crew-shared'])
    expect(sessions[1].shared?.ownerId).toBe('owner-1')
  })

  it('prefers the owned Crew when an id collides', async () => {
    loadProductProjects.mockResolvedValue([ownedSession({ id: 'crew-dup', title: 'Mine' })])
    listSharedProjects.mockResolvedValue({ projects: [sharedRow({ id: 'crew-dup', title: 'Theirs' })] })

    const sessions = await loadWorkSessionsIncludingShared()

    expect(sessions.map(session => session.id)).toEqual(['crew-dup'])
    expect(sessions[0].title).toBe('Mine')
    expect(sessions[0].shared).toBeUndefined()
  })

  it('degrades to owned-only when the shared listing fails', async () => {
    loadProductProjects.mockResolvedValue([ownedSession()])
    listSharedProjects.mockRejectedValue(new Error('boom'))

    const sessions = await loadWorkSessionsIncludingShared()

    expect(sessions.map(session => session.id)).toEqual(['crew-owned'])
  })
})

describe('updateWorkSessionIdentity', () => {
  it('refuses identity writes to someone else’s Crew', async () => {
    const session = sharedProjectToWorkSession(sharedRow())

    await expect(updateWorkSessionIdentity(session, { name: 'Hijacked' })).rejects.toThrow('Only the Crew owner')
  })
})

describe('updateProductProjectSelections', () => {
  it('persists Work runtime selections in workflow.json while preserving other capabilities', async () => {
    const project = parseSessionManifest(JSON.stringify({
      schema_version: 1,
      product: 'work',
      id: 'abc',
      title: 'Site',
      session_id: 'work:project:abc',
      capabilities: { custom_feature: { enabled: true } },
    }), 'Chats/Work/projects/site-abc')!
    getPlannerFileContent.mockResolvedValueOnce({
      success: true,
      data: { content: JSON.stringify({
        schema_version: 1,
        id: 'abc',
        label: 'Site',
        capabilities: { custom_feature: { enabled: true } },
      }) },
    })

    const updated = await updateProductProjectSelections(project, {
      selectedServers: ['google_sheets', 'google_sheets'],
      selectedSkills: ['work-dashboard'],
      selectedSecrets: ['GITHUB_TOKEN', 'GITHUB_TOKEN'],
      selectedGlobalSecrets: ['SHARED_API_TOKEN', 'SHARED_API_TOKEN'],
      workflowContextPaths: ['Workflow/reference', 'Workflow/reference'],
    }, 'Update integrations', 'workflow.json')

    expect(updated.selectedServers).toEqual(['google_sheets'])
    expect(updated.selectedSkills).toEqual(['work-dashboard'])
    expect(updated.selectedSecrets).toEqual(['GITHUB_TOKEN'])
    expect(updated.selectedGlobalSecrets).toEqual(['SHARED_API_TOKEN'])
    expect(updated.workflowContextPaths).toEqual(['Workflow/reference'])
    const [path, content] = updatePlannerFile.mock.calls.at(-1)!
    expect(path).toBe(`${project.workspacePath}/workflow.json`)
    const manifest = JSON.parse(content as string)
    expect(manifest.capabilities.custom_feature).toEqual({ enabled: true })
    expect(manifest.capabilities.selected_servers).toEqual(['google_sheets'])
    expect(manifest.capabilities.selected_skills).toEqual(['work-dashboard'])
    expect(manifest.capabilities.selected_secrets).toEqual(['GITHUB_TOKEN'])
    expect(manifest.capabilities.selected_global_secret_names).toEqual(['SHARED_API_TOKEN'])
    expect(manifest.workflow_context_paths).toEqual(['Workflow/reference'])
  })
})

describe('updateProductProjectLLMConfig', () => {
  it('preserves other project capabilities while replacing the shared LLM configuration', async () => {
    const project = parseSessionManifest(JSON.stringify({
      schema_version: 1,
      product: 'work',
      id: 'abc',
      title: 'Site',
      session_id: 'work:project:abc',
      capabilities: { custom_feature: { enabled: true } },
    }), 'Chats/Work/projects/site-abc')!
    getPlannerFileContent.mockResolvedValueOnce({
      success: true,
      data: {
        content: JSON.stringify({
          schema_version: 1,
          id: 'abc',
          label: 'Site',
          capabilities: { custom_feature: { enabled: true } },
        }),
      },
    })

    const llmConfig = workLLMConfigFromSelection({
      provider: 'codex-cli',
      modelId: 'gpt-6-astra',
      reasoningEffort: 'high',
    })
    const updated = await updateProductProjectLLMConfig(project, llmConfig, 'Update model', 'workflow.json')

    expect(updated.llmConfig).toEqual(llmConfig)
    const [path, content] = updatePlannerFile.mock.calls.at(-1)!
    expect(path).toBe(`${project.workspacePath}/workflow.json`)
    const manifest = JSON.parse(content as string)
    expect(manifest.capabilities.custom_feature).toEqual({ enabled: true })
    expect(manifest.capabilities.llm_config).toEqual(llmConfig)
  })
})

describe('private account persistence',()=>{
 it('round-trips the selected account separately from the model',()=>{
 const selected={provider:'codex-cli' as const,modelId:'gpt-test',connectionId:'account-B'}
 const config=workLLMConfigFromSelection(selected)
 expect(config.builder_llm?.connection_id).toBe('account-B')
 expect(workLLMSelectionFromConfig(config)).toMatchObject(selected)
 })
})

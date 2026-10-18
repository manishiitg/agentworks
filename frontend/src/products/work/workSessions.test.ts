import { describe, expect, it, vi } from 'vitest'
import { CODE_PRODUCT } from './projectProduct'

const updatePlannerFile = vi.hoisted(() => vi.fn().mockResolvedValue({}))
const getPlannerFileContent = vi.hoisted(() => vi.fn())
const createServerProject = vi.hoisted(() => vi.fn())
const serverFiles = vi.hoisted(() => new Map<string, string>())
const createPlannerFolder = vi.hoisted(() => vi.fn().mockResolvedValue({}))
const deleteAgentProfileProject = vi.hoisted(() => vi.fn().mockResolvedValue({ success: true }))
const listSharedProjects = vi.hoisted(() => vi.fn())
const loadProductProjects = vi.hoisted(() => vi.fn())

vi.mock('../../services/api', () => ({
  agentApi: { createProductProject: createServerProject, createPlannerFolder, deleteAgentProfileProject, getPlannerFileContent, listSharedProjects, updatePlannerFile },
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

// The backend owns creation. This response fixture lets consumer tests keep
// testing selection forwarding and later template edits without a browser writer.
createServerProject.mockImplementation(async (product: string, spec: { name: string; purpose: string; icon?: string; role?: string; mode?: string; templates?: { id: string; version: number }[]; selected_skills?: string[]; llm_config?: unknown; files?: Record<string, string> }) => {
  const id = 'server-project'
  const root = 'Crew/server-project'
  const manifest = { schema_version: 1, product, id, title: spec.name, description: spec.purpose, identity: { name: spec.name, icon: spec.icon, role: spec.role }, templates: spec.templates, mode: spec.mode, session_id: `${product}:project:${id}` }
  const runtime = { capabilities: { selected_servers: [], selected_skills: spec.selected_skills || [], selected_secrets: [], selected_global_secret_names: [], llm_config: spec.llm_config }, workflow_context_paths: [] }
  serverFiles.set(`${root}/product.json`, JSON.stringify(manifest))
  serverFiles.set(`${root}/workflow.json`, JSON.stringify(runtime))
  for (const [path, content] of Object.entries(spec.files || {})) serverFiles.set(`${root}/${path}`, content as string)
  return { id, workspace_path: root, product_manifest: manifest, runtime_manifest: runtime }
})

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
  it('forwards model, mode, identity and a selected template to server creation', async () => {
    updatePlannerFile.mockClear()
    createPlannerFolder.mockClear()
    const session = await createWorkSession('Finance desk', 'Review records.', '📊', 'finance-analyst', CODE_PRODUCT, { provider: 'codex-cli', modelId: 'gpt-6-luna', connectionId: 'company-account' }, 'cowork')
    expect(createServerProject).toHaveBeenLastCalledWith('code', expect.objectContaining({ name: 'Finance desk', purpose: 'Review records.', icon: '📊', mode: 'cowork', templates: [{ id: 'finance-analyst', version: 1 }], selected_skills: ['finance-analyst'], llm_config: expect.objectContaining({ builder_llm: expect.objectContaining({ provider: 'codex-cli', model_id: 'gpt-6-luna', connection_id: 'company-account' }) }) }))
    expect(session.workspacePath).toBe('Crew/server-project')
    expect(session.product).toBe('code')
    expect(session.mode).toBe('cowork')
    expect(updatePlannerFile).not.toHaveBeenCalled()
    expect(createPlannerFolder).not.toHaveBeenCalled()
  })

  it('adds Tax Export to Finance Analyst without changing identity, existing skill, or setup progress', async () => {
    const files = serverFiles
    files.clear()
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
    const files = serverFiles
    files.clear()
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
  it('allows cached project cleanup when Files already removed the project', async () => {
    deleteAgentProfileProject.mockRejectedValueOnce({ response: { status: 404 } })
    await expect(deleteWorkSession({ product: 'code', id: 'gone-code' } as WorkSession)).resolves.toBeUndefined()
  })
  it.each([403, 409, 500])('keeps deletion errors visible for status %s', async status => {
    const error = { response: { status } }
    deleteAgentProfileProject.mockRejectedValueOnce(error)
    await expect(deleteWorkSession({ product: 'code', id: 'existing-code' } as WorkSession)).rejects.toBe(error)
  })
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
      canBuild: false,
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

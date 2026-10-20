// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DiscoveredWorkflow, LLMProvider, WorkflowManifest } from '../services/api-types'

vi.hoisted(() => vi.stubGlobal('localStorage', {
  getItem: () => null, setItem: () => {}, removeItem: () => {}, clear: () => {},
}))

vi.mock('../services/api', async importOriginal => ({
  ...await importOriginal<typeof import('../services/api')>(),
  workflowManifestApi: {
    deleteWorkflowFolder: vi.fn(async () => ({ success: true })),
    listWorkflowManifests: vi.fn(),
  },
}))

import { useWorkflowManifestStore } from './useWorkflowManifestStore'
import { workflowManifestApi } from '../services/api'
import { useGlobalPresetStore } from './useGlobalPresetStore'

const manifest = (provider: LLMProvider): WorkflowManifest => ({
  schema_version: 1,
  id: 'workflow-1',
  label: 'Workflow 1',
  capabilities: {
    selected_servers: [],
    selected_tools: [],
    selected_skills: [],
    selected_secrets: [],
    selected_global_secret_names: null,
    browser_mode: 'none',
    use_code_execution_mode: false,
    llm_config: {
      schema_version: 2,
      mode: 'provider_profile',
      provider,
    },
  },
  execution_defaults: {},
  ownership: { employee_id: null },
  schedules: [],
})

describe('useWorkflowManifestStore', () => {
  beforeEach(() => {
    vi.mocked(workflowManifestApi.deleteWorkflowFolder).mockReset().mockResolvedValue({ success: true })
    vi.mocked(workflowManifestApi.listWorkflowManifests).mockReset()
    useWorkflowManifestStore.setState({
      workflows: [{ workspace_path: '/tmp/workflow/', manifest: manifest('muse-cli') } as DiscoveredWorkflow],
      lastRefreshed: null,
      activeWorkflowId: 'workflow-1',
    })
  })

  it('replaces the shared manifest immediately using normalized workspace paths', () => {
    useWorkflowManifestStore.getState().replaceWorkflowManifest('/tmp/workflow', manifest('claude-code'))

    expect(useWorkflowManifestStore.getState().getWorkflowByPath('/tmp/workflow/')?.manifest.capabilities.llm_config?.provider)
      .toBe('claude-code')
  })

  it('adds the authoritative manifest when the shared cache has not loaded yet', () => {
    useWorkflowManifestStore.setState({ workflows: [] })
    useWorkflowManifestStore.getState().replaceWorkflowManifest('/tmp/workflow', manifest('codex-cli'))

    expect(useWorkflowManifestStore.getState().getWorkflowByPath('/tmp/workflow')?.manifest.capabilities.llm_config?.provider)
      .toBe('codex-cli')
  })

  it('removes a deleted workflow without another full discovery scan', async () => {
    vi.mocked(workflowManifestApi.listWorkflowManifests).mockClear()
    await useWorkflowManifestStore.getState().deleteWorkflow('/tmp/workflow')
    expect(useWorkflowManifestStore.getState().workflows).toEqual([])
    expect(useWorkflowManifestStore.getState().activeWorkflowId).toBeNull()
    expect(workflowManifestApi.listWorkflowManifests).not.toHaveBeenCalled()
  })

  it('does not resurrect a deletion from an older in-flight discovery response', async () => {
    let finish!: (value: never) => void
    const oldWorkflows = useWorkflowManifestStore.getState().workflows
    vi.mocked(workflowManifestApi.listWorkflowManifests).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const refresh = useWorkflowManifestStore.getState().refreshWorkflows()
    await useWorkflowManifestStore.getState().deleteWorkflow('/tmp/workflow')
    finish({ workflows: oldWorkflows } as never)
    await refresh
    expect(useWorkflowManifestStore.getState().workflows).toEqual([])
  })

  it('preserves another workflow selected while deletion is pending', async () => {
    let finish!: (value: never) => void
    vi.mocked(workflowManifestApi.deleteWorkflowFolder).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    useGlobalPresetStore.setState(state => ({
      workflowPresets: [
        { id: 'workflow-1', label: 'Deleting', selectedFolder: { filepath: '/tmp/workflow/' } },
        { id: 'workflow-2', label: 'Keep', selectedFolder: { filepath: '/tmp/keep' } },
      ] as typeof state.workflowPresets,
      activePresetIds: { ...state.activePresetIds, workflow: 'workflow-1' },
    }))
    const pending = useGlobalPresetStore.getState().deleteWorkflow('/tmp/workflow')
    useGlobalPresetStore.setState(state => ({
      activePresetIds: { ...state.activePresetIds, workflow: 'workflow-2' },
      selectedPresetFolder: '/tmp/keep',
      currentPresetServers: ['keep-server'],
    }))
    useWorkflowManifestStore.setState({ activeWorkflowId: 'workflow-2' })
    finish({ success: true } as never)
    await pending
    expect(useGlobalPresetStore.getState().workflowPresets.map(preset => preset.id)).toEqual(['workflow-2'])
    expect(useGlobalPresetStore.getState().activePresetIds.workflow).toBe('workflow-2')
    expect(useGlobalPresetStore.getState().selectedPresetFolder).toBe('/tmp/keep')
    expect(useGlobalPresetStore.getState().currentPresetServers).toEqual(['keep-server'])
    expect(useWorkflowManifestStore.getState().activeWorkflowId).toBe('workflow-2')
  })

  it('keeps the workflow visible when the server refuses deletion', async () => {
    vi.mocked(workflowManifestApi.deleteWorkflowFolder).mockRejectedValueOnce(new Error('Forbidden'))
    await expect(useWorkflowManifestStore.getState().deleteWorkflow('/tmp/workflow')).rejects.toThrow('Forbidden')
    expect(useWorkflowManifestStore.getState().workflows).toHaveLength(1)
    expect(useWorkflowManifestStore.getState().activeWorkflowId).toBe('workflow-1')
  })
})

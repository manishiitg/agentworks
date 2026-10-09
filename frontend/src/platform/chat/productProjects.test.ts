import { beforeEach, describe, expect, it, vi } from 'vitest'

const getPlannerFileContent = vi.hoisted(() => vi.fn())
const updatePlannerFile = vi.hoisted(() => vi.fn().mockResolvedValue({}))
const getPlannerFiles = vi.hoisted(() => vi.fn())
const listOwnSharedProjects = vi.hoisted(() => vi.fn())

vi.mock('../../services/api', () => ({ agentApi: { getPlannerFileContent, updatePlannerFile, getPlannerFiles, listOwnSharedProjects } }))

import { agentApi } from '../../services/api'
import { loadProductProjects, parseProductProjectManifest, updateProductProjectIdentity, productMode, updateProductProjectLocalFiles, type ProductProject } from './productProjects'

describe('parseProductProjectManifest', () => {
  it('loads the project bot identity and icon', () => {
    const project = parseProductProjectManifest(JSON.stringify({
      schema_version: 1,
      product: 'work',
      id: 'project-1',
      title: 'Launch',
      description: 'Launch workspace',
      session_id: 'work:project:1',
      identity: {
        icon: '🚀',
        name: 'Nova',
        role: 'Launch partner',
      },
      capabilities: { selected_servers: [], selected_skills: [] },
    }), 'Chats/Work/projects/launch', 'work')

    expect(project?.identity).toEqual({
      icon: '🚀',
      name: 'Nova',
      role: 'Launch partner',
    })
  })
})

describe('Code project mode in product.json', () => {
  const base = { schema_version: 1, product: 'code', id: 'code-1', title: 'App', session_id: 'code:1' }
  const parse = (extra: object) => parseProductProjectManifest(JSON.stringify({ ...base, ...extra }), 'Chats/Code/app', 'code')!
  it('reads the saved mode, ignores an unknown one, and defaults old projects (dev, or local when a folder is saved)', () => {
    expect(productMode(parse({ mode: 'cowork' }))).toBe('cowork')
    expect(productMode(parse({ mode: 'local' }))).toBe('local')
    expect(productMode(parse({ mode: 'sandbox' }))).toBe('dev')
    expect(productMode(parse({}))).toBe('dev')
    expect(productMode(parse({ local_files: { device_id: 'laptop', resource_id: 'app' } }))).toBe('local')
    expect(productMode(parse({ mode: 'dev', local_files: { device_id: 'laptop', resource_id: 'app' } }))).toBe('dev')
  })
})

describe('Code local files in product.json', () => {
  const base = { schema_version: 1, product: 'code', id: 'code-1', title: 'App', session_id: 'code:1' }
  it('reads local_files, ignoring a malformed one', () => {
    expect(parseProductProjectManifest(JSON.stringify({ ...base, local_files: { device_id: 'laptop', resource_id: 'app' } }), 'Chats/Code/app', 'code')?.localFiles).toEqual({ device_id: 'laptop', resource_id: 'app' })
    expect(parseProductProjectManifest(JSON.stringify({ ...base, local_files: { device_id: '../x', resource_id: 'app' } }), 'Chats/Code/app', 'code')?.localFiles).toBeUndefined()
  })

  it('writes and clears local_files without touching the rest of product.json', async () => {
    const project = { product: 'code', id: 'code-1', title: 'App', workspacePath: 'Chats/Code/app' } as ProductProject<'code'>
    getPlannerFileContent.mockResolvedValue({ content: JSON.stringify({ ...base, identity: { name: 'Nova' } }) })
    const saved = await updateProductProjectLocalFiles(project, { device_id: 'laptop', resource_id: 'app' }, 'Set files location')
    const written = JSON.parse(updatePlannerFile.mock.calls[0][1])
    expect(written.local_files).toEqual({ device_id: 'laptop', resource_id: 'app' })
    expect(written.identity).toEqual({ name: 'Nova' })
    expect(saved.localFiles).toEqual({ device_id: 'laptop', resource_id: 'app' })
    getPlannerFileContent.mockResolvedValue({ content: JSON.stringify({ ...base, local_files: written.local_files }) })
    await updateProductProjectLocalFiles(project, undefined, 'Set files location')
    expect(JSON.parse(updatePlannerFile.mock.calls[1][1]).local_files).toBeUndefined()
  })
})

describe('updateProductProjectIdentity', () => {
  const project = {
    product: 'work',
    id: 'project-1',
    title: 'Launch',
    workspacePath: 'Chats/Work/projects/launch',
  } as ProductProject<'work'>

  beforeEach(() => {
    vi.clearAllMocks()
  })

  function mockManifest(identity: unknown) {
    getPlannerFileContent.mockResolvedValue({
      content: JSON.stringify({ schema_version: 1, identity }),
    })
  }

  it('merges the patch into product.json and preserves omitted fields', async () => {
    mockManifest({ icon: '🚀', name: 'Nova', role: 'Launch partner' })
    const updated = await updateProductProjectIdentity(project, { name: '  Stella  ' }, 'Update identity')

    expect(agentApi.getPlannerFileContent).toHaveBeenCalledWith('Chats/Work/projects/launch/product.json')
    const written = JSON.parse(updatePlannerFile.mock.calls[0][1])
    expect(written.identity).toEqual({ icon: '🚀', name: 'Stella', role: 'Launch partner' })
    expect(updated.identity?.name).toBe('Stella')
  })

  it('writes purpose to the top-level description', async () => {
    mockManifest({ icon: '🚀', name: 'Nova', role: 'Launch partner' })
    const updated = await updateProductProjectIdentity(project, { purpose: '  Ship the launch.  ' }, 'Update identity')

    const written = JSON.parse(updatePlannerFile.mock.calls[0][1])
    expect(written.description).toBe('Ship the launch.')
    expect(written.identity).toEqual({ icon: '🚀', name: 'Nova', role: 'Launch partner' })
    expect(updated.description).toBe('Ship the launch.')
  })

  it('removes emptied fields and drops an emptied identity', async () => {
    mockManifest({ icon: '🚀', name: 'Nova' })
    await updateProductProjectIdentity(project, { icon: '', name: '' }, 'Update identity')

    const written = JSON.parse(updatePlannerFile.mock.calls[0][1])
    expect('identity' in written).toBe(false)
  })

  it('rejects invalid project configuration', async () => {
    getPlannerFileContent.mockResolvedValue({ content: 'not json' })
    await expect(updateProductProjectIdentity(project, { name: 'Nova' }, 'Update identity')).rejects.toThrow('invalid JSON')
  })
})


describe('loadProductProjects with Crews at the shared root', () => {
  const manifest = (id: string, title: string) => JSON.stringify({ schema_version: 1, product: 'work', id, title, session_id: `work:project:${id}` })

  beforeEach(() => {
    vi.clearAllMocks()
    getPlannerFiles.mockResolvedValue({ data: [{ filepath: 'Chats/Work/projects/legacy-1a2b', type: 'folder', children: [{ filepath: 'Chats/Work/projects/legacy-1a2b/product.json', type: 'file' }] }] })
    getPlannerFileContent.mockImplementation(async (path: string) => {
      if (path === 'Chats/Work/projects/legacy-1a2b/product.json') return { content: manifest('legacy', 'Legacy') }
      if (path === 'Crew/moved-3c4d/product.json') return { content: manifest('moved', 'Moved') }
      throw new Error(`unexpected read ${path}`)
    })
  })

  it('lists the Crews in the owner tree and, when asked, the ones the user owns at Crew/<folder>', async () => {
    listOwnSharedProjects.mockResolvedValue({ projects: [{ id: 'moved', workspace_path: 'Crew/moved-3c4d' }] })
    const projects = await loadProductProjects('Chats/Work/projects', 'work', { includeOwnSharedProjects: true })
    expect(projects.map(p => [p.id, p.workspacePath]).sort()).toEqual([
      ['legacy', 'Chats/Work/projects/legacy-1a2b'],
      ['moved', 'Crew/moved-3c4d'],
    ])
    expect(listOwnSharedProjects).toHaveBeenCalledWith('work')
  })

  it('does not ask for them by default, and survives a server that has no such list', async () => {
    const plain = await loadProductProjects('Chats/Work/projects', 'work')
    expect(plain.map(p => p.id)).toEqual(['legacy'])
    expect(listOwnSharedProjects).not.toHaveBeenCalled()
    listOwnSharedProjects.mockRejectedValue(new Error('404'))
    const withOld = await loadProductProjects('Chats/Work/projects', 'work', { includeOwnSharedProjects: true })
    expect(withOld.map(p => p.id)).toEqual(['legacy'])
  })

  it('does not list a Crew twice', async () => {
    listOwnSharedProjects.mockResolvedValue({ projects: [{ id: 'legacy', workspace_path: 'Chats/Work/projects/legacy-1a2b' }] })
    const projects = await loadProductProjects('Chats/Work/projects', 'work', { includeOwnSharedProjects: true })
    expect(projects).toHaveLength(1)
  })
})

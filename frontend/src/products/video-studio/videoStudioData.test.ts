import { describe, expect, it, vi } from 'vitest'

vi.mock('../../services/api', () => ({
  agentApi: {},
  getApiBaseUrl: () => 'http://localhost:8000',
  getAuthToken: () => null,
}))
import { agentApi } from '../../services/api'
import { loadVideoProjects, parsePresentations, parseProjectManifest, parseWorkflowSteps, projectSlug, relativeTime, toCharacterPresentations, toDocumentPresentations, toReferencePresentations } from './videoStudioData'
import type { WorkspacePresentation } from '../../platform/presentations/presentationData'

describe('Video Studio workspace data', () => {
  it('accepts only a complete Video Studio product manifest', () => {
    const project = parseProjectManifest(JSON.stringify({
      schema_version: 1,
      product: 'video-studio',
      id: 'project-1',
      title: 'Launch film',
      description: 'A concise product launch.',
      session_id: 'video-studio:project:project-1',
      created_at: '2026-08-07T09:00:00Z',
    }), 'Chats/Video Studio/projects/launch-film', '2026-08-07T10:00:00Z')

    expect(project).toMatchObject({
      id: 'project-1',
      title: 'Launch film',
      workspacePath: 'Chats/Video Studio/projects/launch-film',
      updatedAt: '2026-08-07T10:00:00Z',
      videos: 0,
    })
    expect(parseProjectManifest('{"product":"video-studio"}', 'Chats/bad')).toBeNull()
  })

  it('parses only ready video presentations with valid payloads', () => {
    const presentations = parsePresentations([
      { id: 'v1', kind: 'media.video', title: 'Final', status: 'ready', revision: 2, updated_at: '2026-08-07T10:00:00Z', payload_json: JSON.stringify({ path: 'outputs/final.mp4', qa_report_path: 'work/quality-report.json', verdict: 'pass' }) },
      { id: 'draft', kind: 'media.video', title: 'Draft', status: 'draft', payload_json: JSON.stringify({ path: 'outputs/draft.mp4' }) },
      { id: 'image', kind: 'media.image', title: 'Frame', status: 'ready', payload_json: JSON.stringify({ path: 'outputs/frame.png' }) },
    ])

    expect(presentations).toHaveLength(1)
    expect(presentations[0]).toMatchObject({ id: 'v1', path: 'outputs/final.mp4', verdict: 'pass', revision: 2, workspacePresentation: { kind: 'media.video' } })
  })

  it('parses the fixed workflow route and produces safe project slugs', () => {
    const steps = parseWorkflowSteps(JSON.stringify({ steps: [{
      id: 'route', type: 'routing', title: 'Route', routes: [
        { route_id: 'cinematic', route_name: 'Cinematic', next_step_id: 'story' },
      ],
    }, { id: 'story', type: 'scripted', title: 'Story' }] }))

    expect(steps).toHaveLength(2)
    expect(steps[0].routes?.[0]).toEqual({ id: 'cinematic', title: 'Cinematic', nextStepId: 'story' })
    expect(projectSlug('  Café / Product Launch!  ')).toBe('cafe-product-launch')
    expect(relativeTime('2026-08-07T09:55:00Z', Date.parse('2026-08-07T10:00:00Z'))).toBe('5m ago')
  })

  // Captured from the live workspace listing: it returns the requested folder
  // as a node whose children are the projects, AND each project again as a
  // top-level sibling. Flattening that walks every product.json twice, which
  // rendered two cards per project and fetched every manifest twice.
  it('returns one project per manifest when the listing describes a file twice', async () => {
    const manifest = (id: string) => JSON.stringify({
      schema_version: 1, product: 'video-studio', id, title: id,
      session_id: `video-studio:project:${id}`, created_at: '2026-08-07T09:00:00Z',
    })
    const projectNode = (slug: string) => ({
      filepath: `Chats/Video Studio/projects/${slug}`,
      type: 'folder',
      children: [{ filepath: `Chats/Video Studio/projects/${slug}/product.json`, last_modified: '2026-08-07T10:00:00Z' }],
    })
    const listing = {
      data: [
        { filepath: 'Chats/Video Studio/projects', type: 'folder', children: [projectNode('alpha'), projectNode('beta')] },
        projectNode('alpha'),
        projectNode('beta'),
      ],
    }

    const getPlannerFileContent = vi.fn(async (filepath: string) => ({
      data: { content: manifest(filepath.includes('alpha') ? 'alpha' : 'beta'), last_modified: '2026-08-07T10:00:00Z' },
    }))
    Object.assign(agentApi, {
      getPlannerFiles: vi.fn(async () => listing),
      getPlannerFileContent,
      queryWorkflowDB: vi.fn(async () => ({ data: { rows: [{ count: 0 }] } })),
    })

    const projects = await loadVideoProjects()

    expect(projects.map((project) => project.id)).toEqual(['alpha', 'beta'])
    expect(getPlannerFileContent).toHaveBeenCalledTimes(2)
  })
})

describe('Video Studio pre-production presentations', () => {
  const presentation = (payload: Record<string, unknown>, overrides: Partial<WorkspacePresentation> = {}): WorkspacePresentation => ({
    id: 'p1',
    kind: 'media.character',
    title: 'Aang',
    status: 'ready',
    revision: 1,
    updatedAt: '2026-08-15T10:00:00Z',
    payload,
    ...overrides,
  } as WorkspacePresentation)

  it('drops a character with no reference image', () => {
    // The reference image is what a generated shot gets compared against, so a
    // character without one cannot do the job the panel exists for -- better
    // absent than rendered as a broken tile that looks approved.
    const characters = toCharacterPresentations([
      presentation({ name: 'Aang', image_path: 'work/characters/aang.png', spec_path: 'work/characters/aang.md', spec: 'bald, blue arrow tattoo', model: 'bytedance/seedance-2.5', provider: 'fal-ai' }),
      presentation({ name: 'Katara', spec_path: 'work/characters/katara.md' }, { id: 'p2' }),
    ])

    expect(characters).toHaveLength(1)
    expect(characters[0]).toMatchObject({
      name: 'Aang',
      imagePath: 'work/characters/aang.png',
      spec: 'bald, blue arrow tattoo',
      model: 'bytedance/seedance-2.5',
      provider: 'fal-ai',
    })
  })

  it('falls back to the presentation title when the payload carries no name', () => {
    const [character] = toCharacterPresentations([
      presentation({ image_path: 'a.png' }, { title: 'Narrator' }),
    ])
    expect(character.name).toBe('Narrator')
  })

  it('drops a document with no path and titles one from its filename', () => {
    const documents = toDocumentPresentations([
      presentation({ path: 'work/longform-script.md', markdown: '# Script' }, { id: 'd1', kind: 'document.markdown', title: '' }),
      presentation({ markdown: 'orphan' }, { id: 'd2', kind: 'document.markdown', title: '' }),
    ])

    expect(documents).toHaveLength(1)
    expect(documents[0]).toMatchObject({ title: 'longform-script.md', path: 'work/longform-script.md', markdown: '# Script' })
  })

  it('presents a location or boundary image as a reference, not a character', () => {
    const references = toReferencePresentations([
      presentation({ path: 'work/references/cafe-start.png', role: 'start_frame', note: 'Keep the doorway and morning light.' }, { id: 'r1', kind: 'media.reference', title: 'Cafe sequence start' }),
      presentation({ role: 'location' }, { id: 'r2', kind: 'media.reference' }),
    ])
    expect(references).toHaveLength(1)
    expect(references[0]).toMatchObject({ title: 'Cafe sequence start', path: 'work/references/cafe-start.png', role: 'start_frame' })
  })
})

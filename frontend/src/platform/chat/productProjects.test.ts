import { describe, expect, it } from 'vitest'
import { parseProductProjectManifest } from './productProjects'

describe('empty Crew manifest (PLAT-746-885)', () => {
  it('opens a newly created Crew without requiring a Main chat', () => {
    const manifest = { schema_version: 1, product: 'work', id: 'example-crew', title: 'Example' }
    expect(parseProductProjectManifest(JSON.stringify(manifest), 'Crew/example', 'work'))
      .toMatchObject({ id: 'example-crew', sessionId: '' })
    expect(parseProductProjectManifest(JSON.stringify({ ...manifest, product: 'code' }), 'Code/example', 'code')).toBeNull()
  })
})

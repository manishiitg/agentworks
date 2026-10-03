import { describe, expect, it } from 'vitest'
import { FamilyWorkspace, documentsURL, familyRelative, sha256Hex, workspacePath } from './workspace'

function fakeRequester(files: Record<string, string>, listing: unknown[] = []) {
  const calls: { method: string; path: string; body?: unknown }[] = []
  const request = async <T,>(method: string, path: string, body?: unknown): Promise<T> => {
    calls.push({ method, path, body })
    if (method === 'GET' && path.startsWith('/api/wp/api/documents?')) return { success: true, data: listing } as T
    if (method === 'GET') {
      const rel = decodeURIComponent(path.replace('/api/wp/api/documents/', ''))
      if (!(rel in files)) throw new Error('not found')
      return { success: true, data: { filepath: rel, content: files[rel] } } as T
    }
    if (method === 'PUT') {
      const rel = decodeURIComponent(path.replace('/api/wp/api/documents/', ''))
      files[rel] = (body as { content: string }).content
      return { success: true } as T
    }
    if (method === 'POST' && path === '/api/wp/api/upload') return { filepath: 'Chats/SparkQuill/inbox/photo.jpg' } as T
    throw new Error(`unexpected ${method} ${path}`)
  }
  return { request, calls, files }
}

describe('paths', () => {
  it('maps family-relative paths onto the user workspace and back', () => {
    expect(workspacePath('reports/x.html')).toBe('Chats/SparkQuill/reports/x.html')
    expect(workspacePath('Chats/SparkQuill/a')).toBe('Chats/SparkQuill/a')
    expect(workspacePath('_users/u1/Chats/SparkQuill/a')).toBe('_users/u1/Chats/SparkQuill/a')
    expect(familyRelative('_users/u1/Chats/SparkQuill/activities/a')).toBe('activities/a')
    expect(familyRelative('Chats/SparkQuill')).toBe('')
    expect(documentsURL('a b/c.html', '/raw')).toBe('/api/wp/api/documents/Chats/SparkQuill/a%20b/c.html/raw')
  })
})

describe('FamilyWorkspace', () => {
  it('reads files, converts the tree and sums sizes', async () => {
    const listing = [
      { filepath: 'Chats/SparkQuill/reports', type: 'folder', children: [{ filepath: 'Chats/SparkQuill/reports/progress.html', type: 'file', size: 10 }] },
      { filepath: 'Chats/SparkQuill/family.json', type: 'file', size: 5 },
    ]
    const { request } = fakeRequester({ 'Chats/SparkQuill/reports/progress.html': '<h1>hi</h1>' }, listing)
    const ws = new FamilyWorkspace(request)
    const f = await ws.readFile('reports/progress.html')
    expect(f).toEqual({ path: 'reports/progress.html', is_text: true, content: '<h1>hi</h1>', size: undefined })
    const tree = await ws.tree()
    expect(tree).toEqual({ nodes: [
      { name: 'reports', path: 'reports', type: 'dir', children: [{ name: 'progress.html', path: 'reports/progress.html', type: 'file', size: 10 }] },
      { name: 'family.json', path: 'family.json', type: 'file', size: 5 },
    ], total_size: 15 })
  })

  it('lists activities from their manifests and follows the current-activity pointer', async () => {
    const files = {
      'Chats/SparkQuill/activities/2026-09-03-fractions/activity.json': JSON.stringify({ title: 'Fractions', subject: 'Math', items: ['quick-check.html'], goal: 'g', created_at: '2026-09-03T00:00:00Z' }),
      'Chats/SparkQuill/activities/2026-09-01-old/activity.json': JSON.stringify({ title: 'Old', created_at: '2026-09-01T00:00:00Z' }),
      'Chats/SparkQuill/current-activity.json': JSON.stringify({ dir: 'activities/2026-09-03-fractions' }),
    }
    const listing = [
      { filepath: 'Chats/SparkQuill/activities/2026-09-03-fractions', type: 'folder' },
      { filepath: 'Chats/SparkQuill/activities/2026-09-01-old', type: 'folder' },
      { filepath: 'Chats/SparkQuill/activities/stray.txt', type: 'file' },
    ]
    const ws = new FamilyWorkspace(fakeRequester(files, listing).request)
    const acts = await ws.activities()
    expect(acts.map((a) => a.dir)).toEqual(['activities/2026-09-03-fractions', 'activities/2026-09-01-old'])
    expect(acts[0].items).toEqual([{ path: 'activities/2026-09-03-fractions/quick-check.html', name: 'quick-check.html' }])
    const current = await ws.currentActivity()
    expect(current?.title).toBe('Fractions')
  })

  it('hands an activity to the child by moving the pointer and says whether the chat is fresh', async () => {
    const files = {
      'Chats/SparkQuill/activities/2026-09-03-fractions/activity.json': JSON.stringify({ title: 'Fractions', goal: 'add unlike fractions' }),
      'Chats/SparkQuill/activities/2026-09-04-oceans/activity.json': JSON.stringify({ title: 'Oceans' }),
      'Chats/SparkQuill/current-activity.json': JSON.stringify({ dir: 'activities/2026-09-03-fractions' }),
    }
    const fake = fakeRequester(files)
    const ws = new FamilyWorkspace(fake.request)
    const again = await ws.handoff('activities/2026-09-03-fractions')
    expect(again).toEqual({ dir: 'activities/2026-09-03-fractions', title: 'Fractions', goal: 'add unlike fractions', new_session: false })
    const other = await ws.handoff('activities/2026-09-04-oceans')
    expect(other.new_session).toBe(true)
    expect(JSON.parse(files['Chats/SparkQuill/current-activity.json'])).toEqual({ dir: 'activities/2026-09-04-oceans' })
    expect((await ws.handoff('activities/2026-09-04-oceans', false)).new_session).toBe(true)
    expect((await ws.handoff('activities/2026-09-04-oceans', true)).new_session).toBe(false)
    await expect(ws.handoff('activities/missing')).rejects.toThrow('activity not found')
  })

  it('keeps the PIN and child profile in family.json with the family server\'s hashing', async () => {
    const files: Record<string, string> = { 'Chats/SparkQuill/family.json': JSON.stringify({ parent_label: 'mom' }) }
    const ws = new FamilyWorkspace(fakeRequester(files).request)
    expect(await ws.verifyPin('0000')).toEqual({ ok: true }) // nothing set yet
    expect(await ws.setPin('12')).toEqual({ error: 'PIN must be 4–8 digits' })
    expect(await ws.setPin('2468')).toEqual({})
    const saved = JSON.parse(files['Chats/SparkQuill/family.json'])
    expect(saved.parent_label).toBe('mom')
    expect(saved.pin_hash).toBe(await sha256Hex('2468'))
    expect(saved.pin_hash).toHaveLength(64)
    expect(await ws.verifyPin('2468')).toEqual({ ok: true })
    expect(await ws.verifyPin('1111')).toEqual({ ok: false })
    await ws.saveChild({ name: 'Maya', grade: '6', board: 'CBSE' })
    expect(JSON.parse(files['Chats/SparkQuill/family.json'])).toMatchObject({ pin_hash: await sha256Hex('2468'), child: { name: 'Maya', grade: '6', board: 'CBSE' } })
  })

  // engine is shared (family-wide, one paid account); the model within it is
  // per-role — product.yaml declares a different default per profile
  // precisely so a family can run a stronger model for the parent than the
  // child. A single shared `model` field let whichever side picked last
  // silently overwrite the other's; parent_model/child_model fixed that.
  it('keeps parent and child model picks independent under one shared engine', async () => {
    const files: Record<string, string> = {}
    const ws = new FamilyWorkspace(fakeRequester(files).request)

    await ws.saveEngine('parent', 'codex-cli', 'gpt-6-sol', 'family-account', 'medium')
    expect(JSON.parse(files['Chats/SparkQuill/family.json'])).toEqual({ engine: 'codex-cli', parent_model: 'gpt-6-sol', connection_id: 'family-account', parent_reasoning_effort: 'medium' })

    await ws.saveEngine('child', 'codex-cli', 'gpt-6-luna')
    const afterChildPick = JSON.parse(files['Chats/SparkQuill/family.json'])
    expect(afterChildPick).toEqual({ engine: 'codex-cli', parent_model: 'gpt-6-sol', child_model: 'gpt-6-luna', connection_id: 'family-account', parent_reasoning_effort: 'medium' })

    // Switching the family engine removes models and accounts belonging to the previous provider.
    await ws.saveEngine('child', 'claude-code')
    const afterChildEngineSwitch = JSON.parse(files['Chats/SparkQuill/family.json'])
    expect(afterChildEngineSwitch.engine).toBe('claude-code')
    expect(afterChildEngineSwitch.parent_model).toBeUndefined()
    expect(afterChildEngineSwitch.child_model).toBeUndefined()
    expect(afterChildEngineSwitch.connection_id).toBeUndefined()
    expect(afterChildEngineSwitch.parent_reasoning_effort).toBeUndefined()
  })

  it('uploads into a folder and keeps scene state as JSON files', async () => {
    const fake = fakeRequester({})
    const ws = new FamilyWorkspace(fake.request)
    const res = await ws.upload(new File(['x'], 'photo.jpg'), 'inbox')
    expect(res).toEqual({ name: 'photo.jpg', path: 'inbox/photo.jpg' })
    const fd = fake.calls.find((c) => c.path === '/api/wp/api/upload')!.body as FormData
    expect(fd.get('folder_path')).toBe('Chats/SparkQuill/inbox')
    await ws.writeJSON(ws.stateFile('scene:room/1'), { key: 'k', data: { score: 3 } })
    expect(Object.keys(fake.files)).toContain('Chats/SparkQuill/state/scene_room_1.json')
    expect((await ws.readJSON<{ data: unknown }>('state/scene_room_1.json'))?.data).toEqual({ score: 3 })
  })
})

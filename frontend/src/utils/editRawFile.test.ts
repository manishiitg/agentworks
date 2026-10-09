import { describe, expect, it, vi } from 'vitest'
import { createNewFile, newFileNameProblem, saveEditedFile } from './editRawFile'

const api = (stored: string) => ({
  getPlannerFileContent: vi.fn(async () => ({ success: true, data: { content: stored } })),
  updatePlannerFile: vi.fn(async () => ({ success: true })),
})

describe('saveEditedFile', () => {
  it('writes the edited raw text, keeping backslash sequences as typed', async () => {
    const files = api('const a = "x\\ny"')
    expect(await saveEditedFile(files, 'Code/p/a.ts', 'const a = "x\\ny"', 'const a = "x\\nz"')).toBe('saved')
    expect(files.updatePlannerFile).toHaveBeenCalledWith('Code/p/a.ts', 'const a = "x\\nz"', 'Edit a.ts')
  })

  it('refuses and writes nothing when the file changed since it was opened', async () => {
    const files = api('changed by the agent')
    expect(await saveEditedFile(files, 'Code/p/a.ts', 'what I opened', 'my edit')).toBe('changed')
    expect(files.updatePlannerFile).not.toHaveBeenCalled()
  })
})

describe('createNewFile', () => {
  it('creates an empty file, but never overwrites one that exists', async () => {
    const missing = { ...api(''), getPlannerFileContent: vi.fn(async () => { throw { response: { status: 404 } } }) }
    expect(await createNewFile(missing, 'Code/p/src', 'new.ts')).toBe('created')
    expect(missing.updatePlannerFile).toHaveBeenCalledWith('Code/p/src/new.ts', '', 'Create new.ts')
    const present = api('keep me')
    expect(await createNewFile(present, 'Code/p/src', 'a.ts')).toBe('exists')
    expect(present.updatePlannerFile).not.toHaveBeenCalled()
  })

  it('accepts one plain file name only', () => {
    expect(newFileNameProblem('.env')).toBeNull()
    for (const bad of ['', ' a', 'a/b', '..', 'a\\b']) expect(newFileNameProblem(bad)).not.toBeNull()
  })
})

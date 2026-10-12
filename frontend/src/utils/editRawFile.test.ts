import { describe, expect, it, vi } from 'vitest'
import { createNewFile, newFileNameProblem, readRawFile, saveEditedFile } from './editRawFile'

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
    // The live GET returns 200 even when the requested file is missing.
    const missing = { ...api(''), getPlannerFileContent: vi.fn(async () => ({ success: true, data: { filepath: '' }, message: 'File does not exist' })) }
    expect(await createNewFile(missing, 'Code/p/src', 'new.ts')).toBe('created')
    expect(missing.getPlannerFileContent).not.toHaveBeenCalled()
    expect(missing.updatePlannerFile).toHaveBeenCalledWith('Code/p/src/new.ts', '', 'Create new.ts', { createOnly: true })
    const present = { ...api('keep me'), updatePlannerFile: vi.fn(async () => { throw { response: { status: 409 } } }) }
    expect(await createNewFile(present, 'Code/p/src', 'a.ts')).toBe('exists')
    expect(present.updatePlannerFile).toHaveBeenCalledOnce()
    const denied = { ...api(''), updatePlannerFile: vi.fn(async () => { throw { response: { status: 403 } } }) }
    await expect(createNewFile(denied, 'Code/p/src', 'a.ts')).rejects.toMatchObject({ response: { status: 403 } })
  })

  it('opens and edits the new empty text file without treating a missing file as empty', async () => {
    const empty = { ...api(''), getPlannerFileContent: vi.fn(async () => ({ success: true, data: { filepath: 'Code/p/src/new.ts', encoding: 'utf-8' } })) }
    expect(await readRawFile(empty, 'Code/p/src/new.ts')).toBe('')
    expect(await saveEditedFile(empty, 'Code/p/src/new.ts', '', 'export {}')).toBe('saved')
    const missing = { ...api(''), getPlannerFileContent: vi.fn(async () => ({ success: true, data: { filepath: '' } })) }
    await expect(readRawFile(missing, 'Code/p/src/no.ts')).rejects.toThrow('empty response')
  })

  it('accepts one plain file name only', () => {
    expect(newFileNameProblem('.env')).toBeNull()
    for (const bad of ['', ' a', 'a/b', '..', 'a\\b']) expect(newFileNameProblem(bad)).not.toBeNull()
  })
})

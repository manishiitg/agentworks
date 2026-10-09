import { describe, expect, it, vi } from 'vitest'
import { saveEditedFile } from './editRawFile'

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

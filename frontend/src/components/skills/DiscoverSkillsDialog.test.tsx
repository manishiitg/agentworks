// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import DiscoverSkillsDialog from './DiscoverSkillsDialog'

const fixture = vi.hoisted(() => ({ discover: vi.fn(), importDiscovered: vi.fn(), folder: vi.fn() }))
vi.mock('../../api/skills', () => ({ skillsApi: fixture }))
vi.mock('./SkillImportDialog', () => ({ default: () => <div>Existing GitHub and ZIP importer</div> }))
const host = document.createElement('div')
document.body.append(host)
const root = createRoot(host)
afterEach(() => { act(() => root.render(null)); fixture.discover.mockReset(); fixture.importDiscovered.mockReset(); fixture.folder.mockReset() })

// PLAT-746-1008: discovering and previewing are reads; only an explicit Import
// submits the opaque source ID to the current project's existing import path.
it('previews source instructions before explicitly importing into this project', async () => {
  const skill = { id: 'own-source', name: 'review', description: 'Review changes', source: 'Personal · your login', user_invocable: true }
  fixture.discover.mockImplementation(async (_workspace, _provider, id) => id
    ? { ...skill, document: 'Read the actual diff.', files: ['SKILL.md', 'references/checklist.md'] }
    : { skills: [skill], message: 'Files found, not installed.' })
  fixture.importDiscovered.mockResolvedValue({ success: true, skill_name: 'review' })
  const imported = vi.fn()
  await act(async () => root.render(<DiscoverSkillsDialog workspacePath="Crew/example" close={() => {}} onImported={imported} />))
  expect(fixture.importDiscovered).not.toHaveBeenCalled()
  const row = [...document.querySelectorAll('button')].find(button => button.textContent?.includes('review'))!
  await act(async () => row.click())
  expect(document.body.textContent).toContain('Read the actual diff.')
  expect(document.body.textContent).toContain('Personal · your login')
  expect(fixture.importDiscovered).not.toHaveBeenCalled()
  const copy = [...document.querySelectorAll('button')].find(button => button.textContent === 'Import into this project')!
  await act(async () => copy.click())
  expect(fixture.importDiscovered).toHaveBeenCalledWith('Crew/example', 'claude-code', 'own-source')
  expect(imported).toHaveBeenCalledOnce()
  expect(document.body.textContent).toContain('The original is unchanged.')
})

it('uploads a chosen folder for preview before a separate explicit import', async () => {
  fixture.discover.mockResolvedValue({ skills: [], message: 'No native sources.' })
  fixture.folder.mockImplementation(async (_workspace, _files, copying) => copying ? { success: true, skill_name: 'review' } : { name: 'review', document: 'Folder instructions.', files: ['review/SKILL.md'] })
  await act(async () => root.render(<DiscoverSkillsDialog workspacePath="Crew/example" close={() => {}} />))
  const fromFolder = [...document.querySelectorAll('button')].find(button => button.textContent === 'From folder…')!
  await act(async () => fromFolder.click())
  const input = document.querySelector<HTMLInputElement>('[aria-label="Choose skill folder"]')!
  const file = new File(['---\nname: review\ndescription: Review\n---\nFolder instructions.'],'SKILL.md')
  Object.defineProperty(file,'webkitRelativePath',{ value: 'review/SKILL.md' })
  Object.defineProperty(input,'files',{ configurable: true, value: [file] })
  await act(async () => input.dispatchEvent(new Event('change',{ bubbles: true })))
  expect(fixture.folder).not.toHaveBeenCalled()
  await act(async () => [...document.querySelectorAll('button')].find(button => button.textContent === 'Preview instructions')!.click())
  expect(fixture.folder).toHaveBeenLastCalledWith('Crew/example',[file],false)
  expect(document.body.textContent).toContain('Folder instructions.')
  await act(async () => [...document.querySelectorAll('button')].find(button => button.textContent === 'Import into this project')!.click())
  expect(fixture.folder).toHaveBeenLastCalledWith('Crew/example',[file],true)
})

// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import DiscoverSkillsDialog from './DiscoverSkillsDialog'

const fixture = vi.hoisted(() => ({ discover: vi.fn(), importDiscovered: vi.fn() }))
vi.mock('../../api/skills', () => ({ skillsApi: fixture }))
vi.mock('./SkillImportDialog', () => ({ default: () => <div>Existing GitHub and ZIP importer</div> }))
const host = document.createElement('div')
document.body.append(host)
const root = createRoot(host)
afterEach(() => { act(() => root.render(null)); fixture.discover.mockReset(); fixture.importDiscovered.mockReset() })

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

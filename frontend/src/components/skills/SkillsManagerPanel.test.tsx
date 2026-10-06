// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import SkillsManagerPanel from './SkillsManagerPanel'
import { skillsApi } from '../../api/skills'

vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => true, READ_ONLY_TITLE: 'Read only' }))
vi.mock('../../api/skills', () => ({ skillsApi: { listSkills: vi.fn(), deleteSkill: vi.fn() } }))

it('shows step and native skills alongside chat skills and uninstalls within their workspace', async () => {
  const workspace = 'Workflow/reports'
  const inventory = {
    skills: ['chat', 'step', 'native'].map(name => ({
      folder_name: name, file_path: `${name === 'native' ? '.agents/skills' : 'skills'}/${name}/SKILL.md`,
      frontmatter: { name, description: `Instructions for ${name}` }, content: 'Body',
    })), total: 3, usage: { chat: ['Main chat'], step: ['Step: Publish'] },
  }
  vi.mocked(skillsApi.listSkills).mockResolvedValue(inventory)
  vi.mocked(skillsApi.deleteSkill).mockImplementation(async () => {
    vi.mocked(skillsApi.listSkills).mockResolvedValue({ ...inventory, skills: inventory.skills.filter(skill => skill.folder_name !== 'step'), usage: { chat: ['Main chat'] } })
  })
  const savedConfirm = window.confirm
  window.confirm = vi.fn(() => true)
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const removed = vi.fn()
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => { root.render(<SkillsManagerPanel workspacePath={workspace} selectedSkills={['chat']} onUninstalled={removed} />) })
    expect(skillsApi.listSkills).toHaveBeenCalledWith(workspace)
    expect(host.textContent).toContain('Step: Publish')
    expect(host.textContent).toContain('.agents/skills/native/SKILL.md')
    expect(host.textContent).not.toContain('Platform connected')
    const expand = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Instructions for step'))!
    await act(async () => { expand.click() })
    const uninstall = [...host.querySelectorAll('button')].find(button => button.textContent?.trim() === 'Uninstall')!
    await act(async () => { uninstall.click() })
    expect(window.confirm).toHaveBeenCalledWith(expect.stringContaining('Step: Publish'))
    expect(skillsApi.deleteSkill).toHaveBeenCalledWith('step', workspace)
    expect(removed).toHaveBeenCalledWith('step')
    expect(host.textContent).not.toContain('Step: Publish')
    expect(host.textContent).toContain('.agents/skills/native/SKILL.md')
  } finally {
    await act(async () => { root.unmount() })
    host.remove()
    window.confirm = savedConfirm
    vi.restoreAllMocks()
  }
})

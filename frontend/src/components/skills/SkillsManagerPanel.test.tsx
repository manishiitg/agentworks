// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import SkillsManagerPanel from './SkillsManagerPanel'
import { skillsApi } from '../../api/skills'
import { agentApi } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'

const access = vi.hoisted(() => ({ canWrite: true }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => access.canWrite, READ_ONLY_TITLE: 'Read only' }))
vi.mock('../../api/skills', () => ({ skillsApi: { listSkills: vi.fn(), deleteSkill: vi.fn() } }))
vi.mock('../../services/api', () => ({ agentApi: { getAgentProfile: vi.fn() } }))
vi.mock('../ui/MarkdownRenderer', () => ({ MarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div> }))
vi.mock('../../stores/useAuthStore', async () => {
  const { create } = await import('zustand')
  return { useAuthStore: create(() => ({ user: { id: 'owner' } })) }
})
vi.mock('../../stores/useWorkspaceConnectionStore', async () => {
  const { create } = await import('zustand')
  return { useWorkspaceConnectionStore: create(() => ({ activeWorkspaceId: 'local' })) }
})

it('shows imported files including matching builtin names across Crew, Goals and Relay without changing platform skills or access', async () => {
  const skill = (name: string, file_path: string, fields = {}) => ({ folder_name: name, file_path, frontmatter: { name, description: `Instructions for ${name}` }, content: 'Read this custom skill', ...fields })
  const files = [
    skill('written-in-chat', 'skills/written-in-chat/SKILL.md'),
    skill('imported-native', '.agents/skills/imported-native/SKILL.md', { source: 'owner/repo', source_type: 'github' }),
    skill('copied-from-brain', '.pi/skills/copied-from-brain/SKILL.md', { brain_source: 'folder/example' }),
    skill('generated-file', '.agents/skills/generated-file/SKILL.md', { file_managed: true }),
    skill('builtin-file', 'skills/builtin-file/SKILL.md', { builtin: true }),
    skill('declared-default', '.agents/skills/declared-default/SKILL.md'),
    skill('old-projection-without-origin', '.pi/skills/old-projection-without-origin/SKILL.md'),
  ]
  vi.mocked(skillsApi.listSkills).mockResolvedValue({ skills: files, total: files.length })
  vi.mocked(agentApi.getAgentProfile).mockResolvedValue({ skill_defaults_by_mode: { builder: ['declared-default'], run: [] } } as never)
  const selection = vi.fn()
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    for (const [profileId, scope] of [['work', 'Crew/test'], ['agentworks', 'Workflow/goal'], ['relays', 'Workflow/relay']]) {
      access.canWrite = false
      await act(async () => root.render(<SkillsManagerPanel customOnly profileId={profileId} workspacePath={scope} selectedSkills={['written-in-chat', 'declared-default']} onToggleSkill={selection} additionalSkills={[skill('nested-project-skill', 'skills/custom/nested-project-skill/SKILL.md')]} />))
      expect(host.textContent).toContain('Custom skills')
      expect([...host.querySelectorAll('[aria-label^="Read skill "]')].map(item => item.getAttribute('aria-label'))).toEqual([
        'Read skill builtin-file', 'Read skill copied-from-brain', 'Read skill imported-native', 'Read skill nested-project-skill', 'Read skill written-in-chat',
      ])
      expect(host.textContent).not.toContain('Platform skills')
      expect(host.textContent).not.toContain('declared-default')
      expect(host.textContent).not.toContain('Runtime folder inventory')
      expect(host.querySelector('[aria-pressed]')).toBeNull()
      expect(host.textContent).not.toContain('Selected')
      expect(host.textContent).not.toContain('Not selected')
      expect(host.textContent).not.toContain('Main chat')
      await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Read skill written-in-chat"]')!.click())
      expect(host.textContent).toContain('Read this custom skill')
      expect(host.textContent).not.toContain('Selected')
      expect([...host.querySelectorAll('button')].some(button => button.textContent?.trim() === 'Uninstall')).toBe(false)
      expect(host.querySelector('[aria-pressed]')).toBeNull()
      expect(selection).not.toHaveBeenCalled()
    }
    expect(skillsApi.deleteSkill).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()); host.remove(); access.canWrite = true; vi.clearAllMocks() }
})

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
    expect(host.textContent).toContain('native')
    expect(host.textContent).not.toContain('Platform connected')
    const expand = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Instructions for step'))!
    await act(async () => { expand.click() })
    const uninstall = [...host.querySelectorAll('button')].find(button => button.textContent?.trim() === 'Uninstall')!
    await act(async () => { uninstall.click() })
    expect(window.confirm).toHaveBeenCalledWith(expect.stringContaining('Step: Publish'))
    expect(skillsApi.deleteSkill).toHaveBeenCalledWith('step', workspace)
    expect(removed).toHaveBeenCalledWith('step')
    expect(host.textContent).not.toContain('Step: Publish')
    expect(host.textContent).toContain('native')
  } finally {
    await act(async () => { root.unmount() })
    host.remove()
    window.confirm = savedConfirm
    vi.restoreAllMocks()
  }
})

it('groups custom skills first without duplicating legacy files, and preserves read-only access', async () => {
  const skill = (name: string, managed = false) => ({ folder_name: name, file_path: `skills/${name}/SKILL.md`, frontmatter: { name, description: `Instructions for ${name}` }, content: '', managed })
  vi.mocked(skillsApi.listSkills).mockResolvedValue({ skills: [skill('platform', true), skill('custom')], total: 2 })
  access.canWrite = false
  const onToggle = vi.fn(), onOpen = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<SkillsManagerPanel workspacePath="Crew/example" groupBySource showRefresh={false} additionalSkills={[skill('custom'), skill('legacy')]} selectedSkills={['custom']} onToggleSkill={onToggle} onOpenFile={onOpen} />))
    expect([...host.querySelectorAll('section')].map(section => section.getAttribute('aria-label'))).toEqual(['Project skills', 'Platform skills'])
    const custom = host.querySelector('section[aria-label="Project skills"]')!
    expect(custom.querySelectorAll('[aria-label^="Read skill "]')).toHaveLength(2)
    expect(host.querySelector('[aria-label="Refresh skills"]')).toBeNull()
    expect(host.querySelector<HTMLButtonElement>('[aria-label="Stop using custom in this main chat"]')!.disabled).toBe(true)
    const expand = [...custom.querySelectorAll<HTMLButtonElement>('[aria-label^="Read skill "]')].find(button => button.textContent?.includes('Instructions for custom'))!
    await act(async () => expand.click())
    expect(onOpen).toHaveBeenCalledWith('skills/custom/SKILL.md')
    expect(onOpen).toHaveBeenCalledWith('skills/custom/SKILL.md')
    expect(onToggle).not.toHaveBeenCalled()
    expect(host.querySelector('[aria-label="Add legacy for this main chat"]')).toBeNull()
    expect(host.querySelector('section[aria-label="Platform skills"]')?.textContent).toContain('platform')
  } finally { await act(async () => root.unmount()); host.remove(); access.canWrite = true; vi.clearAllMocks() }
})

it('classifies every declared automatic skill as Platform even when its workspace file is unmanaged', async () => {
  const skill = (name: string) => ({ folder_name: name, file_path: `.agents/skills/${name}/SKILL.md`, frontmatter: { name, description: `Instructions for ${name}` }, content: '', managed: false })
  const automatic = ['code-reviewer', 'another-profile-default']
  vi.mocked(skillsApi.listSkills).mockResolvedValue({ skills: [...automatic.map(skill), skill('custom')], total: 3 })
  vi.mocked(agentApi.getAgentProfile).mockResolvedValue({ skill_defaults_by_mode: { builder: automatic, run: ['runtime-only'] } })
  const toggle = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<SkillsManagerPanel workspacePath="Crew/example" groupBySource profileId="work" onToggleSkill={toggle} selectionScopeLabel="Crew" />))
    const platform = host.querySelector('section[aria-label="Platform skills"]')!
    const custom = host.querySelector('section[aria-label="Project skills"]')!
    expect(platform.textContent).toContain('code-reviewer')
    expect(platform.textContent).toContain('another-profile-default')
    expect([...platform.querySelectorAll('span')].filter(span => span.textContent === 'Builder default')).toHaveLength(2)
    expect(platform.querySelector('[aria-pressed]')).toBeNull()
    expect(platform.textContent).toContain('runtime-only')
    expect(custom.textContent).not.toContain('code-reviewer')
    expect(custom.querySelector<HTMLButtonElement>('[aria-label="Use custom in this Crew"]')?.textContent).toBe('Use here')
    await act(async () => custom.querySelector<HTMLButtonElement>('[aria-label="Use custom in this Crew"]')!.click())
    expect(toggle).toHaveBeenCalledWith('custom')
    await act(async () => platform.querySelector<HTMLButtonElement>('[aria-label^="Read skill "]')!.click())
    expect(host.querySelector('section[aria-label="Skill instructions: another-profile-default"]')).not.toBeNull()
    expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Uninstall')).toBe(false)
  } finally { await act(async () => root.unmount()); host.remove(); vi.clearAllMocks() }
})

it.each([['work', 'Crew'], ['agentworks', 'Goal'], ['relays', 'Flow']])('shows the complete recorded inventory and mode defaults for %s', async (profileId, label) => {
  const skill = (name: string) => ({ folder_name: name, file_path: `skills/${name}/SKILL.md`, frontmatter: { name, description: `Instructions for ${name}` }, content: '# Read this\n\nKeep existing permissions.' })
  vi.mocked(agentApi.getAgentProfile).mockResolvedValue({ skill_defaults_by_mode: { builder: ['profile-default'], run: ['run-default'] }, resolved_features: [{ tools: ['google_workspace_cli'] }] })
  vi.mocked(skillsApi.listSkills).mockResolvedValue({ skills: ['profile-default', 'gog', 'ui-ux-pro-max', 'custom'].map(name => ({ ...skill(name), ...(name === 'gog' ? { source: 'openclaw/openclaw', source_type: 'github' } : {}) })), total: 4 })
  const toggle = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<SkillsManagerPanel workspacePath="Workflow/example" profileId={profileId} selectionScopeLabel={label} onToggleSkill={toggle} />))
    expect(agentApi.getAgentProfile).toHaveBeenCalledWith(profileId, undefined, true)
    expect(host.querySelector('[aria-label="Refresh skills"]')).toBeNull()
    const platform = host.querySelector('section[aria-label="Platform skills"]')!
    const added = host.querySelector('section[aria-label="Project skills"]')!
    expect(platform.textContent).toContain('profile-default')
    expect(platform.textContent).toContain('Builder default')
    expect(platform.textContent).toContain('run-default')
    expect(platform.textContent).toContain('Run default')
    expect(platform.querySelector('[aria-pressed]')).toBeNull()
    expect(added.textContent).toContain('gog')
    expect(added.textContent).toContain('ui-ux-pro-max')
    expect(added.textContent).not.toContain('Builder default')
    expect(added.querySelector('[aria-label^="Use custom"]')?.textContent).toBe('Use here')
    await act(async () => added.querySelector<HTMLButtonElement>('[aria-label="Read skill gog"]')!.click())
    expect(host.textContent).toContain('Recorded source: openclaw/openclaw (github)')
    expect(host.textContent).toContain('Keep existing permissions.')
    expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Read instructions')).toBe(false)
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Back to Skills"]')!.click())
    expect(host.querySelector('section[aria-label="Project skills"]')?.textContent).toContain('ui-ux-pro-max')
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Read skill profile-default"]')!.click())
    expect(host.querySelector('section[aria-label="Skill instructions: profile-default"]')?.textContent).toContain('Keep existing permissions.')
    expect(toggle).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()); host.remove(); vi.clearAllMocks() }
})

it('drops a stale platform lookup when the account changes and offers retry on metadata errors', async () => {
  vi.mocked(skillsApi.listSkills).mockResolvedValue({ skills: [], total: 0 })
  let resolveOld!: (profile: Record<string, unknown>) => void
  vi.mocked(agentApi.getAgentProfile).mockReturnValueOnce(new Promise(resolve => { resolveOld = resolve })).mockRejectedValueOnce(new Error('Forbidden')).mockResolvedValueOnce({ skills: [] })
  const initial = useAuthStore.getState().user
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<SkillsManagerPanel workspacePath="Crew/test" profileId="work" onToggleSkill={vi.fn()} />))
    await act(async () => useAuthStore.setState({ user: { ...initial, id: 'changed-owner' } as NonNullable<typeof initial> }))
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Product defaults could not be loaded')
    expect(host.querySelector('section[aria-label="Project skills"]')).not.toBeNull()
    await act(async () => resolveOld({ skills: ['old-default'] }))
    expect(host.textContent).not.toContain('old-default')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Try again')!.click())
    expect(host.querySelector('[role="alert"]')).toBeNull()
    expect(host.querySelector('section[aria-label="Project skills"]')).not.toBeNull()
  } finally { await act(async () => root.unmount()); host.remove(); useAuthStore.setState({ user: initial }); vi.clearAllMocks() }
})

it('offers a retry on failure when the standalone refresh control is hidden', async () => {
  vi.mocked(skillsApi.listSkills).mockRejectedValueOnce(new Error('Could not load skills')).mockResolvedValueOnce({ skills: [], total: 0 })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<SkillsManagerPanel workspacePath="Crew/example" showRefresh={false} />))
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not load skills')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Try again')!.click())
    expect(host.querySelector('[role="alert"]')).toBeNull()
  } finally { await act(async () => root.unmount()); host.remove(); vi.clearAllMocks() }
})

it('opens runtime defaults directly and keeps mode-specific instructions separate from a saved file', async () => {
  const definition = (body: string) => ({ folder_name: 'shared-name', file_path: 'Platform', frontmatter: { name: 'shared-name', description: 'Default guide' }, content: body, managed: true, builtin: true })
  vi.mocked(agentApi.getAgentProfile).mockResolvedValue({ skill_defaults_by_mode: { builder: ['shared-name', 'no-file'], run: ['shared-name'] }, skill_definitions_by_mode: { builder: [definition('Builder body'), { ...definition('Runtime-only body'), folder_name: 'no-file', frontmatter: { name: 'no-file', description: 'Runtime only' } }], run: [definition('Run body')] } })
  vi.mocked(skillsApi.listSkills).mockResolvedValue({ skills: [{ ...definition('Saved body'), file_path: 'skills/shared-name/SKILL.md' }], total: 1 })
  const toggle = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<SkillsManagerPanel workspacePath="Workflow/example" profileId="agentworks" onToggleSkill={toggle} />))
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Read skill shared-name"]')!.click())
    expect(host.textContent).toContain('Saved body')
    expect(host.textContent).not.toContain('Builder body')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Run default')!.click())
    expect(host.textContent).toContain('Run body')
    expect(host.textContent).not.toContain('Saved body')
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Back to Skills"]')!.click())
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Read skill no-file"]')!.click())
    expect(host.textContent).toContain('Runtime-only body')
    expect(toggle).not.toHaveBeenCalled()
    expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Uninstall')).toBe(false)
  } finally { await act(async () => root.unmount()); host.remove(); vi.clearAllMocks() }
})

it('lets a Crew owner edit and remove an ordinary skill from its compact inventory', async () => {
  const skill = { folder_name: 'scene-guide', file_path: 'skills/scene-guide/SKILL.md', frontmatter: { name: 'scene-guide', description: 'Project procedure' }, content: 'My instructions', managed: false, builtin: false }
  vi.mocked(skillsApi.listSkills).mockResolvedValue({ skills: [skill], total: 1 })
  vi.mocked(agentApi.getAgentProfile).mockResolvedValue({ skill_defaults_by_mode: { builder: [], run: [] } } as never)
  const open = vi.fn(), removed = vi.fn()
  const confirm = window.confirm; window.confirm = vi.fn(() => true)
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<SkillsManagerPanel customOnly allowManage profileId="work" workspacePath="Crew/example" onOpenFile={open} onUninstalled={removed} />))
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Read skill scene-guide"]')!.click())
    await act(async () => [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Open in Files')!.click())
    expect(open).toHaveBeenCalledWith('skills/scene-guide/SKILL.md')
    const uninstall = [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.trim() === 'Uninstall')!
    expect(uninstall).toBeDefined()
    access.canWrite = false
    await act(async () => root.render(<SkillsManagerPanel customOnly allowManage profileId="work" workspacePath="Crew/example" onOpenFile={open} onUninstalled={removed} />))
    expect([...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.trim() === 'Uninstall')!.disabled).toBe(true)
    access.canWrite = true
    await act(async () => root.render(<SkillsManagerPanel customOnly allowManage profileId="work" workspacePath="Crew/example" onOpenFile={open} onUninstalled={removed} />))
    await act(async () => [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.trim() === 'Uninstall')!.click())
    expect(skillsApi.deleteSkill).toHaveBeenCalledWith('scene-guide', 'Crew/example')
    expect(removed).toHaveBeenCalledWith('scene-guide')
  } finally { await act(async () => root.unmount()); host.remove(); window.confirm = confirm; access.canWrite = true; vi.clearAllMocks() }
})

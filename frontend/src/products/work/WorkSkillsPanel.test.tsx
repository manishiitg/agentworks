// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { WorkSkillsPanel } from './WorkSkillsPanel'
import { useChatStore } from '../../stores/useChatStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const manager = vi.hoisted(() => ({ props: {} as Record<string, unknown> }))
vi.mock('../../stores/useChatStore', async () => {
  const { create } = await import('zustand')
  return { useChatStore: create(() => ({ chatTabs: {}, addToast: vi.fn(), setTabConfig: vi.fn(), setTabMetadata: vi.fn() })) }
})
vi.mock('../../components/skills/SkillsManagerPanel', () => ({ default: (props: Record<string, unknown>) => {
  manager.props = props
  return <button onClick={() => (props.onToggleSkill as (name: string) => void)('custom')}>Use custom</button>
} }))

afterEach(() => { vi.clearAllMocks(); useChatStore.setState({ chatTabs: {} }) })

it('uses the shared inventory and changes selection only for the current project’s chats', async () => {
  const tab = (tabId: string, project: string) => ({ tabId, sessionId: `${tabId}-session`, config: { selectedSkills: ['existing'] }, metadata: { agentProfileId: 'work', agentProfileProjectId: project } })
  useChatStore.setState({ chatTabs: { current: tab('current', 'test'), sibling: tab('sibling', 'test'), other: tab('other', 'elsewhere') } as unknown as ReturnType<typeof useChatStore.getState>['chatTabs'] })
  const save = vi.fn().mockResolvedValue(undefined)
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<WorkSkillsPanel workspacePath="Crew/test" projectId="test" tabId="current" onSelectedSkillsChange={save} />))
    expect(manager.props).toMatchObject({ customOnly: true, profileId: 'work', profileVersion: 3, workspacePath: 'Crew/test', selectedSkills: ['existing'], selectionScopeLabel: 'Crew' })
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(save).toHaveBeenCalledWith(['existing', 'custom'])
    expect(useChatStore.getState().setTabConfig).toHaveBeenCalledTimes(2)
    expect(useChatStore.getState().setTabConfig).toHaveBeenCalledWith('sibling', { selectedSkills: ['existing', 'custom'] })
    expect(useChatStore.getState().setTabConfig).not.toHaveBeenCalledWith('other', expect.anything())
  } finally { await act(async () => root.unmount()); host.remove() }
})

it('keeps project files readable and leaves saved selection unchanged when persistence fails', async () => {
  const save = vi.fn().mockRejectedValue(new Error('Read only'))
  const open = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<WorkSkillsPanel workspacePath="Crew/test" projectId="test" tabId="current" onSelectedSkillsChange={save} onOpenFile={open} />))
    expect(manager.props.additionalSkills).toBeUndefined()
    ;(manager.props.onOpenFile as (path: string) => void)('skills/custom/nested/SKILL.md')
    expect(open).toHaveBeenCalledWith('Crew/test/skills/custom/nested/SKILL.md')
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(useChatStore.getState().addToast).toHaveBeenCalledWith('Read only', 'error')
    expect(useChatStore.getState().setTabConfig).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()); host.remove() }
})

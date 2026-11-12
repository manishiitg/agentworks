// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { TeamTab } from './TeamTab'
import { TeamActivity } from './TeamActivity'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { openAgentTab } from '../../utils/openAgentTab'
import { openCrewAccess } from './MacSharedAgents'
import { teamRequest } from './teamApi'

vi.mock('./teamApi', () => ({
  useTeamTab: (project: string) => ({ target: { project }, view: {
    team: { on: project !== 'off', folder: { folders: [] } }, people: [], intro: '',
  } }),
  crewPeople: () => new Promise(() => {}),
  teamRequest: vi.fn(),
  teamFailure: (error: Error) => ({ message: error.message }),
}))
vi.mock('../../utils/openAgentTab', () => ({ openAgentTab: vi.fn() }))
vi.mock('./CrewWorksElsewhere', () => ({ CrewWhereRow: () => null }))
vi.mock('../../services/liveFeed', () => ({ liveFeed: { subscribe: () => () => {} } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-883: activity must be discoverable separately from setup, and a failed read must not disappear.
it('opens activity, shows lookup failure with retry, and separates People, Agents, Activity and Sync', async () => {
  vi.mocked(teamRequest).mockRejectedValueOnce(new Error('Unavailable')).mockResolvedValue({ feed: [] })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const tab = (name: string) => [...host.querySelectorAll<HTMLButtonElement>('[role="tab"]')].find(button => button.textContent?.startsWith(name))!
  try {
    await act(async () => root.render(<TeamTab projectId="one" />))
    expect(tab('Activity').getAttribute('aria-selected')).toBe('true')
    expect(host.querySelector('[role="alert"]')).not.toBeNull()
    expect(host.querySelector('section[aria-label="People"]')).toBeNull()
    await act(async () => host.querySelector<HTMLButtonElement>('[role="alert"] button')!.click())
    expect(host.querySelector('[role="alert"]')).toBeNull()
    expect(host.querySelector('section[aria-label="Activity"]')).not.toBeNull()
    await act(async () => tab('Agents').click())
    expect(host.querySelector('section[aria-label="Agents"]')).not.toBeNull()
    await act(async () => tab('People').click())
    expect(host.querySelector('section[aria-label="Activity"]')).toBeNull()
    expect(host.querySelector('section[aria-label="People"]')).not.toBeNull()
    await act(async () => openCrewAccess('one', '', 'team', 'activity'))
    expect(tab('Activity').getAttribute('aria-selected')).toBe('true')
    await act(async () => openCrewAccess('one', '', 'team', 'sync'))
    expect(tab('Sync').getAttribute('aria-selected')).toBe('true')
    await act(async () => tab('Sync').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true })))
    expect(document.activeElement).toBe(tab('Activity'))
    await act(async () => root.render(<TeamTab projectId="off" />))
    expect(host.querySelector('[role="tablist"]')).toBeNull()
    expect(host.querySelector('section[aria-label="People"]')).not.toBeNull()
  } finally { await act(async () => root.unmount()); host.remove() }
})

// A Mac copy and its server use different project prefixes, but the same own physical tab.
// Never open that own tab for a different person's identically named native tab.
it('opens only the current person physical agent across the Mac/server project prefix', async () => {
  const previous = useChatStore.getState().chatTabs
  useChatStore.setState({ chatTabs: { 'local-tab': { tabId: 'local-tab', metadata: { agentProfileProjectId: 'local-project', agentProfileConversationKey: 'local-project:chat:side', agentProfileIsMainChat: false, agentProfileTabCanWrite: true } } } as never })
  const host = document.createElement('div'), root = createRoot(host)
  const data = (you: boolean) => ({ feed: [{ id: 'question:question', kind: 'question', at: new Date().toISOString(), person: 'Example', title: 'Research', verb: 'asked Main', message: { id: 'question', from: { person: 'Example', label: 'Research' }, to: { person: 'Example', agent: 'Research', you, label: 'Research', chat_key: 'server-project:chat:side', tab: 'side' }, question: 'Example question', sent_at: new Date().toISOString(), status: 'answered', status_line: 'Answered', answer: 'Example answer' } }] })
  try {
    vi.mocked(openAgentTab).mockClear()
    vi.mocked(teamRequest).mockResolvedValue(data(true) as never)
    await act(async () => root.render(<TeamActivity projectId="local-project" target={{ project: 'server-project' }} />))
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-expanded]')!.click())
    await act(async () => [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.includes('Open'))!.click())
    expect(openAgentTab).toHaveBeenCalledWith('local-tab')
    vi.mocked(openAgentTab).mockClear()
    vi.mocked(teamRequest).mockResolvedValue(data(false) as never)
    await act(async () => root.render(<TeamActivity key="different-person" projectId="local-project" target={{ project: 'server-project' }} />))
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-expanded]')!.click())
    await act(async () => [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.includes('Open'))!.click())
    expect(openAgentTab).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()); useChatStore.setState({ chatTabs: previous }) }
})

// Activity contains permission-filtered text: a different account must not see the previous answer while its own read waits.
it('clears permission-filtered activity immediately when the account changes', async () => {
  const previous = useAuthStore.getState().user
  useAuthStore.setState({ user: { id: 'first-person' } as never })
  const host = document.createElement('div'), root = createRoot(host)
  vi.mocked(teamRequest).mockResolvedValueOnce({ feed: [{ id: 'question:private', kind: 'question', at: new Date().toISOString(), person: 'Example', title: 'Research', verb: 'asked Main', line: 'Visible only to the first account', message: { id: 'private', question: 'Visible only to the first account', from: {}, to: {} } }] } as never).mockImplementation(() => new Promise(() => {}))
  try {
    await act(async () => root.render(<TeamActivity projectId="same-project" target={{ project: 'same-project' }} />))
    expect(host.textContent).toContain('Visible only to the first account')
    await act(async () => useAuthStore.setState({ user: { id: 'second-person' } as never }))
    expect(host.textContent).not.toContain('Visible only to the first account')
    expect(host.textContent).toContain('Loading Team activity')
  } finally { await act(async () => root.unmount()); useAuthStore.setState({ user: previous }) }
})

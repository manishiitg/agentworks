// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { TeamTab } from './TeamTab'
import { teamRequest } from './teamApi'

vi.mock('./teamApi', () => ({
  useTeamTab: (project: string) => ({ target: { project }, view: {
    team: { on: project !== 'off', folder: { folders: [] } }, people: [], intro: '',
  } }),
  crewPeople: () => new Promise(() => {}),
  teamRequest: vi.fn(),
  teamFailure: (error: Error) => ({ message: error.message }),
}))
vi.mock('./CrewWorksElsewhere', () => ({ CrewWhereRow: () => null }))
vi.mock('../../services/liveFeed', () => ({ liveFeed: { subscribe: () => () => {} } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-883: activity must be discoverable separately from setup, and a failed read must not disappear.
it('opens activity, shows lookup failure with retry, and keeps settings in their own tab', async () => {
  vi.mocked(teamRequest).mockRejectedValueOnce(new Error('Unavailable')).mockResolvedValue({ now: [], messages: [], other: [], foot: '' })
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
    await act(async () => tab('Settings').click())
    expect(host.querySelector('section[aria-label="Activity"]')).toBeNull()
    expect(host.querySelector('section[aria-label="People"]')).not.toBeNull()
    await act(async () => tab('Settings').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true })))
    expect(document.activeElement).toBe(tab('Activity'))
    await act(async () => root.render(<TeamTab projectId="off" />))
    expect(host.querySelector('[role="tablist"]')).toBeNull()
    expect(host.querySelector('section[aria-label="People"]')).not.toBeNull()
  } finally { await act(async () => root.unmount()); host.remove() }
})

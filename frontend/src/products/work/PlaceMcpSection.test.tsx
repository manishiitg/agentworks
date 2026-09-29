// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

const placeMock = vi.hoisted(() => ({
  list: vi.fn(async () => [
    { name: 'gmail', catalog: 'GoogleGmail', url: 'https://gmailmcp.googleapis.com/mcp/v1', owner: 'u1', owner_name: 'manish', mine: false, connected: true, active: true },
  ]),
  add: vi.fn(async () => ({ name: 'googledrive', oauth: false })),
  connect: vi.fn(async () => ({})),
  remove: vi.fn(async () => undefined),
}))
vi.mock('../../api/placeMcp', () => ({ placeMcpApi: placeMock }))
vi.mock('../../api/personalMcp', () => ({
  personalMcpApi: { catalog: vi.fn(async () => [{ name: 'googledrive', catalog: 'GoogleDrive', sign_in: true, needs_client: false }]) },
}))

import { PlaceMcpSection } from './PlaceMcpSection'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.clearAllMocks() })

async function render(canEdit: boolean) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => { root.render(<PlaceMcpSection workspacePath="Workflow/w" placeNoun="workflow" canEdit={canEdit} />) })
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  return host
}

it('lists the connections with whose login they use', async () => {
  const host = await render(false)
  expect(host.textContent).toContain('GoogleGmail')
  expect(host.textContent).toContain("manish's login")
  // A viewer who cannot edit adds nothing.
  expect(host.textContent).not.toContain('Add with your login')
})

it('warns that everyone using the workflow acts with your login before adding', async () => {
  const host = await render(true)
  const addButton = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Add with your login'))!
  await act(async () => { addButton.click() })
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  const drive = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('GoogleDrive'))!
  await act(async () => { drive.click() })
  expect(document.body.textContent).toContain('can use GoogleDrive as you')
  expect(document.body.textContent).toContain('Slack channel')
  expect(placeMock.add).not.toHaveBeenCalled()
  const confirm = [...document.body.querySelectorAll('button')].find(button => button.textContent?.includes('Add with my login'))!
  await act(async () => { confirm.click() })
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  expect(placeMock.add).toHaveBeenCalledWith('Workflow/w', 'GoogleDrive')
})

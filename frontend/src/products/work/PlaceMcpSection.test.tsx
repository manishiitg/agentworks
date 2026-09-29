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
const catalogMock = vi.hoisted(() => ({
  entries: [
    { name: 'googledrive', catalog: 'GoogleDrive', sign_in: true, needs_client: false },
  ] as { name: string; catalog: string; sign_in: boolean; needs_client: boolean; group?: string }[],
}))
vi.mock('../../api/placeMcp', () => ({ placeMcpApi: placeMock }))
vi.mock('../../api/personalMcp', () => ({ personalMcpApi: { catalog: vi.fn(async () => catalogMock.entries) } }))
vi.mock('../../api/secrets', () => ({ secretsApi: { listWorkflowSecrets: vi.fn(async () => [{ name: 'LINEAR_KEY' }]) } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { is_admin: false } }) }))
vi.mock('./McpAppsSection', () => ({ McpAppsSection: () => null }))

import { PlaceMcpSection } from './PlaceMcpSection'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.clearAllMocks(); catalogMock.entries = [{ name: 'googledrive', catalog: 'GoogleDrive', sign_in: true, needs_client: false }] })

async function render(canEdit: boolean, noun = 'workflow', path = 'Workflow/w') {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => { root.render(<PlaceMcpSection workspacePath={path} placeNoun={noun} canEdit={canEdit} onAsk={async () => undefined} />) })
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  return host
}
const settle = () => act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
const button = (root: ParentNode, text: string) => [...root.querySelectorAll('button')].find(b => b.textContent?.includes(text))!
const openPicker = async (host: HTMLElement) => { await act(async () => { button(host, 'Add with your login').click() }); await settle() }

it('lists the connections with whose login they use', async () => {
  const host = await render(false)
  expect(host.textContent).toContain('GoogleGmail')
  expect(host.textContent).toContain("manish's login")
  expect(host.textContent).toContain('Connected')
  // A viewer who cannot edit adds nothing.
  expect(host.textContent).not.toContain('Add with your login')
})

it('warns that everyone using the workflow acts with your login before adding', async () => {
  const host = await render(true)
  await openPicker(host)
  await act(async () => { button(host, 'GoogleDrive').click() })
  expect(document.body.textContent).toContain('can use GoogleDrive as you')
  expect(document.body.textContent).toContain('Slack channel')
  expect(placeMock.add).not.toHaveBeenCalled()
  await act(async () => { button(document.body, 'Add with my login').click() })
  await settle()
  expect(placeMock.add).toHaveBeenCalledWith('Workflow/w', 'GoogleDrive')
})

// A Code is a place like a Crew: the same screen, worded for the Code.
it('names the Code and shows service marks for a sign-in group', async () => {
  catalogMock.entries = [
    { name: 'googledrive', catalog: 'GoogleDrive', sign_in: true, needs_client: false, group: 'google' },
    { name: 'googlecalendar', catalog: 'GoogleCalendar', sign_in: true, needs_client: false, group: 'google' },
  ]
  placeMock.add.mockResolvedValueOnce({ name: 'googledrive', oauth: true }).mockResolvedValueOnce({ name: 'googlecalendar', oauth: true })
  const host = await render(true, 'Code', 'Chats/Code/projects/p1')
  expect(host.textContent).toContain('Everyone who uses this Code')
  await openPicker(host)
  expect(host.querySelector('[data-testid="mcp-group-google"]')).not.toBeNull()
  await act(async () => { (host.querySelector('button[aria-label="Add Drive"]') as HTMLButtonElement).click() })
  await act(async () => { (host.querySelector('button[aria-label="Add Calendar"]') as HTMLButtonElement).click() })
  await act(async () => { button(host, 'Add 2 services with your login').click() })
  await act(async () => { button(document.body, 'Add with my login').click() })
  await settle()
  expect(placeMock.add.mock.calls).toEqual([['Chats/Code/projects/p1', 'GoogleDrive'], ['Chats/Code/projects/p1', 'GoogleCalendar']])
  // One sign-in covers both.
  expect(placeMock.connect).toHaveBeenCalledTimes(1)
  expect(placeMock.connect).toHaveBeenCalledWith('Chats/Code/projects/p1', 'googledrive', undefined)
})

it('asks for your own OAuth app when the provider has none registered', async () => {
  placeMock.list.mockResolvedValue([{ name: 'gmail', catalog: 'GoogleGmail', url: 'https://x', owner: 'u1', owner_name: 'me', mine: true, connected: false, active: true }])
  placeMock.connect.mockResolvedValueOnce({ status: 'needs_client_id', redirect_uri: 'https://app.example.com/api/oauth/callback' } as never)
  const host = await render(true, 'Code', 'Chats/Code/projects/p1')
  await act(async () => { button(host, 'Sign in').click() })
  await settle()
  expect(host.querySelector('[data-testid="place-mcp-client-prompt"]')?.textContent).toContain('https://app.example.com/api/oauth/callback')
  const set = (label: string, value: string) => {
    const input = host.querySelector(`input[aria-label="${label}"]`) as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }
  await act(async () => { set('OAuth client ID', 'cid.apps.googleusercontent.com'); set('OAuth client secret', 'shh') })
  await act(async () => { [...host.querySelectorAll('[data-testid="place-mcp-client-prompt"] button')].find(b => b.textContent === 'Sign in')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
  await settle()
  expect(placeMock.connect).toHaveBeenLastCalledWith('Chats/Code/projects/p1', 'gmail', { clientId: 'cid.apps.googleusercontent.com', clientSecret: 'shh' })
  placeMock.list.mockResolvedValue([{ name: 'gmail', catalog: 'GoogleGmail', url: 'https://gmailmcp.googleapis.com/mcp/v1', owner: 'u1', owner_name: 'manish', mine: false, connected: true, active: true }])
})

it('adds a server that is not listed, with an API-key header from the project secrets', async () => {
  const host = await render(true, 'Crew', 'Chats/Work/projects/p1')
  await openPicker(host)
  await act(async () => { button(host, 'Add a server that is not listed').click() })
  await settle()
  const set = (label: string, value: string, tag: 'input' | 'select' = 'input') => {
    const element = host.querySelector(`${tag}[aria-label="${label}"]`) as HTMLInputElement | HTMLSelectElement
    const proto = tag === 'select' ? HTMLSelectElement.prototype : HTMLInputElement.prototype
    Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(element, value)
    element.dispatchEvent(new Event(tag === 'select' ? 'change' : 'input', { bubbles: true }))
  }
  await act(async () => { set('Server name', 'linear'); set('Server URL', 'https://mcp.linear.app/mcp'); set('API key header', 'Authorization') })
  await act(async () => { set('Secret for the header', 'LINEAR_KEY', 'select') })
  await act(async () => { button(host, 'Add server').click() })
  await act(async () => { button(document.body, 'Add with my login').click() })
  await settle()
  expect(placeMock.add).toHaveBeenCalledWith('Chats/Work/projects/p1', {
    name: 'linear', url: 'https://mcp.linear.app/mcp', headers: { Authorization: { secret: 'LINEAR_KEY', format: 'Bearer {}' } },
  })
})

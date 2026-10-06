// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { TooltipProvider } from '../ui/tooltip'

const placeMock = vi.hoisted(() => ({
  tools: vi.fn(async () => ({ status: 'ok', tools: [{ name: 'read_file', description: 'Read one file', server: 'gmail', parameters: { path: { type: 'string' } }, required: ['path'] }] })),
  list: vi.fn(async () => [
    { name: 'gmail', catalog: 'GoogleGmail', url: 'https://gmailmcp.googleapis.com/mcp/v1', owner: 'u1', owner_name: 'manish', mine: true, connected: true, active: true },
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
const catalogFn = vi.hoisted(() => vi.fn())
vi.mock('../../api/mcpCatalog', () => ({ mcpCatalogApi: { catalog: catalogFn } }))
const secretsApi = vi.hoisted(() => ({ get: vi.fn(async () => ({ data: { secrets: [{ name: 'LINEAR_KEY' }] } })), post: vi.fn(async () => ({ data: {} })) }))
vi.mock('../../services/api', () => ({ default: secretsApi }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { is_admin: false } }) }))
vi.mock('../../products/work/McpAppsSection', () => ({ McpAppsSection: () => null }))

import { usePlaceMcpConnections } from '../../components/integrations/usePlaceMcpConnections'
import { McpConnectionsPanel } from '../../components/integrations/McpConnectionsPanel'
function PlaceBrowser(props: Parameters<typeof usePlaceMcpConnections>[0]) { const model = usePlaceMcpConnections(props); return <McpConnectionsPanel {...model}>{model.dialogs}</McpConnectionsPanel> }

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
catalogFn.mockImplementation(async () => catalogMock.entries)
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.clearAllMocks(); catalogFn.mockImplementation(async () => catalogMock.entries); catalogMock.entries = [{ name: 'googledrive', catalog: 'GoogleDrive', sign_in: true, needs_client: false }] })

async function render(canEdit: boolean, noun = 'workflow', path = 'Workflow/w', onAsk?: (message: string) => Promise<void>, chatSessionId?: string) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => { root.render(<TooltipProvider><PlaceBrowser workspacePath={path} placeNoun={noun} canEdit={canEdit} onAsk={onAsk} chatSessionId={chatSessionId} /></TooltipProvider>) })
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  if (canEdit) { await act(async () => { host.querySelector<HTMLButtonElement>('button[role="tab"][title="Available MCPs"]')!.click() }) }
  return host
}
const settle = () => act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
const button = (root: ParentNode, text: string) => [...root.querySelectorAll('button')].find(b => b.textContent?.includes(text))!
// The connectors people can add are on the page itself now; there is no button to reveal them.
const openPicker = async (_host: HTMLElement) => { await settle() }

it('lists the connections with whose login they use', async () => {
  const host = await render(false)
  expect(host.textContent).toContain('GoogleGmail')
  expect(host.textContent).toContain("Connected by you")
  expect(host.textContent).toContain('Connected')
  // A viewer who cannot edit adds nothing: no connector list.
  expect(host.querySelector('[aria-label="Available servers"]')).toBeNull()
  expect(host.querySelector('[aria-label="Available servers"]')).toBeNull()
})

it('Connect sends the request to the agent chat, no popup, and says whose login it uses', async () => {
  const onAsk = vi.fn(async (_message: string) => undefined)
  const host = await render(true, 'Crew', 'Workflow/w', onAsk)
  // The rule is a plain line on the page, not a dialog in the way.
  expect(host.textContent).toContain('used by everyone with access to this')
  expect(host.textContent).toContain('Available')
  expect(host.textContent).toContain('Google apps tab')
  expect(host.textContent).toContain('GITHUB_TOKEN')
  await act(async () => { host.querySelector('[aria-label="Available servers"] button')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
  await settle()
  expect(document.body.textContent).not.toContain('with my login')
  const input = host.querySelector<HTMLInputElement>('input[aria-label="GoogleDrive connection name"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'Drive · Engineering')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => { host.querySelector<HTMLButtonElement>('form button[type="submit"]')!.click() })
  await settle()
  // The chat does the connecting (and sends back any sign-in link); the screen adds nothing itself.
  expect(onAsk).toHaveBeenCalledTimes(1)
  expect(String(onAsk.mock.calls[0][0])).toContain('Connect GoogleDrive')
  expect(String(onAsk.mock.calls[0][0])).toContain('First read the attached work-mcp skill')
  expect(String(onAsk.mock.calls[0][0])).toContain('Check the connection status')
  expect(String(onAsk.mock.calls[0][0])).toContain('label="Drive · Engineering"')
  expect(placeMock.add).not.toHaveBeenCalled()
})

// A Code is a place like a Crew: the same screen, worded for the Code.
it('names the Code and shows service marks for a sign-in group', async () => {
  catalogMock.entries = [
    { name: 'googledrive', catalog: 'GoogleDrive', sign_in: true, needs_client: false, group: 'google' },
    { name: 'googlecalendar', catalog: 'GoogleCalendar', sign_in: true, needs_client: false, group: 'google' },
  ]
  placeMock.add.mockResolvedValueOnce({ name: 'googledrive', oauth: true }).mockResolvedValueOnce({ name: 'googlecalendar', oauth: true })
  const host = await render(true, 'Code', 'Chats/Code/projects/p1')
  expect(host.textContent).toContain('used by everyone with access to this Code')
  await openPicker(host)
  expect(host.querySelector('[aria-label="Google Workspace"]')).not.toBeNull()
  await act(async () => { (host.querySelector('input[aria-label="Add GoogleDrive"]') as HTMLButtonElement).click() })
  await act(async () => { (host.querySelector('input[aria-label="Add GoogleCalendar"]') as HTMLButtonElement).click() })
  await act(async () => { button(host, 'Connect 2 services').click() })
  await settle()
  expect(placeMock.add.mock.calls).toEqual([['Chats/Code/projects/p1', 'GoogleDrive'], ['Chats/Code/projects/p1', 'GoogleCalendar']])
  // One sign-in covers both.
  expect(placeMock.connect).toHaveBeenCalledTimes(1)
  expect(placeMock.connect).toHaveBeenCalledWith('Chats/Code/projects/p1', 'googledrive', undefined, undefined)
})

it('asks for your own OAuth app when the provider has none registered', async () => {
  placeMock.list.mockResolvedValue([{ name: 'gmail', catalog: 'GoogleGmail', url: 'https://x', owner: 'u1', owner_name: 'me', mine: true, connected: false, active: true }])
  placeMock.connect.mockResolvedValueOnce({ status: 'needs_client_id', redirect_uri: 'https://app.example.com/api/oauth/callback' } as never)
  const host = await render(true, 'Code', 'Chats/Code/projects/p1')
  await act(async () => { button(host, 'Connected').click() }); await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Actions for GoogleGmail"]')!.click() }); await act(async () => { button(host, 'Sign in').click() })
  await settle()
  expect(host.querySelector('[data-testid="mcp-client-prompt"]')?.textContent).toContain('https://app.example.com/api/oauth/callback')
  const set = (label: string, value: string) => {
    const input = host.querySelector(`input[aria-label="${label}"]`) as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }
  await act(async () => { set('OAuth client ID', 'cid.apps.googleusercontent.com'); set('OAuth client secret', 'shh') })
  await act(async () => { [...host.querySelectorAll('[data-testid="mcp-client-prompt"] button')].find(b => b.textContent === 'Sign in')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
  await settle()
  expect(placeMock.connect).toHaveBeenLastCalledWith('Chats/Code/projects/p1', 'gmail', { clientId: 'cid.apps.googleusercontent.com', clientSecret: 'shh' }, undefined)
  placeMock.list.mockResolvedValue([{ name: 'gmail', catalog: 'GoogleGmail', url: 'https://gmailmcp.googleapis.com/mcp/v1', owner: 'u1', owner_name: 'manish', mine: true, connected: true, active: true }])
})

it('adds a server that is not listed, with an API-key header from private secrets', async () => {
  const host = await render(true, 'Crew', 'Chats/Work/projects/p1')
  await openPicker(host)
  await act(async () => { button(host, 'Add custom server').click() })
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
  await settle()
  expect(placeMock.add).toHaveBeenCalledWith('Chats/Work/projects/p1', {
    name: 'linear', url: 'https://mcp.linear.app/mcp', headers: { Authorization: { secret: 'LINEAR_KEY', format: 'Bearer {}' } },
  })
})

// A connector list that fails to load must say so and offer a retry (it used to sit on
// "Loading…" forever, which looks like there is nothing to connect); an empty list says that.
it('reports a failed connector list with a retry, and an empty one plainly', async () => {
  catalogFn.mockRejectedValueOnce(new Error('network'))
  const host = await render(true, 'Code', 'Chats/Code/projects/p1')
  await openPicker(host)
  await settle()
  expect(host.textContent).toContain('Could not load the list of connectors')
  expect(host.textContent).not.toContain('Loading…')
  catalogMock.entries = [{ name: 'linear', catalog: 'Linear', sign_in: true, needs_client: false }]
  await act(async () => { button(host, 'Retry').click() })
  await settle()
  expect(host.textContent).not.toContain('Could not load the list of connectors')
  expect(host.textContent).toContain('Linear')
  catalogMock.entries = []
  const empty = await render(true, 'Crew', 'Chats/Work/projects/p2')
  await openPicker(empty)
  await settle()
  expect(empty.textContent).toContain('No servers available to add')
})

// A sign-in finishing in the other tab turns a connection to "connected"; the chat is told through
// onAsk, but opening the screen with connections already connected sends nothing.
it('tells the chat when a connection becomes connected, not on first load', async () => {
  const listed = [{ name: 'u1__linear', catalog: 'Linear', url: 'https://x', owner: 'u1', owner_name: 'me', mine: true, connected: false, active: true }]
  placeMock.list.mockImplementation(async () => [...listed])
  const onAsk = vi.fn(async (_message: string) => undefined)
  const host = await render(true, 'Crew', 'Workflow/w', onAsk)
  expect(onAsk).not.toHaveBeenCalled()
  listed[0] = { ...listed[0], connected: true }
  await act(async () => { window.dispatchEvent(new Event('focus')) })
  await settle()
  expect(host).toBeTruthy()
  expect(onAsk).toHaveBeenCalledTimes(1)
  expect(String(onAsk.mock.calls[0][0])).toContain('Linear is now connected in this Crew')
})


it('stores a new custom key privately and sends only its reference to the project', async () => {
  const host = await render(true, 'Code', 'Chats/Code/projects/p1')
  await act(async () => { button(host, 'Add custom server').click() })
  await settle()
  const set = (label: string, value: string) => {
    const input = host.querySelector(`input[aria-label="${label}"]`) as HTMLInputElement
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }
  await act(async () => { set('Server name', 'my_linear'); set('Server URL', 'https://mcp.linear.app/mcp'); set('API key header', 'Authorization'); set('Private API key', 'test-private-key') })
  expect((button(host, 'Add server') as HTMLButtonElement).disabled).toBe(false)
  await act(async () => { button(host, 'Add server').click() })
  await settle()
  expect(secretsApi.post).toHaveBeenCalledWith('/api/me/secrets', { name: 'MCP_MY_LINEAR_KEY', value: 'test-private-key' })
  expect(placeMock.add).toHaveBeenCalledWith('Chats/Code/projects/p1', {
    name: 'my_linear', url: 'https://mcp.linear.app/mcp', headers: { Authorization: { secret: 'MCP_MY_LINEAR_KEY', format: 'Bearer {}' } },
  })
  expect(JSON.stringify(placeMock.add.mock.calls)).not.toContain('test-private-key')
  expect(host.querySelector('input[aria-label="Private API key"]')).toBeNull()
})

it('discovers private tools only when expanded and shows the shared JSON schema layout', async () => {
  placeMock.list.mockResolvedValueOnce([{ name: 'gmail', catalog: 'GoogleGmail', url: 'https://x', owner: 'u1', owner_name: 'me', mine: true, connected: true, active: true }])
  const host = await render(false)
  expect(placeMock.tools).not.toHaveBeenCalled()
  await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-label="Show tools on GoogleGmail"]')!.click() })
  await settle()
  expect(placeMock.tools).toHaveBeenCalledWith('gmail')
  expect(host.querySelector('[data-tool-card="read_file"]')).not.toBeNull()
  const args = host.querySelector<HTMLElement>('summary[aria-label="Arguments for read_file"]')!
  await act(async () => { args.click() })
  expect(host.querySelector('pre[aria-label="Input JSON schema"]')?.textContent).toContain('"path"')
  expect(host.querySelector('pre')?.textContent).toContain('"required"')
})

it('binds private sign-in to the product chat and avoids a duplicate focus notification', async () => {
  const listed = [{ name: 'notion', catalog: 'Notion', url: 'https://x', owner: 'u1', owner_name: 'me', mine: true, connected: false, active: true, sign_in: true }]
  placeMock.list.mockImplementation(async () => [...listed])
  catalogMock.entries = []
  placeMock.connect.mockResolvedValueOnce({ auth_url: 'https://example.com/authorize' } as never)
  const popup = vi.spyOn(window, 'open').mockImplementation(() => null)
  const onAsk = vi.fn(async (_message: string) => undefined)
  const host = await render(true, 'Crew', 'Chats/Work/projects/p2', onAsk, 'crew-chat')
  await act(async () => { button(host, 'Connected').click() })
  await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Actions for Notion"]')!.click() })
  await act(async () => { button(host, 'Sign in').click() })
  await settle()
  expect(placeMock.connect).toHaveBeenCalledWith('Chats/Work/projects/p2', 'notion', undefined, 'crew-chat')
  listed[0] = { ...listed[0], connected: true }
  await act(async () => { window.dispatchEvent(new Event('focus')) })
  await settle()
  expect(onAsk).not.toHaveBeenCalled()
  expect(host.textContent).toContain('Connected')
  popup.mockRestore()
})


it.each(['Crew', 'Code', 'workflow', 'Relay'])('adds multiple named accounts through the shared UI in %s', async noun => {
  catalogMock.entries = [{ name: 'notion', catalog: 'Notion', sign_in: true, needs_client: false }]
  placeMock.list.mockResolvedValue([
    { name: 'notion_a', label: 'Notion · Engineering', catalog: 'Notion', url: 'https://x', owner: 'u1', owner_name: 'me', mine: true, connected: true, active: true, sign_in: true },
    { name: 'notion_b', label: 'Notion · Sales', catalog: 'Notion', url: 'https://x', owner: 'u1', owner_name: 'me', mine: true, connected: true, active: true, sign_in: true },
  ] as never)
  placeMock.add.mockResolvedValueOnce({ name: 'notion_c', oauth: true })
  const host = await render(true, noun, 'Workflow/w', undefined, 'chat-1')
  expect(host.textContent).toContain('Notion') // provider stays available for another account
  await act(async () => { button(host, 'Add connection').click() })
  const input = host.querySelector<HTMLInputElement>('input[aria-label="Notion connection name"]')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'Notion · Support')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => { host.querySelector<HTMLButtonElement>('form button[type="submit"]')!.click() })
  await settle()
  expect(placeMock.add).toHaveBeenCalledWith('Workflow/w', { catalog: 'Notion', label: 'Notion · Support' })
  expect(placeMock.connect).toHaveBeenCalledWith('Workflow/w', 'notion_c', undefined, 'chat-1')
  await act(async () => { button(host, 'Connected').click() })
  expect(host.textContent).toContain('Notion · Engineering')
  expect(host.textContent).toContain('Notion · Sales')
  await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Actions for Notion · Sales"]')!.click() })
  await act(async () => { button(document.body, 'Sign in again').click() })
  await settle()
  expect(placeMock.connect).toHaveBeenLastCalledWith('Workflow/w', 'notion_b', undefined, 'chat-1')
  await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Actions for Notion · Engineering"]')!.click() })
  await act(async () => { button(document.body, 'Remove from this project').click() })
  await settle()
  expect(placeMock.remove).toHaveBeenCalledWith('Workflow/w', 'notion_a', 'u1')
})

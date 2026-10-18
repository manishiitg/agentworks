// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import DefaultRelayDashboard from './DefaultRelayDashboard'

const { get, openAsk, send, releases, auth } = vi.hoisted(() => ({ get: vi.fn(), openAsk: vi.fn(), send: vi.fn(), releases: vi.fn(), auth: { id: 'owner' } }))
vi.mock('../../../services/api', () => ({ default: { get } }))
vi.mock('../../../api/workflowWebhooks', () => ({ workflowWebhooksApi: { relayReleases: releases } }))
vi.mock('../../../utils/projectAskConversation', () => ({ openProjectAskConversation: openAsk }))
vi.mock('../../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: send }))
vi.mock('../../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('../../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: unknown) => unknown) => select({ user: { id: auth.id } }) }))
vi.mock('../../ChatArea', () => ({ default: ({ tabId }: { tabId: string }) => <div data-native-tab={tabId}>Existing Flow conversation and replies</div> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const cleanups: (() => Promise<void>)[] = []
afterEach(async () => { for (const cleanup of cleanups.splice(0)) await cleanup(); vi.resetAllMocks(); auth.id = 'owner' })

function fixture(version = 'v1', canAsk = true) {
  return { product: { id: 'relay-1', name: 'Invoice OCR', purpose: 'Process invoices' }, version, can_ask: canAsk, fetched_at: '2026-10-13T10:00:00Z', runs: [{ run: 'iteration-2-hook', run_id: 'ocr-2', caller: 'finance-b', version, status: 'failed', error: version === 'v1' ? 'Missing invoice date' : 'Later error', attempt: 1, started_at: 10, duration_s: 2, input: { invoice_id: 'invoice-42' }, result: { valid: false }, steps: [{ id: 'validate', name: 'Validate invoice', status: 'failed', error: 'Missing invoice date', started_at: 10, completed_at: 12, output: { valid: false } }] }] }
}

async function mount(renderEmptyState?: (canCreate: boolean) => React.ReactNode) {
  releases.mockResolvedValue({ active_version: 'v1', releases: [{ version: 'v1' }] })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(async () => { await act(async () => root.unmount()); host.remove() })
  const render = async (refreshToken = 0) => { await act(async () => root.render(<DefaultRelayDashboard workspacePath="Workflow/ocr" refreshToken={refreshToken} renderEmptyState={renderEmptyState} />)) }
  await render()
  return { host, render }
}

function click(host: HTMLElement, label: string) {
  const button = [...host.querySelectorAll('button')].find(item => item.textContent?.trim() === label)
  if (!button) throw new Error('Missing button: ' + label)
  button.click()
}

it('explains a selected failed invoice through the existing chat with frozen run evidence', async () => {
  get.mockResolvedValue({ data: fixture() }); openAsk.mockResolvedValue('existing-relay-chat'); send.mockResolvedValue({ tabId: 'existing-relay-chat' })
  const { host, render } = await mount()
  expect(host.textContent).toContain('Missing invoice date')
  expect(host.textContent).toContain('invoice-42')
  expect(host.textContent).toContain('Started by finance-b')
  await act(async () => click(host, 'Ask about this run'))
  get.mockResolvedValue({ data: fixture('v2') })
  await render(1)
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(openAsk).toHaveBeenCalledWith({ product: 'relay', projectId: 'relay-1', workspacePath: 'Workflow/ocr' })
  expect(send).toHaveBeenCalledTimes(1)
  const queued = send.mock.calls[0][0]
  expect(queued.tabId).toBe('existing-relay-chat')
  expect(queued.notificationId).toMatch(/^relay-run-question:/)
  expect(queued.message).toContain('"version": "v1"')
  expect(queued.message).toContain('invoice-42')
  expect(queued.message).toContain('Missing invoice date')
  expect(queued.message).not.toContain('Later error')
  expect(queued.message).toContain('does not itself request a retry')
  expect(host.querySelector('[data-native-tab]')?.getAttribute('data-native-tab')).toBe('existing-relay-chat')
})

it('shows shared run diagnostics while keeping Run chat and draft tests unavailable', async () => {
  auth.id = 'finance-a'; get.mockResolvedValue({ data: fixture('v1', false) })
  const { host } = await mount()
  expect(host.textContent).toContain('Started by finance-b')
  expect(host.textContent).toContain('Missing invoice date')
  expect(host.textContent).toContain('Ask is unavailable for this Flow.')
  expect([...host.querySelectorAll('option')].some(item => item.value === 'draft')).toBe(false)
  expect([...host.querySelectorAll('button')].some(item => item.textContent === 'Ask about this run')).toBe(false)
  expect(openAsk).not.toHaveBeenCalled(); expect(send).not.toHaveBeenCalled()
})

it('lets a Run person ask about a run but never offers draft tests', async () => {
  auth.id = 'finance-a'; get.mockResolvedValue({ data: fixture('v1', true) })
  const { host } = await mount()
  expect([...host.querySelectorAll('button')].some(item => item.textContent === 'Ask about this run')).toBe(true)
  expect([...host.querySelectorAll('option')].some(item => item.value === 'draft')).toBe(false)
})

it('rechecks current Ask access before submitting the selected record', async () => {
  get.mockResolvedValue({ data: fixture() })
  const { host } = await mount()
  await act(async () => click(host, 'Ask about this run'))
  get.mockResolvedValue({ data: fixture('v1', false) })
  await act(async () => host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(openAsk).not.toHaveBeenCalled(); expect(send).not.toHaveBeenCalled()
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('no longer available')
})

it('discards a previous account’s in-flight conversation lookup', async () => {
  get.mockResolvedValue({ data: fixture() })
  let finish!: (tabId: string) => void
  openAsk.mockImplementation(() => new Promise<string>(resolve => { finish = resolve }))
  const { host, render } = await mount()
  await act(async () => click(host, 'Open Ask'))
  auth.id = 'finance-a'; get.mockResolvedValue({ data: fixture('v1', false) })
  await render()
  await act(async () => finish('previous-owner-chat'))
  expect(host.querySelector('[data-native-tab]')).toBeNull()
  expect(send).not.toHaveBeenCalled()
})

it('shows the empty introduction for an owner without removing version and Ask controls', async () => {
  get.mockResolvedValue({ data: { ...fixture(), can_draft: true, runs: [] } })
  const { host } = await mount(canCreate => <section aria-label="Dashboard introduction">Common dashboard examples{canCreate && <button>Create dashboard</button>}</section>)
  expect(host.querySelector('[aria-label="Dashboard introduction"]')).not.toBeNull()
  expect(host.textContent).toContain('Create dashboard')
  expect(host.querySelector('select')).not.toBeNull()
  expect([...host.querySelectorAll('option')].some(option => option.value === 'draft')).toBe(true)
  expect(host.textContent).toContain('No recorded runs for this version.')
  expect(host.textContent).toContain('Open Ask')
  expect(openAsk).not.toHaveBeenCalled()
  expect(send).not.toHaveBeenCalled()
})

it('uses the server capability to withhold authoring from a reader’s empty dashboard', async () => {
  auth.id = 'reader'
  get.mockResolvedValue({ data: { ...fixture('v1', false), can_draft: false, runs: [] } })
  const { host } = await mount(canCreate => <section>Common dashboard examples{canCreate && <button>Create dashboard</button>}</section>)
  expect(host.textContent).toContain('Common dashboard examples')
  expect(host.textContent).not.toContain('Create dashboard')
  expect([...host.querySelectorAll('option')].some(option => option.value === 'draft')).toBe(false)
  expect(host.textContent).toContain('Ask is unavailable for this Flow.')
})

it('keeps recorded runs primary and does not treat a failed fetch as an empty dashboard', async () => {
  const empty = vi.fn(() => <div>Common dashboard examples</div>)
  get.mockResolvedValue({ data: fixture() })
  const { host, render } = await mount(empty)
  expect(host.textContent).toContain('Missing invoice date')
  expect(empty).not.toHaveBeenCalled()
  get.mockRejectedValue(new Error('Access denied'))
  await render(1)
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Access denied')
  expect(empty).not.toHaveBeenCalled()
})

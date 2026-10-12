// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { ReportEmbedProvider, type ReportDataApi } from './reportEmbedContext'
import { ReportQuestionFrame } from './ReportQuestionFrame'

vi.mock('./reportHostRuntime', () => ({
  withReportBootstrap: (html: string) => html,
  applyReportTheme: vi.fn(),
  installReportHost: vi.fn(),
}))
const { addToast } = vi.hoisted(() => ({ addToast: vi.fn() }))
vi.mock('../../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast }) } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => Promise<void>)[] = []
afterEach(async () => { for (const cleanup of cleanups.splice(0)) await cleanup(); vi.clearAllMocks() })

async function mount(sendChatMessage: ReportDataApi['sendChatMessage'], allowQuestions = true) {
  const api: ReportDataApi = {
    workspacePath: 'Workflow/demo', sendChatMessage,
    query: vi.fn(), get: vi.fn(), getText: vi.fn(), getHtml: vi.fn(), renderMarkdown: vi.fn(),
    fileUrl: vi.fn(), mediaUrl: vi.fn(), openFile: vi.fn(), updateField: vi.fn(), updateFields: vi.fn(), run: vi.fn(),
  }
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  let mounted = true
  const unmount = async () => { if (mounted) { mounted = false; await act(async () => root.unmount()); host.remove() } }
  cleanups.push(unmount)
  await act(async () => root.render(<ReportEmbedProvider value={{ data: api }}>
    <ReportQuestionFrame html="<p>Revenue</p>" title="Sales" documentPath="db/reports/sales.html" allowQuestions={allowQuestions} className="" />
  </ReportEmbedProvider>))
  const frame = host.querySelector('iframe')!
  const doc = frame.contentDocument!
  doc.body.innerHTML = '<table><thead><tr><th>Account</th><th>Revenue</th></tr></thead><tbody><tr><td>Example account</td><td id="revenue"><span>42</span></td></tr></tbody></table><p id="note">Revenue is unchanged</p><input type="password" value="private">'
  await act(async () => frame.dispatchEvent(new Event('load')))
  return { frame, doc, unmount }
}

function click(label: string) {
  const button = Array.from(document.querySelectorAll('button')).find(button => button.textContent?.trim() === label)
  if (!button) throw new Error(`Missing ${label}`)
  button.click()
}
async function contextMenu(target: Element, options: MouseEventInit = {}, trusted = true) {
  const event = new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 30, clientY: 20, ...options })
  // The DOM emulator has no hardware input; real browsers set this on native input.
  if (trusted) Object.defineProperty(event, 'isTrusted', { value: true })
  await act(async () => target.dispatchEvent(event))
  return event
}

it('asks about the clicked number with its original label/source context, sends once and permits a failed enqueue retry', async () => {
  const send = vi.fn().mockRejectedValueOnce(new Error('Chat unavailable')).mockResolvedValue({ status: 'queued', tabId: 'project-chat', reused: true, queuedBehindRunningTurn: true })
  const { frame, doc } = await mount(send)
  const event = await contextMenu(doc.querySelector('#revenue span')!)
  expect(event.defaultPrevented).toBe(true)
  expect(send).not.toHaveBeenCalled()
  await act(async () => click('Ask agent about this'))
  expect(document.querySelector('dialog')?.open).toBe(true)
  expect(document.querySelector('blockquote')?.textContent).toBe('42')
  expect(document.querySelector('textarea')?.value).toBe('Where did this come from?')
  // The page can refresh while the user writes; the question still refers to 42.
  doc.querySelector('#revenue span')!.textContent = '99'
  const submit = () => document.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  await act(async () => { submit(); submit() })
  expect(send).toHaveBeenCalledTimes(1)
  expect(document.querySelector('[role="alert"]')?.textContent).toBe('Chat unavailable')
  const message = send.mock.calls[0][0]
  expect(message).toContain('Where did this come from?')
  expect(message).toContain('"text": "42"')
  expect(message).toContain('Example account')
  expect(message).toContain('"column": "Revenue"')
  expect(message).toContain('db/reports/sales.html')
  expect(message).toContain('Workflow/demo')
  expect(message).not.toContain('"text": "99"')
  expect(message.length).toBeLessThan(12000)
  await act(async () => submit())
  expect(send).toHaveBeenCalledTimes(2)
  expect(send.mock.calls[1]).toEqual(send.mock.calls[0])
  expect(document.querySelector('dialog')).toBeNull()
  expect(addToast).toHaveBeenCalledWith('Question queued behind the current agent turn.', 'info')
  expect(document.querySelector('iframe')).toBe(frame)
})

it('captures a text selection, cancels without sending and preserves native menus and document cleanup', async () => {
  const send = vi.fn()
  const { doc, unmount } = await mount(send)
  const note = doc.querySelector('#note')!
  const range = doc.createRange()
  range.setStart(note.firstChild!, 0)
  range.setEnd(note.firstChild!, 7)
  doc.getSelection()!.addRange(range)
  await contextMenu(note)
  await act(async () => click('Ask agent about this'))
  expect(document.querySelector('blockquote')?.textContent).toBe('Revenue')
  await act(async () => click("Why hasn't this changed?"))
  expect(document.querySelector('textarea')?.value).toBe("Why hasn't this changed?")
  await act(async () => click('Cancel'))
  expect(send).not.toHaveBeenCalled()
  expect((await contextMenu(note, { shiftKey: true })).defaultPrevented).toBe(false)
  expect((await contextMenu(note, {}, false)).defaultPrevented).toBe(false)
  expect((await contextMenu(doc.querySelector('input')!)).defaultPrevented).toBe(false)
  const touch = new PointerEvent('pointerdown', { bubbles: true, pointerType: 'touch' })
  Object.defineProperty(touch, 'isTrusted', { value: true })
  doc.querySelector('#note')!.dispatchEvent(touch)
  expect((await contextMenu(note)).defaultPrevented).toBe(false)
  await unmount()
  expect((await contextMenu(note)).defaultPrevented).toBe(false)
  const standalone = await mount(send, false)
  expect((await contextMenu(standalone.doc.querySelector('#revenue span')!)).defaultPrevented).toBe(false)
  expect(document.querySelector('[role="menu"]')).toBeNull()
  expect(send).not.toHaveBeenCalled()
})

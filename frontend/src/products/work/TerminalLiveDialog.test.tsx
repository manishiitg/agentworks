// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { TerminalLiveDialog } from './TerminalLiveDialog'
import { teamRequest } from './teamApi'

vi.mock('./teamApi', () => ({ teamRequest: vi.fn(), teamFailure: (error: Error) => ({ message: error.message }) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-1030: a current server refusal must remove a previously shown native
// conversation; closing the viewer must stop further reads.
it('drops terminal text when Live access is revoked and stops reading on close', async () => {
  vi.useFakeTimers()
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  vi.mocked(teamRequest).mockResolvedValueOnce({ messages: [{ Role: 'assistant', Parts: [{ Text: 'Native private sentinel' }] }] }).mockRejectedValue(new Error('This conversation is private.'))
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  let mounted = true
  try {
    await act(async () => root.render(<TerminalLiveDialog target={{ project: 'example' }} agent={{ id: 'terminal', name: 'Helper' }} close={() => {}} />))
    expect(document.body.textContent).toContain('Native private sentinel')
    await act(async () => vi.advanceTimersByTimeAsync(5000))
    expect(document.body.textContent).not.toContain('Native private sentinel')
    expect(document.querySelector('[role="alert"]')?.textContent).toBe('This conversation is private.')
    await act(async () => root.unmount())
    mounted = false
    const calls = vi.mocked(teamRequest).mock.calls.length
    await act(async () => vi.advanceTimersByTimeAsync(10000))
    expect(vi.mocked(teamRequest).mock.calls.length).toBe(calls)
  } finally {
    if (mounted) await act(async () => root.unmount())
    host.remove()
    vi.useRealTimers()
  }
})

// The owner uses the existing bridge adapter, not a terminal input API. Their
// ask box is absent from the teammate Live viewer.
it('sends an owner question through the original AgentBridge inbox only', async () => {
  vi.useFakeTimers()
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  vi.mocked(teamRequest).mockReset().mockImplementation(async (_target, path, _method, body) => {
    if (path?.endsWith('/live')) return { messages: [] } as never
    if ((body as {message?:string})?.message) return { inbox_id: 'exact-inbox' } as never
    return { messages: [{message_id:'answer',message:'Bridge-only answer'}] } as never
  })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<TerminalLiveDialog target={{project:'example'}} agent={{id:'native',name:'Helper'}} owner close={() => {}} />))
    const input = document.querySelector<HTMLTextAreaElement>('textarea')!
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!
      setter.call(input, 'A native agent question')
      input.dispatchEvent(new Event('input', {bubbles:true}))
      input.dispatchEvent(new Event('change', {bubbles:true}))
    })
    await act(async () => document.querySelector('form')!.dispatchEvent(new Event('submit', {bubbles:true,cancelable:true})))
    const sent = vi.mocked(teamRequest).mock.calls.find(call => (call[3] as {message?:string})?.message)
    expect(sent?.[1]).toBe('/terminal/native/ask')
    expect(sent?.[2]).toBe('POST')
    expect(sent?.[3]).toMatchObject({message:'A native agent question'})
    expect(document.body.textContent).toContain('Bridge-only answer')
    await act(async () => root.render(<TerminalLiveDialog target={{project:'example'}} agent={{id:'native',name:'Helper'}} close={() => {}} />))
    expect(document.querySelector('textarea')).toBeNull()
  } finally {
    await act(async () => root.unmount()); host.remove(); vi.useRealTimers()
  }
})

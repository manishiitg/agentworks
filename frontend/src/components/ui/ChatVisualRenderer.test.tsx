// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import ChatVisualRenderer from './ChatVisualRenderer'
import { ChatVisualRepairContext, type FixChatVisual } from './ChatElement'

const host = document.createElement('div')
const root = createRoot(host)
afterEach(() => act(() => root.render(null)))

const broken = [
  '{"op":"add","path":"/root","value":"visual"}',
  '{"op":"add","path":"/elements/visual","value":{"type":"sequence","props":{"title":"Task handoff","participants":[],"messages":[]},"children":[]}}',
  '{"op":"add","path":"/elements/visual/props/participants/-","value":{"id":"lead","label":"Lead"}}',
  '{"op":"add","path":"/elements/visual/props/messages/-","value":{"from":"lead","to":"lead","label":"Note"}}',
].join('\n')
const render = (source: string, send?: FixChatVisual, stream?: 'progressive') => root.render(
  <ChatVisualRepairContext.Provider value={send}>
    <ChatVisualRenderer source={source} stream={stream} renderMarkdown={text => text} />
  </ChatVisualRepairContext.Provider>,
)

it('sends only the broken block on click, suppresses a repeat click, and lets the person retry a refused send', async () => {
  let finish!: (ok: boolean) => void
  const send = vi.fn<FixChatVisual>()
    .mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    .mockResolvedValueOnce(true)
  await act(async () => render(broken, send))
  expect(send).not.toHaveBeenCalled()
  await act(async () => host.querySelector('summary')!.click())
  expect(send).not.toHaveBeenCalled()
  const button = host.querySelector('button')!
  await act(async () => { button.click(); button.click() })
  expect(send).toHaveBeenCalledTimes(1)
  expect(send).toHaveBeenCalledWith(broken)
  expect(button.disabled).toBe(true)
  await act(async () => finish(false))
  expect(button.disabled).toBe(false)
  expect(host.querySelector('[role=status]')?.textContent).toContain('Try again')
  await act(async () => button.click())
  expect(send).toHaveBeenCalledTimes(2)
  expect(button.textContent).toBe('Request sent')
  expect(host.querySelector('pre')?.textContent).toBe(broken)
})

it('offers no repair for a pending or valid visual, or outside a chat that can send', async () => {
  const send = vi.fn<FixChatVisual>()
  await act(async () => render(broken, send, 'progressive'))
  expect(host.querySelector('[data-chat-element-pending]')).not.toBeNull()
  expect(host.querySelector('button')).toBeNull()
  await act(async () => render(broken))
  expect(host.querySelector('summary')?.textContent).toBe('View source')
  expect(host.querySelector('button')).toBeNull()
  await act(async () => render(broken.replace('"label":"Lead"', '"title":"Lead"'), send))
  expect(host.querySelector('[data-chat-element-error]')).toBeNull()
  expect(send).not.toHaveBeenCalled()
})

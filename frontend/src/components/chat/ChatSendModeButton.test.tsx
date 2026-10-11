// @vitest-environment happy-dom
import React, { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ChatSendModeButton, type ChatDeliveryMode } from './ChatSendModeButton'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('lets a keyboard user select Queue next, send with that intent, and retain it when idle', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const send = vi.fn()
  function Composer({ busy }: { busy: boolean }) {
    const [mode, setMode] = useState<ChatDeliveryMode>('steer')
    return <ChatSendModeButton mode={mode} busy={busy} disabled={false} onModeChange={setMode} onSend={() => send(mode)} />
  }
  const originalScroll = HTMLElement.prototype.scrollIntoView
  HTMLElement.prototype.scrollIntoView = vi.fn()
  try {
    await act(async () => root.render(<Composer busy />))
    expect(host.querySelector('[data-testid="chat-submit-button"]')?.textContent).toContain('Steer')
    await act(async () => {
      const trigger = host.querySelector<HTMLButtonElement>('[role="combobox"]')!
      trigger.focus()
      trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    })
    const queue = [...document.querySelectorAll<HTMLElement>('[role="option"]')].find(option => option.textContent?.includes('Queue next'))!
    expect(queue).toBeTruthy()
    await act(async () => {
      queue.focus()
      queue.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    expect(host.querySelector('[data-testid="chat-submit-button"]')?.textContent).toContain('Queue next')
    await act(async () => host.querySelector<HTMLButtonElement>('[data-testid="chat-submit-button"]')!.click())
    expect(send).toHaveBeenLastCalledWith('queue')
    await act(async () => root.render(<Composer busy={false} />))
    expect(host.querySelector('[data-testid="chat-submit-button"]')?.textContent).toContain('Send')
    await act(async () => host.querySelector<HTMLButtonElement>('[data-testid="chat-submit-button"]')!.click())
    expect(send).toHaveBeenLastCalledWith('queue')
  } finally {
    await act(async () => root.unmount())
    HTMLElement.prototype.scrollIntoView = originalScroll
    host.remove()
  }
})

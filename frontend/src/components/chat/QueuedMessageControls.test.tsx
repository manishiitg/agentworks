// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { QueuedWaitingAge, QueuedInboxStatus, QueuedMessageNotice } from './QueuedMessageControls'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('formats only server-provided waiting age and updates as time passes', async () => {
  vi.useFakeTimers()
  const host = document.createElement('div')
  const root = createRoot(host)
  vi.setSystemTime(new Date('2026-01-01T12:40:00Z'))
  try {
    await act(async () => root.render(<QueuedWaitingAge />))
    expect(host.textContent).toBe('')
    await act(async () => root.render(<QueuedWaitingAge since="2026-01-01T12:00:00Z" />))
    expect(host.textContent).toBe('Waiting 40 min')
    await act(async () => vi.advanceTimersByTime(60_000))
    expect(host.textContent).toBe('Waiting 41 min')
  } finally { await act(async () => root.unmount()); vi.useRealTimers() }
})

// Native uncertainty is a durable server verdict, not a browser retry decision.
it('shows an unconfirmed incoming steer without queue controls or a spinner', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    const metadata = { source: 'agent_message', delivery_status: 'unconfirmed' }
    await act(async () => root.render(<><QueuedInboxStatus metadata={metadata} /><QueuedMessageNotice metadata={metadata} /></>))
    expect(host.querySelector('[aria-label="Delivery unconfirmed"]')).not.toBeNull()
    expect(host.textContent).toContain('will not be sent again automatically')
    expect(host.querySelector('button')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})

// A durable receipt outranks an older queue poll; never infer delivery from age.
it('keeps the restart reason on the clock and clears stale queue presentation on delivery', async () => {
  const { useQueuedMessageControlsStore } = await import('../../stores/useQueuedMessageControlsStore')
  const { DeliveryTick } = await import('../events/system/DeliveryTick')
  const host = document.createElement('div'), root = createRoot(host)
  const pending = { client_message_id: 'm', message_id: 'queue-m', content: 'Hello', can_manage: true, refreshing: true }
  const actQueue = vi.fn()
  useQueuedMessageControlsStore.getState().set('s', { messages: { m: pending }, busyMessageId: null, canEdit: true, editable: true, act: actQueue })
  const render = (receipt: Record<string, unknown>) => <><DeliveryTick sessionId="s" metadata={receipt} /><QueuedInboxStatus sessionId="s" metadata={receipt} /><QueuedMessageNotice sessionId="s" metadata={receipt} /></>
  try {
    await act(async () => root.render(render({ client_message_id: 'm', delivery_status: 'queued_for_turn' })))
    expect(host.textContent).not.toContain('next turn')
    expect(host.querySelector('[data-testid="delivery-tick"]')?.getAttribute('title')).toContain('next turn')
    for (const receipt of [{ delivery_status: 'sent_to_cli' }, { confirmation: 'confirmed' }, { delivery_status: 'unconfirmed', confirmation: 'confirmed' }]) {
      await act(async () => root.render(render({ client_message_id: 'm', ...receipt })))
      expect(host.querySelector('p')).toBeNull()
      expect(host.querySelector('[title*="restarts"]')).toBeNull()
    }
    await act(async () => {
      useQueuedMessageControlsStore.getState().set('s', { messages: { m: { ...pending, delivery_error: 'Not confirmed by the agent.' } }, busyMessageId: null, canEdit: true, editable: true, act: actQueue })
      root.render(render({ client_message_id: 'm', delivery_status: 'unconfirmed' }))
    })
    expect(host.textContent).toContain('Delivery unconfirmed')
    expect(actQueue).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()); useQueuedMessageControlsStore.getState().set('s', undefined) }
})

// Pending setup is not permission to hide the server-admitted Steer action.
it('allows steering during a pending setup refresh when the server admits it', async () => {
 const { QueuedMessageControls } = await import('./QueuedMessageControls')
 const { useQueuedMessageControlsStore } = await import('../../stores/useQueuedMessageControlsStore')
 const host=document.createElement('div'),root=createRoot(host),actQueue=vi.fn()
 const message={client_message_id:'m',message_id:'queued-m',content:'Continue',can_manage:true,can_steer:true,refreshing:true}
 useQueuedMessageControlsStore.getState().set('refresh-chat',{messages:{m:message},busyMessageId:null,canEdit:true,editable:true,act:actQueue})
 try {
  await act(async()=>root.render(<QueuedMessageControls sessionId="refresh-chat" metadata={{client_message_id:'m'}}/>))
  const steer=Array.from(host.querySelectorAll('button')).find(button=>button.textContent==='Steer now')
  expect(steer).toBeDefined()
  await act(async()=>steer!.click())
  expect(actQueue).toHaveBeenCalledWith(message,'steer')
 } finally {await act(async()=>root.unmount());useQueuedMessageControlsStore.getState().set('refresh-chat',undefined)}
})

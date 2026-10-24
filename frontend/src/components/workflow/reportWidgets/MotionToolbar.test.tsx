// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { MotionToolbar } from '../../../../dashboard-runtime/src/MotionToolbar'
import { reportMotionCapture, reportMotionFor } from './reportMotion'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('controls and follows the real clock, capture seeks and independent subscribers', async () => {
  const frame = document.createElement('iframe'); document.body.append(frame)
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const target = frame.contentDocument!
  const animation = vi.spyOn(target.defaultView!, 'requestAnimationFrame').mockReturnValue(1)
  const error = vi.fn()
  const clock = reportMotionFor(target, error).create({ id: 'demo', duration: 12, render: () => {} })
  const throwing = clock.subscribe(() => { throw Error('authored listener failed') })
  const listener = vi.fn()
  const unsubscribe = clock.subscribe(listener)
  const button = (label: string) => [...host.querySelectorAll('button')].find(node => node.textContent === label)!
  try {
    await act(async () => root.render(<MotionToolbar clock={clock} positionLabel="Oct 14" detail="Day 14 of 23"/>))
    expect(host.querySelector('[role="group"]')?.getAttribute('data-motion-placement')).toBe('floating')
    await act(async () => button('Play').click())
    expect(button('Pause')).toBeTruthy()
    await act(async () => button('Pause').click())
    const speed = host.querySelector('select')!
    await act(async () => { speed.value = '2'; speed.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(clock.getState().speed).toBe(2)
    await act(async () => { reportMotionCapture(target, 12) })
    expect(host.querySelector('input')?.value).toBe('12')
    expect(host.querySelector('[aria-label="Elapsed playback time"]')?.textContent).toBe('12.0 / 12s')
    expect(host.querySelector('input')?.getAttribute('aria-valuetext')).toContain('Oct 14')
    expect(listener).toHaveBeenLastCalledWith(expect.objectContaining({ time: 12, playing: false }))
    await act(async () => button('Replay').click())
    expect(clock.getState()).toMatchObject({ time: 0, playing: true, speed: 2 })
    await act(async () => root.render(<MotionToolbar clock={clock} placement="inline" showReplay={false} showSpeed={false} showTime={false}/>))
    expect(button('Replay')).toBeUndefined()
    expect(host.querySelector('select')).toBeNull()
    expect(host.querySelector('[role="group"]')?.getAttribute('data-motion-placement')).toBe('inline')
    unsubscribe(); unsubscribe(); throwing()
    const calls = listener.mock.calls.length
    await act(async () => clock.seek(3))
    expect(listener).toHaveBeenCalledTimes(calls)
    expect(error).toHaveBeenCalledWith(expect.objectContaining({ message: 'authored listener failed' }))
  } finally { await act(async () => root.unmount()); clock.destroy(); animation.mockRestore(); host.remove(); frame.remove() }
})

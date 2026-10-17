// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { usePointerDrag } from './usePointerDrag'

const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.unstubAllGlobals() })

function mountDrag(axis: 'x' | 'y' = 'x') {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const onMove = vi.fn(), onEnd = vi.fn()
  function Divider() {
    const { start } = usePointerDrag()
    return <button onPointerDown={event => start(event, { onMove, onEnd }, axis)}>Resize</button>
  }
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  act(() => root.render(<Divider />))
  const target = host.querySelector('button')!
  const captures = new Set<number>()
  target.setPointerCapture = vi.fn(id => { captures.add(id) })
  target.hasPointerCapture = vi.fn(id => captures.has(id))
  target.releasePointerCapture = vi.fn(id => { captures.delete(id) })
  let unmounted = false
  const unmount = () => { if (!unmounted) { act(() => root.unmount()); host.remove(); unmounted = true } }
  cleanups.push(unmount)
  const pointer = (type: string, init: PointerEventInit = {}) => {
    act(() => { target.dispatchEvent(new PointerEvent(type, { bubbles: true, pointerId: 1, isPrimary: true, button: 0, buttons: 1, clientX: 300, ...init })) })
  }
  return { target, pointer, onMove, onEnd, unmount }
}

it('resizes only while pressed, then removes listeners after React clears currentTarget', () => {
  const { pointer, onMove, onEnd, target } = mountDrag()
  pointer('pointermove', { buttons: 0 })
  expect(onMove).not.toHaveBeenCalled()
  pointer('pointerdown')
  pointer('pointermove', { clientX: 450 })
  expect(onMove).toHaveBeenLastCalledWith(450)
  pointer('pointerup', { buttons: 0 })
  pointer('pointermove', { buttons: 0, clientX: 900 })
  expect(onMove).toHaveBeenCalledTimes(1)
  expect(onEnd).toHaveBeenCalledTimes(1)
  expect(target.hasPointerCapture(1)).toBe(false)
  pointer('pointerdown')
  pointer('pointermove', { clientX: 500 })
  pointer('pointerup', { buttons: 0 })
  expect(onMove).toHaveBeenCalledTimes(2)
  expect(onEnd).toHaveBeenCalledTimes(2)
})

it('tracks the vertical position for shortcut reordering and stops after release', () => {
  const { pointer, onMove, onEnd } = mountDrag('y')
  pointer('pointerdown')
  pointer('pointermove', { clientX: 450, clientY: 210 })
  expect(onMove).toHaveBeenLastCalledWith(210)
  pointer('pointerup', { buttons: 0 })
  pointer('pointermove', { clientY: 500 })
  expect(onMove).toHaveBeenCalledTimes(1)
  expect(onEnd).toHaveBeenCalledTimes(1)
})

it.each(['pointercancel', 'lostpointercapture', 'blur', 'released-buttons', 'unmount'])('stops interrupted dragging on %s', reason => {
  const { pointer, onMove, onEnd, unmount } = mountDrag()
  pointer('pointerdown')
  pointer('pointermove')
  if (reason === 'blur') act(() => window.dispatchEvent(new Event('blur')))
  else if (reason === 'unmount') unmount()
  else if (reason === 'released-buttons') pointer('pointermove', { buttons: 0 })
  else pointer(reason)
  pointer('pointermove', { clientX: 700 })
  pointer('pointerup', { buttons: 0 })
  expect(onMove).toHaveBeenCalledTimes(1)
  expect(onEnd).toHaveBeenCalledTimes(reason === 'unmount' ? 0 : 1)
})

it('ignores secondary buttons and other pointers', () => {
  const { pointer, onMove, onEnd } = mountDrag()
  pointer('pointerdown', { button: 2, buttons: 2 })
  pointer('pointermove')
  pointer('pointerdown', { isPrimary: false })
  pointer('pointermove')
  expect(onMove).not.toHaveBeenCalled()
  pointer('pointerdown')
  pointer('pointermove', { pointerId: 2 })
  pointer('pointerup', { pointerId: 2, buttons: 0 })
  expect(onEnd).not.toHaveBeenCalled()
  pointer('pointermove')
  expect(onMove).toHaveBeenCalledTimes(1)
  pointer('pointerup', { buttons: 0 })
  expect(onEnd).toHaveBeenCalledTimes(1)
})

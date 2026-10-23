// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { usePulseToggle } from './usePulseToggle'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('enables reviews without goal setup and reports save failures', async () => {
 const update=vi.fn().mockResolvedValue(undefined); const notify=vi.fn()
 let state!: ReturnType<typeof usePulseToggle>
 function Probe() { state=usePulseToggle('Workflow/example',false,update,notify); return null }
 const host=document.createElement('div'); const root=createRoot(host)
 try {
  await act(async()=>root.render(<Probe />))
  await act(async()=>state.toggleMonitor())
  expect(update).toHaveBeenCalledExactlyOnceWith('Workflow/example',{pulse_enabled:true})
  expect(notify).toHaveBeenLastCalledWith('Autopilot turned on','success')
  expect(state.monitorSaving).toBe(false)
  update.mockRejectedValueOnce(new Error('Owner access required'))
  await act(async()=>state.toggleMonitor())
  expect(notify).toHaveBeenLastCalledWith('Owner access required','error')
  expect(state.monitorSaving).toBe(false)
 } finally { act(()=>root.unmount()) }
})

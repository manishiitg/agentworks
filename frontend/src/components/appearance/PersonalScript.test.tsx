// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { PersonalScript } from './PersonalScript'
const fixture=vi.hoisted(()=>({destroy:vi.fn(),mount:vi.fn()}))
vi.mock('./scriptSandbox',()=>({mountScriptSandbox:fixture.mount}))
afterEach(()=>{vi.restoreAllMocks();vi.unstubAllGlobals();fixture.mount.mockReset();fixture.destroy.mockReset()})
it('terminates scripts when hidden or removed and reprojects only palette and activity',async()=>{
 const media=new EventTarget() as EventTarget&{matches:boolean};media.matches=false
 vi.stubGlobal('matchMedia',()=>media)
 vi.spyOn(document,'hidden','get').mockReturnValue(false)
 fixture.mount.mockImplementation((_host,_script,_inputs,status)=>{status('ready');return {destroy:fixture.destroy}})
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host)
 const script={code:'function render(){}',fps:20}
 await act(async()=>root.render(<PersonalScript script={script} colors={{accent:'#123456'}} working/>))
 expect(document.documentElement.hasAttribute('data-appearance-script')).toBe(true)
 expect(fixture.mount.mock.calls[0][2]()).toEqual({colors:{accent:'#123456'},working:true})
 vi.spyOn(document,'hidden','get').mockReturnValue(true)
 await act(async()=>document.dispatchEvent(new Event('visibilitychange')))
 expect(fixture.destroy).toHaveBeenCalledTimes(1)
 expect(document.documentElement.hasAttribute('data-appearance-script')).toBe(false)
 vi.spyOn(document,'hidden','get').mockReturnValue(false)
 await act(async()=>document.dispatchEvent(new Event('visibilitychange')))
 expect(fixture.mount).toHaveBeenCalledTimes(2)
 media.matches=true
 await act(async()=>media.dispatchEvent(new Event('change')))
 expect(fixture.destroy).toHaveBeenCalledTimes(2)
 await act(async()=>root.unmount());host.remove()
 expect(fixture.destroy).toHaveBeenCalledTimes(3)
 expect(document.documentElement.hasAttribute('data-appearance-script')).toBe(false)
})

// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { mountScriptSandbox, scriptSandboxDocument } from './scriptSandbox'

afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks();vi.unstubAllGlobals()})
// #2844: source never enters srcdoc, privileges are not inherited, and a stuck
// Worker is replaceable without letting user JS block the app's renderer thread.
it('isolates code on a private port, whitelists inputs and kills stalled frames',()=>{
 vi.useFakeTimers()
 vi.spyOn(document,'hidden','get').mockReturnValue(false)
 vi.stubGlobal('matchMedia',()=>({matches:false}))
 const channels:FakeChannel[]=[]
 class FakeChannel {
  port1={onmessage:undefined as undefined|((e:{data:unknown})=>void),postMessage:vi.fn(),start:vi.fn(),close:vi.fn()}
  port2={close:vi.fn()}
  constructor(){channels.push(this)}
 }
 vi.stubGlobal('MessageChannel',FakeChannel)
 const host=document.createElement('div');document.body.append(host)
 const state=vi.fn(),code='function render(){const literal="</script><script>bad()</script>";}'
 const engine=mountScriptSandbox(host,{code,fps:30},()=>({colors:{accent:'#abcdef',invalid:'url(secret)'},working:true}),state)
 const iframe=host.querySelector('iframe')!
 expect(iframe.getAttribute('sandbox')).toBe('allow-scripts')
 expect(iframe.tabIndex).toBe(-1)
 expect(iframe.srcdoc).not.toContain(code)
 expect(iframe.srcdoc).toContain("connect-src 'none'")
 expect(iframe.srcdoc).toContain("default-src 'none'")
 expect(iframe.srcdoc).not.toContain('allow-same-origin')
 const p=channels[0].port1
 p.onmessage!({data:{type:'connected'}})
 expect(p.postMessage).toHaveBeenCalledWith({type:'start',code})
 p.onmessage!({data:{type:'ready'}})
 const draw=p.postMessage.mock.calls.at(-1)![0]
 expect(draw).toMatchObject({type:'draw',colors:{accent:'#abcdef'},working:true})
 expect(draw.width).toBeLessThanOrEqual(1280);expect(draw.height).toBeLessThanOrEqual(720)
 p.onmessage!({data:{type:'frame',id:999}})
 expect(state).not.toHaveBeenCalled()
 vi.advanceTimersByTime(1501)
 expect(state).toHaveBeenCalledWith('stopped')
 expect(p.postMessage).toHaveBeenCalledWith({type:'stop'})
 engine.destroy();expect(host.querySelector('iframe')).toBeNull();expect(p.close).toHaveBeenCalled();host.remove()
 expect(scriptSandboxDocument('example')).not.toContain('unsafe-eval')
})

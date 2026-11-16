// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { mountAppearanceShader } from './shaderEngine'
import { defaultAppearance, validateTheme } from '../../stores/appearanceTheme'
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks();vi.unstubAllGlobals()})

// #2770: a slow personal shader is decoration, never a reason to keep spending
// GPU time; hidden/reduced-motion/unmount must stop scheduling and release GL.
it('bounds the backing buffer, sheds slow GPU work and cleans up pending callbacks',()=>{
 vi.useFakeTimers()
 let hidden=false
 vi.spyOn(document,'hidden','get').mockImplementation(()=>hidden)
 const motion={matches:false,addEventListener:vi.fn(),removeEventListener:vi.fn()}
 vi.stubGlobal('matchMedia',()=>motion)
 vi.stubGlobal('innerWidth',4000);vi.stubGlobal('innerHeight',2000)
 vi.stubGlobal('requestAnimationFrame',(cb:FrameRequestCallback)=>setTimeout(()=>cb(performance.now()),1))
 vi.stubGlobal('cancelAnimationFrame',clearTimeout)
 const timer={TIME_ELAPSED_EXT:1,QUERY_RESULT_AVAILABLE_EXT:2,QUERY_RESULT_EXT:3,GPU_DISJOINT_EXT:4,createQueryEXT:vi.fn(()=>({})),beginQueryEXT:vi.fn(),endQueryEXT:vi.fn(),getQueryObjectEXT:vi.fn((_q:object,key:number)=>key===2?true:60e6),deleteQueryEXT:vi.fn()}
 const gl:any=new Proxy({getExtension:vi.fn((name:string)=>name==='EXT_disjoint_timer_query'?timer:null),getShaderParameter:()=>true,getProgramParameter:()=>true,getParameter:()=>false,createShader:()=>({}),createProgram:()=>({}),createBuffer:()=>({}),getUniformLocation:()=>({}),getAttribLocation:()=>0,drawArrays:vi.fn(),deleteBuffer:vi.fn(),deleteProgram:vi.fn(),deleteShader:vi.fn()}, {get:(target,key)=>key in target?(target as any)[key]:typeof key==='string'&&key===key.toUpperCase()?1:vi.fn()})
 const canvas=document.createElement('canvas');vi.spyOn(canvas,'getContext').mockReturnValue(gl)
 const status=vi.fn(),engine=mountAppearanceShader(canvas,{code:'vec3 theme(vec2 uv){return vec3(uv,0.);}',speed:1,intensity:1},status)!
 expect(canvas.width).toBeLessThanOrEqual(1280);expect(canvas.height).toBeLessThanOrEqual(720)
 hidden=true;document.dispatchEvent(new Event('visibilitychange'))
 const before=gl.drawArrays.mock.calls.length
 vi.advanceTimersByTime(1000);expect(gl.drawArrays).toHaveBeenCalledTimes(before)
 hidden=false;document.dispatchEvent(new Event('visibilitychange'))
 vi.advanceTimersByTime(5000)
 expect(status.mock.calls.some(([state])=>state==='throttled')).toBe(true)
 expect(status.mock.calls.some(([state])=>state==='stopped')).toBe(true)
 const stopped=gl.drawArrays.mock.calls.length
 vi.advanceTimersByTime(1000);expect(gl.drawArrays).toHaveBeenCalledTimes(stopped)
 engine.destroy();expect(gl.deleteProgram).toHaveBeenCalledOnce();expect(timer.deleteQueryEXT).toHaveBeenCalled()
 expect(vi.getTimerCount()).toBe(0)
 motion.matches=true
 const still=mountAppearanceShader(canvas,{code:'vec3 theme(vec2 uv){return vec3(uv,0.);}',speed:1,intensity:1},status)!
 const stillFrames=gl.drawArrays.mock.calls.length
 vi.advanceTimersByTime(3000);expect(gl.drawArrays).toHaveBeenCalledTimes(stillFrames)
 still.destroy();expect(vi.getTimerCount()).toBe(0)
 gl.getShaderParameter=()=>false
 expect(mountAppearanceShader(canvas,{code:'invalid GLSL',speed:1,intensity:1},status)).toBeNull()
 expect(status).toHaveBeenLastCalledWith('failed')
 expect(vi.getTimerCount()).toBe(0)
})

it('refuses executable decoration fields and invalid shader schema before projection',()=>{
 const base=defaultAppearance()
 expect(validateTheme({...base,background_shader:{code:'vec3 theme(vec2 uv){return vec3(uv,0.);}',speed:0,intensity:.5},decorations:[{id:'ship',children:[{id:'hull'}]}]}).decorations).toHaveLength(1)
 for(const patch of [{decorations:[{id:'ship',onClick:'code'}]},{decorations:[{id:'ship'},{id:'ship'}]},{decorations:[{id:'../file'}]},{background_shader:{code:'x'.repeat(16385),speed:1,intensity:1}},{background_shader:{code:'x',speed:3,intensity:1}}])expect(()=>validateTheme({...base,...patch})).toThrow()
})

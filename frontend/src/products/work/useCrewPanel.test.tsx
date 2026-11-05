// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
const mocks=vi.hoisted(()=>({get:vi.fn(),status:'live' as 'live'|'offline',statuses:new Set<(s:'live'|'offline')=>void>(),notice:()=>{}}))
vi.mock('../../services/api',()=>({default:{get:mocks.get}}))
vi.mock('../../services/liveFeed',()=>({liveFeed:{getStatus:()=>mocks.status,onStatus:(fn:(s:'live'|'offline')=>void)=>{mocks.statuses.add(fn);return()=>mocks.statuses.delete(fn)},subscribe:(_k:unknown,_w:unknown,fn:()=>void)=>{mocks.notice=fn;return()=>{}}}}))
import { useCrewPanel } from './useCrewPanel'
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
beforeEach(()=>{vi.useFakeTimers();mocks.get.mockReset();mocks.status='live';Object.defineProperty(document,'hidden',{configurable:true,value:false})})
afterEach(()=>vi.useRealTimers())
const panel=(project='example',busy=false)=>({project,workspace:`Crew/${project}`,busy,mac_elsewhere:false,sections:[],details:{}})

it('keeps data scoped during an account switch and removes it after refusal',async()=>{
 let finish:(v:unknown)=>void=()=>{}
 mocks.get.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve})).mockResolvedValue({data:panel('new')})
 let value:ReturnType<typeof useCrewPanel>
 function Probe({scope}:{scope:string}){value=useCrewPanel(scope,'example','Crew/example','chat');return null}
 const root=createRoot(document.createElement('div'))
 try {
  await act(async()=>root.render(<Probe scope="old"/>))
  await act(async()=>root.render(<Probe scope="new"/>))
  expect(value!.data?.project).toBe('new')
  await act(async()=>finish({data:panel('private-old')}))
  expect(value!.data?.project).toBe('new')
  mocks.get.mockRejectedValue(new Error('403'))
  await act(async()=>mocks.notice())
  expect(value!.data).toBeUndefined();expect(value!.error).toBeTruthy()
 }finally{await act(async()=>root.unmount())}
})

it('uses notices while live, faster fallback only while busy, and returns to notices after reconnect',async()=>{
 mocks.get.mockResolvedValue({data:panel()})
 function Probe(){useCrewPanel('account','example','Crew/example');return null}
 const root=createRoot(document.createElement('div'))
 try {
  await act(async()=>root.render(<Probe/>));expect(mocks.get).toHaveBeenCalledTimes(1)
  await act(async()=>vi.advanceTimersByTimeAsync(30_000));expect(mocks.get).toHaveBeenCalledTimes(1)
  await act(async()=>mocks.notice());expect(mocks.get).toHaveBeenCalledTimes(2)
  await act(async()=>{mocks.status='offline';mocks.statuses.forEach(fn=>fn('offline'))})
  mocks.get.mockResolvedValue({data:panel('example',true)})
  await act(async()=>vi.advanceTimersByTimeAsync(15_000));expect(mocks.get).toHaveBeenCalledTimes(3)
  await act(async()=>vi.advanceTimersByTimeAsync(2_000));expect(mocks.get).toHaveBeenCalledTimes(4)
  await act(async()=>{mocks.status='live';mocks.statuses.forEach(fn=>fn('live'));mocks.notice()})
  const before=mocks.get.mock.calls.length
  await act(async()=>vi.advanceTimersByTimeAsync(30_000));expect(mocks.get).toHaveBeenCalledTimes(before)
 }finally{await act(async()=>root.unmount())}
})

// PLAT-856-544: external Git edits have no server notice. Returning to the
// window/tab must refresh even when the stream stayed healthy.
it('refreshes on return after external edits without adding live polling',async()=>{
 mocks.get.mockResolvedValue({data:panel()})
 function Probe(){useCrewPanel('account','example','Crew/example');return null}
 const root=createRoot(document.createElement('div'))
 try {
  await act(async()=>root.render(<Probe/>))
  await act(async()=>window.dispatchEvent(new Event('focus')))
  expect(mocks.get).toHaveBeenCalledTimes(2)
  Object.defineProperty(document,'hidden',{configurable:true,value:true})
  await act(async()=>document.dispatchEvent(new Event('visibilitychange')))
  expect(mocks.get).toHaveBeenCalledTimes(2)
  await act(async()=>vi.advanceTimersByTimeAsync(1000))
  Object.defineProperty(document,'hidden',{configurable:true,value:false})
  await act(async()=>document.dispatchEvent(new Event('visibilitychange')))
  expect(mocks.get).toHaveBeenCalledTimes(3)
  await act(async()=>vi.advanceTimersByTimeAsync(30_000))
  expect(mocks.get).toHaveBeenCalledTimes(3)
 }finally{await act(async()=>root.unmount())}
})

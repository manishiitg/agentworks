// @vitest-environment happy-dom
import React,{act} from 'react'
import {createRoot} from 'react-dom/client'
import {expect,it,vi} from 'vitest'
import {TeamMonitorAgentRow} from './TeamMonitorAgentRow'
Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true})
// #2917: person selection must not prefix/truncate the person name into every agent row.
it('protects agent identity before the live line and keeps idle agents on one dimmed line',async()=>{
 const host=document.createElement('div'),root=createRoot(host),open=vi.fn()
 try {
  await act(async()=>root.render(<TeamMonitorAgentRow name="Builder (fork)" cli="codex" activity="running" line="A very long live line" team="Example" onOpen={open}/>))
  const row=host.querySelector('button')!
  expect(row.querySelector('[aria-label="Codex"] img')?.getAttribute('src')).toBe('/provider-icons/codex.png')
  expect(row.querySelector('.font-medium')?.textContent).toBe('Builder (fork)')
  expect(row.querySelector('.font-medium')?.classList.contains('shrink-0')).toBe(true)
  expect(row.querySelector('.flex-1')?.classList.contains('truncate')).toBe(true)
  expect(row.querySelectorAll('button')).toHaveLength(0)
  await act(async()=>row.click());expect(open).toHaveBeenCalledOnce()
  await act(async()=>root.render(<TeamMonitorAgentRow name="Main" cli="claude" activity="idle" team="Example" onOpen={open}/>))
  expect(host.querySelector('button')?.classList.contains('opacity-50')).toBe(true)
  expect(host.querySelector('[aria-label="idle"]')?.textContent).toBe('○')
  expect(host.textContent).toContain('Main')
 } finally {await act(async()=>root.unmount())}
})

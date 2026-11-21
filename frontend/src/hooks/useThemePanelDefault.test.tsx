// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { useThemePanelDefault } from './useThemePanelDefault'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// #2768: a theme must not re-close/re-open a panel the person explicitly chose.
it('uses the theme only until a manual, linked or restored panel choice in the same scope', async () => {
 const host=document.createElement('div'), root=createRoot(host)
 function Harness({start,scope}:{start?:boolean;scope:string}) {
  const [open,choose]=useThemePanelDefault(start,scope)
  return <button aria-expanded={open} onClick={()=>choose(!open)}>Panel</button>
 }
 const render=(start:boolean|undefined,scope='own/project')=>act(async()=>root.render(<Harness start={start} scope={scope}/>))
 try {
  await render(false)
  expect(host.querySelector('button')!.getAttribute('aria-expanded')).toBe('false')
  await act(async()=>host.querySelector('button')!.click())
  await render(true); await render(false)
  expect(host.querySelector('button')!.getAttribute('aria-expanded')).toBe('true')
  await act(async()=>host.querySelector('button')!.click())
  await render(true)
  expect(host.querySelector('button')!.getAttribute('aria-expanded')).toBe('false')
  await render(undefined,'other/project')
  expect(host.querySelector('button')!.getAttribute('aria-expanded')).toBe('true')
  await render(false,'other/project')
  expect(host.querySelector('button')!.getAttribute('aria-expanded')).toBe('false')
 } finally { await act(async()=>root.unmount()) }
})

// @vitest-environment happy-dom
import {expect,it,vi} from 'vitest'
import {revealSidebarTarget,sidebarProjectShortcuts} from './sidebarProjectOrder'
it('keeps admitted pins in saved order, without moving them when selection changes', () => {
 const rows = [{ id: 'recent', selected: true }, { id: 'a', selected: false }, { id: 'b', selected: false }]
 const order = () => sidebarProjectShortcuts(rows, row => row.id, ['b', 'gone', 'a'], ['recent'])
 expect(order()).toEqual({ pinnedRows: [rows[2], rows[1]], recentRows: [] })
 rows[0].selected = false
 rows[1].selected = true
 expect(order()).toEqual({ pinnedRows: [rows[2], rows[1]], recentRows: [] })
})
it('falls back to at most five admitted recent projects, in visit order', () => {
 const rows = ['a', 'b', 'c', 'd', 'e', 'f']
 const order = (recent: string[]) => sidebarProjectShortcuts(rows, row => row, ['gone'], recent)
 expect(order(['gone', 'c', 'b', 'a', 'f', 'e', 'd'])).toEqual({ pinnedRows: [], recentRows: ['c', 'b', 'a', 'f', 'e'] })
 expect(order(['gone'])).toEqual({ pinnedRows: [], recentRows: [] })
})
it('scrolls only an off-screen sidebar target into view',()=>{
 const target=document.createElement('button'),viewport=document.createElement('div')
 const scroll=vi.fn();target.scrollIntoView=scroll
 const rect=(top:number,bottom:number)=>({top,bottom,left:0,right:100,width:100,height:bottom-top,x:0,y:top,toJSON:()=>({})})
 viewport.getBoundingClientRect=()=>rect(0,200)
 target.getBoundingClientRect=()=>rect(50,80)
 revealSidebarTarget(target,viewport);expect(scroll).not.toHaveBeenCalled()
 target.getBoundingClientRect=()=>rect(220,250)
 revealSidebarTarget(target,viewport);expect(scroll).toHaveBeenCalledOnce()
 const project=document.createElement('div'),header=document.createElement('button')
 header.setAttribute('aria-current','page');header.getBoundingClientRect=()=>rect(50,80)
 target.setAttribute('role','listitem');target.setAttribute('aria-current','page')
 project.append(header,target);scroll.mockClear()
 revealSidebarTarget(project,viewport);expect(scroll).toHaveBeenCalledOnce() // agent, not visible project header
})

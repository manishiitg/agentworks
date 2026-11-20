// @vitest-environment happy-dom
import {expect,it,vi} from 'vitest'
import {revealSidebarTarget,sidebarProjectShortcuts} from './sidebarProjectOrder'
it('keeps admitted pins in saved order, without moving them when selection changes', () => {
 const rows = [{ id: 'recent', selected: true }, { id: 'a', selected: false }, { id: 'b', selected: false }]
 const order = () => sidebarProjectShortcuts(rows, row => row.id, ['b', 'gone', 'a'], ['recent'])
 expect(order()).toEqual({ pinnedRows: [rows[2], rows[1]], recentRows: [], openRow: undefined })
 rows[0].selected = false
 rows[1].selected = true
 expect(order()).toEqual({ pinnedRows: [rows[2], rows[1]], recentRows: [], openRow: undefined })
})
it('falls back to at most five admitted recent projects, in visit order', () => {
 const rows = ['a', 'b', 'c', 'd', 'e', 'f']
 const order = (recent: string[]) => sidebarProjectShortcuts(rows, row => row, ['gone'], recent)
 expect(order(['gone', 'c', 'b', 'a', 'f', 'e', 'd'])).toEqual({ pinnedRows: [], recentRows: ['c', 'b', 'a', 'f', 'e'], openRow: undefined })
 expect(order(['gone'])).toEqual({ pinnedRows: [], recentRows: [], openRow: undefined })
})
it('shows the open project once, only when no pin or recent shortcut already shows it', () => {
 const rows = [{ id: 'a', selected: false }, { id: 'b', selected: true }, { id: 'c', selected: false }]
 const order = (pins: string[], recent: string[]) => sidebarProjectShortcuts(rows, row => row.id, pins, recent, row => row.selected).openRow
 expect(order(['a'], [])).toBe(rows[1])
 expect(order(['a', 'b'], [])).toBeUndefined()
 expect(order([], ['b'])).toBeUndefined()
 expect(order([], ['a'])).toBe(rows[1])
 rows[1].selected = false
 expect(order(['a'], [])).toBeUndefined()
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

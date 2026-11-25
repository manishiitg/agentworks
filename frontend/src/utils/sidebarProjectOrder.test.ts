// @vitest-environment happy-dom
import {expect,it,vi} from 'vitest'
import {markSidebarInteraction,resetSidebarInteraction,revealSidebarTarget,sidebarProjectShortcuts} from './sidebarProjectOrder'
it('lists pins in saved order, then up to five recent ones that are not pinned, the open one first', () => {
 const rows = [{ id: 'a', selected: false }, { id: 'b', selected: true }, { id: 'c', selected: false }, { id: 'd', selected: false }]
 const order = (pins: string[], recent: string[]) => sidebarProjectShortcuts(rows, row => row.id, pins, recent, row => row.selected)
 // Pins keep their order and drop rows that are gone; recent stays visible next to them (owner, 24 Nov).
 expect(order(['c', 'gone', 'a'], ['d', 'a'])).toEqual({ pinnedRows: [rows[2], rows[0]], recentRows: [rows[1], rows[3]], openRow: undefined })
 // The open project is listed once, in pins when pinned.
 expect(order(['b'], ['b', 'a']).recentRows).toEqual([rows[0]])
 const many = ['a', 'b', 'c', 'd', 'e', 'f'].map(id => ({ id, selected: false }))
 expect(sidebarProjectShortcuts(many, row => row.id, [], ['gone', 'f', 'e', 'd', 'c', 'b', 'a']).recentRows.map(row => row.id)).toEqual(['f', 'e', 'd', 'c', 'b'])
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

 // Scrolled down inside a tall project while no active agent listitem is mounted yet:
 // must not yank back up to the off-screen project heading.
 const tallProject=document.createElement('div'),tallHeader=document.createElement('button')
 const headerScroll=vi.fn();tallHeader.scrollIntoView=headerScroll
 tallHeader.setAttribute('aria-current','page')
 tallHeader.getBoundingClientRect=()=>rect(-300,-270)
 tallProject.getBoundingClientRect=()=>rect(-300,400)
 tallProject.append(tallHeader)
 revealSidebarTarget(tallProject,viewport)
 expect(headerScroll).not.toHaveBeenCalled()

 // Clicking directly inside the sidebar suppresses auto-scroll during the grace window.
 scroll.mockClear()
 markSidebarInteraction(1000)
 revealSidebarTarget(project,viewport,1200)
 expect(scroll).not.toHaveBeenCalled()
 revealSidebarTarget(project,viewport,2000)
 expect(scroll).toHaveBeenCalledOnce()
 resetSidebarInteraction()
})

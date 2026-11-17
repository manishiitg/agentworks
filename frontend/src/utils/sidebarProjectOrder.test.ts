// @vitest-environment happy-dom
import {expect,it,vi} from 'vitest'
import {revealSidebarTarget,sidebarProjectOrder} from './sidebarProjectOrder'
// #2945: switching agents must not bounce projects above and below saved pins.
it('keeps saved pin/group order stable and never hides a project',()=>{
 const rows:{id:string;selected?:boolean}[]=[{id:'ordinary'},{id:'pinned'},{id:'opened'},{id:'group'}],pins=['pinned','opened']
 const order=()=>sidebarProjectOrder(rows,row=>row.id,pins,row=>row.id!=='ordinary',row=>row.id==='group')
 const expected=['pinned','opened','group','ordinary']
 expect([...order().pinned,...order().rest].map(row=>row.id)).toEqual(expected)
 rows[0].selected=true;rows[2].selected=false
 expect([...order().pinned,...order().rest].map(row=>row.id)).toEqual(expected)
 expect(pins).toEqual(['pinned','opened'])
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
})

import {expect,it} from 'vitest'
import {sidebarProjectOrder} from './sidebarProjectOrder'
// #2917: a group/pin choice used to hide the project of a chat opened from elsewhere.
it('keeps the current project first, pins next and every other project after group matches without changing saved choices',()=>{
 const rows=[{id:'ordinary'},{id:'pinned'},{id:'opened'},{id:'group'}],pins=['pinned','opened']
 const order=(current:string)=>sidebarProjectOrder(rows,row=>row.id,row=>row.id===current,pins,()=>true,row=>row.id==='group')
 let result=order('opened')
 expect([...result.selected,...result.pinned,...result.rest].map(row=>row.id)).toEqual(['opened','pinned','group','ordinary'])
 expect(pins).toEqual(['pinned','opened'])
 result=order('ordinary')
 expect([...result.selected,...result.pinned,...result.rest].map(row=>row.id)).toEqual(['ordinary','pinned','opened','group'])
 // Even a saved location choice must not hide the current selection.
 const outside=sidebarProjectOrder(rows,row=>row.id,row=>row.id==='opened',[],row=>row.id!=='opened',()=>false)
 expect(outside.selected.map(row=>row.id)).toEqual(['opened'])
})

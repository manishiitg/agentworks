/** Pins/groups order admitted projects. Selection never changes this order or hides another project. */
export function sidebarProjectOrder<T>(rows:T[],key:(row:T)=>string,pins:readonly string[],inLocation:(row:T)=>boolean,preferred:(row:T)=>boolean) {
 const byKey=new Map(rows.map(row=>[key(row),row])),pinnedKeys=new Set(pins)
 const pinned=pins.flatMap(id=>{const row=byKey.get(id);return row?[row]:[]})
 const rest=rows.filter(row=>!pinnedKeys.has(key(row))).sort((a,b)=>Number(inLocation(b))-Number(inLocation(a))||Number(preferred(b))-Number(preferred(a)))
 return {pinned,rest}
}

/** Do not move an already visible selection, even when the current agent changes. */
export function revealSidebarTarget(target:HTMLElement,viewport:HTMLElement) {
 const box=target.getBoundingClientRect(),view=viewport.getBoundingClientRect()
 if(box.top<view.top||box.bottom>view.bottom)target.scrollIntoView?.({block:'nearest',inline:'nearest'})
}

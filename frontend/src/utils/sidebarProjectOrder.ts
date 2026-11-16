/** Selection is always visible first. Pins and groups order the admitted inventory; they never remove it. */
export function sidebarProjectOrder<T>(rows:T[],key:(row:T)=>string,current:(row:T)=>boolean,pins:readonly string[],inLocation:(row:T)=>boolean,preferred:(row:T)=>boolean) {
 const selected=rows.filter(current),selectedKeys=new Set(selected.map(key)),byKey=new Map(rows.map(row=>[key(row),row])),pinnedKeys=new Set(pins)
 const pinned=pins.flatMap(id=>{const row=byKey.get(id);return row&&!selectedKeys.has(id)?[row]:[]})
 const rest=rows.filter(row=>inLocation(row)&&!selectedKeys.has(key(row))&&!pinnedKeys.has(key(row))).sort((a,b)=>Number(preferred(b))-Number(preferred(a)))
 return {selected,pinned,rest}
}

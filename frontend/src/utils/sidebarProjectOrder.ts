/** Resolve presentation shortcuts against the current admitted inventory, never saved names or old rows. */
export function sidebarProjectShortcuts<T>(rows: T[], key: (row: T) => string, pins: readonly string[], recent: readonly string[], isOpen?: (row: T) => boolean) {
  const byKey = new Map(rows.map(row => [key(row), row]))
  const resolve = (keys: readonly string[]) => keys.flatMap(id => {
    const row = byKey.get(id)
    return row === undefined ? [] : [row]
  })
  const pinnedRows = resolve(pins)
  const recentRows = pinnedRows.length ? [] : resolve(recent).slice(0, 5)
  // The open project stays reachable in the menu while it is open, without pinning it (owner, 20 Nov).
  const open = isOpen ? rows.find(isOpen) : undefined
  const openRow = open !== undefined && !pinnedRows.includes(open) && !recentRows.includes(open) ? open : undefined
  return { pinnedRows, recentRows, openRow }
}

/** Do not move an already visible selection, even when the current agent changes. */
export function revealSidebarTarget(project:HTMLElement,viewport:HTMLElement) {
 const target=project.querySelector<HTMLElement>('[role="listitem"][aria-current="page"]')||project.querySelector<HTMLElement>('[aria-current="page"]')||project
 const box=target.getBoundingClientRect(),view=viewport.getBoundingClientRect()
 if(box.top<view.top||box.bottom>view.bottom)target.scrollIntoView?.({block:'nearest',inline:'nearest'})
}

/** Resolve presentation shortcuts against the current admitted inventory, never saved names or old rows. */
export function sidebarProjectShortcuts<T>(rows: T[], key: (row: T) => string, pins: readonly string[], recent: readonly string[], isOpen?: (row: T) => boolean) {
  const byKey = new Map(rows.map(row => [key(row), row]))
  const resolve = (keys: readonly string[]) => keys.flatMap(id => {
    const row = byKey.get(id)
    return row === undefined ? [] : [row]
  })
  const pinnedRows = resolve(pins)
  // Pinned first, then always up to five recent ones that are not pinned (owner, 24 Nov). The open project is the
  // most recent, so it leads that list instead of a separate "Open now" row.
  const open = isOpen ? rows.find(isOpen) : undefined
  const recentRows = [...(open !== undefined ? [open] : []), ...resolve(recent)]
    .filter((row, index, all) => !pinnedRows.includes(row) && all.indexOf(row) === index)
    .slice(0, 5)
  return { pinnedRows, recentRows, openRow: undefined as T | undefined }
}

/** Do not move an already visible selection, even when the current agent changes. */
export function revealSidebarTarget(project:HTMLElement,viewport:HTMLElement) {
 const target=project.querySelector<HTMLElement>('[role="listitem"][aria-current="page"]')||project.querySelector<HTMLElement>('[aria-current="page"]')||project
 const box=target.getBoundingClientRect(),view=viewport.getBoundingClientRect()
 if(box.top<view.top||box.bottom>view.bottom)target.scrollIntoView?.({block:'nearest',inline:'nearest'})
}

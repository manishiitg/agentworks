/** Resolve presentation shortcuts against the current admitted inventory, never saved names or old rows. */
export function sidebarProjectShortcuts<T>(rows: T[], key: (row: T) => string, pins: readonly string[], recent: readonly string[], isOpen?: (row: T) => boolean, sectionedKeys?: ReadonlySet<string>) {
  const byKey = new Map(rows.map(row => [key(row), row]))
  const resolve = (keys: readonly string[]) => keys.flatMap(id => {
    const row = byKey.get(id)
    return row === undefined ? [] : [row]
  })
  const pinnedRows = resolve(pins)
  // Pinned first, then custom sections, then up to five recent ones not pinned or sectioned.
  const open = isOpen ? rows.find(isOpen) : undefined
  const recentRows = [...(open !== undefined ? [open] : []), ...resolve(recent)]
    .filter((row, index, all) => !pinnedRows.includes(row) && !sectionedKeys?.has(key(row)) && all.indexOf(row) === index)
    .slice(0, 5)
  return { pinnedRows, recentRows, openRow: undefined as T | undefined }
}

const SIDEBAR_CLICK_GRACE_MS = 600
let lastSidebarInteractionAt = 0

/** Records a pointer interaction inside the sidebar so clicking a row never yanks scroll position. */
export function markSidebarInteraction(now = Date.now()) {
  lastSidebarInteractionAt = now
}

/** Reset interaction state for tests. */
export function resetSidebarInteraction() {
  lastSidebarInteractionAt = 0
}

/** Do not move an already visible selection, even when the current agent changes. */
export function revealSidebarTarget(project: HTMLElement, viewport: HTMLElement, now = Date.now()) {
  if (lastSidebarInteractionAt > 0 && now - lastSidebarInteractionAt < SIDEBAR_CLICK_GRACE_MS) return
  const view = viewport.getBoundingClientRect()
  const activeAgent = project.querySelector<HTMLElement>('[role="listitem"][aria-current="page"]')
  if (activeAgent) {
    const box = activeAgent.getBoundingClientRect()
    if (box.top < view.top || box.bottom > view.bottom) activeAgent.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
    return
  }
  // When no active agent row is mounted yet (e.g. reopening a closed/hidden agent or switching to live),
  // never yank back up to the project heading if the project is already visible in the viewport.
  const projectBox = project.getBoundingClientRect()
  if (projectBox.bottom > view.top && projectBox.top < view.bottom) return
  const target = project.querySelector<HTMLElement>('[aria-current="page"]') || project
  const box = target.getBoundingClientRect()
  if (box.top < view.top || box.bottom > view.bottom) target.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
}

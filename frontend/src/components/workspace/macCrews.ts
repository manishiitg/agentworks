// Crews that live on the owner's Mac and are reached through the server's
// relay twin (PLAT-856-509, Phase 1 contract section 5). The server decides
// every field; the list only groups and words them.

export type MacInfo = { device_id: string; name: string; online: boolean; last_seen?: string }
export type MacCrewState = { state: 'working' | 'idle' | 'waiting'; last_line?: string; at?: string }
export type CrewLocationFields = { location?: 'mac' | 'server'; mac?: MacInfo; state?: MacCrewState }

/** Mac Crews grouped per Mac, in first-seen order, plus everything else. */
export function groupByMac<T extends CrewLocationFields>(projects: T[]): { macs: Array<{ mac: MacInfo; rows: T[] }>; server: T[] } {
  const macs = new Map<string, { mac: MacInfo; rows: T[] }>()
  const server: T[] = []
  for (const project of projects) {
    if (project.location !== 'mac' || !project.mac?.device_id) { server.push(project); continue }
    const group = macs.get(project.mac.device_id) || { mac: project.mac, rows: [] }
    group.rows.push(project)
    macs.set(project.mac.device_id, group)
  }
  return { macs: [...macs.values()], server }
}

function clock(at: string | undefined, now: Date): string {
  if (!at) return ''
  const when = new Date(at)
  if (Number.isNaN(when.getTime())) return ''
  const sameDay = when.toDateString() === now.toDateString()
  return sameDay
    ? when.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : when.toLocaleDateString([], { day: 'numeric', month: 'short' })
}

export function macStatusText(mac: MacInfo, now = new Date()): string {
  if (mac.online) return 'Online'
  const seen = clock(mac.last_seen, now)
  return seen ? `Off · last seen ${seen}` : 'Off'
}

/** One short line under a Mac Crew's name. */
export function macCrewStateText(state: MacCrewState | undefined, now = new Date()): string {
  if (!state) return ''
  if (state.state === 'working') return state.last_line ? `Working · ${state.last_line}` : 'Working…'
  if (state.state === 'waiting') return state.last_line ? `Waiting for you · ${state.last_line}` : 'Waiting for you'
  const at = clock(state.at, now)
  return at ? `Idle · last reply ${at}` : 'Idle'
}

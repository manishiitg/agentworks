import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { agentApi } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { connectedLocations, hasDesktopConnections, useConnectedWorkPolling, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { openSidebarProject } from './SidebarServerGroup'
import { openCrewAccess } from '../../products/work/MacSharedAgents'
import type { TeamLiveView } from './TeamLiveAgents'
import { GlobalActivityMonitor, type OwnActivitySummary } from '../GlobalActivityMonitor'

type Crew = { id: string; name: string; team_live?: TeamLiveView }
type Row = { private?: boolean; key: string; personKey: string; person: string; network: string; agent: string; crew: string; activity: string; doing?: string; open: () => void }
type Face = { key: string; name: string; network: string; online: boolean; line: string; working: number; waiting: number }

const tones = ['bg-blue-100 text-blue-700 dark:bg-blue-900/60 dark:text-blue-200', 'bg-violet-100 text-violet-700 dark:bg-violet-900/60 dark:text-violet-200',
  'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/60 dark:text-emerald-200', 'bg-rose-100 text-rose-700 dark:bg-rose-900/60 dark:text-rose-200',
  'bg-amber-100 text-amber-800 dark:bg-amber-900/60 dark:text-amber-200']
const tone = (key: string) => tones[[...key].reduce((sum, char) => sum + char.charCodeAt(0), 0) % tones.length]

/** Every Crew in the person's menu, on this network and each connected one, with its Team as the server words it. */
function useTeamCrews(): { connectionId: string; network: string; crews: Crew[] }[] {
  const user = useAuthStore(state => state.user?.id || '')
  const active = getRawActiveWorkspaceId()
  const desktop = hasDesktopConnections()
  const allowed = (useAuthStore(state => state.user?.allowed_products) || ['work']).includes('work')
  const [here, setHere] = useState<{ scope: string; crews: Crew[] }>({ scope: '', crews: [] })
  const [refresh, setRefresh] = useState(0)
  useLiveRefetch(() => setRefresh(value => value + 1), { kinds: ['sessions'], fallbackMs: 60_000, safetyMs: 60_000, minIntervalMs: 5_000, enabled: allowed && !!user })
  useEffect(() => {
    if (!allowed || !user) return
    let disposed = false
    const identity = captureChatIdentity()
    void agentApi.listWorkspaceNavigation().then(result => {
      if (!disposed && isChatIdentityCurrent(identity)) setHere({ scope: `${active}:${user}`, crews: result.workspaces.filter(crew => crew.profile_id === 'work') })
    }).catch(() => undefined)
    return () => { disposed = true }
  }, [active, user, allowed, refresh])
  useConnectedWorkPolling(desktop)
  const store = useConnectedWorkStore()
  const others = desktop && isChatIdentityCurrent(store.identity)
    ? connectedLocations(store).filter(connection => connection.id !== active).map(connection => ({ connectionId: connection.id, network: connection.name, crews: (store.inventory[connection.id]?.projects || []).filter(project => project.kind === 'crew') }))
    : []
  const activeName = store.connections.find(connection => connection.id === active)?.name || 'Local'
  return [{ connectionId: active, network: activeName, crews: here.scope === `${active}:${user}` ? here.crews : [] }, ...others]
}

function openCrewTeam(connectionId: string, crewId: string) {
  if (connectionId === getRawActiveWorkspaceId()) {
    openSidebarProject('crew', crewId)
    openCrewAccess(crewId, '', 'team', 'activity')
  } else void window.electronAPI?.openDesktopConnection?.(connectionId, `/crew/projects/${encodeURIComponent(crewId)}`)
}

/** The header's Team (owner, 12 Nov, mockup "Monitor with Team" v4): the faces of teammates across every Crew, a
 * ring on those whose agents work or wait, and a panel of who is working where. The server decides every state and
 * word; this only gathers each Crew's reply. The left menu keeps only the person's own agents (PLAT-746-926). */
export function TeamMonitor({ representedSessionIds }: { representedSessionIds?: readonly string[] } = {}) {
  const sources = useTeamCrews()
  const { faces, rows, crewCount } = useMemo(() => {
    const faces = new Map<string, Face>()
    const rows: Row[] = []
    const crews = new Set<string>()
    for (const { connectionId, network, crews: list } of sources) {
      for (const crew of list) {
        const live = crew.team_live
        if (!live) continue
        for (const person of live.people || []) {
          const key = `${connectionId}:${person.user_id}`
          const face = faces.get(key) || { key, name: person.name, network, online: false, line: person.line, working: 0, waiting: 0 }
          if (person.online && !face.online) { face.online = true; face.line = person.line }
          faces.set(key, face)
        }
        for (const agent of live.agents) {
          const personKey = `${connectionId}:${agent.owner_id}`
          const face = faces.get(personKey) || { key: personKey, name: agent.person, network, online: true, line: '', working: 0, waiting: 0 }
          if (agent.activity === 'waiting') face.waiting += 1
          else face.working += 1
          faces.set(personKey, face)
          crews.add(`${connectionId}:${crew.id}`)
          rows.push({ private: agent.private, key: `${connectionId}:${crew.id}:${agent.id}`, personKey, person: agent.person, network, agent: agent.name, crew: crew.name, activity: agent.activity, doing: agent.doing, open: () => openCrewTeam(connectionId, crew.id) })
        }
      }
    }
    const rank = (face: Face) => face.working ? 0 : face.waiting ? 1 : face.online ? 2 : 3
    return { faces: [...faces.values()].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name)), rows, crewCount: crews.size }
  }, [sources])
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  // Your own running work (the former Active work button), shown as the panel's You part (owner, 12 Nov: option 2).
  const [mine, setMine] = useState<OwnActivitySummary>({ working: 0, needsInput: 0 })
  const [slot, setSlot] = useState<HTMLDivElement | null>(null)
  const [only, setOnly] = useState('')
  const rootRef = useRef<HTMLDivElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState({ left: 0, top: 0 })
  useEffect(() => {
    if (!open) return
    const close = (event: PointerEvent) => { const target = event.target as Node; if (target instanceof Element && target.closest('[data-location-filter-menu]')) return; if (!rootRef.current?.contains(target) && !panelRef.current?.contains(target)) setOpen(false) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setOpen(false) }
    document.addEventListener('pointerdown', close)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('pointerdown', close); document.removeEventListener('keydown', escape) }
  }, [open])
  useLayoutEffect(() => {
    if (!open) return
    const place = () => {
      const trigger = rootRef.current?.getBoundingClientRect()
      const panel = panelRef.current?.getBoundingClientRect()
      if (!trigger || !panel) return
      setPosition({ left: Math.max(12, Math.min(trigger.right - panel.width, window.innerWidth - panel.width - 12)), top: trigger.bottom + 8 })
    }
    place()
    window.addEventListener('resize', place)
    return () => window.removeEventListener('resize', place)
  }, [open, rows.length, mine.working, mine.needsInput])

  const shown = faces.filter(face => face.online || face.working || face.waiting)
  const mineActive = mine.working > 0 || mine.needsInput > 0
  const own = <GlobalActivityMonitor representedSessionIds={representedSessionIds} slot={open && mineActive ? slot : null} onSummary={setMine} onClose={close} />
  if (!shown.length && !mineActive) return <div ref={rootRef} className="relative">{own}</div>
  const mineLabel = mine.needsInput ? `${mine.needsInput} need${mine.needsInput === 1 ? 's' : ''} you` : mine.working ? `${mine.working} running` : ''
  const working = faces.filter(face => face.working).length
  const online = faces.filter(face => face.online).length
  const label = working ? `${working} working` : `${online} online`
  // With more than one network every person and Crew names its network (owner, 12 Nov: "for more than one network we
  // need some identification").
  const networks = [...new Set(faces.map(face => face.network))]
  const multi = networks.length > 1
  const title = shown.map(face => `${face.name}${multi ? ` (${face.network})` : ''} · ${face.working ? `${face.working} working` : face.waiting ? 'waiting' : face.line || 'online'}`).join('\n')
  const avatar = (face: Face, size: string) => <span className={`relative grid shrink-0 place-items-center rounded-full font-semibold ${size} ${tone(face.key)} ${face.working ? 'ring-2 ring-blue-500' : face.waiting ? 'ring-2 ring-amber-500' : ''} ${face.online || face.working || face.waiting ? '' : 'opacity-40'}`}>
    {face.name.slice(0, 1).toUpperCase()}
    {face.online && <span aria-hidden="true" className="absolute -bottom-0.5 -right-0.5 h-2 w-2 rounded-full border-2 border-background bg-emerald-500" />}
  </span>
  const listed = rows.filter(row => !only || row.personKey === only)
  const workingRows = listed.filter(row => row.activity !== 'waiting')
  const waitingRows = listed.filter(row => row.activity === 'waiting')
  const waiting = rows.filter(row => row.activity === 'waiting').length
  const rowView = (row: Row) => {
    const face = faces.find(item => item.key === row.personKey)
    return <button key={row.key} type="button" role="menuitem" onClick={() => { setOpen(false); row.open() }} className="flex w-full items-start gap-2.5 rounded-md px-2 py-2 text-left hover:bg-muted">
      {face && avatar(face, 'h-6 w-6 text-[11px]')}
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-baseline gap-1.5"><span className="truncate text-sm font-medium text-foreground">{row.person}&apos;s {row.agent}</span>{row.private && <span className="shrink-0 text-[10px] text-muted-foreground">Only you</span>}<span className="shrink-0 rounded-full border border-border px-1.5 text-[10px] text-muted-foreground">{multi ? `${row.network} · ${row.crew}` : row.crew}</span></span>
        <span className={`block truncate text-xs ${row.activity === 'waiting' ? 'text-amber-600 dark:text-amber-400' : 'text-muted-foreground'}`}>{row.doing || (row.activity === 'waiting' ? 'Waiting' : 'Working')}</span>
      </span>
    </button>
  }

  const chip = (face: Face) => <button key={face.key} type="button" title={face.line} onClick={() => setOnly(current => current === face.key ? '' : face.key)}
          className={`flex items-center gap-1.5 rounded-full border py-0.5 pl-0.5 pr-2.5 text-xs ${only === face.key ? 'border-primary text-foreground' : 'border-border text-muted-foreground'} ${face.online || face.working || face.waiting ? '' : 'opacity-60'}`}>
          {avatar(face, 'h-5 w-5 text-[9px]')}{face.name}<span className="text-muted-foreground">{face.working || face.waiting ? face.working + face.waiting : face.online ? '' : face.line.replace(/^Last seen /, '')}</span>
  </button>
  const team = shown.length > 0
  const parts = [mineLabel, team ? `Team · ${label}` : ''].filter(Boolean)
  return <div ref={rootRef} className="relative">
    {own}
    <button type="button" onClick={() => setOpen(value => !value)} aria-expanded={open} aria-label={`${parts.join(', ')}. Open to see what is running and who is working.`} title={[mineLabel && `You · ${mineLabel}`, title].filter(Boolean).join('\n')}
      className="flex h-8 shrink-0 items-center gap-2 rounded-full border border-border px-1.5 text-xs text-muted-foreground hover:bg-muted md:pr-2.5">
      {mineActive && <span className="flex items-center gap-1.5 pl-1">
        <span aria-hidden="true" className={`h-1.5 w-1.5 rounded-full motion-safe:animate-pulse ${mine.needsInput ? 'bg-amber-500' : 'bg-blue-500'}`} />
        <span className={`hidden whitespace-nowrap font-medium md:inline ${mine.needsInput ? 'text-amber-600 dark:text-amber-400' : 'text-blue-600 dark:text-blue-400'}`}>{mineLabel}</span>
      </span>}
      {mineActive && team && <span aria-hidden="true" className="hidden h-3.5 w-px bg-border md:block" />}
      {team && <>
        <span className="flex -space-x-1.5">
          {shown.slice(0, 4).map((face, index) => <span key={face.key} className={index >= 2 ? 'hidden md:block' : ''}>{avatar(face, 'h-5 w-5 text-[9px] border-2 border-background')}</span>)}
        </span>
        {shown.length > 2 && <span className="text-[10px] md:hidden">+{shown.length - 2}</span>}
        <span className="hidden whitespace-nowrap md:inline">Team · <span className={working ? 'font-medium text-blue-600 dark:text-blue-400' : ''}>{label}</span></span>
      </>}
    </button>
    {open && createPortal(<div ref={panelRef} role="menu" aria-label="Running work and Team" style={position} className="fixed z-50 flex max-h-[75vh] w-96 max-w-[calc(100vw-1.5rem)] flex-col overflow-y-auto rounded-lg border border-border bg-popover shadow-xl">
      {mineActive && <div ref={setSlot} className={`pt-2 ${team ? 'border-b border-border pb-1' : 'pb-1'}`} />}
      {team && <>
        <div className="px-3 pb-1.5 pt-3 text-sm font-semibold text-foreground">Team</div>
        {rows.length > 0 && <div className="px-3 pb-2 text-xs text-muted-foreground">{`${rows.length - waiting} agent${rows.length - waiting === 1 ? '' : 's'} working${waiting ? ` · ${waiting} waiting` : ''} · in ${crewCount} Crew${crewCount === 1 ? '' : 's'}`}</div>}
        <div className="flex flex-wrap gap-1.5 px-3 pb-2">
          <button type="button" onClick={() => setOnly('')} className={`rounded-full border px-2.5 py-0.5 text-xs ${only ? 'border-border text-muted-foreground' : 'border-primary text-foreground'}`}>Everyone</button>
          {!multi && faces.map(chip)}
        </div>
        {multi && networks.map(network => <div key={network} className="px-3 pb-2">
          <div className="pb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{network}</div>
          <div className="flex flex-wrap gap-1.5">{faces.filter(face => face.network === network).map(chip)}</div>
        </div>)}
        <div className="px-1 pb-2">
          {workingRows.length > 0 && <div className="px-2 pb-1 pt-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">Working now</div>}
          {workingRows.map(rowView)}
          {waitingRows.length > 0 && <div className="px-2 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">Waiting</div>}
          {waitingRows.map(rowView)}
          {!listed.length && rows.length > 0 && <p className="px-2 py-3 text-xs text-muted-foreground">No agent of theirs is working right now.</p>}
        </div>
      </>}
    </div>, document.body)}
  </div>
}

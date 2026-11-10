import { useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'
import { Check, FolderPlus, ListFilter, Pencil, Plus, X } from 'lucide-react'
import { llmConfigService, type MenuGroups } from '../../services/llm-config-api'

// Left menu groups and filter (owner, 10 Nov, approved mockup "Menu Filters Groups"). The server keeps the groups
// (menu_groups.go); the screen sends the full list on each change and shows what the server answers.

type Group = MenuGroups['groups'][number]
export type MenuFilter = 'all' | 'mine' | 'shared' | `group:${string}`

const useMenuGroupsStore = create<{ groups: Group[]; loaded: boolean; error: string; set: (groups: Group[]) => void; fail: (error: string) => void }>()(set => ({
  groups: [], loaded: false, error: '',
  set: groups => set({ groups, loaded: true, error: '' }),
  fail: error => set({ error }),
}))

// The picked filter is a per-viewer convenience, kept in this browser only.
const useMenuFilterStore = create<{ filter: MenuFilter; pick: (filter: MenuFilter) => void }>()(persist(set => ({
  filter: 'all',
  pick: filter => set({ filter }),
}), {
  name: 'menu-filter-v1',
  storage: createJSONStorage(() => ({
    getItem: key => { try { return window.localStorage.getItem(key) } catch { return null } },
    setItem: (key, value) => { try { window.localStorage.setItem(key, value) } catch { /* Keep the in-memory choice. */ } },
    removeItem: key => { try { window.localStorage.removeItem(key) } catch { /* Storage is optional. */ } },
  })),
}))

let loading: Promise<void> | null = null
function loadGroups() {
  loading ??= llmConfigService.getMenuGroups()
    .then(result => useMenuGroupsStore.getState().set(result.groups || []))
    .catch(() => useMenuGroupsStore.getState().fail('Could not load your groups.'))
    .finally(() => { loading = null })
  return loading
}
async function saveGroups(groups: Group[]) {
  try {
    const result = await llmConfigService.setMenuGroups({ groups })
    useMenuGroupsStore.getState().set(result.groups || [])
  } catch {
    useMenuGroupsStore.getState().fail('Could not save your groups. Try again.')
  }
}

export function useMenuGroups() {
  const state = useMenuGroupsStore()
  const { filter, pick } = useMenuFilterStore()
  useEffect(() => { if (!state.loaded) void loadGroups() }, [state.loaded])
  // Groups can change from chat or MCP too (the menu_groups tool); read them again when the window comes back.
  useEffect(() => {
    const reload = () => { if (!document.hidden) void loadGroups() }
    window.addEventListener('focus', reload)
    return () => window.removeEventListener('focus', reload)
  }, [])
  // A group that was deleted (here or elsewhere) falls back to All.
  const current: MenuFilter = filter.startsWith('group:') && state.loaded && !state.groups.some(group => `group:${group.id}` === filter) ? 'all' : filter
  return { groups: state.groups, error: state.error, filter: current, pick }
}

/** A project shared with the person: a Team copy on this Mac, or someone else's Crew on a server. */
export function isSharedProject(project: { owned_by_caller?: boolean; shared_line?: string }) {
  return project.owned_by_caller === false || Boolean(project.shared_line)
}

export function matchesMenuFilter(filter: MenuFilter, groups: Group[], key: string, project: { owned_by_caller?: boolean; shared_line?: string }) {
  if (filter === 'mine') return !isSharedProject(project)
  if (filter === 'shared') return isSharedProject(project)
  if (filter.startsWith('group:')) return groups.find(group => `group:${group.id}` === filter)?.projects.includes(key) ?? true
  return true
}

function usePopover() {
  const trigger = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null)
  useEffect(() => {
    if (!position) return
    const outside = (event: MouseEvent) => {
      const target = event.target as Node
      if (!panel.current?.contains(target) && !trigger.current?.contains(target)) setPosition(null)
    }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setPosition(null); trigger.current?.focus() } }
    const dismiss = () => setPosition(null)
    document.addEventListener('mousedown', outside)
    document.addEventListener('keydown', escape)
    window.addEventListener('resize', dismiss)
    return () => { document.removeEventListener('mousedown', outside); document.removeEventListener('keydown', escape); window.removeEventListener('resize', dismiss) }
  }, [position])
  const toggle = (width: number, alignRight: boolean) => {
    if (position) { setPosition(null); return }
    const rect = trigger.current?.getBoundingClientRect()
    if (rect) setPosition({ top: Math.max(8, Math.min(rect.bottom + 4, window.innerHeight - 260)), left: Math.max(8, Math.min(alignRight ? rect.right - width : rect.left, window.innerWidth - width - 8)) })
  }
  const render = (label: string, width: string, children: ReactNode) => position && createPortal(
    <div ref={panel} role="dialog" aria-label={label} style={position} className={`fixed z-[100] ${width} rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg`}>{children}</div>, document.body)
  return { trigger, open: !!position, close: () => setPosition(null), toggle, render }
}

const item = 'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'

function NameInput({ initial = '', placeholder, onDone }: { initial?: string; placeholder: string; onDone: (name: string | null) => void }) {
  const [value, setValue] = useState(initial)
  return <form className="flex items-center gap-1 px-1 py-1" onSubmit={event => { event.preventDefault(); onDone(value.trim() || null) }}>
    <input autoFocus value={value} maxLength={40} placeholder={placeholder} onChange={event => setValue(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') { event.stopPropagation(); onDone(null) } }}
      className="h-7 min-w-0 flex-1 rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus-visible:ring-2 focus-visible:ring-ring" />
    <button type="submit" aria-label="Save" className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md hover:bg-muted"><Check className="h-3.5 w-3.5" aria-hidden="true" /></button>
  </form>
}

/** The "All projects" heading as a dropdown: All, Mine, Shared with me, then the person's groups. */
/** A network the menu can show: the Mac or a connected server (the desktop app only). */
export type MenuNetwork = { id: string; name: string }

export function ProjectFilterMenu({ counts, networks = [], network, onNetwork }: { counts: (filter: MenuFilter) => number; networks?: MenuNetwork[]; network?: string; onNetwork?: (id: string) => void }) {
  const { groups, error, filter, pick } = useMenuGroups()
  const popover = usePopover()
  const [editing, setEditing] = useState<string | null>(null) // a group id, or "new"
  const label = filter === 'mine' ? 'Mine' : filter === 'shared' ? 'Shared with me' : filter.startsWith('group:') ? groups.find(group => `group:${group.id}` === filter)?.name || 'All projects' : 'All projects'
  const choose = (next: MenuFilter) => { pick(next); popover.close() }
  // Networks sit at the top of the same menu (owner, 10 Nov: they were chips that did not fit); "all" shows every one.
  const networkName = network === 'all' ? 'All networks' : networks.find(item => item.id === network)?.name
  const networkOption = (id: string, name: string) => <button key={`network:${id}`} type="button" onClick={() => { onNetwork?.(id); popover.close() }} className={`${item} ${network === id ? 'bg-muted' : ''}`}>
    <span className="min-w-0 flex-1 truncate">{name}</span>{network === id && <Check className="h-3.5 w-3.5" aria-hidden="true" />}
  </button>
  const option = (value: MenuFilter, name: string) => <button key={value} type="button" onClick={() => choose(value)} className={`${item} ${filter === value ? 'bg-muted' : ''}`}>
    <span className="min-w-0 flex-1 truncate">{name}</span><span className="text-muted-foreground">{counts(value)}</span>
  </button>
  return <>
    <button ref={popover.trigger} type="button" title="Filter projects" aria-label={`Filter projects: ${label}${networks.length > 1 && networkName ? `, ${networkName}` : ''}`} aria-haspopup="dialog" aria-expanded={popover.open} onClick={() => popover.toggle(224, false)}
      className="flex min-w-0 items-center gap-1 rounded-md px-1 py-0.5 text-sm text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <span className="truncate">{label}</span>{networks.length > 1 && networkName && <span className="max-w-24 shrink-0 truncate text-[11px]">· {networkName}</span>}<ListFilter className={`h-3.5 w-3.5 shrink-0 ${filter === 'all' ? '' : 'text-foreground'}`} aria-hidden="true" />
    </button>
    {popover.render('Show projects', 'w-56', <>
      {networks.length > 1 && <>
        <p className="px-2 pb-0.5 pt-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">Network</p>
        {networks.map(item => networkOption(item.id, item.name))}
        {networkOption('all', 'All networks')}
        <div className="my-1 h-px bg-border" />
      </>}
      {option('all', 'All projects')}
      {option('mine', 'Mine')}
      {option('shared', 'Shared with me')}
      <div className="my-1 h-px bg-border" />
      <p className="px-2 pb-0.5 pt-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">Groups</p>
      {groups.map(group => editing === group.id
        ? <NameInput key={group.id} initial={group.name} placeholder="Group name" onDone={name => { setEditing(null); if (name) void saveGroups(groups.map(g => g.id === group.id ? { ...g, name } : g)) }} />
        : <div key={group.id} className="group/item flex items-center">
          <div className="min-w-0 flex-1">{option(`group:${group.id}`, group.name)}</div>
          <button type="button" aria-label={`Rename ${group.name}`} title="Rename" onClick={() => setEditing(group.id)} className="flex h-7 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground opacity-0 hover:bg-muted hover:text-foreground group-hover/item:opacity-100 focus-visible:opacity-100"><Pencil className="h-3 w-3" aria-hidden="true" /></button>
          <button type="button" aria-label={`Delete ${group.name}`} title="Delete group (its projects stay)" onClick={() => void saveGroups(groups.filter(g => g.id !== group.id))} className="flex h-7 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground opacity-0 hover:bg-muted hover:text-foreground group-hover/item:opacity-100 focus-visible:opacity-100"><X className="h-3 w-3" aria-hidden="true" /></button>
        </div>)}
      {editing === 'new'
        ? <NameInput placeholder="New group name" onDone={name => { setEditing(null); if (name) void saveGroups([...groups, { id: '', name, projects: [] }]) }} />
        : <button type="button" onClick={() => setEditing('new')} className={item}><Plus className="h-3.5 w-3.5" aria-hidden="true" />New group…</button>}
      {error && <p role="alert" className="px-2 py-1 text-[11px] text-destructive">{error}</p>}
    </>)}
  </>
}

/** A row's groups: check a group to put the project in it. */
export function ProjectGroupsMenu({ name, projectKey }: { name: string; projectKey: string }) {
  const { groups, error } = useMenuGroups()
  const popover = usePopover()
  const [adding, setAdding] = useState(false)
  const toggleIn = (group: Group) => void saveGroups(groups.map(g => g.id !== group.id ? g
    : { ...g, projects: g.projects.includes(projectKey) ? g.projects.filter(key => key !== projectKey) : [...g.projects, projectKey] }))
  return <>
    <button ref={popover.trigger} type="button" title="Groups" aria-label={`Groups for ${name}`} aria-haspopup="dialog" aria-expanded={popover.open} onClick={() => popover.toggle(208, true)}
      className="mr-1 flex h-8 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:opacity-0 md:group-hover/crew:opacity-100 md:focus-visible:opacity-100">
      <FolderPlus className="h-3.5 w-3.5" aria-hidden="true" />
    </button>
    {popover.render(`Groups for ${name}`, 'w-52', <>
      <p className="px-2 pb-0.5 pt-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">Groups</p>
      {groups.map(group => <button key={group.id} type="button" role="menuitemcheckbox" aria-checked={group.projects.includes(projectKey)} onClick={() => toggleIn(group)} className={item}>
        <span className="min-w-0 flex-1 truncate">{group.name}</span>{group.projects.includes(projectKey) && <Check className="h-3.5 w-3.5" aria-hidden="true" />}
      </button>)}
      {adding
        ? <NameInput placeholder="New group name" onDone={newName => { setAdding(false); if (newName) void saveGroups([...groups, { id: '', name: newName, projects: [projectKey] }]) }} />
        : <button type="button" onClick={() => setAdding(true)} className={item}><Plus className="h-3.5 w-3.5" aria-hidden="true" />New group…</button>}
      {error && <p role="alert" className="px-2 py-1 text-[11px] text-destructive">{error}</p>}
    </>)}
  </>
}

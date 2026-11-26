import { useEffect, useState } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { Check, ChevronDown, ListChecks } from 'lucide-react'
import { useChatContextView } from '../../products/work/useChatContextView'
import { changeChatContext, menuLabelClass, menuRowClass } from '../../products/work/ChatContextItems'
import api, { workFolderApi } from '../../services/api'
import type { ChatAccessMode, WorkFolderGrant } from '../../services/api-types'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useChatStore } from '../../stores/useChatStore'

// The chat's mode beside the model (#3885): how this chat works (Auto, Auto-review, Plan). What it can reach is the
// project's access, set in Folders on this Mac (owner, 26 Nov: "access is at project level.. mode is chat level"); the
// menu shows it on one line with Change. Shift+Tab still switches Plan.

type Row = { id: ChatAccessMode | 'plan'; label: string; chip: string; detail: string }

const ROWS: Row[] = [
  { id: 'project', label: 'Auto', chip: 'Auto', detail: 'Works without asking, inside the folders it can reach.' },
  { id: 'auto_review', label: 'Auto-review', chip: 'Auto-review', detail: 'The coding app’s own reviewer approves each action and refuses risky ones.' },
  { id: 'plan', label: 'Plan', chip: 'Plan', detail: 'Proposes a plan and changes nothing until you approve it.' },
]

const chipClass = 'inline-flex items-center gap-1 rounded px-1 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:bg-muted'

/** The project's access, for the line under the modes: the folders attached on this Mac and the whole-Mac row. */
function useReach(open: boolean, reloadTick: number) {
  const [reach, setReach] = useState<{ folders: WorkFolderGrant[]; home: string } | null>(null)
  useEffect(() => {
    if (!open) return
    let cancelled = false
    void Promise.all([
      workFolderApi.listWorkFolders().then(response => response.folders || []).catch(() => [] as WorkFolderGrant[]),
      api.get<{ available: boolean; home?: string }>('/api/work/mac-downloads').then(({ data }) => (data.available ? data.home || '' : '')).catch(() => ''),
    ]).then(([folders, home]) => { if (!cancelled) setReach({ folders, home: home.replace(/\/+$/, '') }) })
    return () => { cancelled = true }
  }, [open, reloadTick])
  return reach
}

export const openCrewMacFoldersEvent = 'agentworks:open-crew-mac-folders'

export function ChatModeMenu({ sessionId, projectId, plan, planReady, onPlanToggle, onStart }: {
  sessionId?: string
  // The Crew project this chat belongs to: its side panel opens Folders on this Mac.
  projectId?: string
  // Plan: shown only when this chat's coding app can plan first.
  plan?: { on: boolean }
  planReady?: boolean
  onPlanToggle: () => void
  onStart?: () => void
}) {
  const view = useChatContextView(sessionId || '')
  const access = view?.access
  const [open, setOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [granting, setGranting] = useState(false)
  const [reachReload, setReachReload] = useState(0)
  const reach = useReach(open, reachReload)
  const mode: ChatAccessMode = access?.mode || 'project'
  const current: Row['id'] = plan?.on ? 'plan' : mode
  const row = ROWS.find(item => item.id === current) || ROWS[0]

  if (plan?.on && planReady && onStart) {
    // Codex and Cursor answer a plan as a reply, so the tag offers the one step that starts the work and turns
    // Plan off. Typing still sends changes.
    return (
      <span className="inline-flex items-center gap-1 rounded px-1 py-0.5 text-[11px] text-muted-foreground" data-testid="plan-mode-chip" title="The plan is ready. Start runs it; or type changes.">
        <ListChecks className="h-3 w-3" aria-hidden="true" />
        Plan ready ·
        <button type="button" onClick={onStart} data-testid="plan-start-button"
          className="rounded px-0.5 font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          Start
        </button>
      </span>
    )
  }
  if (!access && !plan) return null

  const pick = async (id: string) => {
    if (id === 'plan') {
      if (!plan?.on) onPlanToggle()
      return
    }
    if (plan?.on) onPlanToggle()
    if (!sessionId || id === mode) return
    setSaving(true)
    try { await changeChatContext(sessionId, { access_mode: id as ChatAccessMode }) } finally { setSaving(false) }
  }
  const option = (id: Row['id']) => access?.options.find(item => item.mode === id)
  const rows = ROWS.filter(item => item.id === 'plan' ? Boolean(plan) : Boolean(access))
  // The saved mode this app cannot run here (Auto-review in a Codex chat) runs as This project: say so.
  const fallback = access && access.mode !== access.effective && !plan?.on
  const wholeMac = Boolean(reach?.home) && Boolean(reach?.folders.some(folder => folder.path.replace(/\/+$/, '') === reach?.home && folder.access === 'read_write'))
  const others = (reach?.folders || []).filter(folder => folder.path.replace(/\/+$/, '') !== reach?.home)
  // A Crew project's side panel is its own (WorkSurface listens for this event); other chats use the workflow pane.
  const giveWholeMac = async () => {
    if (!reach?.home) return
    setGranting(true)
    try {
      await workFolderApi.addWorkFolder({ path: reach.home, alias: 'mac', access: 'read_write' })
      setReachReload(n => n + 1)
    } catch {
      useChatStore.getState().addToast('Could not grant Mac folder access. Try again.', 'error')
    } finally { setGranting(false) }
  }
  const openFolders = () => projectId
    ? window.dispatchEvent(new CustomEvent(openCrewMacFoldersEvent, { detail: { projectId } }))
    : useWorkflowStore.getState().openWorkspaceView('mcp', 'mac-folders')

  return <Menu.Root modal={false} open={open} onOpenChange={setOpen}>
    <Menu.Trigger type="button" data-testid="plan-mode-chip" aria-pressed={plan?.on} aria-label={`Mode: ${row.label}. ${row.detail}`} title={`${row.label}: ${row.detail}${plan ? ' Shift+Tab switches Plan.' : ''}`} className={chipClass}>
      {current === 'plan' && <ListChecks className="h-3 w-3" aria-hidden="true" />}
      <span className={fallback ? 'line-through decoration-muted-foreground/60' : undefined}>{row.chip}</span>
      <ChevronDown className="h-3 w-3 shrink-0" aria-hidden="true" />
    </Menu.Trigger>
    <Menu.Portal>
      <Menu.Content side="top" align="start" sideOffset={8} collisionPadding={12} data-testid="chat-mode-menu"
        className="z-[100] w-[min(320px,calc(100vw-24px))] rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
        <Menu.Label className={menuLabelClass}>Mode</Menu.Label>
        <Menu.RadioGroup value={current} onValueChange={id => void pick(id)}>
          {rows.map(item => {
            const state: { available: boolean; why?: string } | undefined = item.id === 'plan' ? { available: true } : option(item.id)
            const disabled = saving || state?.available === false
            return <Menu.RadioItem key={item.id} value={item.id} disabled={disabled} data-testid={`chat-mode-${item.id}`} className={`${menuRowClass} items-start`}>
              <span className="min-w-0 flex-1">
                <span className="block font-medium">{item.label}</span>
                <span className="block text-xs text-muted-foreground">{state?.available === false && state.why ? state.why : item.detail}</span>
              </span>
              <Menu.ItemIndicator className="pt-0.5"><Check className="h-3.5 w-3.5" aria-hidden="true" /></Menu.ItemIndicator>
            </Menu.RadioItem>
          })}
        </Menu.RadioGroup>
        {fallback && <p className="px-2.5 pt-1 text-xs text-amber-700 dark:text-amber-400" data-testid="chat-mode-fallback">This coding app has no reviewer that works here, so this chat runs as Auto.</p>}
        {access && <>
          <Menu.Separator className="my-1 h-px bg-border" />
          <Menu.Item className={`${menuRowClass} items-start`} data-testid="chat-mode-manage-folders" onSelect={openFolders}
            title="Set in Folders on this Mac. Every chat reaches the same folders; the mode does not change them.">
            <span className="min-w-0 flex-1 text-xs text-muted-foreground" data-testid="chat-mode-reach">
              Can reach: {access.on_server
                ? 'this project’s folders'
                : wholeMac
                  ? 'your whole Mac'
                  : `this project${others.length ? ` · ${others.map(folder => folder.alias || folder.path).join(' · ')}` : ''}`}
            </span>
            <span className="shrink-0 text-xs font-medium text-primary">Change</span>
          </Menu.Item>
          {/* One click to the whole Mac (owner, 26 Nov), the same grant as Folders on this Mac's "Your whole Mac". */}
          {!access.on_server && reach?.home && !wholeMac && <Menu.Item className={`${menuRowClass} items-start`} data-testid="chat-mode-whole-mac" disabled={granting} onSelect={() => void giveWholeMac()}>
            <span className="min-w-0 flex-1">
              <span className="block text-xs font-medium">Give access to your whole Mac</span>
              <span className="block text-xs text-muted-foreground">Read &amp; write everything in your home folder, for all your projects. Turn it off in Folders on this Mac.</span>
            </span>
          </Menu.Item>}
        </>}
      </Menu.Content>
    </Menu.Portal>
  </Menu.Root>
}

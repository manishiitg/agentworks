import { useState, useEffect, useLayoutEffect, useRef, useMemo, useImperativeHandle, type Ref } from 'react'
import { Layers } from 'lucide-react'
import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'
import { loadProductProjects } from '../platform/chat/productProjects'
import { WORK_PROFILE_ID, WORK_PROJECTS_ROOT } from '../products/work/workData'
import { EntityIdentityIcon } from './ui/EntityIdentityIcon'
import { AGENTWORKS_SUBTITLE, AgentWorksIcon, agentWorksFirst, isOwnAgentWorks } from './ui/AgentWorksIdentity'
import { isPlainPickerKey } from '../utils/composerReferences'
import { normalizeWorkspacePath } from '../utils/workspacePathUtils'

interface CrewReference {
  presetId: string
  label: string
  workspacePath: string
  icon?: string
  secondaryLabel?: string
  shared?: boolean
  /** The person's own AgentWorks (server role "agentworks"): own icon, listed first. */
  agentWorks?: boolean
}

const referenceRowKey = (item: CrewReference) => item.presetId

export interface CrewReferencePickerHandle {
  handleKeyDown: (event: KeyboardEvent) => boolean
}

interface CrewReferencePickerProps {
  ref: Ref<CrewReferencePickerHandle>
  isOpen: boolean
  onClose: () => void
  onSelectCrew: (crew: CrewReference) => void
  listId: string
  onActiveOptionChange: (id?: string) => void
  searchQuery: string
  position: { bottom: number; left: number }
  excludeWorkspacePath?: string
}

let lastReferences: { scopeKey: string; items: CrewReference[] } | null = null

export function CrewReferencePicker({
  ref,
  isOpen,
  onClose,
  onSelectCrew,
  listId,
  onActiveOptionChange,
  searchQuery,
  position,
  excludeWorkspacePath,
}: CrewReferencePickerProps) {
  const rowID = (item: CrewReference) => `${listId}-${encodeURIComponent(referenceRowKey(item))}`
  const [selectedPresetId, setSelectedPresetId] = useState<string | null>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const userID = useAuthStore(state => state.user?.id)
  const workspaceID = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scopeKey = `${captureChatIdentity()}:${workspaceID}:${userID ?? ''}`

  useLayoutEffect(() => { setSelectedPresetId(null) }, [isOpen, searchQuery, scopeKey])

  const [referenceResult, setReferenceResult] = useState<{
    scopeKey: string
    items: CrewReference[]
    status: 'loading' | 'ready' | 'error'
  } | null>(null)

  // Reuse this account's last list while the existing, permission-filtered
  // Crew catalog refreshes. Workflows inside a Crew are not separate projects.
  useEffect(() => {
    const cached = lastReferences?.scopeKey === scopeKey ? lastReferences : null
    setReferenceResult({ scopeKey, items: cached?.items ?? [], status: 'loading' })
    if (!isOpen) return
    let cancelled = false
    const identity = captureChatIdentity()
    void loadProductProjects(WORK_PROJECTS_ROOT, WORK_PROFILE_ID, { includeOwnSharedProjects: true }).then(crews => {
      if (cancelled || !isChatIdentityCurrent(identity)) return
      const items = crews.map(crew => {
        const label = crew.identity?.name?.trim() || crew.title
        const agentWorks = isOwnAgentWorks(crew)
        return {
          presetId: `crew:${crew.id}`,
          label,
          secondaryLabel: agentWorks ? AGENTWORKS_SUBTITLE : label === crew.title ? undefined : crew.title,
          agentWorks,
          workspacePath: crew.workspacePath,
          shared: !!crew.shared && crew.shared.canBuild !== true,
          icon: crew.identity?.icon,
        }
      })
      lastReferences = { scopeKey, items }
      setReferenceResult({ scopeKey, items, status: 'ready' })
    }).catch(() => {
      if (cancelled || !isChatIdentityCurrent(identity)) return
      lastReferences = { scopeKey, items: [] }
      setReferenceResult({ scopeKey, items: [], status: 'error' })
    })
    return () => { cancelled = true }
  }, [isOpen, scopeKey])
  const excludedPath = normalizeWorkspacePath(excludeWorkspacePath || '')
  const allReferences = useMemo(() => {
    const raw = isOpen && referenceResult?.scopeKey === scopeKey ? referenceResult?.items || [] : []
    const items = excludedPath ? raw.filter(item => normalizeWorkspacePath(item.workspacePath) !== excludedPath) : raw
    return agentWorksFirst(items, item => !!item.agentWorks)
  }, [isOpen, referenceResult, scopeKey, excludedPath])

  const query = searchQuery.toLowerCase().trim()

  // Internal paths stay searchable but are displayed only on hover.
  const filteredCrews = useMemo(() => {
    if (!query) return allReferences

    const filtered = allReferences.filter(project =>
      project.label.toLowerCase().includes(query) ||
      !!project.secondaryLabel?.toLowerCase().includes(query) ||
      project.workspacePath.toLowerCase().includes(query))

    filtered.sort((a, b) => {
      const aExact = a.label.toLowerCase() === query
      const bExact = b.label.toLowerCase() === query
      if (aExact && !bExact) return -1
      if (!aExact && bExact) return 1
      const aStarts = a.label.toLowerCase().startsWith(query)
      const bStarts = b.label.toLowerCase().startsWith(query)
      if (aStarts && !bStarts) return -1
      if (!aStarts && bStarts) return 1
      return a.label.localeCompare(b.label)
    })

    return filtered
  }, [allReferences, query])

  // Keep an explicit selection by Crew ID when the refreshed list arrives.
  const selectedIndex = filteredCrews.findIndex(item => referenceRowKey(item) === selectedPresetId)
  const selectedCrew = filteredCrews[selectedIndex]
  const activeOption = isOpen && selectedCrew ? rowID(selectedCrew) : undefined
  useLayoutEffect(() => { onActiveOptionChange(activeOption) }, [activeOption, onActiveOptionChange])
  useEffect(() => {
    if (selectedIndex >= 0) listRef.current?.querySelectorAll<HTMLElement>('[role="option"]')[selectedIndex]?.scrollIntoView({ block: 'nearest' })
  }, [activeOption, selectedIndex])

  const status = referenceResult?.scopeKey === scopeKey ? referenceResult?.status : undefined
  const loadingReferences = !status || status === 'loading'
  const loadError = status === 'error'
  const nothingMatches = isOpen && !loadError && !loadingReferences && !!query && filteredCrews.length === 0
  // Only consume a key from the composer when it does something in the picker.
  // Enter with no explicit selection still sends the person's draft as written.
  useImperativeHandle(ref, () => ({
    handleKeyDown(event) {
      if (!isOpen || nothingMatches || !isPlainPickerKey(event)) return false
      if (event.key === 'Escape') {
        onClose()
        return true
      }
      if (event.key === 'Enter' && selectedCrew) {
        onSelectCrew(selectedCrew)
        return true
      }
      if ((event.key === 'ArrowDown' || event.key === 'ArrowUp') && filteredCrews.length) {
        const down = event.key === 'ArrowDown'
        const last = filteredCrews.length - 1
        const next = selectedIndex < 0 ? (down ? 0 : last) : Math.max(0, Math.min(last, selectedIndex + (down ? 1 : -1)))
        setSelectedPresetId(referenceRowKey(filteredCrews[next]))
        return true
      }
      return false
    },
  }))

  useEffect(() => {
    if (nothingMatches) onClose()
  }, [nothingMatches, onClose])

  if (!isOpen || nothingMatches) return null

  return (
    <div
      className="fixed z-50 w-[min(400px,calc(100vw-16px))] rounded-lg border border-border bg-popover text-popover-foreground shadow-lg"
      style={{ bottom: `${position.bottom}px`, left: `${position.left}px` }}
    >
      {/* Header */}
      <div className="px-3 py-2 border-b border-border bg-secondary">
        <div className="flex items-center gap-2">
          <Layers className="w-4 h-4 text-muted-foreground" />
          <span className="text-sm font-medium">References</span>
        </div>
      </div>

      {/* Crew list */}
      <div ref={listRef} id={listId} role="listbox" aria-label="References" className="overflow-y-auto max-h-64">
        {filteredCrews.length === 0 ? (
          <div className="px-3 py-4 text-center text-muted-foreground text-sm">
            {loadingReferences ? 'Loading references… You can still send.' : loadError ? 'Could not load references. You can still send.' : 'No available projects. You can still send.'}
          </div>
        ) : (
          filteredCrews.map(crew => (
            <div
              key={crew.presetId}
              id={rowID(crew)}
              role="option"
              aria-label={crew.label}
              aria-selected={selectedCrew === crew}
              title={[crew.secondaryLabel, crew.shared ? 'Shared with you · Open from Artifacts' : '', crew.workspacePath].filter(Boolean).join('\n') || crew.label}
              className={`px-3 py-2 cursor-pointer flex items-center gap-2 text-sm transition-colors ${
                selectedCrew === crew
                  ? 'bg-primary/10 text-primary border-l-2 border-primary'
                  : 'hover:bg-secondary'
              }`}
              onMouseDown={e => e.preventDefault()}
              onClick={() => onSelectCrew(crew)}
            >
              {crew.agentWorks ? <AgentWorksIcon /> : <EntityIdentityIcon icon={crew.icon} label={crew.label} />}
              <div className="flex-1 min-w-0">
                <div className="flex min-w-0 items-center gap-2">
                  <div className="truncate font-medium">{crew.label}</div>
                  {crew.shared && <span className="shrink-0 text-[10px] text-muted-foreground">Shared</span>}
                </div>
                {crew.agentWorks && <div className="truncate text-xs text-muted-foreground">{AGENTWORKS_SUBTITLE}</div>}
              </div>
            </div>
          ))
        )}
      </div>
      {filteredCrews.length > 0 && loadingReferences && (
        <div className="border-t border-border px-3 py-1.5 text-xs text-muted-foreground" role="status">
          Loading references…
        </div>
      )}

      {/* Footer */}
      <div className="px-3 py-2 border-t border-border bg-secondary text-xs text-muted-foreground">
        ↑↓ choose · Enter to {selectedCrew ? 'select' : 'send'} · Esc to close
      </div>
    </div>
  )
}

export default CrewReferencePicker

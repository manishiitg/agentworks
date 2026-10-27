import React, { useState, useEffect, useLayoutEffect, useRef, useMemo, useId } from 'react'
import { Layers, Search } from 'lucide-react'
import { workflowManifestApi } from '../services/api'
import { useAuthStore } from '../stores/useAuthStore'
import { loadProductProjects } from '../platform/chat/productProjects'
import { WORK_PROFILE_ID, WORK_PROJECTS_ROOT } from '../products/work/workData'
import { EntityIdentityIcon } from './ui/EntityIdentityIcon'
import { AGENTWORKS_SUBTITLE, AgentWorksIcon, agentWorksFirst, isOwnAgentWorks } from './ui/AgentWorksIdentity'
import { isPlainPickerKey } from '../utils/composerReferences'
import { WorkMark } from '../products/work/WorkMark'
import { RunloopMark } from './branding/RunloopLogo'

interface WorkflowItem {
  presetId: string
  label: string
  workspacePath: string
  kind: 'workflow' | 'crew'
  icon?: string
  secondaryLabel?: string
  shared?: boolean
  /** The person's own AgentWorks (server role "agentworks"): own icon, listed first. */
  agentWorks?: boolean
}

const referenceRowKey = (item: WorkflowItem) => item.presetId

interface WorkflowSelectionDialogProps {
  isOpen: boolean
  onClose: (reason?: 'delete-trigger') => void
  onSelectWorkflow: (workflow: WorkflowItem) => void
  searchQuery: string
  position: { bottom: number; left: number }
}

let lastReferences: { userID?: string; workflows: WorkflowItem[]; crews: WorkflowItem[] } | null = null

export const WorkflowSelectionDialog: React.FC<WorkflowSelectionDialogProps> = ({
  isOpen,
  onClose,
  onSelectWorkflow,
  searchQuery: externalSearchQuery,
  position,
}) => {
  const listID = useId()
  const rowID = (item: WorkflowItem) => `${listID}-${encodeURIComponent(referenceRowKey(item))}`
  const [selectedPresetId, setSelectedPresetId] = useState<string | null>(null)
  const [localQuery, setLocalQuery] = useState('')
  const searchInputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const userID = useAuthStore(state => state.user?.id)

  // Refs so keyboard handler always has fresh values
  const selectedIndexRef = useRef(0)
  const onCloseRef = useRef(onClose)
  const onSelectWorkflowRef = useRef(onSelectWorkflow)
  useEffect(() => { onCloseRef.current = onClose }, [onClose])
  useEffect(() => { onSelectWorkflowRef.current = onSelectWorkflow }, [onSelectWorkflow])

  // Sync external search query into local input
  useEffect(() => {
    if (isOpen) setLocalQuery(externalSearchQuery)
  }, [externalSearchQuery, isOpen])

  // Auto-focus on open, reset on close
  useEffect(() => {
    if (isOpen) {
      setSelectedPresetId(null)
      searchInputRef.current?.focus()
    } else {
      setLocalQuery('')
      setSelectedPresetId(null)
    }
  }, [isOpen])

  const [referenceResult, setReferenceResult] = useState<{ userID?: string; items: WorkflowItem[]; pending: number } | null>(null)
  const [loadError, setLoadError] = useState(false)
  // Show this account's last lists at once, so typing after # finds a project without waiting, and fetch fresh
  // permission-filtered lists on every opening; each fresh list replaces its cached half as it arrives. The cache
  // is per account and lives only in this page.
  useEffect(() => {
    let cancelled = false
    const cached = lastReferences?.userID === userID ? lastReferences : null
    setReferenceResult(cached ? { userID, items: [...cached.workflows, ...cached.crews], pending: 2 } : null)
    setLoadError(false)
    if (!isOpen) return
    let workflows: WorkflowItem[] = cached?.workflows ?? []
    let crewProjects: WorkflowItem[] = cached?.crews ?? []
    let pending = 2
    const finish = () => {
      pending -= 1
      if (cancelled) return
      lastReferences = { userID, workflows, crews: crewProjects }
      setReferenceResult({ userID, items: [...workflows, ...crewProjects], pending })
    }
    const failed = () => { if (!cancelled) setLoadError(true) }
    // Each fresh, server-authorized list can be used immediately. A slow
    // discovery service must not hold the other product's results hostage.
    void workflowManifestApi.listWorkflowManifests().then(response => {
      workflows = (response.workflows || []).map(workflow => ({
        presetId: `workflow:${workflow.manifest.id || workflow.workspace_path}`,
        label: workflow.manifest.label,
        workspacePath: workflow.workspace_path,
        kind: 'workflow',
        shared: workflow.my_access === 'read',
        icon: workflow.manifest.icon,
      }))
    }).catch(() => { workflows = []; failed() }).finally(finish)
    void loadProductProjects(WORK_PROJECTS_ROOT, WORK_PROFILE_ID, { includeOwnSharedProjects: true }).then(crews => {
      crewProjects = crews.map(crew => {
        const identityName = crew.identity?.name?.trim() || crew.title
        const agentWorks = isOwnAgentWorks(crew)
        return {
          presetId: `crew:${crew.id}`,
          label: identityName,
          secondaryLabel: agentWorks ? AGENTWORKS_SUBTITLE : identityName === crew.title ? undefined : crew.title,
          agentWorks,
          workspacePath: crew.workspacePath,
          kind: 'crew',
          shared: !!crew.shared && crew.shared.canBuild !== true,
          icon: crew.identity?.icon,
        }
      })
    }).catch(() => { crewProjects = []; failed() }).finally(finish)
    return () => { cancelled = true }
  }, [isOpen, userID])
  const allReferences = useMemo(() => {
    const items = isOpen && referenceResult?.userID === userID ? referenceResult?.items || [] : []
    return agentWorksFirst(items, item => !!item.agentWorks)
  }, [isOpen, referenceResult, userID])

  const query = localQuery.toLowerCase().trim()
  const projectMatches = (project: WorkflowItem) => !query ||
    project.label.toLowerCase().includes(query) ||
    !!project.secondaryLabel?.toLowerCase().includes(query) ||
    project.kind.includes(query) ||
    project.workspacePath.toLowerCase().includes(query)

  // Internal paths stay searchable but are displayed only on hover.
  const filteredWorkflows = (() => {
    if (!localQuery.trim()) return allReferences

    const filtered = allReferences.filter(projectMatches)

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
  })()

  const navigationRows = filteredWorkflows
  const selectedIndex = Math.max(0, navigationRows.findIndex(item => referenceRowKey(item) === selectedPresetId))
  const selectedWorkflow = navigationRows[selectedIndex]

  // Preserve the selected row when another permission-filtered list arrives.
  const filteredWorkflowsRef = useRef(navigationRows)
  useLayoutEffect(() => {
    filteredWorkflowsRef.current = navigationRows
    selectedIndexRef.current = selectedIndex
  }, [navigationRows, selectedIndex])

  useEffect(() => { setSelectedPresetId(null) }, [localQuery])
  const moveSelection = (delta: number) => {
    const items = filteredWorkflowsRef.current
    const nextIndex = Math.max(0, Math.min(selectedIndexRef.current + delta, items.length - 1))
    setSelectedPresetId(items[nextIndex] ? referenceRowKey(items[nextIndex]) : null)
  }

  const handleEnter = () => {
    const item = filteredWorkflowsRef.current[selectedIndexRef.current]
    if (item) onSelectWorkflowRef.current(item)
  }

  // Scroll selected item into view
  useEffect(() => {
    if (listRef.current && selectedIndex >= 0) {
      const el = listRef.current.querySelectorAll<HTMLElement>('[data-reference-row]')[selectedIndex]
      el?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
    }
  }, [selectedIndex])

  // Stable document-level keydown listener using refs
  useEffect(() => {
    if (!isOpen) return

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onCloseRef.current()
      } else if (e.key === 'ArrowDown') {
        e.preventDefault()
        moveSelection(1)
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        moveSelection(-1)
      } else if (e.key === 'Enter') {
        e.preventDefault()
        handleEnter()
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [isOpen])

  // Nothing matches the typed reference (e.g. a ticket number like #1764):
  // close instead of holding the composer's Enter behind an empty picker.
  const loadingReferences = !referenceResult || referenceResult.pending > 0
  const nothingMatches = isOpen && !loadError && !loadingReferences && !!query && filteredWorkflows.length === 0
  useEffect(() => {
    if (nothingMatches) onCloseRef.current()
  }, [nothingMatches])

  if (!isOpen || nothingMatches) return null

  return (
    <div
      className="fixed z-50 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg shadow-lg min-w-[300px] max-w-[400px]"
      style={{ bottom: `${position.bottom}px`, left: `${position.left}px` }}
    >
      {/* Header */}
      <div className="px-3 py-2 border-b border-border bg-secondary">
        <div className="flex items-center gap-2">
          <Layers className="w-4 h-4 text-muted-foreground" />
          <span className="text-sm font-medium">References</span>
        </div>
      </div>

      {/* Search input */}
      <div className="px-3 py-2 border-b border-border">
        <div className="relative">
          <Search className="absolute left-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground pointer-events-none" />
          <input
            ref={searchInputRef}
            type="text"
            role="combobox"
            aria-label="Search references"
            aria-expanded="true"
            aria-controls={listID}
            aria-activedescendant={selectedWorkflow ? rowID(selectedWorkflow) : undefined}
            placeholder="Search projects…"
            value={localQuery}
            onChange={e => setLocalQuery(e.target.value)}
            onKeyDown={e => {
              // The popup owns focus, but an empty search still belongs to
              // the # in the composer. Let Backspace remove that trigger.
              if (e.key === 'Backspace' && !e.currentTarget.value && isPlainPickerKey(e.nativeEvent)) {
                e.preventDefault(); e.stopPropagation(); onCloseRef.current('delete-trigger')
              }
              else if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); handleEnter() }
              else if (e.key === 'ArrowDown') { e.preventDefault(); e.stopPropagation(); moveSelection(1) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); e.stopPropagation(); moveSelection(-1) }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onCloseRef.current() }
            }}
            className="w-full pl-7 pr-2 py-1.5 text-xs rounded-md border border-border bg-background text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-primary"
          />
        </div>
      </div>

      {/* Workflow list */}
      <div ref={listRef} id={listID} role="listbox" aria-label="Projects" className="overflow-y-auto max-h-64">
        {filteredWorkflows.length === 0 ? (
          <div className="px-3 py-4 text-center text-muted-foreground text-sm">
            {loadingReferences ? 'Loading accessible references…' : loadError ? 'Unable to load accessible references. Close and reopen to retry.' : localQuery ? 'No references found' : 'No accessible workflows or Crews available'}
          </div>
        ) : (
          filteredWorkflows.map(workflow => (
            <div key={workflow.presetId}>
            <div
              id={rowID(workflow)}
              role="option"
              aria-label={workflow.label}
              aria-selected={selectedWorkflow === workflow}
              title={[workflow.secondaryLabel, workflow.shared ? 'Shared with you · Open from Dashboards' : '', workflow.workspacePath].filter(Boolean).join('\n') || workflow.label}
              data-reference-row
              className={`px-3 py-2 cursor-pointer flex items-center gap-2 text-sm transition-colors ${
                selectedWorkflow === workflow
                  ? 'bg-primary/10 text-primary border-l-2 border-primary'
                  : 'hover:bg-secondary'
              }`}
              onMouseEnter={() => setSelectedPresetId(referenceRowKey(workflow))}
              onMouseDown={e => { e.preventDefault(); onSelectWorkflow(workflow) }}
            >
              {workflow.agentWorks ? <AgentWorksIcon /> : <EntityIdentityIcon icon={workflow.icon} label={workflow.label} />}
              <div className="flex-1 min-w-0">
                <div className="flex min-w-0 items-center gap-2">
                  <div className="truncate font-medium">{workflow.label}</div>
                  {workflow.kind === 'crew'
                    ? <WorkMark title="Crew" className="h-4 w-4 shrink-0" />
                    : <RunloopMark title="Workflow" className="h-4 w-4 shrink-0" />}
                  {workflow.shared && <span className="shrink-0 text-[10px] text-muted-foreground">Shared</span>}
                </div>
                {workflow.agentWorks && <div className="truncate text-xs text-muted-foreground">{AGENTWORKS_SUBTITLE}</div>}
              </div>
            </div>
            </div>
          ))
        )}
      </div>
      {filteredWorkflows.length > 0 && (loadingReferences || loadError) && (
        <div className="border-t border-border px-3 py-1.5 text-xs text-muted-foreground" role="status">
          {loadingReferences ? 'Loading more references…' : 'Some references could not be loaded. Reopen to retry.'}
        </div>
      )}

      {/* Footer */}
      <div className="px-3 py-2 border-t border-border bg-secondary text-xs text-muted-foreground">
        <div className="flex items-center justify-between">
          <span>↑↓ navigate</span>
          <span>Enter to select • Esc to close</span>
        </div>
      </div>
    </div>
  )
}

export default WorkflowSelectionDialog

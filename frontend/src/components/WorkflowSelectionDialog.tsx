import React, { useState, useEffect, useLayoutEffect, useRef, useMemo, useId } from 'react'
import { Layers, Search } from 'lucide-react'
import { listFunctionReferences, type FunctionReference } from '../api/functionReferences'
import { workflowManifestApi } from '../services/api'
import { useAuthStore } from '../stores/useAuthStore'
import { loadProductProjects } from '../platform/chat/productProjects'
import { WORK_PROFILE_ID, WORK_PROJECTS_ROOT } from '../products/work/workData'
import { EntityIdentityIcon } from './ui/EntityIdentityIcon'
import { PulseIcon } from './workflow/pulseIcon'

interface WorkflowItem {
  presetId: string
  label: string
  workspacePath: string
  kind: 'workflow' | 'crew' | 'pulse'
  icon?: string
  secondaryLabel?: string
  functionTarget?: string
  functionName?: string
}

const referenceRowKey = (item: WorkflowItem) => JSON.stringify([item.presetId, item.functionName ?? null])

interface FunctionResult {
  loading: boolean
  items: FunctionReference[]
  error?: string
}

// Discovery is fresh for each opening/account. Limit concurrent requests so
// searching all accessible projects does not flood the metadata endpoint.
interface FunctionDiscovery {
  cancelled: boolean
  active: number
  queue: WorkflowItem[]
  requested: Set<string>
}

interface WorkflowSelectionDialogProps {
  isOpen: boolean
  onClose: () => void
  onSelectWorkflow: (workflow: WorkflowItem) => void
  searchQuery: string
  position: { bottom: number; left: number }
  // In a Builder chat, the workflow's Pulse is the first entry: #pulse asks it.
  includePulse?: boolean
}

export const WorkflowSelectionDialog: React.FC<WorkflowSelectionDialogProps> = ({
  isOpen,
  onClose,
  onSelectWorkflow,
  searchQuery: externalSearchQuery,
  position,
  includePulse = false
}) => {
  const listID = useId()
  const rowID = (item: WorkflowItem) => `${listID}-${encodeURIComponent(referenceRowKey(item))}`
  const [selectedPresetId, setSelectedPresetId] = useState<string | null>(null)
  const [localQuery, setLocalQuery] = useState('')
  const searchInputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const userID = useAuthStore(state => state.user?.id)
  const functionDiscovery = useMemo<FunctionDiscovery>(() => ({ cancelled: false, active: 0, queue: [], requested: new Set() }), [isOpen, userID])
  const [functionState, setFunctionState] = useState<{ discovery: FunctionDiscovery; results: Record<string, FunctionResult> } | null>(null)
  const functionResults = functionState?.discovery === functionDiscovery ? functionState.results : {}

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

  useEffect(() => {
    functionDiscovery.cancelled = false
    return () => { functionDiscovery.cancelled = true }
  }, [functionDiscovery])
  const [referenceResult, setReferenceResult] = useState<{ userID?: string; items: WorkflowItem[]; pending: number } | null>(null)
  const [loadError, setLoadError] = useState(false)
  // Fetch fresh permission-filtered workflows and user-scoped Crew projects
  // for every opening. Never reuse stale entries after an account change,
  // access revocation, or failed authorization check.
  useEffect(() => {
    let cancelled = false
    setReferenceResult(null)
    setLoadError(false)
    if (!isOpen) return
    let workflows: WorkflowItem[] = []
    let crewProjects: WorkflowItem[] = []
    let pending = 2
    const finish = () => {
      pending -= 1
      if (!cancelled) setReferenceResult({ userID, items: [...workflows, ...crewProjects], pending })
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
        icon: workflow.manifest.icon,
        functionTarget: `workflow:${workflow.manifest.id || workflow.workspace_path}`,
      }))
    }).catch(failed).finally(finish)
    void loadProductProjects(WORK_PROJECTS_ROOT, WORK_PROFILE_ID, { includeOwnSharedProjects: true }).then(crews => {
      crewProjects = crews.map(crew => {
        const identityName = crew.identity?.name?.trim() || crew.title
        return {
          presetId: `crew:${crew.id}`,
          label: identityName,
          secondaryLabel: identityName === crew.title ? undefined : crew.title,
          workspacePath: crew.workspacePath,
          kind: 'crew',
          icon: crew.identity?.icon,
          functionTarget: `workspace:${crew.product}:${crew.id}`,
        }
      })
    }).catch(failed).finally(finish)
    return () => { cancelled = true }
  }, [isOpen, userID])
  const allReferences = useMemo(() => {
    const items = isOpen && referenceResult?.userID === userID ? referenceResult?.items || [] : []
    if (!isOpen || !includePulse) return items
    return [{ presetId: 'pulse', label: 'pulse', secondaryLabel: 'Ask this workflow\'s Pulse', workspacePath: '', kind: 'pulse' as const }, ...items]
  }, [includePulse, isOpen, referenceResult, userID])

  useEffect(() => {
    if (!isOpen) return
    const publish = (project: string, result: FunctionResult) => {
      if (functionDiscovery.cancelled) return
      setFunctionState(previous => ({
        discovery: functionDiscovery,
        results: { ...(previous?.discovery === functionDiscovery ? previous.results : {}), [project]: result },
      }))
    }
    for (const project of allReferences) {
      if (!project.functionTarget || functionDiscovery.requested.has(project.presetId)) continue
      functionDiscovery.requested.add(project.presetId)
      functionDiscovery.queue.push(project)
      publish(project.presetId, { loading: true, items: [] })
    }
    const pump = () => {
      while (!functionDiscovery.cancelled && functionDiscovery.active < 4 && functionDiscovery.queue.length) {
        const project = functionDiscovery.queue.shift()!
        functionDiscovery.active += 1
        void listFunctionReferences(project.functionTarget!).then(items => {
          publish(project.presetId, { loading: false, items })
        }).catch(() => {
          publish(project.presetId, { loading: false, items: [], error: 'Functions are unavailable. Reopen to retry.' })
        }).finally(() => {
          functionDiscovery.active -= 1
          pump()
        })
      }
    }
    pump()
  }, [allReferences, functionDiscovery, isOpen])

  const query = localQuery.toLowerCase().trim()
  const projectMatches = (project: WorkflowItem) => !query ||
    project.label.toLowerCase().includes(query) ||
    !!project.secondaryLabel?.toLowerCase().includes(query) ||
    project.kind.includes(query) ||
    project.workspacePath.toLowerCase().includes(query)
  const visibleFunctions = (project: WorkflowItem) => (functionResults[project.presetId]?.items || []).filter(fn =>
    projectMatches(project) || fn.name.toLowerCase().includes(query) || fn.description?.toLowerCase().includes(query),
  )

  // A function-name match retains its project as context, even when that
  // project's name does not match. Internal paths stay searchable but are
  // displayed only on hover.
  const filteredWorkflows = (() => {
    if (!localQuery.trim()) return allReferences

    const filtered = allReferences.filter(project => projectMatches(project) || visibleFunctions(project).length > 0)

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

  // Projects and their functions share one selection model. Opening function
  // metadata never changes what Enter selects or steals focus from search.
  const navigationRows = filteredWorkflows.flatMap(workflow => [
    workflow,
    ...visibleFunctions(workflow).map(fn => ({ ...workflow, functionName: fn.name })),
  ])
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
  const loadingFunctions = allReferences.some(project => project.functionTarget && (!functionResults[project.presetId] || functionResults[project.presetId].loading))
  const functionError = Object.values(functionResults).some(result => result.error)
  const nothingMatches = isOpen && !loadError && !functionError && !loadingReferences && !loadingFunctions && !!query && filteredWorkflows.length === 0
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
            placeholder="Search projects and functions…"
            value={localQuery}
            onChange={e => setLocalQuery(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); handleEnter() }
              else if (e.key === 'ArrowDown') { e.preventDefault(); e.stopPropagation(); moveSelection(1) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); e.stopPropagation(); moveSelection(-1) }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onCloseRef.current() }
            }}
            className="w-full pl-7 pr-2 py-1.5 text-xs rounded-md border border-border bg-background text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-primary"
          />
        </div>
      </div>

      {/* Workflow list */}
      <div ref={listRef} id={listID} role="listbox" aria-label="Projects and functions" className="overflow-y-auto max-h-64">
        {filteredWorkflows.length === 0 ? (
          <div className="px-3 py-4 text-center text-muted-foreground text-sm">
            {loadingReferences ? 'Loading accessible references…' : loadingFunctions ? 'Searching functions…' : loadError ? 'Unable to load accessible references. Close and reopen to retry.' : functionError ? 'Some functions could not be searched. Reopen to retry.' : localQuery ? 'No references found' : 'No accessible workflows or Crews available'}
          </div>
        ) : (
          filteredWorkflows.map(workflow => (
            <div key={workflow.presetId}>
            <div
              id={rowID(workflow)}
              role="option"
              aria-selected={selectedWorkflow === workflow}
              title={[workflow.secondaryLabel, workflow.workspacePath].filter(Boolean).join('\n') || workflow.label}
              data-reference-row
              className={`px-3 py-2 cursor-pointer flex items-center gap-2 text-sm transition-colors ${
                selectedWorkflow === workflow
                  ? 'bg-primary/10 text-primary border-l-2 border-primary'
                  : 'hover:bg-secondary'
              }`}
              onMouseEnter={() => setSelectedPresetId(referenceRowKey(workflow))}
              onMouseDown={e => { e.preventDefault(); onSelectWorkflow(workflow) }}
            >
              {workflow.kind === 'pulse' ? <PulseIcon className="h-5 w-5 shrink-0 text-primary" aria-hidden="true" /> : <EntityIdentityIcon icon={workflow.icon} label={workflow.label} />}
              <div className="flex-1 min-w-0">
                <div className="flex min-w-0 items-center gap-2">
                  <div className="truncate font-medium">{workflow.label}</div>
                  <span className="shrink-0 rounded border border-border px-1 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{workflow.kind}</span>
                </div>
              </div>
            </div>
            {workflow.functionTarget && (functionResults[workflow.presetId]?.loading || functionResults[workflow.presetId]?.error || visibleFunctions(workflow).length > 0) && <div className="border-b border-border bg-muted/30 py-1 pl-9 pr-3" data-testid="reference-functions">
              {functionResults[workflow.presetId]?.loading || functionResults[workflow.presetId]?.error
                ? <p className="py-2 text-xs text-muted-foreground" role="status">{functionResults[workflow.presetId]?.loading ? 'Loading functions…' : functionResults[workflow.presetId]?.error}</p>
                : visibleFunctions(workflow).map(fn => <button
                    type="button" key={fn.name}
                    id={rowID({ ...workflow, functionName: fn.name })}
                    role="option"
                    tabIndex={-1}
                    aria-label={`${workflow.label}: ${fn.name}`}
                    title={fn.description || fn.name}
                    data-reference-row
                    aria-selected={selectedWorkflow?.presetId === workflow.presetId && selectedWorkflow?.functionName === fn.name}
                    className={`block w-full rounded px-2 py-1.5 text-left ${selectedWorkflow?.presetId === workflow.presetId && selectedWorkflow?.functionName === fn.name ? 'bg-primary/10 text-primary' : 'hover:bg-muted'}`}
                    onMouseEnter={() => setSelectedPresetId(referenceRowKey({ ...workflow, functionName: fn.name }))}
                    onFocus={() => setSelectedPresetId(referenceRowKey({ ...workflow, functionName: fn.name }))}
                    onClick={() => onSelectWorkflow({ ...workflow, functionName: fn.name })}
                  ><span className="block truncate text-xs font-medium">{fn.name}</span></button>)}
            </div>}
            </div>
          ))
        )}
      </div>
      {filteredWorkflows.length > 0 && (loadingReferences || loadingFunctions || loadError || functionError) && (
        <div className="border-t border-border px-3 py-1.5 text-xs text-muted-foreground" role="status">
          {loadingReferences ? 'Loading more references…' : loadingFunctions ? 'Searching functions…' : 'Some references could not be loaded. Reopen to retry.'}
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

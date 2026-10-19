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
  const functionRequest = useRef(0)
  const [functionResult, setFunctionResult] = useState<{ project: string; loading: boolean; items: FunctionReference[]; error?: string } | null>(null)

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

  const userID = useAuthStore(state => state.user?.id)
  useEffect(() => {
    functionRequest.current += 1
    setFunctionResult(null)
    return () => { functionRequest.current += 1 }
  }, [isOpen, userID])
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

  // Filter synchronously
  const filteredWorkflows = useMemo<WorkflowItem[]>(() => {
    if (!localQuery.trim()) return allReferences

    const query = localQuery.toLowerCase().trim()
    const filtered = allReferences.filter(w =>
      w.label.toLowerCase().includes(query) ||
      w.secondaryLabel?.toLowerCase().includes(query) ||
      w.kind.includes(query) ||
      w.workspacePath.toLowerCase().includes(query)
    )

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
  }, [localQuery, allReferences])

  // Projects and their functions share one selection model. Opening function
  // metadata never changes what Enter selects or steals focus from search.
  const navigationRows = useMemo(() => filteredWorkflows.flatMap(workflow => [
    workflow,
    ...(functionResult?.project === workflow.presetId
      ? functionResult.items.map(fn => ({ ...workflow, functionName: fn.name }))
      : []),
  ]), [filteredWorkflows, functionResult])
  const selectedIndex = Math.max(0, navigationRows.findIndex(item => referenceRowKey(item) === selectedPresetId))
  const selectedWorkflow = navigationRows[selectedIndex]
  const selectedProjectId = selectedWorkflow?.presetId
  const selectedFunctionTarget = selectedWorkflow?.functionTarget
  useEffect(() => {
    const request = ++functionRequest.current
    setFunctionResult(null)
    if (!isOpen || !selectedProjectId || !selectedFunctionTarget) return
    setFunctionResult({ project: selectedProjectId, loading: true, items: [] })
    void listFunctionReferences(selectedFunctionTarget).then(items => {
      if (functionRequest.current === request) setFunctionResult({ project: selectedProjectId, loading: false, items })
    }).catch(() => {
      if (functionRequest.current === request) setFunctionResult({ project: selectedProjectId, loading: false, items: [], error: 'Functions are unavailable. Check project access or try again.' })
    })
    return () => { functionRequest.current += 1 }
  }, [isOpen, userID, selectedProjectId, selectedFunctionTarget])

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
  const nothingMatches = isOpen && !loadError && !loadingReferences && !!localQuery.trim() && filteredWorkflows.length === 0
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
            placeholder="Search workflows and Crews..."
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
            {loadingReferences ? 'Loading accessible references…' : loadError ? 'Unable to load accessible references. Close and reopen to retry.' : localQuery ? 'No references found' : 'No accessible workflows or Crews available'}
          </div>
        ) : (
          filteredWorkflows.map(workflow => (
            <div key={workflow.presetId}>
            <div
              id={rowID(workflow)}
              role="option"
              aria-selected={selectedWorkflow === workflow}
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
                {workflow.secondaryLabel && <div className="truncate text-xs text-muted-foreground">{workflow.secondaryLabel}</div>}
                {workflow.workspacePath && <div className="text-xs text-muted-foreground truncate">{workflow.workspacePath}</div>}
              </div>
            </div>
            {functionResult?.project === workflow.presetId && <div className="border-b border-border bg-muted/30 py-1 pl-9 pr-3" data-testid="reference-functions">
              {functionResult.loading || functionResult.error || functionResult.items.length === 0
                ? <p className="py-2 text-xs text-muted-foreground" role="status">{functionResult.loading ? 'Loading functions…' : functionResult.error || 'No published functions yet.'}</p>
                : functionResult.items.map(fn => <button
                    type="button" key={fn.name}
                    id={rowID({ ...workflow, functionName: fn.name })}
                    role="option"
                    data-reference-row
                    aria-selected={selectedWorkflow?.presetId === workflow.presetId && selectedWorkflow?.functionName === fn.name}
                    className={`block w-full rounded px-2 py-1.5 text-left ${selectedWorkflow?.presetId === workflow.presetId && selectedWorkflow?.functionName === fn.name ? 'bg-primary/10 text-primary' : 'hover:bg-muted'}`}
                    onMouseEnter={() => setSelectedPresetId(referenceRowKey({ ...workflow, functionName: fn.name }))}
                    onFocus={() => setSelectedPresetId(referenceRowKey({ ...workflow, functionName: fn.name }))}
                    onClick={() => onSelectWorkflow({ ...workflow, functionName: fn.name })}
                  ><span className="block text-xs font-medium">{fn.name}</span>{fn.description && <span className="line-clamp-2 text-xs text-muted-foreground">{fn.description}</span>}</button>)}
            </div>}
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

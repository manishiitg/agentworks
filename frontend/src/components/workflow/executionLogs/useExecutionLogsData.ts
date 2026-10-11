import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type React from 'react'
import { captureChatIdentity, isChatIdentityCurrent } from '../../../utils/chatIdentity'
import { agentApi } from '../../../services/api'
import type { ExecutionLogsResponse } from '../../../services/api-types'
import { formatLogFileContent, getDefaultRunFolder } from './helpers'

export interface UseExecutionLogsDataArgs {
  workspacePath: string | null
  initialRunFolder: string | null | undefined
  allowDefaultRunFolder?: boolean
  runFolders: string[]
  initialStepId?: string
}

export function useExecutionLogsData({ workspacePath, initialRunFolder, runFolders, allowDefaultRunFolder = true, initialStepId }: UseExecutionLogsDataArgs) {
  const defaultRunFolder = !allowDefaultRunFolder && !initialRunFolder ? '' : getDefaultRunFolder(initialRunFolder, runFolders)
  const runFolderOptions = useMemo(() => {
    if (!defaultRunFolder || runFolders.includes(defaultRunFolder)) return runFolders
    return [defaultRunFolder, ...runFolders]
  }, [defaultRunFolder, runFolders])

  const [loading, setLoading] = useState(false)
  const [logs, setLogs] = useState<ExecutionLogsResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [expandedSteps, setExpandedSteps] = useState<Set<string>>(new Set(initialStepId ? [initialStepId] : []))
  const [expandedValidations, setExpandedValidations] = useState<Set<string>>(new Set())
  const [expandedExecutions, setExpandedExecutions] = useState<Set<string>>(new Set())
  const [expandedArchived, setExpandedArchived] = useState<Set<string>>(new Set())
  const [selectedRunFolder, setSelectedRunFolder] = useState<string>(defaultRunFolder)
  const [stepSearchQueries, setStepSearchQueries] = useState<Record<string, string>>({})
  // Route-wise grouping (PLAT-259 follow-up): distinct routing/branch
  // ("route" major-fork concept) routes actually taken in this run, so
  // steps can be filtered down to just one route's chain. Keyed by
  // `${route_step_id}::${route_id}` rather than route_id alone, since
  // route_id strings ("yes"/"no"/etc.) can collide across two unrelated
  // routing/branch steps in the same plan.
  const [routeFilterKey, setRouteFilterKey] = useState<string | null>(null)
  const routingRouteGroups = useMemo(() => {
    const seen = new Map<string, { key: string; routeStepTitle: string; routeName: string }>()
    Object.values(logs?.steps || {}).forEach(stepLogs => {
      if (stepLogs.route_kind !== 'routing' || !stepLogs.route_id || !stepLogs.route_step_id) return
      const key = `${stepLogs.route_step_id}::${stepLogs.route_id}`
      if (!seen.has(key)) {
        seen.set(key, {
          key,
          routeStepTitle: stepLogs.route_step_title || stepLogs.route_step_id,
          routeName: stepLogs.route_name || stepLogs.route_id,
        })
      }
    })
    return Array.from(seen.values())
  }, [logs])
  
  // State for inline file viewing
  const [expandedFiles, setExpandedFiles] = useState<Set<string>>(new Set())
  const [fileContents, setFileContents] = useState<Record<string, string>>({})
  const [loadingFiles, setLoadingFiles] = useState<Set<string>>(new Set())
  const scope = JSON.stringify([workspacePath, selectedRunFolder])
  const currentScope = useRef(scope)
  currentScope.current = scope
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const expandedFilePaths = useRef(expandedFiles)
  expandedFilePaths.current = expandedFiles
  const requestVersion = useRef(0)
  const focusedStepId = expandedSteps.values().next().value as string | undefined
  // Shrinks the sticky "Back to all steps" bar once the user scrolls past it,
  // so it stops eating vertical space while reading step-detail content below.
  // Uses two different thresholds (hysteresis) rather than one: a single
  // trigger point flickers when scrollTop settles right at the boundary
  // (inertial rebound, a small trackpad nudge), rapidly toggling the bar
  // between sizes. Shrinking requires scrolling further than re-expanding
  // requires scrolling back, so scroll jitter near either point can't flip it.
  const [stepDetailScrolled, setStepDetailScrolled] = useState(false)
  const handleStepDetailScroll = useCallback((event: React.UIEvent<HTMLDivElement>) => {
    const scrollTop = event.currentTarget.scrollTop
    setStepDetailScrolled(prev => (prev ? scrollTop > 4 : scrollTop > 24))
  }, [])
  useEffect(() => {
    setStepDetailScrolled(false)
  }, [focusedStepId])

  // Update selected run folder when prop changes
  useEffect(() => {
    setSelectedRunFolder(defaultRunFolder)
  }, [defaultRunFolder])

  // A route filter from one run's routes rarely means anything for another run
  useEffect(() => {
    setRouteFilterKey(null)
  }, [selectedRunFolder])

  useEffect(() => {
    setLogs(null)
    setError(null)
    setLoading(false)
    expandedFilePaths.current = new Set()
    setExpandedFiles(new Set())
    setFileContents({})
    setLoadingFiles(new Set())
    if (workspacePath && selectedRunFolder) {
      setExpandedSteps(new Set(initialStepId ? [initialStepId] : []))
      setExpandedValidations(new Set())
      setExpandedExecutions(new Set())
      setExpandedArchived(new Set())
      loadLogs()
    } else {
      setLogs(null)
      setError(null)
      setExpandedFiles(new Set())
      setFileContents({})
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspacePath, selectedRunFolder, initialStepId])

  // Memoized on the two inputs it reads, so the poll effect below can list it
  // honestly: its identity changes exactly when workspacePath/selectedRunFolder
  // do, which are the same moments the poll re-armed before.
  const loadLogs = useCallback(async (options?: { silent?: boolean }) => {
    if (!workspacePath || !selectedRunFolder) return

    const requestScope = JSON.stringify([workspacePath, selectedRunFolder])
    if (currentScope.current !== requestScope) return
    const identity = captureChatIdentity()
    const version = ++requestVersion.current
    const owns = () => mounted.current && isChatIdentityCurrent(identity) && currentScope.current === requestScope && requestVersion.current === version
    if (!options?.silent) setLoading(true)
    setError(null)
    try {
      // Use selected run folder
      const data = await agentApi.getExecutionLogs(workspacePath, selectedRunFolder)
      if (!owns()) return
      setLogs(data)
      // Re-read only files the person has opened, so a running conversation
      // updates rather than keeping the first cached snapshot indefinitely.
      await Promise.all(Array.from(expandedFilePaths.current).map(async path => {
        try {
          const content = await agentApi.getLogFile(path)
          if (owns() && expandedFilePaths.current.has(path)) setFileContents(prev => ({...prev, [path]: formatLogFileContent(content)}))
        } catch { /* Keep readable evidence during a transient file-write gap. */ }
      }))
    } catch (err) {
      if (!owns()) return
      console.error('Failed to load execution logs:', err)
      const responseBody = (err as { response?: { data?: unknown } })?.response?.data
      const detail = typeof responseBody === 'string'
        ? responseBody
        : responseBody && typeof responseBody === 'object' && 'error' in responseBody && typeof responseBody.error === 'string'
          ? responseBody.error
          : err instanceof Error ? err.message : ''
      setError(detail ? `Failed to load execution logs: ${detail}` : 'Failed to load execution logs')
    } finally {
      if (owns()) setLoading(false)
    }
  }, [workspacePath, selectedRunFolder])

  useEffect(() => {
    if (!workspacePath || !selectedRunFolder) return

    const intervalId = window.setInterval(() => {
      loadLogs({ silent: true })
    }, 2500)

    return () => window.clearInterval(intervalId)
  }, [workspacePath, selectedRunFolder, loadLogs])

  const toggleStep = (stepId: string) => {
    setExpandedSteps(prev => {
      if (prev.has(stepId)) {
        setExpandedExecutions(new Set())
        setExpandedArchived(new Set())
        setExpandedFiles(new Set())
        return new Set()
      }

      setExpandedExecutions(new Set())
      setExpandedArchived(new Set())
      setExpandedFiles(new Set())

      {
        // Auto-expand latest execution attempt
        const stepLogs = logs?.steps[stepId]
        if (stepLogs && stepLogs.executions && stepLogs.executions.length > 0) {
          const latest = stepLogs.executions[stepLogs.executions.length - 1]
          const execId = `${stepId}-exec-${latest.attempt}-${latest.iteration}`
          setExpandedExecutions(new Set([execId]))
        }
      }
      return new Set([stepId])
    })
  }

  const toggleValidation = (id: string) => {
    setExpandedValidations(prev => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      }
      else {
        next.add(id)
      }
      return next
    })
  }

  const toggleExecution = (id: string) => {
    setExpandedExecutions(prev => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  const toggleArchived = (id: string) => {
    setExpandedArchived(prev => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  // Shared by the manual "View Message & Conversation" toggle and by the
  // auto-expand-on-search effect below -- always ADDS to expandedFiles and
  // loads content if missing, never toggles off. A toggle function called
  // automatically (rather than from a click) risks double-firing under
  // React's dev-mode double-invoked effects and collapsing what it just
  // expanded; this variant has no "off" branch so that risk doesn't exist.
  const ensureFileExpanded = (path: string) => {
    setExpandedFiles(prev => (prev.has(path) ? prev : new Set(prev).add(path)))
    if (fileContents[path] || loadingFiles.has(path)) return
    setLoadingFiles(prev => new Set(prev).add(path))
    const requestScope = scope
    const identity = captureChatIdentity()
    const owns = () => mounted.current && isChatIdentityCurrent(identity) && currentScope.current === requestScope
    agentApi.getLogFile(path).then(content => {
      if (!owns()) return
      const contentStr = formatLogFileContent(content)
      setFileContents(prev => ({ ...prev, [path]: contentStr }))
    }).catch(e => {
      if (!owns()) return
      console.error(e)
      setFileContents(prev => ({ ...prev, [path]: 'Error: Failed to load content' }))
    }).finally(() => {
      if (!owns()) return
      setLoadingFiles(prev => {
        const next = new Set(prev)
        next.delete(path)
        return next
      })
    })
  }

  const toggleFileExpansion = (path: string) => {
    if (expandedFiles.has(path)) {
      setExpandedFiles(prev => {
        const next = new Set(prev)
        next.delete(path)
        return next
      })
      return
    }
    ensureFileExpanded(path)
  }

  // A search hit can be inside a matching execution's own conversation file
  // (a tool call's arguments/result), which stays unfetched until "View
  // Message & Conversation" is clicked -- searching would otherwise still
  // require a manual click per result just to see the hit. Auto-load+expand
  // the conversation for every LLM-attempt execution matching an active
  // search, scoped to steps the user already has open (renderStepContent
  // only runs for expanded steps), so this never fetches for the whole
  // workflow at once.
  useEffect(() => {
    if (!logs) return
    for (const stepId of expandedSteps) {
      const query = stepSearchQueries[stepId]?.trim()
      if (!query) continue
      const stepLogs = logs.steps[stepId]
      for (const exec of stepLogs?.executions || []) {
        if (exec.fast_path === true) continue
        const path = exec.conversation_path
        if (!path || expandedFiles.has(path)) continue
        if (JSON.stringify(exec).toLowerCase().includes(query.toLowerCase())) {
          ensureFileExpanded(path)
        }
      }
    }
    // ensureFileExpanded/expandedFiles/fileContents/loadingFiles intentionally
    // excluded: they change as a RESULT of this effect and re-running on their
    // own change would only ever re-check work already done, not cause a loop,
    // but listing them would re-run this on every fetch completion for no gain.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [logs, expandedSteps, stepSearchQueries])

  return {
    runFolderOptions,
    loading,
    logs,
    error,
    expandedSteps,
    expandedValidations,
    expandedExecutions,
    expandedArchived,
    selectedRunFolder,
    setSelectedRunFolder,
    stepSearchQueries,
    setStepSearchQueries,
    routeFilterKey,
    setRouteFilterKey,
    routingRouteGroups,
    expandedFiles,
    fileContents,
    loadingFiles,
    focusedStepId,
    stepDetailScrolled,
    handleStepDetailScroll,
    loadLogs,
    toggleStep,
    toggleValidation,
    toggleExecution,
    toggleArchived,
    toggleFileExpansion,
  }
}

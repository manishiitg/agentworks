import { useCallback, useEffect, useMemo, useState } from 'react'
import { Activity, AlertTriangle, CheckCircle2, Monitor, RefreshCw, Terminal, Trash2 } from 'lucide-react'
import IconPopover from '../ui/IconPopover'
import { agentApi } from '../../services/api'

interface BrowserProcess {
  pid: number
  cpu: number
  mem_mb: number
  started_at: string
  user_data_dir: string
  type: string
}

interface BrowserSessionTracking {
  browser_session: string
  agent_session: string
  workflow_session: string
  age: string
  idle: string
}

interface WorkflowProcessOwner {
  owner?: string
  workflow_id?: string
  run_id?: string
  step_id?: string
  execution_id?: string
  session_id?: string
}

interface ManagedWorkflowProcess {
  pid: number
  pgid?: number
  ppid?: number
  command: string
  working_dir?: string
  started_at: string
  timeout_sec?: number
  owner?: WorkflowProcessOwner
  status: string
  exit_code?: number
}

interface StaleWorkflowProcess {
  pid: number
  ppid: number
  pgid?: number
  elapsed: number
  command: string
  reason: string
  workflow_id?: string
  run_id?: string
  step_id?: string
}

const HEALTH_POLL_MS = 30_000

const shorten = (value: string | undefined, max = 24) => {
  if (!value) return 'unknown'
  return value.length > max ? `${value.slice(0, max - 3)}...` : value
}

const formatBytesMb = (mb: number) => `${Math.round(mb)} MB`

const formatStartedAt = (value: string | undefined) => {
  if (!value || value === '?') return '?'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

const formatDurationFromNanos = (value: number) => {
  const totalSeconds = Math.max(0, Math.floor(Number(value || 0) / 1_000_000_000))
  const days = Math.floor(totalSeconds / 86400)
  const hours = Math.floor((totalSeconds % 86400) / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${minutes}m`
  return `${minutes}m`
}

const errorMessage = (error: unknown, fallback: string) => {
  const status = (error as { response?: { status?: number } })?.response?.status
  if (status === 404) {
    return 'Runtime monitor unavailable until the workspace server restarts'
  }
  return error instanceof Error ? error.message : fallback
}

export default function RuntimeHealthControl({ embedded = false }: { embedded?: boolean } = {}) {
  const [browserProcesses, setBrowserProcesses] = useState<BrowserProcess[]>([])
  const [browserTracking, setBrowserTracking] = useState<BrowserSessionTracking[]>([])
  const [managedProcesses, setManagedProcesses] = useState<ManagedWorkflowProcess[]>([])
  const [staleProcesses, setStaleProcesses] = useState<StaleWorkflowProcess[]>([])
  const [processThreshold, setProcessThreshold] = useState('2h')
  const [loading, setLoading] = useState(false)
  const [cleaningBrowsers, setCleaningBrowsers] = useState(false)
  const [cleaningProcesses, setCleaningProcesses] = useState(false)
  const [browserError, setBrowserError] = useState<string | null>(null)
  const [processError, setProcessError] = useState<string | null>(null)

  const refreshRuntime = useCallback(async () => {
    setLoading(true)
    const [browserResponse, trackingResponse, processResponse] = await Promise.all([
      agentApi.getBrowserProcesses().catch((error) => {
        setBrowserError(errorMessage(error, 'Failed to fetch browser processes'))
        return null
      }),
      agentApi.getBrowserSessionTracking().catch(() => ({ sessions: [] as BrowserSessionTracking[], count: 0 })),
      agentApi.getWorkflowProcesses().catch((error) => {
        setProcessError(errorMessage(error, 'Failed to fetch workflow processes'))
        return null
      }),
    ])

    if (browserResponse?.success) {
      setBrowserError(null)
      setBrowserProcesses(browserResponse.processes || [])
    }
    setBrowserTracking(trackingResponse.sessions || [])

    if (processResponse?.success) {
      setProcessError(null)
      setManagedProcesses(processResponse.managed || [])
      setStaleProcesses(processResponse.stale || [])
      setProcessThreshold(processResponse.threshold || '2h')
    }
    setLoading(false)
  }, [])

  useEffect(() => {
    void refreshRuntime()
    const interval = window.setInterval(() => {
      void refreshRuntime()
    }, HEALTH_POLL_MS)
    return () => window.clearInterval(interval)
  }, [refreshRuntime])

  const browserSessions = useMemo(() => {
    return browserProcesses.reduce<Record<string, BrowserProcess[]>>((acc, proc) => {
      const key = proc.user_data_dir || 'unknown'
      if (!acc[key]) acc[key] = []
      acc[key].push(proc)
      return acc
    }, {})
  }, [browserProcesses])

  const trackingBySession = useMemo(() => {
    const map = new Map<string, BrowserSessionTracking>()
    for (const session of browserTracking) {
      map.set(session.browser_session, session)
    }
    return map
  }, [browserTracking])

  const browserSessionKeys = Object.keys(browserSessions)
  const browserSessionCount = browserSessionKeys.length
  const trackedWithoutProcess = browserTracking.filter(
    item => !browserSessionKeys.some(session => session.split('/').pop() === item.browser_session)
  )
  const totalCpu = browserProcesses.reduce((sum, process) => sum + process.cpu, 0)
  const totalMem = browserProcesses.reduce((sum, process) => sum + process.mem_mb, 0)
  const attentionCount = browserSessionCount + staleProcesses.length
  const tooltipLabel = `Runtime health: ${browserSessionCount} browser session${browserSessionCount === 1 ? '' : 's'}, ${staleProcesses.length} stale process${staleProcesses.length === 1 ? '' : 'es'}, ${managedProcesses.length} running process${managedProcesses.length === 1 ? '' : 'es'}`

  const cleanupBrowsers = async () => {
    setCleaningBrowsers(true)
    try {
      await agentApi.cleanupBrowserProcesses()
      await refreshRuntime()
    } finally {
      setCleaningBrowsers(false)
    }
  }

  const cleanupBrowserGroup = async (processes: BrowserProcess[]) => {
    await agentApi.cleanupBrowserProcesses(processes.map(process => process.pid))
    await refreshRuntime()
  }

  const cleanupProcesses = async () => {
    setCleaningProcesses(true)
    try {
      await agentApi.cleanupWorkflowProcesses()
      await refreshRuntime()
    } finally {
      setCleaningProcesses(false)
    }
  }

  const badge = attentionCount > 0 ? (
    <span className={`absolute -top-1.5 -right-1.5 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px] font-semibold text-white ${
      staleProcesses.length > 0 ? 'bg-red-500' : 'bg-blue-500'
    }`}>
      {attentionCount > 9 ? '9+' : attentionCount}
    </span>
  ) : null

  const content = (
      <div className="space-y-4 text-sm text-gray-900 dark:text-gray-100">
        <div className="flex items-center justify-between gap-3">
          <div>
            <div className="text-sm font-semibold">Runtime Health</div>
            <div className="text-xs text-gray-500 dark:text-gray-400">
              Browser sessions and workflow shell processes
            </div>
          </div>
          <button
            type="button"
            onClick={refreshRuntime}
            disabled={loading}
            className="p-1.5 rounded-md text-gray-500 hover:text-gray-700 hover:bg-gray-100 disabled:opacity-50 dark:text-gray-400 dark:hover:text-gray-200 dark:hover:bg-gray-700"
            aria-label="Refresh runtime health"
          >
            <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
          </button>
        </div>

        {(browserError || processError) && (
          <div className="rounded-md border border-red-200 bg-red-50 p-2 text-xs text-red-700 dark:border-red-800 dark:bg-red-950/30 dark:text-red-300">
            {browserError && <div>Browsers: {browserError}</div>}
            {processError && <div>Processes: {processError}</div>}
          </div>
        )}

        <section className="space-y-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
              <Monitor className="w-3.5 h-3.5" />
              Browsers
            </div>
            <div className="text-xs text-gray-500 dark:text-gray-400">
              {browserProcesses.length} proc{browserProcesses.length === 1 ? '' : 's'} · {browserSessionCount} session{browserSessionCount === 1 ? '' : 's'}
            </div>
          </div>

          {browserProcesses.length === 0 ? (
            <div className="rounded-md border border-gray-200 bg-gray-50 p-3 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-900/40 dark:text-gray-400">
              No browser sessions running
            </div>
          ) : (
            <>
              <div className="grid grid-cols-3 gap-2 rounded-md border border-gray-200 bg-gray-50 p-2 text-xs dark:border-gray-700 dark:bg-gray-900/40">
                <div>
                  <div className="text-gray-500 dark:text-gray-400">CPU</div>
                  <div className={`font-medium ${totalCpu > 50 ? 'text-red-500' : ''}`}>{totalCpu.toFixed(1)}%</div>
                </div>
                <div>
                  <div className="text-gray-500 dark:text-gray-400">Memory</div>
                  <div className="font-medium">{formatBytesMb(totalMem)}</div>
                </div>
                <div>
                  <div className="text-gray-500 dark:text-gray-400">Tracked</div>
                  <div className="font-medium">{browserTracking.length}</div>
                </div>
              </div>

              <div className="max-h-44 space-y-2 overflow-y-auto pr-1">
                {Object.entries(browserSessions).map(([sessionId, processes]) => {
                  const sessionName = sessionId.split('/').pop() || sessionId
                  const trackInfo = trackingBySession.get(sessionName)
                  const sessionCpu = processes.reduce((sum, process) => sum + process.cpu, 0)
                  const sessionMem = processes.reduce((sum, process) => sum + process.mem_mb, 0)
                  const mainProcess = processes.find(process => process.type === 'main')
                  const startedAt = mainProcess?.started_at || processes[0]?.started_at

                  return (
                    <div key={sessionId} className="rounded-md border border-gray-200 bg-white p-2 text-xs dark:border-gray-700 dark:bg-gray-800/80">
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0 flex-1">
                          <div className="truncate font-mono text-gray-700 dark:text-gray-200" title={sessionId}>
                            {shorten(sessionName, 28)}
                          </div>
                          <div className="mt-0.5 text-gray-500 dark:text-gray-400">
                            {processes.length} proc{processes.length === 1 ? '' : 's'} · CPU {sessionCpu.toFixed(1)}% · {formatBytesMb(sessionMem)} · {formatStartedAt(startedAt)}
                          </div>
                          {trackInfo ? (
                            <div className="mt-0.5 text-blue-600 dark:text-blue-400">
                              agent {shorten(trackInfo.agent_session, 12)} · age {trackInfo.age} · idle {trackInfo.idle}
                            </div>
                          ) : (
                            <div className="mt-0.5 text-orange-600 dark:text-orange-400">
                              orphaned from session tracking
                            </div>
                          )}
                        </div>
                        <button
                          type="button"
                          onClick={() => cleanupBrowserGroup(processes)}
                          className="rounded p-1 text-red-400 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-950/30"
                          aria-label={`Kill browser session ${sessionName}`}
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      </div>
                    </div>
                  )
                })}
              </div>

              {trackedWithoutProcess.length > 0 && (
                <div className="rounded-md border border-orange-200 bg-orange-50 p-2 text-xs text-orange-700 dark:border-orange-800 dark:bg-orange-950/30 dark:text-orange-300">
                  {trackedWithoutProcess.length} tracked browser session{trackedWithoutProcess.length === 1 ? '' : 's'} without a process
                </div>
              )}

              <button
                type="button"
                onClick={cleanupBrowsers}
                disabled={cleaningBrowsers}
                className="w-full rounded-md bg-red-50 px-3 py-1.5 text-xs font-medium text-red-600 transition-colors hover:bg-red-100 disabled:opacity-50 dark:bg-red-950/30 dark:text-red-300 dark:hover:bg-red-950/50"
              >
                {cleaningBrowsers ? 'Cleaning browsers...' : `Kill All Browser Sessions (${browserSessionCount})`}
              </button>
            </>
          )}
        </section>

        <section className="space-y-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
              <Terminal className="w-3.5 h-3.5" />
              Workflow Processes
            </div>
            <div className="text-xs text-gray-500 dark:text-gray-400">
              {managedProcesses.length} running · {staleProcesses.length} stale
            </div>
          </div>

          {staleProcesses.length === 0 && managedProcesses.length === 0 ? (
            <div className="rounded-md border border-gray-200 bg-gray-50 p-3 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-900/40 dark:text-gray-400">
              No workflow shell processes running
            </div>
          ) : (
            <>
              {staleProcesses.length > 0 && (
                <div className="space-y-2">
                  <div className="flex items-center gap-1.5 text-xs font-medium text-red-600 dark:text-red-300">
                    <AlertTriangle className="w-3.5 h-3.5" />
                    Stale candidates older than {processThreshold}
                  </div>
                  <div className="max-h-36 space-y-2 overflow-y-auto pr-1">
                    {staleProcesses.map(process => (
                      <div key={`stale-${process.pid}`} className="rounded-md border border-red-200 bg-red-50 p-2 text-xs dark:border-red-900/70 dark:bg-red-950/25">
                        <div className="flex items-center justify-between gap-2">
                          <div className="min-w-0">
                            <div className="font-medium text-red-700 dark:text-red-300">
                              PID {process.pid} · {formatDurationFromNanos(process.elapsed)}
                            </div>
                            <div className="truncate text-red-600/80 dark:text-red-300/80" title={process.command}>
                              {shorten(process.workflow_id || 'workflow', 18)} / {shorten(process.step_id || 'step', 18)}
                            </div>
                          </div>
                        </div>
                      </div>
                    ))}
                  </div>
                  <button
                    type="button"
                    onClick={cleanupProcesses}
                    disabled={cleaningProcesses}
                    className="w-full rounded-md bg-red-50 px-3 py-1.5 text-xs font-medium text-red-600 transition-colors hover:bg-red-100 disabled:opacity-50 dark:bg-red-950/30 dark:text-red-300 dark:hover:bg-red-950/50"
                  >
                    {cleaningProcesses ? 'Cleaning processes...' : `Kill Stale Workflow Processes (${staleProcesses.length})`}
                  </button>
                </div>
              )}

              {managedProcesses.length > 0 && (
                <div className="space-y-2">
                  <div className="flex items-center gap-1.5 text-xs font-medium text-gray-600 dark:text-gray-300">
                    <CheckCircle2 className="w-3.5 h-3.5 text-green-500" />
                    Running and tracked
                  </div>
                  <div className="max-h-36 space-y-2 overflow-y-auto pr-1">
                    {managedProcesses.slice(0, 6).map(process => {
                      const owner = process.owner || {}
                      return (
                        <div key={`managed-${process.pid}`} className="rounded-md border border-gray-200 bg-white p-2 text-xs dark:border-gray-700 dark:bg-gray-800/80">
                          <div className="flex items-start justify-between gap-2">
                            <div className="min-w-0">
                              <div className="font-medium text-gray-700 dark:text-gray-200">
                                PID {process.pid} · {process.status}
                              </div>
                              <div className="truncate text-gray-500 dark:text-gray-400" title={process.command}>
                                {shorten(owner.workflow_id || 'workflow', 18)} / {shorten(owner.step_id || 'step', 18)}
                              </div>
                            </div>
                            <div className="shrink-0 text-gray-400 dark:text-gray-500">
                              {formatStartedAt(process.started_at)}
                            </div>
                          </div>
                        </div>
                      )
                    })}
                    {managedProcesses.length > 6 && (
                      <div className="text-center text-xs text-gray-500 dark:text-gray-400">
                        +{managedProcesses.length - 6} more running processes
                      </div>
                    )}
                  </div>
                </div>
              )}
            </>
          )}
        </section>
      </div>
  )
  if (embedded) return content
  return (
    <IconPopover
      icon={<Activity className="w-4 h-4" />}
      label={tooltipLabel}
      navigationLabel="Runtime health"
      badge={badge}
      panelClassName="w-[28rem]"
    >
      {content}
    </IconPopover>
  )
}

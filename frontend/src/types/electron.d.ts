interface ElectronUpdateProgress {
  status: 'downloading' | 'ready' | 'error'
  version?: string
  percent?: number
  transferred?: number
  total?: number
  message?: string
}

interface ElectronBridge {
  isWalkthroughDismissed?: (key: string) => boolean
  dismissWalkthrough?: (key: string) => void
  getApiBaseUrl?: () => string
  getWorkspaceApiBaseUrl?: () => string
  getAppVersion?: () => Promise<string>
  setDockBadge?: (text: string) => void
  setRunningActivity?: (payload: { count: number }) => void
  openExternal?: (url: string) => void
  printToPDF?: (filename: string) => Promise<unknown>
  saveFlowImage?: (payload: Record<string, unknown>) => Promise<unknown>
  captureFlowImage?: (payload: Record<string, unknown>) => Promise<string | null>
  captureRegion?: (payload: Record<string, unknown>) => Promise<string | null>
  pickWorkflowFolder?: () => Promise<string | null>
  logRendererError?: (payload: unknown) => void
  onUpdateProgress?: (callback: (progress: ElectronUpdateProgress) => void) => (() => void)
  restartToInstall?: () => void
}

interface Window {
  electronAPI?: ElectronBridge
  __APP_RUNTIME_CONFIG__?: unknown
  __ZUSTAND_DEVTOOLS__?: Record<string, unknown>
  __longTaskObserver?: PerformanceObserver
  __longTasks?: Array<{ duration: number; time: string }>
  __fpsSamples?: number[]
  getEventListeners?: (target: EventTarget) => Record<string, unknown[]>
  webkitAudioContext?: typeof AudioContext
}

interface Performance {
  readonly memory?: {
    readonly usedJSHeapSize: number
    readonly totalJSHeapSize: number
    readonly jsHeapSizeLimit: number
  }
}

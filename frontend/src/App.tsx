import LlmModalHost from './components/topbar/LlmModalHost'
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
// Code is a second product definition over the same project surface.
import { CODE_PRODUCT } from './products/work/projectProduct'
import { useShallow } from "zustand/react/shallow";
import { useEffect, useCallback, useRef, useState, lazy, Suspense } from "react";
import { ThemeProvider } from "./contexts/ThemeContext.tsx";
import { UpdateProgressToast } from "./components/UpdateProgressToast";
import { GlobalHumanFeedbackPrompt } from "./components/GlobalHumanFeedbackPrompt";
import { resetSessionId } from "./services/api";
import { AuthWrapper } from "./components/AuthWrapper";
import { activateTab } from "./utils/activateTab";
import { Loader2 } from "lucide-react";
import { WorkflowLayout } from "./components/workflow";
import { ModePresetBar } from "./components/ModePresetBar";
import { useAppStore, useMCPStore, useGlobalPresetStore, useWorkflowStore, useChatStore } from "./stores";
import { useModeStore } from "./stores/useModeStore";
import { useProductSurfaceStore } from "./stores/useProductSurfaceStore";
import { useAuthStore } from "./stores/useAuthStore";
import { deploymentDefaultProductSurface, isEnabledProductSurface, intersectAllowedProductSurfaces } from "./products/productSurfaceConfig";
import { useLLMStore } from "./stores/useLLMStore";
import { normalizeEventViewMode, waitForChatStoreHydration, type ChatTab } from "./stores/useChatStore";
import { useLLMDefaults } from "./hooks/useLLMDefaults";
import { TooltipProvider } from "./components/ui/tooltip";
import "./App.css";

// Extend window interface for global functions
declare global {
  interface Window {
    highlightFile?: (filepath: string) => void;
    perfDiag?: () => void;
    apiPerf?: () => void;
    apiLog?: (filter?: string) => void;
  }
}

import { apiLogEntries, summarizeApiTimings } from './utils/apiTiming'
import ToastHost from './components/ui/ToastHost'
import QuickSwitcher from './components/QuickSwitcher'

const queryClient = new QueryClient();

const WorkflowsOverviewPage = lazy(() => import('./components/ActivityPage'))
const SchedulesPage = lazy(() => import('./components/SchedulesPage'))
const VideoStudioSurface = lazy(() => import('./products/video-studio/VideoStudioSurface').then(module => ({ default: module.VideoStudioSurface })))
const DominionSurface = lazy(() => import('./products/dominion/DominionSurface').then(module => ({ default: module.DominionSurface })))
const SparkQuillSurface = lazy(() => import('./products/sparkquill/SparkQuillSurface').then(module => ({ default: module.SparkQuillSurface })))
const WorkSurface = lazy(() => import('./products/work/WorkSurface').then(module => ({ default: module.WorkSurface })))

const FileSurfaceFallback = () => (
  <div className="flex h-full min-h-40 items-center justify-center text-muted-foreground">
    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
    Loading viewer...
  </div>
)

const WORKSPACE_COLLAPSING_POPUP_SELECTOR = [
  '[data-workspace-popup="true"]',
  '[role="dialog"]',
  '[class~="fixed"][class~="inset-0"]'
].join(',')
const WORKSPACE_COLLAPSE_IGNORE_SELECTOR = '[data-workspace-collapse-ignore="true"]'
const READ_ONLY_WORKFLOW_RESTORE_SELECTION_WINDOW_MS = 60 * 1000

const workflowTabSortTimestamp = (tab: ChatTab) => tab.lastAccessedAt ?? tab.createdAt ?? 0

const isInteractiveWorkflowTab = (tab: ChatTab | null | undefined): tab is ChatTab =>
  !!tab && tab.metadata?.mode === 'workflow' && tab.metadata?.isViewOnly !== true

const isRecentExplicitReadOnlyWorkflowTab = (tab: ChatTab | null | undefined): tab is ChatTab => {
  const restoredAt = tab?.metadata?.readOnlyRestoredAt
  return !!tab &&
    tab.metadata?.mode === 'workflow' &&
    tab.metadata?.isViewOnly === true &&
    typeof restoredAt === 'number' &&
    Date.now() - restoredAt <= READ_ONLY_WORKFLOW_RESTORE_SELECTION_WINDOW_MS
}

const hasOpenWorkspaceCollapsingPopup = () => {
  if (typeof document === 'undefined') return false

  return Array.from(document.querySelectorAll<HTMLElement>(WORKSPACE_COLLAPSING_POPUP_SELECTOR))
    .some((element) => {
      if (element.closest(WORKSPACE_COLLAPSE_IGNORE_SELECTOR)) {
        return false
      }
      const style = window.getComputedStyle(element)
      return (
        style.display !== 'none' &&
        style.visibility !== 'hidden' &&
        style.position === 'fixed'
      )
    })
}


function App() {
  const productSurface = useProductSurfaceStore(state => state.productSurface)
  const setProductSurface = useProductSurfaceStore(state => state.setProductSurface)
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)

  // A dedicated deployment is an allowlist, not a visual preference. Correct
  // persisted desktop selections before rendering so a stale SparkQuill or
  // Dominion choice cannot expose a product disabled on this host -- or, now,
  // a product this specific logged-in user isn't granted.
  useEffect(() => {
    const userAllowedSurfaces = intersectAllowedProductSurfaces([productSurface], allowedProducts)
    if (userAllowedSurfaces.length === 0 || !isEnabledProductSurface(productSurface)) {
      const fallback = intersectAllowedProductSurfaces([deploymentDefaultProductSurface()], allowedProducts)
      setProductSurface(fallback[0] ?? deploymentDefaultProductSurface())
    }
  }, [productSurface, setProductSurface, allowedProducts])

  // Store subscriptions
  const { hasCompletedInitialSetup, selectedModeCategory, setModeCategory, completeInitialSetup } = useModeStore(useShallow(state => ({
    hasCompletedInitialSetup: state.hasCompletedInitialSetup,
    selectedModeCategory: state.selectedModeCategory,
    setModeCategory: state.setModeCategory,
    completeInitialSetup: state.completeInitialSetup,
  })))
  const showProviders = useLLMStore(state => state.showLLMModal)
  
  // Load LLM defaults from backend
  useLLMDefaults()

  // App Store subscriptions for workspace and chat
  const {
    setSelectedPresetId,
    workspaceMinimized,
    setWorkspaceMinimized,
    setWorkspaceMinimizedForLayout,
    showWorkflowsOverview,
    setShowWorkflowsOverview,
    showSchedulesOverview,
    setShowSchedulesOverview
  } = useAppStore(useShallow(state => ({
    setSelectedPresetId: state.setSelectedPresetId,
    workspaceMinimized: state.workspaceMinimized,
    setWorkspaceMinimized: state.setWorkspaceMinimized,
    setWorkspaceMinimizedForLayout: state.setWorkspaceMinimizedForLayout,
    showWorkflowsOverview: state.showWorkflowsOverview,
    setShowWorkflowsOverview: state.setShowWorkflowsOverview,
    showSchedulesOverview: state.showSchedulesOverview,
    setShowSchedulesOverview: state.setShowSchedulesOverview,
  })))
  const [hasOpenedWorkflowsOverview, setHasOpenedWorkflowsOverview] = useState(showWorkflowsOverview)
  const [hasOpenedSchedulesOverview, setHasOpenedSchedulesOverview] = useState(showSchedulesOverview)
  
  // Expose performance diagnostics on window for DevTools console
  useEffect(() => {
    window.perfDiag = () => {
      const chatState = useChatStore.getState()
      const tabs = Object.values(chatState.chatTabs)
      const workflowTabs = tabs.filter(t => t.metadata?.mode === 'workflow')
      const streamingTabs = tabs.filter(t => t.isStreaming)
      const sseConns = chatState.sseConnections
      const sseCount = Object.keys(sseConns).length

      let totalEvents = 0
      let totalEventBytes = 0
      let largestEventBytes = 0
      const eventDetails: Array<{ session: string; tabs: number; tabNames: string; events: number; evtSizeKB: number; avgEventKB: number; largestEventKB: number; largestEventType: string; mode: string; preset: string; streaming: boolean; hasSSE: boolean }> = []
      const duplicateSessionDetails: Array<{ session: string; tabs: number; tabNames: string; events: number; evtSizeKB: number }> = []
      const largestEvents: Array<{ session: string; type: string; sizeKB: number; id: string; tabNames: string }> = []
      const tabsBySession = new Map<string, typeof tabs>()
      for (const tab of tabs) {
        if (tab.sessionId) {
          tabsBySession.set(tab.sessionId, [...(tabsBySession.get(tab.sessionId) || []), tab])
        }
      }
      for (const [sid, sessionTabs] of tabsBySession.entries()) {
        const events = chatState.tabEvents[sid] || []
        const count = events.length
        const sizeEstimate = JSON.stringify(events).length
        const tabNames = sessionTabs.map(t => t.name.slice(0, 24)).join(', ')
        totalEvents += count
        totalEventBytes += sizeEstimate

        let largestForSessionBytes = 0
        let largestForSessionType = ''
        for (const event of events) {
          const eventBytes = JSON.stringify(event).length
          if (eventBytes > largestForSessionBytes) {
            largestForSessionBytes = eventBytes
            largestForSessionType = event.type || '(unknown)'
          }
          if (eventBytes > largestEventBytes) largestEventBytes = eventBytes
          if (eventBytes >= 50 * 1024) {
            largestEvents.push({
              session: sid.slice(0, 8),
              type: event.type || '(unknown)',
              sizeKB: Math.round(eventBytes / 1024),
              id: (event.id || '').slice(0, 16),
              tabNames,
            })
          }
        }

        if (count > 0) {
          const firstTab = sessionTabs[0]
          eventDetails.push({
            session: sid.slice(0, 8),
            tabs: sessionTabs.length,
            tabNames,
            events: count,
            evtSizeKB: Math.round(sizeEstimate / 1024),
            avgEventKB: count > 0 ? Math.round(sizeEstimate / count / 1024) : 0,
            largestEventKB: Math.round(largestForSessionBytes / 1024),
            largestEventType: largestForSessionType,
            mode: firstTab.metadata?.mode || '?',
            preset: (firstTab.metadata?.presetQueryId || '').slice(0, 8),
            streaming: sessionTabs.some(t => t.isStreaming),
            hasSSE: !!sseConns[sid]
          })
        }

        if (sessionTabs.length > 1) {
          duplicateSessionDetails.push({
            session: sid.slice(0, 8),
            tabs: sessionTabs.length,
            tabNames,
            events: count,
            evtSizeKB: Math.round(sizeEstimate / 1024),
          })
        }
      }
      largestEvents.sort((a, b) => b.sizeKB - a.sizeKB)

      // Streaming text sizes
      const streamingTextSizes: Array<{ session: string; sizeKB: number; chars: number }> = []
      let totalStreamingBytes = 0
      for (const [sid, text] of Object.entries(chatState.streamingText)) {
        if (text && text.length > 0) {
          const size = text.length * 2 // approx bytes (UTF-16)
          totalStreamingBytes += size
          streamingTextSizes.push({ session: sid.slice(0, 8), sizeKB: Math.round(size / 1024), chars: text.length })
        }
      }
      const completedStreamingTextSizes: Array<{ session: string; sizeKB: number; chars: number }> = []
      let totalCompletedStreamingBytes = 0
      for (const [sid, text] of Object.entries(chatState.completedStreamingText || {})) {
        if (text && text.length > 0) {
          const size = text.length * 2
          totalCompletedStreamingBytes += size
          completedStreamingTextSizes.push({ session: sid.slice(0, 8), sizeKB: Math.round(size / 1024), chars: text.length })
        }
      }

      // SSE connection details
      const sseDetails: Array<{ session: string; tab: string }> = []
      for (const [sid, _conn] of Object.entries(sseConns)) {
        const tab = tabs.find(t => t.sessionId === sid)
        sseDetails.push({ session: sid.slice(0, 8), tab: tab?.name?.slice(0, 25) || '(orphan!)' })
      }
      // Detect orphan SSE connections (no matching tab)
      const orphanSSE = sseDetails.filter(s => s.tab === '(orphan!)')

      // Orphan tabEvents (no matching tab)
      const tabSessionIds = new Set(tabs.map(t => t.sessionId).filter(Boolean))
      const orphanEvents: Array<{ session: string; events: number; sizeKB: number }> = []
      for (const [sid, events] of Object.entries(chatState.tabEvents)) {
        if (!tabSessionIds.has(sid)) {
          orphanEvents.push({ session: sid.slice(0, 8), events: events.length, sizeKB: Math.round(JSON.stringify(events).length / 1024) })
        }
      }

      // localStorage sizes
      const storeKeys = ['chat-store', 'workflow-store', 'global-preset-storage', 'mode-store', 'mcp-store']
      const storageSizes: Record<string, number> = {}
      let totalLSBytes = 0
      for (const key of storeKeys) {
        const val = localStorage.getItem(key)
        if (val) {
          storageSizes[key] = Math.round(val.length / 1024)
          totalLSBytes += val.length
        }
      }

      // Memory usage (if available)
      const mem = performance.memory
      const memInfo = mem ? {
        usedHeap: Math.round(mem.usedJSHeapSize / 1024 / 1024),
        totalHeap: Math.round(mem.totalJSHeapSize / 1024 / 1024),
        limit: Math.round(mem.jsHeapSizeLimit / 1024 / 1024)
      } : null

      // DOM node count + breakdown of heavy subtrees
      const domNodes = document.querySelectorAll('*').length
      const domBreakdown: Array<{ selector: string; nodes: number }> = []
      const selectors = ['[class*="chat"]', '[class*="event"]', '[class*="message"]', '[class*="editor"]', '[class*="monaco"]', 'svg', 'pre', 'code', '.react-flow', '.react-flow__node', '.react-flow__edge']
      for (const sel of selectors) {
        try {
          const count = document.querySelectorAll(sel).length
          if (count > 50) domBreakdown.push({ selector: sel, nodes: count })
        } catch { /* ignore invalid selectors */ }
      }

      // Active timers/intervals estimate
      // Check for event listeners on window
      const eventListenerCount = typeof window.getEventListeners === 'function'
        ? Object.values(window.getEventListeners(window)).reduce((sum, arr) => sum + arr.length, 0)
        : 'N/A (use DevTools)'

      // Long task detection — start monitoring
      if (!window.__longTaskObserver) {
        try {
          const longTasks: Array<{ duration: number; time: string }> = [];
          window.__longTasks = longTasks
          const observer = new PerformanceObserver((list) => {
            for (const entry of list.getEntries()) {
              longTasks.push({ duration: Math.round(entry.duration), time: new Date().toLocaleTimeString() })
              if (longTasks.length > 100) longTasks.shift()
            }
          })
          observer.observe({ entryTypes: ['longtask'] })
          window.__longTaskObserver = observer
        } catch { /* longtask not supported */ }
      }
      const longTasks = window.__longTasks || []
      const recentLongTasks = longTasks.slice(-10)

      // Frame rate measurement — start if not already running
      if (!window.__fpsSamples) {
        const fpsSamples: number[] = [];
        window.__fpsSamples = fpsSamples
        let lastTime = performance.now()
        let frameCount = 0
        const measureFPS = () => {
          frameCount++
          const now = performance.now()
          if (now - lastTime >= 1000) {
            fpsSamples.push(frameCount)
            if (fpsSamples.length > 30) fpsSamples.shift()
            frameCount = 0
            lastTime = now
          }
          requestAnimationFrame(measureFPS)
        }
        requestAnimationFrame(measureFPS)
      }
      const fpsSamples = window.__fpsSamples
      const avgFPS = fpsSamples.length > 0 ? Math.round(fpsSamples.reduce((a, b) => a + b, 0) / fpsSamples.length) : 'measuring...'
      const minFPS = fpsSamples.length > 0 ? Math.min(...fpsSamples) : 'measuring...'

      // --- OUTPUT ---
      console.log('%c === PERF DIAGNOSTICS ===', 'color: cyan; font-weight: bold; font-size: 14px')

      // Memory
      if (memInfo) {
        const usedPct = Math.round((memInfo.usedHeap / memInfo.limit) * 100)
        const memColor = usedPct > 80 ? 'red' : usedPct > 50 ? 'orange' : 'green'
        console.log(`%c Memory: ${memInfo.usedHeap} MB / ${memInfo.totalHeap} MB (limit: ${memInfo.limit} MB) [${usedPct}% used]`, `color: ${memColor}; font-weight: bold`)
      } else {
        console.log('Memory: N/A (use Chrome-based browser)')
      }

      // FPS
      const fpsColor = (typeof avgFPS === 'number' && avgFPS < 30) ? 'red' : (typeof avgFPS === 'number' && avgFPS < 50) ? 'orange' : 'green'
      console.log(`%c FPS: avg=${avgFPS}, min=${minFPS} (last ${fpsSamples.length}s)`, `color: ${fpsColor}; font-weight: bold`)

      // Tabs & SSE
      console.log(`\nTabs: ${tabs.length} total (${workflowTabs.length} workflow, ${streamingTabs.length} streaming)`)
      console.log(`SSE connections: ${sseCount}${orphanSSE.length > 0 ? ` ⚠️ ${orphanSSE.length} ORPHAN` : ''}`)
      if (orphanSSE.length > 0) {
        console.log('%c Orphan SSE connections (no tab):', 'color: red; font-weight: bold')
        console.table(orphanSSE)
      }

      // Events
      console.log(`\nEvents in memory: ${totalEvents} across ${eventDetails.length} unique sessions (~${Math.round(totalEventBytes / 1024)} KB, largest event ${Math.round(largestEventBytes / 1024)} KB)`)
      if (eventDetails.length > 0) {
        // Sort by estimated memory descending to show biggest first
        eventDetails.sort((a, b) => b.evtSizeKB - a.evtSizeKB)
        console.table(eventDetails)
      }
      if (duplicateSessionDetails.length > 0) {
        console.log(`%c Duplicate tab references: ${duplicateSessionDetails.length} session(s) shown in multiple tabs`, 'color: orange; font-weight: bold')
        console.table(duplicateSessionDetails)
      }
      if (largestEvents.length > 0) {
        console.log(`%c Large retained events (>=50 KB): ${largestEvents.length} total, top 20 shown`, 'color: orange; font-weight: bold')
        console.table(largestEvents.slice(0, 20))
      }

      // Orphan events
      if (orphanEvents.length > 0) {
        console.log(`%c Orphan tabEvents (no matching tab): ${orphanEvents.length} sessions, ${orphanEvents.reduce((s, o) => s + o.events, 0)} events`, 'color: red; font-weight: bold')
        console.table(orphanEvents)
      }

      // Streaming text
      if (streamingTextSizes.length > 0) {
        console.log(`\nStreaming text buffers: ${streamingTextSizes.length} active (~${Math.round(totalStreamingBytes / 1024)} KB)`)
        console.table(streamingTextSizes)
      }
      if (completedStreamingTextSizes.length > 0) {
        console.log(`\nCompleted streaming buffers: ${completedStreamingTextSizes.length} retained (~${Math.round(totalCompletedStreamingBytes / 1024)} KB)`)
        console.table(completedStreamingTextSizes.sort((a, b) => b.sizeKB - a.sizeKB))
      }

      // DOM
      const domColor = domNodes > 10000 ? 'red' : domNodes > 5000 ? 'orange' : 'green'
      console.log(`%c \nDOM nodes: ${domNodes}`, `color: ${domColor}; font-weight: bold`)
      if (domBreakdown.length > 0) {
        console.table(domBreakdown)
      }

      // Long tasks
      console.log(`\nLong tasks (>50ms): ${longTasks.length} total`)
      if (recentLongTasks.length > 0) {
        const avgDuration = Math.round(recentLongTasks.reduce((s, t) => s + t.duration, 0) / recentLongTasks.length)
        const maxDuration = Math.max(...recentLongTasks.map(t => t.duration))
        console.log(`  Recent ${recentLongTasks.length}: avg=${avgDuration}ms, max=${maxDuration}ms`)
        console.table(recentLongTasks)
      }

      // localStorage
      console.log(`\nlocalStorage: ${Math.round(totalLSBytes / 1024)} KB total`)
      console.table(storageSizes)

      // Window event listeners
      console.log(`\nWindow event listeners: ${eventListenerCount}`)

      // React Flow specific diagnostics
      const rfNodes = document.querySelectorAll('.react-flow__node').length
      const rfEdges = document.querySelectorAll('.react-flow__edge').length
      const rfContainers = document.querySelectorAll('.react-flow').length
      if (rfContainers > 0) {
        console.log(`%c \nReact Flow: ${rfContainers} container(s), ${rfNodes} nodes, ${rfEdges} edges`, 'color: #9C27B0; font-weight: bold')
        if (rfContainers > 1) {
          console.log(`%c  ⚠️ Multiple React Flow containers detected — possible leak!`, 'color: red')
        }
      }

      // Workflow store state
      try {
        const wfStore = window.__ZUSTAND_DEVTOOLS__?.['workflow-store'] || null
        if (!wfStore) {
          // Try direct import path
          const presetStates = JSON.parse(localStorage.getItem('workflow-store') || '{}')?.state?._presetStates
          if (presetStates) {
            const presetCount = Object.keys(presetStates).length
            const presetSizeKB = Math.round(JSON.stringify(presetStates).length / 1024)
            console.log(`\nWorkflow preset states cached: ${presetCount} (${presetSizeKB} KB in localStorage)`)
          }
        }
      } catch { /* ignore */ }

      // Summary warnings
      const warnings: string[] = []
      if (memInfo && memInfo.usedHeap > memInfo.limit * 0.8) warnings.push(`Heap at ${Math.round((memInfo.usedHeap / memInfo.limit) * 100)}% of limit!`)
      if (domNodes > 10000) warnings.push(`${domNodes} DOM nodes — UI will lag`)
      if (totalEvents > 5000) warnings.push(`${totalEvents} events in memory — consider clearing old tabs`)
      if (orphanSSE.length > 0) warnings.push(`${orphanSSE.length} orphan SSE connections leaking`)
      if (orphanEvents.length > 0) warnings.push(`${orphanEvents.reduce((s, o) => s + o.events, 0)} orphan events in memory (no tab)`)
      if (totalStreamingBytes > 5 * 1024 * 1024) warnings.push(`${Math.round(totalStreamingBytes / 1024 / 1024)} MB in streaming text buffers`)
      if (totalLSBytes > 5 * 1024 * 1024) warnings.push(`localStorage is ${Math.round(totalLSBytes / 1024 / 1024)} MB — may cause slow persistence`)
      if (typeof avgFPS === 'number' && avgFPS < 30) warnings.push(`FPS avg=${avgFPS} — UI is janky`)
      if (rfContainers > 1) warnings.push(`${rfContainers} React Flow containers — possible mount leak`)
      if (rfNodes > 200) warnings.push(`${rfNodes} React Flow nodes in DOM — heavy canvas`)

      if (warnings.length > 0) {
        console.log('%c \n⚠️  WARNINGS:', 'color: red; font-weight: bold; font-size: 13px')
        warnings.forEach(w => console.log(`%c  • ${w}`, 'color: red'))
      } else {
        console.log('%c \n✅ No obvious issues detected', 'color: green; font-weight: bold')
      }

      console.log('%c ========================', 'color: cyan; font-weight: bold')
      console.log('%c Tip: Run perfDiag() again after interacting to see trends', 'color: gray; font-style: italic')
    }
    return () => { delete window.perfDiag }
  }, [])

  // Expose API response-time diagnostics on window for DevTools console.
  // Every request through services/api.ts (both the agent and workspace
  // axios instances) is timed client-side only -- nothing is sent anywhere.
  useEffect(() => {
    window.apiPerf = () => {
      const { aggregates, recent } = summarizeApiTimings()

      console.log('%c === API PERF DIAGNOSTICS ===', 'color: cyan; font-weight: bold; font-size: 14px')

      if (aggregates.length === 0) {
        console.log('No API calls recorded yet -- interact with the app, then run apiPerf() again.')
        console.log('%c ============================', 'color: cyan; font-weight: bold')
        return
      }

      console.log(`\nBy endpoint (${aggregates.length} distinct, sorted by total time spent):`)
      console.table(aggregates)

      console.log(`\nMost recent 30 calls, slowest first:`)
      console.table(recent.map(r => ({
        method: r.method,
        path: r.path,
        status: r.status,
        durationMs: Math.round(r.durationMs),
        at: new Date(r.timestamp).toLocaleTimeString(),
      })))

      const slow = aggregates.filter(a => a.avgMs > 2000)
      if (slow.length > 0) {
        console.log('%c \n⚠️  Endpoints averaging over 2s:', 'color: red; font-weight: bold')
        slow.forEach(a => console.log(`%c  • ${a.endpoint}: avg=${a.avgMs}ms p95=${a.p95Ms}ms (${a.calls} calls)`, 'color: red'))
      }

      console.log('%c ============================', 'color: cyan; font-weight: bold')
      console.log('%c Tip: Run apiPerf() again after interacting to see fresh timings', 'color: gray; font-style: italic')
    }
    return () => { delete window.apiPerf }
  }, [])

  // Expose per-call API request/response inspection on window for DevTools
  // console. apiPerf() above is the timing summary; this is the detail view
  // for a specific endpoint (or the most recent calls if no filter is
  // given) -- console.table can't usefully render nested request/response
  // objects, so each call prints as its own expandable group instead.
  useEffect(() => {
    window.apiLog = (filter?: string) => {
      const matches = apiLogEntries(filter)
      if (matches.length === 0) {
        console.log(filter ? `No recorded calls matching %c${filter}` : 'No API calls recorded yet -- interact with the app, then run apiLog() again.', 'font-weight: bold')
        return
      }
      console.log(`%c === API LOG${filter ? ` (filter: ${filter})` : ''} — ${matches.length} call(s), most recent last ===`, 'color: cyan; font-weight: bold; font-size: 14px')
      for (const entry of matches) {
        const at = new Date(entry.timestamp).toLocaleTimeString()
        console.groupCollapsed(`${entry.method} ${entry.path} — ${Math.round(entry.durationMs)}ms — ${entry.status} — ${at}`)
        if (entry.requestParams !== undefined) console.log('request params:', entry.requestParams)
        if (entry.requestBody !== undefined) console.log('request body:', entry.requestBody)
        console.log('response body:', entry.responseBody)
        console.groupEnd()
      }
    }
    return () => { delete window.apiLog }
  }, [])

  const [showQuickSwitcher, setShowQuickSwitcher] = useState(false)
  const [quickSwitcherInitialQuery, setQuickSwitcherInitialQuery] = useState('')
  useEffect(() => {
    if (productSurface !== 'agentworks' && productSurface !== 'work' && productSurface !== 'code') setShowQuickSwitcher(false)
  }, [productSurface])

  
  // Ref to prevent duplicate default tab creation (React StrictMode runs effects twice)

  
  const clearActivePreset = useGlobalPresetStore(state => state.clearActivePreset)
  const applyPreset = useGlobalPresetStore(state => state.applyPreset)
  const getActivePreset = useGlobalPresetStore(state => state.getActivePreset)

  useEffect(() => {
    const handleOpenQuickSwitcher = (event: Event) => {
      const surface = useProductSurfaceStore.getState().productSurface
      if (surface !== 'agentworks' && surface !== 'work' && surface !== 'code') return
      const detail = (event as CustomEvent<{ query?: string }>).detail
      setQuickSwitcherInitialQuery(detail?.query || '')
      setShowQuickSwitcher(true)
    }

    window.addEventListener('open-quick-switcher', handleOpenQuickSwitcher)
    return () => window.removeEventListener('open-quick-switcher', handleOpenQuickSwitcher)
  }, [])


  const hasInitializedRef = useRef(false)

  // Initialize stores on mount
  useEffect(() => {
    // Prevent double calls in React StrictMode
    if (hasInitializedRef.current) {
      return
    }
    hasInitializedRef.current = true

    // Initialize MCP store
    useMCPStore.getState().refreshTools()
    
    // LLM list is refreshed after loadDefaultsFromBackend() in useLLMDefaults (so supported_providers is set)
    
    // Initialize global preset store
    useGlobalPresetStore.getState().refreshPresets()
    
    // Initialize workflow store (load phases)
    useWorkflowStore.getState().loadPhases()
  }, [])

  // AgentWorks has two product surfaces: Automations and Activity. The
  // former profile-less Chat landing was removed; product-owned chats render
  // outside this shell and continue to use their own agent profiles.
  useEffect(() => {
    if (productSurface !== 'agentworks' && productSurface !== 'relays') return
    if (!hasCompletedInitialSetup || selectedModeCategory !== 'workflow') {
      setModeCategory('workflow')
      setShowWorkflowsOverview(productSurface === 'agentworks')
      completeInitialSetup()
    }
  }, [completeInitialSetup, hasCompletedInitialSetup, productSurface, selectedModeCategory, setModeCategory, setShowWorkflowsOverview])

  // Ensure a chat tab is selected after restore (fix for page reload issue)
  // This ensures that when tabs are restored from localStorage, we select the first tab of the current mode
  // if activeTabId is null or invalid or belongs to a different mode
  useEffect(() => {
    if (!hasCompletedInitialSetup || (productSurface !== 'agentworks' && productSurface !== 'relays') || selectedModeCategory !== 'workflow') return

    let cancelled = false

    const ensureActiveTab = async () => {
      await waitForChatStoreHydration()
      if (cancelled) return

      // When switching back to workflow mode, restore the active workflow execution tab and
      // ensure the chat panel is visible — otherwise activeTabId stays on whatever mode the
      // user came from (e.g. multi-agent) and the ChatArea inside WorkflowLayout shows wrong content.
      if (selectedModeCategory === 'workflow') {
        const chatStore = useChatStore.getState()
        const workflowStore = useWorkflowStore.getState()
        const activeTabId = chatStore.activeTabId
        const activeTab = activeTabId ? chatStore.getTab(activeTabId) : null
        const activePresetId = useGlobalPresetStore.getState().activePresetIds.workflow

        const activeTabMatchesPreset = activeTab &&
          activeTab.metadata?.mode === 'workflow' &&
          activeTab.metadata?.presetQueryId === activePresetId
        const explicitReadOnlyActiveTab = activeTabMatchesPreset && isRecentExplicitReadOnlyWorkflowTab(activeTab)
          ? activeTab
          : null
        // Tab must match workflow mode and the active preset. Read-only Schedule/Bot
        // tabs only stay active immediately after an explicit open action.
        const hasValidActiveTab = activeTabMatchesPreset &&
          (isInteractiveWorkflowTab(activeTab) || !!explicitReadOnlyActiveTab)

        // Prefer the workflow tab the user last had active for this preset.
        let workflowTabs = Object.values(chatStore.chatTabs)
          .filter(tab => isInteractiveWorkflowTab(tab) && (tab.sessionId || tab.isStreaming))
          .sort((a, b) => workflowTabSortTimestamp(b) - workflowTabSortTimestamp(a))

        if (activePresetId) {
          const presetTabs = workflowTabs.filter(tab => tab.metadata?.presetQueryId === activePresetId)
          if (presetTabs.length > 0) workflowTabs = presetTabs
        }

        const rememberedWorkflowTab = workflowStore.activeWorkflowTabId
          ? chatStore.getTab(workflowStore.activeWorkflowTabId)
          : null
        const rememberedWorkflowTabMatchesPreset = rememberedWorkflowTab &&
          isInteractiveWorkflowTab(rememberedWorkflowTab) &&
          rememberedWorkflowTab.metadata?.presetQueryId === activePresetId
        const builderTab = workflowTabs.find(tab => tab.metadata?.phaseId === 'workflow-builder')
        const streamingTab = workflowTabs.find(tab => chatStore.getTabStreamingStatus(tab.tabId) || tab.isStreaming)
        const activeWorkflowViewMode = normalizeEventViewMode(
          activeTab?.metadata?.mode === 'workflow'
            ? activeTab.viewMode
            : chatStore.eventViewModePreference
        )
        const targetWorkflowTab = explicitReadOnlyActiveTab || (
          activeWorkflowViewMode === 'terminal'
            ? streamingTab ||
              (hasValidActiveTab ? activeTab : null) ||
              (rememberedWorkflowTabMatchesPreset ? rememberedWorkflowTab : null) ||
              builderTab ||
              workflowTabs[0]
            : builderTab ||
              (hasValidActiveTab ? activeTab : null) ||
              (rememberedWorkflowTabMatchesPreset ? rememberedWorkflowTab : null) ||
              streamingTab ||
              workflowTabs[0]
        )

        if (targetWorkflowTab) {
          if (!hasValidActiveTab || activeTabId !== targetWorkflowTab.tabId) {
            activateTab(targetWorkflowTab.tabId)
          }

          const shouldShowWorkflowChat =
            workflowStore.showChatArea ||
            targetWorkflowTab.metadata?.phaseId === 'workflow-builder'

          if (shouldShowWorkflowChat) {
            workflowStore.setShowChatArea(true)
          }
        } else {
          // No active workflow tabs - clear activeTabId so WorkflowLayout's ChatArea
          // doesn't display content from another mode
          useChatStore.setState({ activeTabId: null })
        }
        return
      }

    }

    void ensureActiveTab()

    return () => {
      cancelled = true
    }
  }, [hasCompletedInitialSetup, productSurface, selectedModeCategory])

  // Restore active presets after stores are initialized
  const hasRestoredPresetRef = useRef(false)
  useEffect(() => {
    // AgentWorks only restores automation presets. Product chats own their
    // profile configuration and never use the removed generic-chat preset.
    if ((productSurface === 'agentworks' || productSurface === 'relays') && hasCompletedInitialSetup && selectedModeCategory === 'workflow') {
      // Add a small delay to ensure stores are fully initialized
      const timer = setTimeout(() => {
        const activePreset = getActivePreset(selectedModeCategory)
        if (activePreset) {
          hasRestoredPresetRef.current = true
          const result = applyPreset(activePreset.id, selectedModeCategory)
          if (!result.success) {
            console.error('[APP] Failed to restore preset:', result.error)
          }
        }
        // For workflow mode with no preset found, don't mark as restored — retry below
      }, 500) // 500ms delay to ensure stores are ready

      return () => clearTimeout(timer)
    }
  }, [hasCompletedInitialSetup, selectedModeCategory, productSurface, getActivePreset, applyPreset])

  // Retry preset restoration for workflow mode after presets finish loading from manifests
  // The 500ms timer above may fire before refreshPresets() completes
  const workflowPresetsForRestore = useGlobalPresetStore(state => state.workflowPresets)
  useEffect(() => {
    if (hasRestoredPresetRef.current) return
    if (!hasCompletedInitialSetup || selectedModeCategory !== 'workflow') return
    if (workflowPresetsForRestore.length === 0) return // Presets not loaded yet

    const activePreset = getActivePreset('workflow')
    if (activePreset) {
      hasRestoredPresetRef.current = true
      applyPreset(activePreset.id, 'workflow')
    }
  }, [hasCompletedInitialSetup, selectedModeCategory, workflowPresetsForRestore, getActivePreset, applyPreset])


  // Start new chat function
  const startNewChat = useCallback(() => {
    // Preserve active preset for workflow mode, clear for other modes
    if (selectedModeCategory === 'workflow') {
      // For workflow mode, preserve the active preset
      const { getActivePreset } = useGlobalPresetStore.getState()
      const activePreset = getActivePreset(selectedModeCategory)
      if (activePreset) {
        // Keep the preset selected, just clear the filter
        setSelectedPresetId(null) // Clear filter but keep preset active
        // Don't clear the activePresetId in global store for these modes
        // The preset will be re-applied after chat state is reset
      } else {
        // No preset selected, clear everything
        setSelectedPresetId(null)
        clearActivePreset(selectedModeCategory)
      }
    } else {
      // For other modes (chat, multi-agent), clear preset state as before
      setSelectedPresetId(null); // Clear selected preset filter
      if (selectedModeCategory) {
        clearActivePreset(selectedModeCategory); // Also clear in global store
      }
    }
    
    // Reset the global session ID to force generation of a new one
    resetSessionId();
    
    // Clear the requiresNewChat flag after successful new chat initialization
    useAppStore.getState().clearRequiresNewChat();
    
    // Re-apply active preset for workflow mode after chat reset
    if (selectedModeCategory === 'workflow') {
      const { getActivePreset } = useGlobalPresetStore.getState()
      const activePreset = getActivePreset(selectedModeCategory)
      if (activePreset) {
        // Use setTimeout to ensure chat state reset is complete before applying preset
        setTimeout(() => {
          const result = applyPreset(activePreset.id, selectedModeCategory)
          if (!result.success) {
            console.error('[NEW_CHAT] Failed to re-apply preset:', result.error)
          }
        }, 100)
      }
    }
  }, [setSelectedPresetId, clearActivePreset, selectedModeCategory, applyPreset]);

  // Minimize toggle functions
  const toggleWorkspaceMinimize = useCallback(() => {
    setWorkspaceMinimized(!workspaceMinimized)
  }, [workspaceMinimized, setWorkspaceMinimized])

  // After a Ctrl+1 mode switch, restore the most recently-accessed
  // tab matching the new mode. Without this the activeTabId stays on
  // whatever was selected before (often a tab in the *other* mode), so
  // the workflow's chat panel doesn't pick up the running session and
  // the user has to click the tab manually.
  const restoreMostRecentWorkflowTab = useCallback(() => {
    const chatStore = useChatStore.getState()
    const currentTab = chatStore.activeTabId ? chatStore.chatTabs[chatStore.activeTabId] : null
    if (
      currentTab &&
      currentTab.metadata?.mode === 'workflow' &&
      (isInteractiveWorkflowTab(currentTab) || isRecentExplicitReadOnlyWorkflowTab(currentTab))
    ) return
    const candidates = Object.values(chatStore.chatTabs).filter(t =>
      t.metadata?.mode === 'workflow' && isInteractiveWorkflowTab(t)
    )
    if (candidates.length === 0) return
    const mostRecent = candidates.reduce((best, t) =>
      workflowTabSortTimestamp(t) > workflowTabSortTimestamp(best) ? t : best
    , candidates[0])
    activateTab(mostRecent.tabId)
  }, [])

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      // Ctrl/Cmd + 1 for Workflow mode
      if ((event.ctrlKey || event.metaKey) && event.key === '1') {
        event.preventDefault()
        const { setModeCategory } = useModeStore.getState()
        setModeCategory('workflow')
        useLLMStore.getState().setShowLLMModal(false)
        setShowWorkflowsOverview(false)
        setShowSchedulesOverview(false)
        restoreMostRecentWorkflowTab()
        return
      }
      // Ctrl/Cmd + 3 for Activity view
      if ((event.ctrlKey || event.metaKey) && event.key === '3') {
        event.preventDefault()
        useLLMStore.getState().setShowLLMModal(false)
        setShowSchedulesOverview(false)
        setShowWorkflowsOverview(true)
        return
      }
      // Ctrl/Cmd + 6 for workspace minimize
      if ((event.ctrlKey || event.metaKey) && event.key === '6') {
        event.preventDefault()
        toggleWorkspaceMinimize()
        return
      }
      // Ctrl/Cmd + 7 for auto-scroll
      if ((event.ctrlKey || event.metaKey) && event.key === '7') {
        event.preventDefault()
        const chatStore = useChatStore.getState()
        chatStore.setAutoScroll(!chatStore.autoScroll)
        return
      }
      // Ctrl/Cmd + K opens the shared AgentWorks/Crew switcher.
      if ((event.ctrlKey || event.metaKey) && event.key === 'k') {
        const surface = useProductSurfaceStore.getState().productSurface
        if (surface !== 'agentworks' && surface !== 'work' && surface !== 'code') return
        event.preventDefault()
        setQuickSwitcherInitialQuery('')
        setShowQuickSwitcher(prev => !prev)
        return
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [restoreMostRecentWorkflowTab, setShowWorkflowsOverview, setShowSchedulesOverview, toggleWorkspaceMinimize])

  useEffect(() => {
    if (showWorkflowsOverview) {
      setHasOpenedWorkflowsOverview(true)
    }
  }, [showWorkflowsOverview])

  useEffect(() => {
    if (showSchedulesOverview) {
      setHasOpenedSchedulesOverview(true)
    }
  }, [showSchedulesOverview])

  useEffect(() => {
    if (showWorkflowsOverview || showProviders || showSchedulesOverview) {
      setWorkspaceMinimizedForLayout(true)
      return
    }

    if (selectedModeCategory === 'workflow' || selectedModeCategory === 'multi-agent') {
      const { workspaceMinimizedByMode } = useAppStore.getState()
      setWorkspaceMinimizedForLayout(Boolean(workspaceMinimizedByMode?.[selectedModeCategory]))
    }
  }, [selectedModeCategory, showWorkflowsOverview, showProviders, showSchedulesOverview, setWorkspaceMinimizedForLayout])

  useEffect(() => {
    const collapseWorkspaceForPopup = () => {
      const { workspaceMinimized: currentWorkspaceMinimized } = useAppStore.getState()
      if (!currentWorkspaceMinimized && hasOpenWorkspaceCollapsingPopup()) {
        setWorkspaceMinimizedForLayout(true)
      }
    }

    collapseWorkspaceForPopup()

    const observer = new MutationObserver(collapseWorkspaceForPopup)
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ['class', 'style', 'role', 'data-workspace-popup', 'data-workspace-collapse-ignore'],
    })

    return () => observer.disconnect()
  }, [setWorkspaceMinimizedForLayout])

  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <AuthWrapper>
        <TooltipProvider>
        {/* Mounted above the surface switch so toasts raised from the top bar
            render on every surface, not only the ones that mount a chat. */}
        <ToastHost />
        {showQuickSwitcher && (
          <QuickSwitcher
            isOpen
            onClose={() => setShowQuickSwitcher(false)}
            initialQuery={quickSwitcherInitialQuery}
          />
        )}
        {productSurface === 'video-studio' ? (
          <Suspense fallback={<FileSurfaceFallback />}><VideoStudioSurface /></Suspense>
        ) : productSurface === 'dominion' ? (
          <Suspense fallback={<FileSurfaceFallback />}><DominionSurface /></Suspense>
        ) : productSurface === 'sparkquill' ? (
          <Suspense fallback={<FileSurfaceFallback />}><SparkQuillSurface /></Suspense>
        ) : productSurface === 'work' ? (
          <Suspense fallback={<FileSurfaceFallback />}><WorkSurface key="work" /></Suspense>
        ) : productSurface === 'code' ? (
          <Suspense fallback={<FileSurfaceFallback />}><WorkSurface key="code" product={CODE_PRODUCT} /></Suspense>
        ) : (
        <>
        <UpdateProgressToast />
        <GlobalHumanFeedbackPrompt />
        <div className="h-screen bg-background flex">
          {/* AgentWorks contains Automations and Activity. The former left
              sidebar was removed; its controls now live in the top bar
              (ModePresetBar → WorkspaceTopBarControls). */}
          <div className="flex-1 flex flex-col min-w-0 min-h-0 relative z-10 overflow-hidden">
            {/* Global Mode & Preset Bar - only above middle content area, not sidebars */}
            <ModePresetBar />
            
            <div className="flex-1 min-h-0 overflow-hidden relative">
                {hasOpenedWorkflowsOverview && (
                  <div className={showWorkflowsOverview && !showProviders ? 'h-full' : 'hidden'}>
                    <Suspense fallback={<FileSurfaceFallback />}>
                      <WorkflowsOverviewPage />
                    </Suspense>
                  </div>
                )}
                {hasOpenedSchedulesOverview && (
                  <div className={showSchedulesOverview && !showProviders ? 'h-full' : 'hidden'}>
                    <Suspense fallback={<FileSurfaceFallback />}>
                      <SchedulesPage />
                    </Suspense>
                  </div>
                )}
                <LlmModalHost />
                <div className={!showWorkflowsOverview && !showProviders && !showSchedulesOverview ? 'h-full' : 'hidden'}>
                  <WorkflowLayout
                    className="h-full"
                    onNewChat={startNewChat}
                  />
                </div>
            </div>
          </div>

        </div>

        </>
        )}
        </TooltipProvider>
        </AuthWrapper>
      </ThemeProvider>
    </QueryClientProvider>
  );
}

export default App;

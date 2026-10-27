import { createRoot } from 'react-dom/client'
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/600.css'
import './index.css'
import App from './App.tsx'
import ServerConnectionStatus from './components/ServerConnectionStatus'
import ErrorBoundary from './components/ErrorBoundary'
import { UpdateAvailableBar } from './components/UpdateAvailableBar'
import { applyRuntimeBranding } from './runtime-branding'
import { useCapabilitiesStore } from './stores/useCapabilitiesStore'
import { clearStaleChunkReloadFlag, reloadOnceForStaleChunk } from './utils/staleChunkReload'

applyRuntimeBranding(window.__APP_RUNTIME_CONFIG__ as Parameters<typeof applyRuntimeBranding>[0])

// Kick off the capabilities load at app entry (this always runs on a full load),
// independent of any component mount. The store retries until the backend answers,
// so capabilities can't be left empty by frontend-faster-than-backend startup timing.
void useCapabilitiesStore.getState().fetchCapabilities()

// "ResizeObserver loop completed with undelivered notifications." is a
// Chromium-only diagnostic, not a real failure: it fires whenever a
// ResizeObserver callback itself triggers another layout change within the
// same frame, which this app's canvas/report views (heavy ResizeObserver
// users) hit routinely. Left uncaptured it drowns out genuine crashes in
// both the console and main.log — this session lost real time grepping
// thousands of these lines out by hand to find one actual ReferenceError.
function isBenignRendererError(message: string): boolean {
  return message.includes('ResizeObserver loop completed with undelivered notifications')
}

// Capture uncaught renderer errors so a blank screen always leaves a trace in
// the Electron main log (<userData>/logs/main.log) — DevTools is often
// impossible to open at the moment the window blanks out.
function reportRendererError(kind: string, detail: unknown) {
  try {
    const message = detail instanceof Error ? detail.message : String(detail)
    if (isBenignRendererError(message)) return
    const payload = {
      kind,
      message,
      stack: detail instanceof Error ? detail.stack : undefined,
      url: window.location.href,
      time: new Date().toISOString(),
    }

    console.error('[renderer-error]', payload)
    ;(window as unknown as { electronAPI?: { logRendererError?: (p: unknown) => void } })
      .electronAPI?.logRendererError?.(payload)
  } catch {
    /* never throw from the error reporter */
  }
}

// A deploy renames the hashed chunks; a tab from before it fails its next lazy
// import. Vite reports that as vite:preloadError -- reload once instead of
// surfacing "Something went wrong" (utils/staleChunkReload.ts).
window.addEventListener('vite:preloadError', (e) => {
  if (reloadOnceForStaleChunk((e as unknown as { payload?: unknown }).payload ?? e)) e.preventDefault()
})
window.addEventListener('load', () => clearStaleChunkReloadFlag())
window.addEventListener('error', (e) => reportRendererError('window.error', e.error ?? e.message))
window.addEventListener('unhandledrejection', (e) => reportRendererError('unhandledrejection', e.reason))

createRoot(document.getElementById('root')!).render(
  <ErrorBoundary onError={(error) => reportRendererError('react', error)}>
    <ServerConnectionStatus>
      <UpdateAvailableBar />
      <App />
    </ServerConnectionStatus>
  </ErrorBoundary>
)

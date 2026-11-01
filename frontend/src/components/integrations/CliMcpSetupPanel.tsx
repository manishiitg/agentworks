import { lazy, Suspense } from 'react'

const SetupContent = lazy(() => import('./CliMcpSetupContent').then(module => ({ default: module.CliMcpSetupContent })))

/** Every product defers connection instructions until its setup panel is open. */
export function CliMcpSetupPanel(props: { target?: 'agentworks' | 'vault'; endpoint?: string; projectName?: string; connectionName?: string } = {}) {
  return <Suspense fallback={<p role="status" className="text-sm text-muted-foreground">Loading connection setup…</p>}><SetupContent {...props} /></Suspense>
}

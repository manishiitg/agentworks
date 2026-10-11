import { useEffect, useState } from 'react'
import { workflowWebhooksApi } from '../../api/workflowWebhooks'
import type { RelaySourceGraph } from './relayGraphAnnotations'

/** The backend's AST decides the execution model, including multiline signatures. */
export function useRelaySourceGraph(relayID: string | null | undefined, source: string | null) {
  const [state, setState] = useState<{ source: string | null; graph?: RelaySourceGraph; error?: string; loading: boolean }>({ source: null, loading: false })
  useEffect(() => {
    let active = true
    if (!relayID || !source) { setState({ source, loading: false }); return }
    setState({ source, loading: true })
    void (async () => {
      try {
        const graph = await workflowWebhooksApi.relayGraph(relayID, source)
        if (active) setState({ source, loading: false, graph })
      } catch (error) {
        if (active) setState({ source, loading: false, error: error instanceof Error ? error.message : 'Could not inspect Python source' })
      }
    })()
    return () => { active = false }
  }, [relayID, source])
  const current: typeof state = state.source === source ? state : { source, loading: !!source && !!relayID }
  // The existing comment renderer continues to own legacy graphs.
  return { ...current, graph: current.graph?.native ? current.graph : undefined, native: !!current.graph?.native }
}

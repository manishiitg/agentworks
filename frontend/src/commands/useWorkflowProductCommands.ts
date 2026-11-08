import { useEffect } from 'react'
import { loadWorkflowProductCommands } from './agentworksProductData'
import { toAgentworksCommandDefinitions } from './agentworksProductCommands'
import { setProductCommands } from './registry'

export function useWorkflowProductCommands() {
  useEffect(() => {
    let cancelled = false
    // Clear immediately: even a cached fetch resolves after the first render.
    setProductCommands([])
    loadWorkflowProductCommands('agentworks')
      .then(commands => { if (!cancelled) setProductCommands(toAgentworksCommandDefinitions(commands)) })
      .catch(() => { if (!cancelled) setProductCommands([]) })
    return () => { cancelled = true; setProductCommands([]) }
  }, [])
}

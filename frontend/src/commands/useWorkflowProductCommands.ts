import { useEffect } from 'react'
import { loadWorkflowProductCommands } from './agentworksProductData'
import { toAgentworksCommandDefinitions } from './agentworksProductCommands'
import { setProductCommands } from './registry'

// Relays shares the builder runtime, while its product.yaml owns its commands.
export function useWorkflowProductCommands(isRelay: boolean) {
  useEffect(() => {
    let cancelled = false
    // Clear immediately: even a cached fetch resolves after the first render.
    setProductCommands([])
    loadWorkflowProductCommands(isRelay ? 'relays' : 'agentworks')
      .then(commands => { if (!cancelled) setProductCommands(toAgentworksCommandDefinitions(commands)) })
      .catch(() => { if (!cancelled) setProductCommands([]) })
    return () => { cancelled = true; setProductCommands([]) }
  }, [isRelay])
}

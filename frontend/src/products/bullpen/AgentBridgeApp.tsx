import { useLayoutEffect } from 'react'
import { TooltipProvider } from '../../components/ui/tooltip'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { BullpenSurface } from './BullpenSurface'

/** The server selects this shell only for a messaging-only installation. */
export default function AgentBridgeApp() {
  useLayoutEffect(() => {
    useProductSurfaceStore.getState().setProductSurface('bullpen')
    if (window.location.pathname !== '/agentbridge') {
      window.history.replaceState({}, '', '/agentbridge')
    }
  }, [])
  return <TooltipProvider><BullpenSurface messagingOnly /></TooltipProvider>
}

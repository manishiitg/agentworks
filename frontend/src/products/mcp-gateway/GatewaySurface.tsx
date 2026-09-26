import { gatewayAdminUrl } from '../productSurfaceConfig'

/**
 * Embedded MCP Gateway surface. The gateway is a separate service with its
 * own admin UI, rendered here like any other product. Entry is hidden unless
 * a gateway URL is configured; the null branch only fires for a persisted
 * surface after the URL was removed.
 */
export function GatewaySurface() {
  const url = gatewayAdminUrl()
  if (!url) {
    return (
      <div className="flex h-full items-center justify-center p-8 text-center text-sm text-slate-500">
        No MCP Gateway is configured for this deployment.
      </div>
    )
  }
  return (
    <iframe
      title="MCP Gateway"
      src={url}
      className="h-full w-full border-0 bg-white"
    />
  )
}

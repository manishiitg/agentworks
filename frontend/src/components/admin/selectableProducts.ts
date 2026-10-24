// These are controls for optional product assignments. Core account services
// have no enable/disable control; actual access and availability stay server-owned.
// Personal products are offered only on their dedicated deployments.
const MAIN_PRODUCTS = ['agentworks', 'relays', 'work', 'code']
const ACCOUNT_SERVICES = new Set(['bullpen', 'knowledgebase', 'mcp-gateway'])

export function selectableProducts(serverProducts: string[], enabledSurfaces: string[]): string[] {
  const enabled = new Set(enabledSurfaces)
  serverProducts = serverProducts.filter(id => !ACCOUNT_SERVICES.has(id))
  const main = serverProducts.filter((id) => MAIN_PRODUCTS.includes(id) && enabled.has(id))
  if (main.length > 0) return main
  // A dedicated product deployment (its own single surface): offer that one.
  return serverProducts.filter((id) => enabled.has(id))
}

// The products an admin can grant in Users & access. AgentWorks has three
// main products (Goals, Work/Crew, Code); the others (Video Studio, Dominion,
// SparkQuill) are dedicated deployments with their own users and are never
// offered on a shared one. Only what this deployment actually opens is listed.
const MAIN_PRODUCTS = ['agentworks', 'work', 'code', 'mcp-gateway']

export function selectableProducts(serverProducts: string[], enabledSurfaces: string[]): string[] {
  const enabled = new Set(enabledSurfaces)
  const main = serverProducts.filter((id) => MAIN_PRODUCTS.includes(id) && enabled.has(id))
  if (main.length > 0) return main
  // A dedicated product deployment (its own single surface): offer that one.
  return serverProducts.filter((id) => enabled.has(id))
}

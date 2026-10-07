// Dominion rootless deployment: route browser requests through Caddy on this origin. Never point this file at
// 127.0.0.1 -- that is the visitor's own machine. Every product surface is enabled (owner, 2026-10-07: "everything
// same as excellence/confida"); per-person narrowing happens server-side. Dominion stays the landing product.
window.__APP_RUNTIME_CONFIG__ = {
  apiBaseUrl: "",
  workspaceApiBaseUrl: "/api/wp",
  cdpEnabled: false,
  gatewayUrl: "https://trader.tectonicmarkets.com",
  appName: "Dominion",
  faviconUrl: "/logo.svg",
  enabledProductSurfaces: ["dominion", "agentworks", "relays", "work", "code", "mcp-gateway", "knowledgebase"],
  defaultProductSurface: "dominion"
};

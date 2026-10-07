// Citymall (agents.citymall.live) rootless deployment: browser requests go
// through nginx on this origin to the gateway. Never point this file at
// 127.0.0.1 -- that is the visitor's own machine, not this server.
// build-and-activate.sh copies this file over the build's runtime-config.js.
window.__APP_RUNTIME_CONFIG__ = {
  apiBaseUrl: "",
  workspaceApiBaseUrl: "/api/wp",
  cdpEnabled: false,
  gatewayUrl: "https://agents.citymall.live",
  appName: "Citymall Agents",
  enabledProductSurfaces: ["agentworks", "work", "code", "mcp-gateway", "knowledgebase"],
  defaultProductSurface: "agentworks"
};

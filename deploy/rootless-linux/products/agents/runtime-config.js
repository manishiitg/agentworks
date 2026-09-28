// agents.excellencetechnologies.in (Code only): route browser requests through
// Caddy on this origin. Never point this file at 127.0.0.1 -- that is the
// visitor's own machine, not this server.
window.__APP_RUNTIME_CONFIG__ = {
  apiBaseUrl: "",
  workspaceApiBaseUrl: "/api/wp",
  cdpEnabled: false,
  appName: "AgentWorks",
  faviconUrl: "/logo.svg",
  enabledProductSurfaces: ["code"],
  defaultProductSurface: "code"
};

// confida (AgentWorks) rootless deployment: route browser requests through
// Caddy on this origin. Never point this file at 127.0.0.1 -- that is the
// visitor's own machine, not this server.
//
// frontend/public/runtime-config.js is a LOCAL DEV file: it bakes in
// whatever ports a locally-running dev agent/workspace happen to use, and
// `npm run build` copies it into dist/ verbatim like any other public/
// asset. deploy-cf.sh overwrites it with this file after
// every build -- do not delete this step.
window.__APP_RUNTIME_CONFIG__ = {
  apiBaseUrl: "",
  workspaceApiBaseUrl: "/api/wp",
  cdpEnabled: false,
  gatewayUrl: "https://confida.agentworkshq.com",
  // Confida branding (brand/: symbol and wordmark from confida.ai, which
  // is monochrome, so no brandColor: the AgentWorks primary stays).
  appName: "Confida",
  faviconUrl: "/brand/icon.svg",
  markUrl: "/brand/icon.svg",
  logoUrl: "/brand/logo.svg",
  logoDarkUrl: "/brand/logo-white.svg",
  enabledProductSurfaces: ["agentworks", "relays", "work", "code", "mcp-gateway"],
  defaultProductSurface: "agentworks"
};

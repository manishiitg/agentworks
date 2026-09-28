// Public EC2 deployment: route browser requests through Caddy on this origin.
// Never point this file at 127.0.0.1; that is the visitor's own machine.
window.__APP_RUNTIME_CONFIG__ = {
  apiBaseUrl: "",
  workspaceApiBaseUrl: "/api/wp",
  cdpEnabled: false,
  defaultProductSurface: "video-studio",
  enabledProductSurfaces: ["agentworks", "video-studio", "work", "code"],
  appName: "Video Studio",
  faviconUrl: "/video-studio-favicon.svg"
};

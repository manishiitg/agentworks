// Public EC2 deployment: route browser requests through Caddy on this origin.
// Never point this file at 127.0.0.1; that is the visitor's own machine.
window.__APP_RUNTIME_CONFIG__ = {
  apiBaseUrl: "",
  workspaceApiBaseUrl: "/api/wp",
  cdpEnabled: false,
  defaultProductSurface: "video-studio",
  gatewayUrl: "https://app.example.com",
  enabledProductSurfaces: ["agentworks", "video-studio", "work", "code", "mcp-gateway", "knowledgebase"],
  // Neutral placeholder. The real branding, URL and colors live in the private deployments repo
  // (products/<server>/runtime-config.js and brand/) and are shipped with each deploy.
  appName: "Video Studio",
  faviconUrl: "/brand/icon.svg",
  markUrl: "/brand/icon.svg",
  logoUrl: "/brand/icon.svg",
  logoDarkUrl: "/brand/icon.svg",
  brandColor: "#3A7A91"
};

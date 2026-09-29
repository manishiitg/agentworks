// Public EC2 deployment: route browser requests through Caddy on this origin.
// Never point this file at 127.0.0.1; that is the visitor's own machine.
window.__APP_RUNTIME_CONFIG__ = {
  apiBaseUrl: "",
  workspaceApiBaseUrl: "/api/wp",
  cdpEnabled: false,
  defaultProductSurface: "video-studio",
  enabledProductSurfaces: ["agentworks", "video-studio", "work", "code"],
  // REAL Training Systems branding (brand/: name and colors from
  // realtrainingsys.com, which has a text wordmark and no logo file). The
  // brand color is their teal #305b6e lightened so it reads on dark screens.
  appName: "REAL Training Systems",
  faviconUrl: "/brand/icon.svg",
  markUrl: "/brand/icon.svg",
  logoUrl: "/brand/logo.svg",
  logoDarkUrl: "/brand/logo-white.svg",
  brandColor: "#3A7A91"
};

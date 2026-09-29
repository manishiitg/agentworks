// agents.excellencetechnologies.in (Code only): route browser requests through
// Caddy on this origin. Never point this file at 127.0.0.1 -- that is the
// visitor's own machine, not this server.
window.__APP_RUNTIME_CONFIG__ = {
  apiBaseUrl: "",
  workspaceApiBaseUrl: "/api/wp",
  cdpEnabled: false,
  // Excellence Technologies branding (brand/: from excellencetechnologies.in).
  appName: "Excellence Technologies",
  faviconUrl: "/brand/icon.svg",
  markUrl: "/brand/icon.svg",
  logoUrl: "/brand/logo.svg",
  logoDarkUrl: "/brand/logo-white.svg",
  brandColor: "#109AAA",
  enabledProductSurfaces: ["code"],
  defaultProductSurface: "code"
};

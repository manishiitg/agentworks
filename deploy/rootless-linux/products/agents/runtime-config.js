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
  // The mark shows on the sign-in card only. No logoUrl / logoDarkUrl: the
  // wide company logo no longer appears in the app's top bar.
  markUrl: "/brand/icon.svg",
  brandColor: "#109AAA",
  enabledProductSurfaces: ["code"],
  defaultProductSurface: "code"
};

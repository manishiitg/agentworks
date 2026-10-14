const { contextBridge, ipcRenderer } = require('electron')

// The packaged app serves the frontend from the same origin as the API, so the
// web app should talk to its own origin rather than the hardcoded dev default
// — which would be wrong whenever the port shifted.
//
// backend() reports 'platform': M1 of
// docs/design/sparkquill_desktop_on_platform_plan.md moved the shell off the
// standalone family-server onto the AgentWorks platform binaries
// (workspace-server + agent-server), which is what the learning app's
// existing platform-mode client (api/index.ts, platform/runtimeConfig.ts)
// already targets.
//
// onWindowVisibility lets the app free the speech model when the window is
// hidden and warm it again on show (main.js sends the event; the app holds
// the login token the platform's /api/voice/* routes need).
contextBridge.exposeInMainWorld('sparkquill', {
  isDesktop: true,
  apiBaseUrl: () => window.location.origin,
  backend: () => 'platform',
  // A native macOS notification for a message Quill sent the parent
  // (saved updates). main.js shows it; the renderer keeps its own copy on screen.
  notify: (title, body) => ipcRenderer.send('sparkquill-notify', { title: String(title || ''), body: String(body || '') }),
  onWindowVisibility: (callback) => {
    ipcRenderer.on('window-visibility', (_event, payload) => callback(!!(payload && payload.visible)))
  },
})

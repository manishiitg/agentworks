const { app, BrowserWindow, dialog, shell, nativeTheme, Menu, Tray, ipcMain, nativeImage, session } = require('electron');
const path = require('path');
const { spawn } = require('child_process');
const http = require('http');
const https = require('https');
const fs = require('fs');
const crypto = require('crypto');
const {
  importLoginShellEnv,
  createBoundedLogWriter: createSharedBoundedLogWriter,
  waitForHealth: waitForServersHealth,
  makeOpenExternalUrl,
  attachExternalNavigation,
  installSignalShutdown,
  spawnServer,
} = require('./lib');

// Keep the historical data path while presenting the AgentWorks name to macOS.
// Changing userData would make existing installs appear empty after the rename.
const COMPAT_USER_DATA_PATH = path.join(app.getPath('appData'), 'runloop-desktop');
const CONFIGURED_USER_DATA_PATH = String(process.env.RUNLOOP_USER_DATA_DIR || '').trim();
const USER_DATA_PATH = CONFIGURED_USER_DATA_PATH
  ? path.resolve(CONFIGURED_USER_DATA_PATH)
  : COMPAT_USER_DATA_PATH;
app.setName('AgentWorks');
app.setPath('userData', USER_DATA_PATH);

const { createWalkthroughState } = require('./walkthroughState');
// Initialize lazily, after legacy profile migration has completed.
let walkthroughState;
const getWalkthroughState = () => walkthroughState ||
  (walkthroughState = createWalkthroughState(app.getPath('userData')));
ipcMain.on('get-walkthrough-dismissals', event => {
  event.returnValue = getWalkthroughState().getDismissed();
});
ipcMain.on('dismiss-walkthrough', (_event, key) => {
  try {
    getWalkthroughState().dismiss(key);
  } catch (error) {
    diagLog('[main] Failed to save walkthrough dismissal:', String(error));
  }
});

// Dynamic ports (assigned at runtime)
let dynamicAgentPort = 0;
let dynamicWorkspacePort = 0;
const workspaceApiToken = crypto.randomBytes(32).toString('hex');

const HEALTH_TIMEOUT_MS = 90000;
const HEALTH_POLL_MS = 500;
const HEALTH_INITIAL_DELAY_MS = 3000;
const DEFAULT_AUTH_SECRET = 'dev-secret-change-in-production';
const DEFAULT_MANAGED_LOG_MAX_BYTES = 25 * 1024 * 1024;
const LOG_TRIM_KEEP_RATIO = 0.75;
const DEFAULT_AGENT_PROMPT_LOG_MAX_SESSIONS = 10;
const AGENT_PROMPT_LOG_PRUNE_INTERVAL_MS = 10 * 60 * 1000;

function parsePositiveIntegerEnv(name, fallback) {
  const raw = process.env[name];
  if (!raw) return fallback;
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function parseNonNegativeIntegerEnv(name, fallback) {
  const raw = process.env[name];
  if (raw === undefined || raw === '') return fallback;
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback;
}

// Enforce dark mode for system UI (title bar, context menus)
nativeTheme.themeSource = 'dark';

// GUI-launched Mac apps inherit a minimal PATH (no Homebrew, no nvm, no ~/.local/bin),
// so spawned tools like `claude`, `npx`, etc. are not found. Read PATH from the user's
// login shell once at startup and use it for all spawned children. Shared with the
// other desktop shells in ./lib (see lib/index.js).
importLoginShellEnv();

const MANAGED_LOG_MAX_BYTES = parsePositiveIntegerEnv('RUNLOOP_MAX_LOG_BYTES', DEFAULT_MANAGED_LOG_MAX_BYTES);
const AGENT_PROMPT_LOG_MAX_SESSIONS = parseNonNegativeIntegerEnv(
  'RUNLOOP_AGENT_PROMPTS_MAX_SESSIONS',
  parseNonNegativeIntegerEnv('LOG_AGENT_PROMPTS_MAX_SESSIONS', DEFAULT_AGENT_PROMPT_LOG_MAX_SESSIONS)
);

let workspaceProcess = null;
let agentProcess = null;
let mainWindow = null;
let settingsWindow = null;
let tray = null;
let agentPromptLogPruneInterval = null;
let baseDockIcon = null;
let dockActivityFrameTimer = null;
let dockActivityFrameIndex = 0;
let dockActivityRunningCount = 0;
let dockActivityBounceId = null;

// --- Diagnostic logging -----------------------------------------------------
// Console output is lost when the renderer blanks out and DevTools/terminal
// can't be opened. Mirror crash/error diagnostics to <userData>/logs/main.log
// so a post-mortem is always available. Best-effort; the logger never throws.
let diagLogStream = null;
function safeStringify(value) {
  try { return JSON.stringify(value); } catch (_e) { return String(value); }
}

// Bounded log writers are shared with the other desktop shells (lib/boundedLog.js);
// this keeps the local signature every call site here uses.
function createBoundedLogWriter(filePath, maxBytes = MANAGED_LOG_MAX_BYTES) {
  return createSharedBoundedLogWriter(filePath, { maxBytes, keepRatio: LOG_TRIM_KEEP_RATIO, appName: 'AgentWorks' });
}

function pruneAgentPromptLogs(userDataPath) {
  if (AGENT_PROMPT_LOG_MAX_SESSIONS <= 0) return;

  const promptRoot = path.join(userDataPath, 'logs', 'agent_prompts');
  let entries;
  try {
    entries = fs.readdirSync(promptRoot, { withFileTypes: true });
  } catch (_e) {
    return;
  }

  const sessionDirs = [];
  for (const entry of entries) {
    if (!entry.isDirectory()) continue;
    const fullPath = path.join(promptRoot, entry.name);
    try {
      const stat = fs.statSync(fullPath);
      sessionDirs.push({ name: entry.name, fullPath, mtimeMs: stat.mtimeMs });
    } catch (_e) {
      /* best-effort cleanup */
    }
  }

  if (sessionDirs.length <= AGENT_PROMPT_LOG_MAX_SESSIONS) return;

  sessionDirs.sort((a, b) => {
    if (b.mtimeMs !== a.mtimeMs) return b.mtimeMs - a.mtimeMs;
    return b.name.localeCompare(a.name);
  });

  for (const dir of sessionDirs.slice(AGENT_PROMPT_LOG_MAX_SESSIONS)) {
    try {
      fs.rmSync(dir.fullPath, { recursive: true, force: true });
      console.log(`[main] Pruned old agent prompt log session: ${dir.name}`);
    } catch (err) {
      console.warn('[main] Failed to prune agent prompt log session:', dir.name, err && err.message ? err.message : err);
    }
  }
}

function startAgentPromptLogPruning(userDataPath) {
  pruneAgentPromptLogs(userDataPath);
  if (agentPromptLogPruneInterval) clearInterval(agentPromptLogPruneInterval);
  agentPromptLogPruneInterval = setInterval(() => pruneAgentPromptLogs(userDataPath), AGENT_PROMPT_LOG_PRUNE_INTERVAL_MS);
  if (agentPromptLogPruneInterval.unref) agentPromptLogPruneInterval.unref();
}

function diagLog(...args) {
  const text = args.map((a) => (typeof a === 'string' ? a : safeStringify(a))).join(' ');
  const line = `[${new Date().toISOString()}] ${text}\n`;
  console.error(line.trimEnd());
  try {
    if (!diagLogStream) {
      const logsDir = path.join(app.getPath('userData'), 'logs');
      if (!fs.existsSync(logsDir)) fs.mkdirSync(logsDir, { recursive: true });
      diagLogStream = createBoundedLogWriter(path.join(logsDir, 'main.log'));
    }
    diagLogStream.write(line);
  } catch (_e) {
    /* best-effort: never let logging crash the app */
  }
}

// Renderer forwards uncaught errors / unhandled rejections / React error-boundary
// catches here (via the electronAPI.logRendererError preload bridge), so a blank
// screen leaves a trace even with no DevTools.
ipcMain.on('renderer-error', (_event, payload) => {
  diagLog('[renderer-error]', payload);
});

// Opt-in out-of-band debugging: launch with ELECTRON_REMOTE_DEBUG_PORT=9222 and
// attach Chrome at http://localhost:9222 even when the window is wedged/blank.
if (process.env.ELECTRON_REMOTE_DEBUG_PORT) {
  app.commandLine.appendSwitch('remote-debugging-port', process.env.ELECTRON_REMOTE_DEBUG_PORT);
}

function migrateLegacyUserData() {
  // A caller-selected profile is intentionally isolated. Importing a sibling
  // legacy profile would silently defeat that isolation on first launch.
  if (CONFIGURED_USER_DATA_PATH) {
    return;
  }
  const userDataPath = app.getPath('userData');
  const hasCurrentData = fs.existsSync(userDataPath) && fs.readdirSync(userDataPath).length > 0;
  if (hasCurrentData) {
    return;
  }

  const legacyProductName = ['Agent', 'Forge'].join('');
  const legacyUserDataPath = path.join(path.dirname(userDataPath), legacyProductName);
  if (legacyUserDataPath === userDataPath || !fs.existsSync(legacyUserDataPath)) {
    return;
  }

  try {
    fs.mkdirSync(userDataPath, { recursive: true });
    fs.cpSync(legacyUserDataPath, userDataPath, { recursive: true, errorOnExist: false });
    console.log(`[main] Migrated legacy user data from ${legacyUserDataPath} to ${userDataPath}`);
  } catch (error) {
    console.warn('[main] Failed to migrate legacy AgentWorks user data:', error);
  }
}

// Settings Management
function loadSettings() {
  const configPath = path.join(app.getPath('userData'), 'config.json');
  try {
    if (fs.existsSync(configPath)) {
      return JSON.parse(fs.readFileSync(configPath, 'utf8'));
    }
  } catch (e) {
    console.error('Failed to load settings:', e);
  }
  return { docsDir: '', authSecret: '', logAgentPrompts: false };
}

// Show a modal asking the user for the AUTH_SECRET used to encrypt provider keys.
// mode='unlock' (existing encrypted file) or 'create' (no file yet — just choose a secret).
// Resolves with the entered value (empty string = skip).
function promptAuthSecret(mode) {
  return new Promise((resolve) => {
    const win = new BrowserWindow({
      width: 460,
      height: 320,
      resizable: false,
      modal: true,
      parent: mainWindow || undefined,
      title: mode === 'unlock' ? 'Unlock Workspace Keys' : 'Set Workspace Encryption Secret',
      webPreferences: { nodeIntegration: true, contextIsolation: false },
    });
    let answered = false;
    const finish = (value) => {
      if (answered) return;
      answered = true;
      ipcMain.removeListener('auth-secret-result', handler);
      try { win.close(); } catch (_) {}
      resolve(value);
    };
    const handler = (_event, value) => finish(value);
    ipcMain.on('auth-secret-result', handler);
    win.on('closed', () => finish(''));
    win.loadFile(path.join(__dirname, 'auth-prompt.html'), { hash: mode });
  });
}

// Resolve workspace docs dir on first launch.
// Precedence: RUNLOOP_DOCS_DIR env > settings.docsDir > prompt user to pick > default (userData/workspace-docs)
async function resolveDocsDir() {
  if (process.env.RUNLOOP_DOCS_DIR) return process.env.RUNLOOP_DOCS_DIR;

  const settings = loadSettings();
  if (settings.docsDir && fs.existsSync(settings.docsDir)) return settings.docsDir;

  // First launch (or path was deleted): ask user to choose.
  const choice = await dialog.showMessageBox({
    type: 'question',
    title: 'Choose workspace folder',
    message: 'Where should AgentWorks store your workspace documents?',
    detail: 'Pick an existing folder to use, or let AgentWorks create a default one in your application data directory.',
    buttons: ['Choose folder…', 'Use default'],
    defaultId: 0,
    cancelId: 1,
  });

  let chosen;
  if (choice.response === 0) {
    const result = await dialog.showOpenDialog({
      title: 'Select workspace-docs folder',
      properties: ['openDirectory', 'createDirectory'],
    });
    if (!result.canceled && result.filePaths[0]) chosen = result.filePaths[0];
  }
  if (!chosen) chosen = path.join(app.getPath('userData'), 'workspace-docs');

  fs.mkdirSync(chosen, { recursive: true });
  saveSettings({ ...settings, docsDir: chosen });
  return chosen;
}

function saveSettings(settings) {
  const configPath = path.join(app.getPath('userData'), 'config.json');
  fs.writeFileSync(configPath, JSON.stringify(settings, null, 2));
}

function generateAuthSecret() {
  return crypto.randomBytes(32).toString('hex');
}

function hasStoredProviderKeysPayload(keys) {
  if (!keys || typeof keys !== 'object') return false;

  const stringFields = [
    'openrouter',
    'openai',
    'anthropic',
    'zai',
    'kimi',
    'vertex',
    'codex_cli',
    'cursor_cli',
    'agy_cli',
    'minimax',
    'minimax_coding_plan',
    'elevenlabs',
    'deepgram',
  ];
  if (stringFields.some((field) => typeof keys[field] === 'string' && keys[field].trim())) {
    return true;
  }
  if (keys.bedrock && typeof keys.bedrock.region === 'string' && keys.bedrock.region.trim()) {
    return true;
  }
  if (
    keys.azure &&
    typeof keys.azure.endpoint === 'string' &&
    keys.azure.endpoint.trim() &&
    typeof keys.azure.api_key === 'string' &&
    keys.azure.api_key.trim()
  ) {
    return true;
  }
  return false;
}

function decryptProviderKeysContentWithSecret(content, secret) {
  const data = Buffer.from(content, 'base64');
  const nonceSize = 12;
  const tagSize = 16;
  if (data.length < nonceSize + tagSize) throw new Error('encrypted provider keys payload is too short');

  const nonce = data.subarray(0, nonceSize);
  const ciphertext = data.subarray(nonceSize, data.length - tagSize);
  const tag = data.subarray(data.length - tagSize);
  const key = crypto.createHmac('sha256', Buffer.from(secret)).update('secrets-encryption-key').digest();
  const decipher = crypto.createDecipheriv('aes-256-gcm', key, nonce);
  decipher.setAAD(Buffer.from('provider-keys'));
  decipher.setAuthTag(tag);
  const plaintext = Buffer.concat([decipher.update(ciphertext), decipher.final()]).toString('utf8');
  return JSON.parse(plaintext);
}

function encryptProviderKeysPayloadWithSecret(payload, secret) {
  const plaintext = Buffer.from(JSON.stringify(payload));
  const nonce = crypto.randomBytes(12);
  const key = crypto.createHmac('sha256', Buffer.from(secret)).update('secrets-encryption-key').digest();
  const cipher = crypto.createCipheriv('aes-256-gcm', key, nonce);
  cipher.setAAD(Buffer.from('provider-keys'));
  const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);
  const tag = cipher.getAuthTag();
  return Buffer.concat([nonce, ciphertext, tag]).toString('base64');
}

function removeFileIfPresent(filePath) {
  try {
    fs.rmSync(filePath, { force: true });
  } catch (err) {
    diagLog('[main] Failed to remove empty provider keys file:', String(err && err.message ? err.message : err));
  }
}

function inspectProviderKeysFileForStartup(providerKeysPath) {
  if (!fs.existsSync(providerKeysPath)) return { needsUnlock: false };

  let content = '';
  try {
    content = fs.readFileSync(providerKeysPath, 'utf8').trim();
  } catch (err) {
    diagLog('[main] Failed to inspect provider keys file:', String(err && err.message ? err.message : err));
    return { needsUnlock: true };
  }

  if (!content) {
    removeFileIfPresent(providerKeysPath);
    return { needsUnlock: false };
  }

  try {
    const plaintext = JSON.parse(content);
    if (!hasStoredProviderKeysPayload(plaintext)) {
      removeFileIfPresent(providerKeysPath);
      return { needsUnlock: false };
    }
    return { needsUnlock: false, migratableKeys: plaintext };
  } catch (_) {}

  try {
    const plaintext = decryptProviderKeysContentWithSecret(content, DEFAULT_AUTH_SECRET);
    if (!hasStoredProviderKeysPayload(plaintext)) {
      removeFileIfPresent(providerKeysPath);
      return { needsUnlock: false };
    }
    return { needsUnlock: false, migratableKeys: plaintext };
  } catch (_) {}

  return { needsUnlock: true };
}

function persistAuthSecret(settings, authSecret) {
  saveSettings({ ...settings, authSecret });
  process.env.AUTH_SECRET = authSecret;
  return authSecret;
}

function persistGeneratedAuthSecret(settings) {
  return persistAuthSecret(settings, generateAuthSecret());
}

function migrateProviderKeysToGeneratedSecret(providerKeysPath, keys, settings) {
  const generated = generateAuthSecret();
  const encoded = encryptProviderKeysPayloadWithSecret(keys, generated);
  fs.writeFileSync(providerKeysPath, encoded);
  return persistAuthSecret(settings, generated);
}

async function clearFrontendCacheIfVersionChanged() {
  if (!app.isPackaged) return;

  const settings = loadSettings();
  const currentVersion = app.getVersion();
  if (settings.lastFrontendCacheVersion === currentVersion) return;

  try {
    console.log(`[main] Clearing frontend cache for version ${currentVersion}`);
    await session.defaultSession.clearCache();
    await session.defaultSession.clearStorageData({
      storages: ['serviceworkers', 'cachestorage'],
    });
    saveSettings({ ...settings, lastFrontendCacheVersion: currentVersion });
  } catch (err) {
    diagLog('[main] Failed to clear frontend cache after version change:', String(err && err.message ? err.message : err));
  }
}

function withDesktopVersionCacheBust(url) {
  if (!app.isPackaged || process.env.DEV_URL) return url;

  try {
    const parsed = new URL(url);
    parsed.searchParams.set('runloop_version', app.getVersion());
    return parsed.toString();
  } catch (_) {
    return url;
  }
}

// IPC Handlers for Settings
ipcMain.handle('get-settings', () => loadSettings());
ipcMain.handle('get-app-version', () => app.getVersion());
ipcMain.handle('pick-docs-dir', async () => {
  const result = await dialog.showOpenDialog({
    title: 'Select workspace-docs folder',
    properties: ['openDirectory', 'createDirectory'],
  });
  if (result.canceled || !result.filePaths[0]) return null;
  return result.filePaths[0];
});
ipcMain.handle('pick-workflow-folder', async () => {
  const result = await dialog.showOpenDialog(mainWindow || undefined, {
    title: 'Attach a folder to this workflow',
    properties: ['openDirectory'],
  });
  if (result.canceled || !result.filePaths[0]) return null;
  return result.filePaths[0];
});

ipcMain.on('save-settings', (event, settings) => {
  saveSettings(settings);
  if (settings.docsDir) process.env.RUNLOOP_DOCS_DIR = settings.docsDir;
  if (settings.authSecret !== undefined) process.env.AUTH_SECRET = settings.authSecret;
  if (settingsWindow) settingsWindow.close();
  
  // Restart servers to apply changes
  dialog.showMessageBox(mainWindow, {
    type: 'info',
    title: 'Restart Required',
    message: 'Settings saved. The application servers will now restart to apply changes.',
    buttons: ['OK']
  }).then(() => {
    restartServers();
  });
});

// IPC Handler for dynamic workspace port
ipcMain.on('get-workspace-port', (event) => {
  event.returnValue = process.env.DEV_URL ? 8081 : dynamicWorkspacePort;
});

function openSettingsWindow() {
  if (settingsWindow) {
    settingsWindow.focus();
    return;
  }

  settingsWindow = new BrowserWindow({
    width: 500,
    height: 600,
    title: 'Settings',
    backgroundColor: '#1e1e1e',
    parent: mainWindow,
    modal: true,
    webPreferences: {
      nodeIntegration: true,
      contextIsolation: false // Simplifies simple settings UI
    }
  });

  settingsWindow.loadFile(path.join(__dirname, 'settings.html'));
  settingsWindow.on('closed', () => {
    settingsWindow = null;
  });
}

function restartServers() {
  killChildren();
  // Small delay to ensure ports freed
  setTimeout(() => {
    const userDataPath = app.getPath('userData');
    startAgentPromptLogPruning(userDataPath);
    spawnWorkspace(userDataPath)
      .then(() => spawnAgent(userDataPath))
      .then(() => {
        const agentHealthUrl = `http://127.0.0.1:${dynamicAgentPort}/api/health`;
        const workspaceHealthUrl = `http://127.0.0.1:${dynamicWorkspacePort}/health`;
        return waitForHealth(agentHealthUrl, workspaceHealthUrl);
      })
      .then(() => {
        mainWindow.reload(); // Reload frontend to reconnect
      })
      .catch(err => {
        showErrorAndExit('Failed to restart servers: ' + err);
      });
  }, 2000); // Increased delay to 2s
}

// Setup Native Application Menu
function createMenu() {
  const isMac = process.platform === 'darwin';

  const template = [
    // { role: 'appMenu' }
    ...(isMac ? [{
      label: app.name,
      submenu: [
        { role: 'about' },
        { type: 'separator' },
        { label: 'Check for Updates…', click: () => checkForUpdates(true) },
        { type: 'separator' },
        { label: 'Settings...', click: openSettingsWindow },
        { type: 'separator' },
        { role: 'services' },
        { type: 'separator' },
        { role: 'hide' },
        { role: 'hideOthers' },
        { role: 'unhide' },
        { type: 'separator' },
        { role: 'quit' }
      ]
    }] : []),
    // { role: 'fileMenu' }
    {
      label: 'File',
      submenu: [
        { label: 'Check for Updates…', click: () => checkForUpdates(true) },
        { label: 'Settings...', click: openSettingsWindow },
        { type: 'separator' },
        { role: isMac ? 'close' : 'quit' }
      ]
    },
    // { role: 'editMenu' }
    {
      label: 'Edit',
      submenu: [
        { role: 'undo' },
        { role: 'redo' },
        { type: 'separator' },
        { role: 'cut' },
        { role: 'copy' },
        { role: 'paste' },
        ...(isMac ? [
          { role: 'pasteAndMatchStyle' },
          { role: 'delete' },
          { role: 'selectAll' },
          { type: 'separator' },
          {
            label: 'Speech',
            submenu: [
              { role: 'startSpeaking' },
              { role: 'stopSpeaking' }
            ]
          }
        ] : [
          { role: 'delete' },
          { type: 'separator' },
          { role: 'selectAll' }
        ])
      ]
    },
    // { role: 'viewMenu' }
    {
      label: 'View',
      submenu: [
        { role: 'reload' },
        { role: 'forceReload' },
        { role: 'toggleDevTools' },
        { type: 'separator' },
        { role: 'resetZoom' },
        { role: 'zoomIn' },
        { role: 'zoomOut' },
        { type: 'separator' },
        { role: 'togglefullscreen' }
      ]
    },
    // { role: 'windowMenu' }
    {
      label: 'Window',
      submenu: [
        { role: 'minimize' },
        { role: 'zoom' },
        ...(isMac ? [
          { type: 'separator' },
          { role: 'front' },
          { type: 'separator' },
          { role: 'window' }
        ] : [
          { role: 'close' }
        ])
      ]
    },
    {
      role: 'help',
      submenu: [
        {
          label: 'Learn More',
          click: async () => {
            await shell.openExternal('https://github.com/manishiitg/coding-agent-loop')
          }
        }
      ]
    }
  ];

  const menu = Menu.buildFromTemplate(template);
  Menu.setApplicationMenu(menu);
}

ipcMain.handle('print-to-pdf', async (event, suggestedFilename) => {
  const { canceled, filePath } = await dialog.showSaveDialog(mainWindow, {
    defaultPath: suggestedFilename,
    filters: [{ name: 'PDF Files', extensions: ['pdf'] }]
  })
  if (canceled || !filePath) return { canceled: true }
  const data = await event.sender.printToPDF({
    printBackground: true,
    pageSize: 'A4',
    margins: { marginType: 'custom', top: 15000, bottom: 15000, left: 10000, right: 10000 }
  })
  await require('fs').promises.writeFile(filePath, data)
  return { canceled: false }
})

ipcMain.handle('save-flow-image', async (event, { filename, dataUrl, format }) => {
  const extension = format === 'jpeg' ? 'jpg' : format === 'svg' ? 'svg' : 'png';
  const safeFilename = (filename || `workflow-flow.${extension}`).replace(/[\\/]/g, '-');
  const downloadsDir = app.getPath('downloads');
  const parsed = path.parse(safeFilename);
  let filePath = path.join(downloadsDir, safeFilename);
  let suffix = 1;
  while (fs.existsSync(filePath)) {
    filePath = path.join(downloadsDir, `${parsed.name}-${suffix}${parsed.ext || `.${extension}`}`);
    suffix += 1;
  }

  const rawData = String(dataUrl || '');
  const commaIndex = rawData.indexOf(',');
  const base64 = commaIndex >= 0 ? rawData.slice(commaIndex + 1) : rawData;
  const buffer = Buffer.from(base64, 'base64');
  if (format === 'png' && !buffer.subarray(0, 8).equals(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]))) {
    throw new Error('PNG export payload was invalid.');
  }
  if (format === 'svg' && !buffer.toString('utf8', 0, 128).trimStart().startsWith('<svg')) {
    throw new Error('SVG export payload was invalid.');
  }
  await fs.promises.writeFile(filePath, buffer);
  return { canceled: false, filePath };
});

ipcMain.handle('capture-flow-image', async (event, { filename, format, rect }) => {
  const extension = format === 'jpeg' ? 'jpg' : 'png';
  const safeFilename = (filename || `workflow-flow.${extension}`).replace(/[\\/]/g, '-');
  const downloadsDir = app.getPath('downloads');
  const parsed = path.parse(safeFilename);
  let filePath = path.join(downloadsDir, safeFilename);
  let suffix = 1;
  while (fs.existsSync(filePath)) {
    filePath = path.join(downloadsDir, `${parsed.name}-${suffix}${parsed.ext || `.${extension}`}`);
    suffix += 1;
  }

  const bounds = {
    x: Math.max(0, Math.round(rect?.x || 0)),
    y: Math.max(0, Math.round(rect?.y || 0)),
    width: Math.max(1, Math.round(rect?.width || 1)),
    height: Math.max(1, Math.round(rect?.height || 1)),
  };
  const image = await event.sender.capturePage(bounds);
  const buffer = format === 'jpeg' ? image.toJPEG(95) : image.toPNG();
  await fs.promises.writeFile(filePath, buffer);
  return { canceled: false, filePath };
});

// Capture a single on-screen rectangle and return it as a PNG data URL WITHOUT
// writing to disk. Used by the report exporter to scroll-and-stitch a tall
// report into one full-length, pixel-perfect image (capturePage only grabs the
// visible viewport, so the renderer captures each slice and stitches them).
ipcMain.handle('capture-region', async (event, { rect }) => {
  const bounds = {
    x: Math.max(0, Math.round(rect?.x || 0)),
    y: Math.max(0, Math.round(rect?.y || 0)),
    width: Math.max(1, Math.round(rect?.width || 1)),
    height: Math.max(1, Math.round(rect?.height || 1)),
  };
  const image = await event.sender.capturePage(bounds);
  return { dataUrl: image.toDataURL() };
});

// IPC Handler for Dock Badge
ipcMain.on('set-dock-badge', (event, text) => {
  if (process.platform === 'darwin') {
    app.dock.setBadge(text);
  }
});

ipcMain.on('set-running-activity', (_event, payload) => {
  const count = Number(payload && payload.count);
  dockActivityRunningCount = Number.isFinite(count) && count > 0 ? Math.floor(count) : 0;
  updateDockActivityAnimation();
});

// External-URL handling is shared with the other desktop shells (lib/externalNav.js).
const openExternalUrl = makeOpenExternalUrl(shell);

// IPC Handler for opening external URLs
ipcMain.on('open-external', (event, url) => {
  console.log('[main] Received open-external request for:', url);
  openExternalUrl(url, 'ipc');
});

function getResourcesDir() {
  if (app.isPackaged) {
    return process.resourcesPath;
  }
  return path.join(__dirname, 'resources');
}

// Unify all agent log files under <userData>/logs/.
//
// The Go agent server hardcodes paths like `logs/llm_debug.log` and
// `logs/schedule.log` (also `logs/agent_prompts/` when LOG_AGENT_PROMPTS=true)
// relative to its cwd, which in packaged mode is the .app's Resources dir.
// That means those logs land *inside the app bundle* and get wiped on every
// reinstall/upgrade. We symlink Resources/logs → <userData>/logs so all log
// files end up in the same persistent place as agent.log + workspace.log.
//
// Idempotent: safe to call on every launch.
function unifyAgentLogsDir(userDataPath) {
  if (!app.isPackaged) return; // dev runs from agent_go/, leave logs there
  try {
    const userLogsDir = path.join(userDataPath, 'logs');
    const resourcesLogsDir = path.join(getResourcesDir(), 'logs');
    fs.mkdirSync(userLogsDir, { recursive: true });

    let stat;
    try { stat = fs.lstatSync(resourcesLogsDir); } catch (_) { stat = null; }

    if (stat && stat.isSymbolicLink()) {
      const current = fs.readlinkSync(resourcesLogsDir);
      if (current === userLogsDir) return; // already linked correctly
      fs.unlinkSync(resourcesLogsDir);
    } else if (stat && stat.isDirectory()) {
      // Move any existing files into userLogsDir (don't lose old logs), then rm dir.
      for (const entry of fs.readdirSync(resourcesLogsDir)) {
        const src = path.join(resourcesLogsDir, entry);
        const dst = path.join(userLogsDir, entry);
        try {
          if (!fs.existsSync(dst)) fs.renameSync(src, dst);
        } catch (e) {
          console.warn('[main] could not move existing log', entry, e.message);
        }
      }
      try { fs.rmSync(resourcesLogsDir, { recursive: true, force: true }); } catch (_) {}
    }

    fs.symlinkSync(userLogsDir, resourcesLogsDir, 'dir');
    console.log(`[main] Linked ${resourcesLogsDir} → ${userLogsDir}`);
  } catch (err) {
    console.warn('[main] Failed to unify logs dir:', err && err.message ? err.message : err);
  }
}

function getBinaryPath(name) {
  const base = getResourcesDir();
  return path.join(base, name);
}

function getAppIconPath() {
  if (app.isPackaged) {
    return path.join(process.resourcesPath, 'icons', 'icon.png');
  }
  return path.join(__dirname, 'resources', 'icons', 'icon.png');
}

function loadAppIcon() {
  const iconPath = getAppIconPath();
  if (!fs.existsSync(iconPath)) {
    console.warn(`[main] App icon not found at ${iconPath}`);
    return null;
  }

  const icon = nativeImage.createFromPath(iconPath);
  if (icon.isEmpty()) {
    console.warn(`[main] Failed to load app icon from ${iconPath}`);
    return null;
  }

  return icon;
}

function applyAppIcon() {
  const icon = loadAppIcon();
  if (!icon) return;
  baseDockIcon = icon;

  if (process.platform === 'darwin' && app.dock) {
    app.dock.setIcon(icon);
  }
}

function createDockActivityIcon(frameIndex) {
  const baseIcon = baseDockIcon || loadAppIcon();
  if (!baseIcon || baseIcon.isEmpty()) return null;
  baseDockIcon = baseIcon;

  const size = 256;
  const bitmap = Buffer.from(baseIcon.resize({ width: size, height: size }).toBitmap());
  const frames = [
    { ringOpacity: 0.28, dotOpacity: 0.95, radius: 22 },
    { ringOpacity: 0.58, dotOpacity: 1, radius: 34 },
    { ringOpacity: 0.24, dotOpacity: 0.9, radius: 46 },
  ];
  const frame = frames[frameIndex % frames.length];
  const cx = 196;
  const cy = 60;
  const stroke = 14;
  const cyan = { r: 34, g: 211, b: 238 };

  const blendPixel = (x, y, opacity) => {
    if (x < 0 || y < 0 || x >= size || y >= size) return;
    const idx = (y * size + x) * 4;
    const inv = 1 - opacity;
    // Electron bitmap buffers are BGRA; keep alpha opaque so the Dock tile stays crisp.
    bitmap[idx] = Math.round(bitmap[idx] * inv + cyan.b * opacity);
    bitmap[idx + 1] = Math.round(bitmap[idx + 1] * inv + cyan.g * opacity);
    bitmap[idx + 2] = Math.round(bitmap[idx + 2] * inv + cyan.r * opacity);
    bitmap[idx + 3] = 255;
  };

  const minX = Math.max(0, Math.floor(cx - frame.radius - stroke));
  const maxX = Math.min(size - 1, Math.ceil(cx + frame.radius + stroke));
  const minY = Math.max(0, Math.floor(cy - frame.radius - stroke));
  const maxY = Math.min(size - 1, Math.ceil(cy + frame.radius + stroke));

  for (let y = minY; y <= maxY; y += 1) {
    for (let x = minX; x <= maxX; x += 1) {
      const dist = Math.hypot(x - cx, y - cy);
      if (dist <= 18) {
        blendPixel(x, y, frame.dotOpacity);
      } else if (Math.abs(dist - frame.radius) <= stroke / 2) {
        blendPixel(x, y, frame.ringOpacity);
      }
    }
  }

  return nativeImage.createFromBitmap(bitmap, { width: size, height: size });
}

function isMainWindowBackgrounded() {
  if (!mainWindow || mainWindow.isDestroyed()) return true;
  return mainWindow.isMinimized() || !mainWindow.isVisible();
}

function stopDockActivityAnimation() {
  if (dockActivityFrameTimer) {
    clearInterval(dockActivityFrameTimer);
    dockActivityFrameTimer = null;
  }
  if (process.platform === 'darwin' && app.dock && dockActivityBounceId !== null) {
    app.dock.cancelBounce(dockActivityBounceId);
    dockActivityBounceId = null;
  }
  dockActivityFrameIndex = 0;
  if (process.platform === 'darwin' && app.dock && baseDockIcon && !baseDockIcon.isEmpty()) {
    app.dock.setIcon(baseDockIcon);
  }
}

function renderDockActivityFrame() {
  if (process.platform !== 'darwin' || !app.dock) return;
  const icon = createDockActivityIcon(dockActivityFrameIndex);
  dockActivityFrameIndex += 1;
  if (icon && !icon.isEmpty()) {
    app.dock.setIcon(icon);
  }
}

function updateDockActivityAnimation() {
  if (process.platform !== 'darwin' || !app.dock) return;
  const shouldAnimate = dockActivityRunningCount > 0 && isMainWindowBackgrounded();
  if (!shouldAnimate) {
    stopDockActivityAnimation();
    return;
  }

  if (!dockActivityFrameTimer) {
    renderDockActivityFrame();
    dockActivityFrameTimer = setInterval(renderDockActivityFrame, 650);
    if (dockActivityBounceId === null) {
      dockActivityBounceId = app.dock.bounce('informational');
    }
  }
}

function openMainWindow() {
  if (mainWindow) {
    mainWindow.show();
    mainWindow.focus();
    return;
  }

  const devUrl = process.env.DEV_URL;
  if (devUrl) {
    createWindow(devUrl);
    return;
  }

  if (dynamicAgentPort) {
    createWindow();
  }
}

function createTray() {
  if (process.platform !== 'darwin' || tray) {
    return;
  }

  const iconPath = getAppIconPath();
  if (!fs.existsSync(iconPath)) {
    console.warn(`[main] Tray icon not found at ${iconPath}`);
    return;
  }

  const icon = nativeImage.createFromPath(iconPath);
  if (icon.isEmpty()) {
    console.warn(`[main] Failed to load tray icon from ${iconPath}`);
    return;
  }

  const trayIcon = icon.resize({ width: 18, height: 18 });

  tray = new Tray(trayIcon);
  tray.setToolTip('AgentWorks');
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: 'Open AgentWorks', click: openMainWindow },
    { type: 'separator' },
    { label: 'Quit AgentWorks (Stop Servers)', click: () => app.quit() },
  ]));
  tray.on('click', openMainWindow);
}

// isPortInUse function is no longer needed with dynamic ports
// but we keep it if we ever need to check specific ports

function spawnWorkspace(userDataPath) {
  return new Promise((resolve, reject) => {
    const bin = getBinaryPath('workspace-server');
    if (!fs.existsSync(bin)) {
      return reject(new Error(`Workspace server binary not found at ${bin}. Place workspace-server in desktop/resources/ for development.`));
    }
    const docsDir = process.env.RUNLOOP_DOCS_DIR || path.join(userDataPath, 'workspace-docs');
    const dataDir = path.join(userDataPath, 'data');
    const logsDir = path.join(userDataPath, 'logs');

    if (!fs.existsSync(dataDir)) {
      fs.mkdirSync(dataDir, { recursive: true });
    }
    if (!fs.existsSync(logsDir)) {
      fs.mkdirSync(logsDir, { recursive: true });
    }

    const logFile = path.join(logsDir, 'workspace.log');
    const logStream = createBoundedLogWriter(logFile);

    // Load Settings
    const settings = loadSettings();
    const env = { 
      ...process.env, 
      DOCS_DIR: docsDir, 
      DATA_DIR: dataDir,
      // workspace-server executes /api/execute shell commands. Mark it native so
      // the safe shell env preserves the imported login-shell PATH/HOME instead
      // of using the Docker-style minimal PATH.
      NATIVE_WORKSPACE: 'true',
      WORKSPACE_API_TOKEN: workspaceApiToken
    };

    // Prefer fixed port 45679 so frontend localStorage stays stable across launches;
    // spawnServer (lib/servers.js) falls forward to the next free port.
    spawnServer({
      name: 'workspace', bin, args: ['server', '--port', spawnServer.PORT_PLACEHOLDER, '--docs-dir', docsDir],
      preferredPort: 45679, env, log: logStream, echoToConsole: true,
    }).then(({ child, port }) => {
      workspaceProcess = child;
      dynamicWorkspacePort = port;
      console.log(`[main] Workspace server started on dynamic port: ${dynamicWorkspacePort}`);
      resolve();
    }).catch(reject);
  });
}

function spawnAgent(userDataPath) {
  return new Promise((resolve, reject) => {
    const bin = getBinaryPath('agent-server');
    if (!fs.existsSync(bin)) {
      return reject(new Error(`Agent server binary not found at ${bin}. Place agent-server in desktop/resources/ for development.`));
    }
    const cwd = app.isPackaged ? getResourcesDir() : path.join(__dirname, '..', 'agent_go');
    
    // Ensure logs directory exists
    const logsDir = path.join(userDataPath, 'logs');
    if (!fs.existsSync(logsDir)) {
      fs.mkdirSync(logsDir, { recursive: true });
    }

    const logFile = path.join(logsDir, 'agent.log');
    const logStream = createBoundedLogWriter(logFile);

    // Load settings + resolve docsDir up-front so the MCP rewrite below
    // (and the env block further down) can both use them.
    const settings = loadSettings();
    const docsDir = process.env.RUNLOOP_DOCS_DIR || path.join(userDataPath, 'workspace-docs');

    // MCP Config handling
    const configDir = path.join(userDataPath, 'configs');
    if (!fs.existsSync(configDir)) {
      fs.mkdirSync(configDir, { recursive: true });
    }
    const mcpConfigPath = path.join(configDir, 'mcp_servers.json');

    // Copy default config if it doesn't exist in userData
    if (!fs.existsSync(mcpConfigPath)) {
      const defaultConfigPath = path.join(cwd, 'configs', 'mcp_servers_clean.json');
      if (fs.existsSync(defaultConfigPath)) {
        try {
          fs.copyFileSync(defaultConfigPath, mcpConfigPath);
          console.log(`[agent] Copied default config to ${mcpConfigPath}`);
        } catch (err) {
          console.error(`[agent] Failed to copy default config: ${err}`);
        }
      } else {
        console.warn(`[agent] Default MCP config not found at ${defaultConfigPath}`);
      }
    }

    // Rewrite relative `../workspace-docs` paths to the absolute resolved
    // docsDir, plus ensure the Downloads subdir exists. The default MCP
    // config (and many older user configs) hardcode `../workspace-docs/...`
    // which only resolves when the agent's cwd is `<repo>/agent_go/`. In the
    // packaged app the cwd is the .app's Resources dir, so the relative path
    // points at a non-existent location and stdio MCPs fail with "chdir ...:
    // no such file or directory". Patches both `working_dir` strings and any
    // arg values. Idempotent.
    try {
      fs.mkdirSync(path.join(docsDir, 'Downloads'), { recursive: true });
    } catch (e) {
      console.warn('[agent] Could not ensure Downloads dir:', e.message);
    }
    try {
      let raw = fs.readFileSync(mcpConfigPath, 'utf8');
      const original = raw;

      // (a) Migrate legacy relative `../workspace-docs` to absolute current docsDir.
      raw = raw.replaceAll('../workspace-docs', docsDir);

      // (b) Migrate any previously-recorded absolute docsDir to the new one.
      // This is what makes the rewrite dynamic when the user changes their
      // workspace folder via Settings → Workspace Folder → Change…
      if (settings.previousDocsDir && settings.previousDocsDir !== docsDir) {
        raw = raw.replaceAll(settings.previousDocsDir, docsDir);
      }

      if (raw !== original) {
        fs.writeFileSync(mcpConfigPath, raw);
        console.log(`[agent] Rewrote workspace-docs paths in mcp_servers.json → ${docsDir}`);
      }
    } catch (e) {
      console.warn('[agent] Could not rewrite mcp_servers.json:', e.message);
    }

    // Remember which docsDir we just substituted so the next spawn (after a
    // potential folder change) can migrate from it.
    if (settings.previousDocsDir !== docsDir) {
      saveSettings({ ...settings, previousDocsDir: docsDir });
    }

    // Port resolved by spawnServer — prefer fixed 45678 so frontend localStorage persists.
    const args = [
      'server',
      '--port', spawnServer.PORT_PLACEHOLDER,
      '--log-file', logFile,
      '--log-level', 'debug',
      '--mcp-config', mcpConfigPath
    ];

    // settings and docsDir are loaded earlier (before the MCP rewrite block).
    // WORKSPACE_DOCS_PATH below tells the agent the real filesystem path so
    // LLM-generated shell commands (jq, cat, etc.) use the correct absolute
    // paths. The same docsDir is also passed to workspace-server via --docs-dir
    // in spawnWorkspace().
    const env = {
      ...process.env,
      // Keep workflow coding-CLI projections stable across app/server restarts
      // and separate from workspace documents. The Go server also has a safe
      // per-user fallback for direct launches, but Desktop owns a more precise
      // application data root and should pass it explicitly.
      RUNLOOP_USER_DATA_DIR: userDataPath,
      AGENTWORKS_STATE_ROOT: process.env.AGENTWORKS_STATE_ROOT || path.join(userDataPath, 'state'),
      WORKSPACE_API_URL: `http://127.0.0.1:${dynamicWorkspacePort}`,
      WORKSPACE_DOCS_PATH: docsDir,
      DOCS_DIR: docsDir,
      LOG_FILE: logFile,
      // Both servers run as native binaries on the host (no Docker). Without
      // this, the agent assumes the workspace is in Docker and emits
      // host.docker.internal URLs in MCP_API_URL — which the LLM-generated
      // shell commands then fail to reach (host.docker.internal isn't
      // resolvable on macOS without Docker Desktop running).
      NATIVE_WORKSPACE: 'true',
      WORKSPACE_API_TOKEN: workspaceApiToken
    };


    // Debug: persist the final system prompt + user message + tool calls for
    // every LLM call to <cwd>/logs/agent_prompts/{session_id}/. Off by default
    // because output is verbose; useful when debugging why the LLM produced
    // a particular shell command or chose a tool. Do not inherit this from the
    // user's shell in production; the explicit app setting is authoritative.
    if (settings.logAgentPrompts === true) {
      env.LOG_AGENT_PROMPTS = 'true';
    } else {
      delete env.LOG_AGENT_PROMPTS;
    }

    spawnServer({
      name: 'agent', bin, args, preferredPort: 45678, cwd,
      env: (port) => ({
        ...env,
        // OAuth tools run without an incoming browser request, so they need a
        // callback origin in the process environment. Use the port selected by
        // the desktop launcher; an explicit deployment value still wins.
        PUBLIC_URL: env.PUBLIC_URL || `http://127.0.0.1:${port}`,
        MCP_AGENT_SERVER_URL: env.MCP_AGENT_SERVER_URL || `http://127.0.0.1:${port}`,
      }),
      log: logStream, echoToConsole: true,
    }).then(({ child, port }) => {
      agentProcess = child;
      dynamicAgentPort = port;
      const startupMsg = `[agent] Spawned agent-server with log-file=${logFile}, port=${port}\n`;
      console.log(startupMsg.trim());
      logStream.write(startupMsg);
      resolve();
    }).catch(reject);
  });
}

// Readiness polling is shared with the other desktop shells (lib/health.js).
function waitForHealth(agentUrl, workspaceUrl) {
  return waitForServersHealth({
    checks: [
      { name: 'agent (port ' + dynamicAgentPort + ')', url: agentUrl },
      { name: 'workspace (port ' + dynamicWorkspacePort + ')', url: workspaceUrl },
    ],
    timeoutMs: HEALTH_TIMEOUT_MS,
    pollMs: HEALTH_POLL_MS,
    initialDelayMs: HEALTH_INITIAL_DELAY_MS,
    hint: 'Ensure agent-server and workspace-server are in desktop/resources/.',
  });
}

// Update flow for the unsigned macOS build.
//
// We don't use electron-updater / Squirrel.Mac — those assume a signed +
// notarized app. With our ad-hoc build they fail unpredictably.
//
// Instead: poll GitHub Releases on startup; if a newer tag exists, prompt
// the user. On "Update" we spawn install.sh in a *detached* shell (nohup so
// it survives AgentWorks quitting) and quit ourselves. install.sh kills any
// leftover AgentWorks processes, downloads the new dmg, replaces
// the app bundle, strips quarantine, and relaunches.
function fetchJsonWithRedirects(url, redirectsLeft = 6) {
  return new Promise((resolve, reject) => {
    const req = https.get(url, { headers: { 'User-Agent': 'AgentWorks' } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        res.resume();
        if (redirectsLeft <= 0) {
          reject(new Error('Too many redirects'));
          return;
        }
        const redirectUrl = new URL(res.headers.location, url).toString();
        fetchJsonWithRedirects(redirectUrl, redirectsLeft - 1).then(resolve, reject);
        return;
      }

      let data = '';
      res.on('data', (chunk) => data += chunk);
      res.on('end', () => {
        if (res.statusCode !== 200) {
          reject(new Error(`GitHub returned HTTP ${res.statusCode}`));
          return;
        }
        try {
          resolve(JSON.parse(data));
        } catch (err) {
          reject(new Error(`Could not parse GitHub response: ${err.message}`));
        }
      });
    });
    req.on('error', reject);
  });
}

function checkForUpdates(manual = false) {
  if (!app.isPackaged) {
    console.log('[update] Skipping check — not packaged');
    if (manual) {
      dialog.showMessageBox({
        type: 'info',
        title: 'Updates',
        message: 'Update checks are disabled in development mode.',
        buttons: ['OK'],
      });
    }
    return;
  }

  // NOT /releases/latest. This repository also ships SparkQuill, whose tags are
  // namespaced `sparkquill-v*`, and /releases/latest returns whichever app
  // released most recently regardless of app. When a SparkQuill release is
  // newest that endpoint returns it, its tag parses to a garbage version, the
  // comparison below says "not newer", and AgentWorks silently stops seeing its
  // OWN updates. Confirmed live on 2026-08-02. Filter to plain `v<digit>` tags.
  fetchJsonWithRedirects('https://api.github.com/repos/manishiitg/coding-agent-loop/releases?per_page=30')
    .then(async (releases) => {
      const release = Array.isArray(releases)
        ? releases.find((r) => r && !r.draft && /^v\d/.test(r.tag_name || ''))
        : null;
      if (!release) {
        if (manual) {
          dialog.showMessageBox({ type: 'info', title: 'Updates', message: 'No AgentWorks releases found.', buttons: ['OK'] });
        }
        return;
      }
      const latestVersion = (release.tag_name || '').replace(/^v/, '');
      const currentVersion = app.getVersion();
      if (!latestVersion || !isNewerVersion(latestVersion, currentVersion)) {
        if (manual) {
          dialog.showMessageBox({
            type: 'info',
            title: "You're up to date",
            message: `AgentWorks ${currentVersion} is the latest version.`,
            buttons: ['OK'],
          });
        }
        return;
      }
      if (updateState.downloading || (updateState.dmgPath && updateState.version === latestVersion)) {
        // A download for this version is already in flight or finished — don't
        // start a second one. Re-surface the ready prompt if it's done.
        if (updateState.dmgPath) promptInstallReady(latestVersion);
        else if (manual) {
          dialog.showMessageBox({ type: 'info', title: 'Downloading update', message: `AgentWorks ${latestVersion} is downloading…`, buttons: ['OK'] });
        }
        return;
      }
      const notes = formatReleaseNotes(release.body);
      const choice = await dialog.showMessageBox({
        type: 'info',
        title: 'Update Available',
        message: `AgentWorks ${latestVersion} is available.`,
        detail:
          `You're on ${currentVersion}.\n\n` +
          (notes ? `What's new in ${latestVersion}:\n${notes}\n\n` : '') +
          `AgentWorks will download the new version (~150 MB) in the background — you can keep working. When it's ready you'll be asked to restart to install (a few seconds).`,
        buttons: ['Download', 'Full Notes…', 'Later'],
        defaultId: 0,
        cancelId: 2,
      });
      if (choice.response === 0) {
        downloadAndPrepareUpdate(latestVersion);
      } else if (choice.response === 1) {
        if (release.html_url) shell.openExternal(release.html_url);
      }
    })
    .catch((err) => {
      console.warn('[update] check failed:', err?.message || err);
      if (manual) {
        dialog.showErrorBox('Update check failed', String(err?.message || err));
      }
    });
}

// Compare semver-ish strings (e.g. "1.25.10" vs "1.25.9"). Numeric per dotted
// segment; missing segments treated as 0. Returns true if a > b.
function isNewerVersion(a, b) {
  const pa = a.split('.').map((n) => parseInt(n, 10) || 0);
  const pb = b.split('.').map((n) => parseInt(n, 10) || 0);
  const len = Math.max(pa.length, pb.length);
  for (let i = 0; i < len; i++) {
    const x = pa[i] || 0, y = pb[i] || 0;
    if (x > y) return true;
    if (x < y) return false;
  }
  return false;
}

// Render a GitHub release body (markdown) down to plain text for the native
// update dialog's `detail` field, so users see what's in the update before
// downloading it. Strips markdown syntax and caps the length to keep it sane.
function formatReleaseNotes(body, maxLen = 1400) {
  if (!body || !body.trim()) return '';
  let text = body
    .replace(/```[\s\S]*?```/g, '')        // drop fenced code blocks
    .replace(/^#{1,6}\s*/gm, '')           // strip header marks (## …)
    .replace(/\*\*(.*?)\*\*/g, '$1')       // bold
    .replace(/`([^`]+)`/g, '$1')           // inline code
    .replace(/^\s*[-*]\s+/gm, '• ')        // bullets
    .replace(/^\s*---+\s*$/gm, '')         // horizontal rules
    .replace(/\n{3,}/g, '\n\n')            // collapse blank runs
    .trim();
  if (text.length > maxLen) {
    text = text.slice(0, maxLen).replace(/\s+\S*$/, '') + '…';
  }
  return text;
}

// Update state for the background-download flow. The DMG is fetched while the
// app stays usable (progress to the dock + renderer); install is a fast
// mount+copy+relaunch using the pre-downloaded file, triggered on user consent.
let updateState = { downloading: false, version: null, dmgPath: null };

function updateCacheDir() {
  return path.join(app.getPath('userData'), 'updates');
}

// Remove any previously-cached update artifacts so a stale/partial dmg can't be
// reused and the cache doesn't accumulate ~150 MB files across versions.
function cleanUpdateCache() {
  try {
    const dir = updateCacheDir();
    if (!fs.existsSync(dir)) return;
    for (const f of fs.readdirSync(dir)) {
      try { fs.unlinkSync(path.join(dir, f)); } catch (_) {}
    }
  } catch (_) {}
}

function sendUpdateProgress(payload) {
  try {
    if (mainWindow && !mainWindow.isDestroyed()) {
      mainWindow.webContents.send('update-progress', payload);
    }
  } catch (_) {}
}

// Download url → destPath, following GitHub's redirect to the asset CDN, and
// reporting (transferred, total) bytes as the body streams in.
function downloadFileWithProgress(url, destPath, onProgress, redirectsLeft = 6) {
  return new Promise((resolve, reject) => {
    const req = https.get(url, { headers: { 'User-Agent': 'AgentWorks' } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        res.resume();
        if (redirectsLeft <= 0) { reject(new Error('Too many redirects')); return; }
        downloadFileWithProgress(res.headers.location, destPath, onProgress, redirectsLeft - 1).then(resolve, reject);
        return;
      }
      if (res.statusCode !== 200) {
        res.resume();
        reject(new Error(`HTTP ${res.statusCode}`));
        return;
      }
      const total = parseInt(res.headers['content-length'] || '0', 10);
      let transferred = 0;
      const file = fs.createWriteStream(destPath);
      res.on('data', (chunk) => {
        transferred += chunk.length;
        if (onProgress) onProgress(transferred, total);
      });
      res.on('error', (err) => { file.destroy(); try { fs.unlinkSync(destPath); } catch (_) {} reject(err); });
      file.on('error', (err) => { try { fs.unlinkSync(destPath); } catch (_) {} reject(err); });
      file.on('finish', () => file.close((err) => err ? reject(err) : resolve()));
      res.pipe(file);
    });
    req.on('error', reject);
  });
}

// Background download of the update dmg with progress, then prompt to install.
// targetVersion is the bare version (no leading "v").
async function downloadAndPrepareUpdate(targetVersion) {
  if (updateState.downloading) return;
  const versionNoV = String(targetVersion).replace(/^v/, '');
  updateState.downloading = true;
  updateState.version = versionNoV;
  updateState.dmgPath = null;

  const dmgName = `AgentWorks-${versionNoV}-arm64.dmg`;
  const url = `https://github.com/manishiitg/coding-agent-loop/releases/download/v${versionNoV}/${dmgName}`;
  const dir = updateCacheDir();
  try { fs.mkdirSync(dir, { recursive: true }); } catch (_) {}
  cleanUpdateCache();
  const destPath = path.join(dir, dmgName);

  console.log('[update] downloading v' + versionNoV + ' in background → ' + destPath);
  sendUpdateProgress({ status: 'downloading', version: versionNoV, percent: 0, transferred: 0, total: 0 });
  if (tray) { try { tray.setToolTip(`AgentWorks — downloading v${versionNoV}…`); } catch (_) {} }

  let lastEmit = 0;
  try {
    await downloadFileWithProgress(url, destPath, (transferred, total) => {
      const percent = total > 0 ? transferred / total : 0;
      try { if (mainWindow && !mainWindow.isDestroyed()) mainWindow.setProgressBar(percent > 0 ? percent : -1); } catch (_) {}
      const now = Date.now();
      if (now - lastEmit > 200 || (total > 0 && transferred >= total)) {
        lastEmit = now;
        sendUpdateProgress({ status: 'downloading', version: versionNoV, percent, transferred, total });
      }
    });
  } catch (err) {
    updateState.downloading = false;
    try { if (mainWindow && !mainWindow.isDestroyed()) mainWindow.setProgressBar(-1); } catch (_) {}
    if (tray) { try { tray.setToolTip('AgentWorks'); } catch (_) {} }
    console.error('[update] download failed:', err?.message || err);
    sendUpdateProgress({ status: 'error', version: versionNoV, message: String(err?.message || err) });
    dialog.showErrorBox('Update download failed', `Could not download AgentWorks ${versionNoV}.\n\n${String(err?.message || err)}`);
    return;
  }

  updateState.downloading = false;
  updateState.dmgPath = destPath;
  try { if (mainWindow && !mainWindow.isDestroyed()) mainWindow.setProgressBar(-1); } catch (_) {}
  if (tray) { try { tray.setToolTip(`AgentWorks — v${versionNoV} ready to install`); } catch (_) {} }
  sendUpdateProgress({ status: 'ready', version: versionNoV, percent: 1 });

  promptInstallReady(versionNoV);
}

// Non-blocking "Restart & Install / Later" prompt once the dmg is downloaded.
async function promptInstallReady(versionNoV) {
  if (!updateState.dmgPath || !fs.existsSync(updateState.dmgPath)) return;
  const choice = await dialog.showMessageBox({
    type: 'info',
    title: 'Update Ready',
    message: `AgentWorks ${versionNoV} is downloaded and ready to install.`,
    detail: 'Installing takes a few seconds and relaunches the app. Any in-progress chats or workflow runs will be interrupted.',
    buttons: ['Restart & Install', 'Later'],
    defaultId: 0,
    cancelId: 1,
  });
  if (choice.response === 0) installDownloadedUpdate();
}

// Fast install of the already-downloaded dmg: install.sh skips the download via
// RUNLOOP_DMG_PATH, so the post-quit gap is just mount+copy+relaunch.
function installDownloadedUpdate() {
  if (!updateState.dmgPath || !fs.existsSync(updateState.dmgPath)) {
    dialog.showErrorBox('Update error', 'The downloaded update was not found. Please check for updates again.');
    return;
  }
  const innerCmd = `export RUNLOOP_VERSION='v${updateState.version}'; export RUNLOOP_DMG_PATH='${updateState.dmgPath}'; curl -fsSL https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/install.sh | bash > /tmp/runloop-update.log 2>&1`;
  const wrapped = `nohup bash -c ${JSON.stringify(innerCmd)} >/dev/null 2>&1 &`;

  console.log('[update] spawning detached installer for v' + updateState.version + ' (pre-downloaded)');
  try {
    const child = spawn('/bin/bash', ['-lc', wrapped], { detached: true, stdio: 'ignore' });
    child.unref();
  } catch (err) {
    dialog.showErrorBox('Update failed to start', String(err?.message || err));
    return;
  }

  try {
    const { Notification } = require('electron');
    if (Notification.isSupported()) {
      new Notification({
        title: 'Installing AgentWorks…',
        body: `Installing v${updateState.version}. The app will reopen automatically in a few seconds.`,
        silent: false,
      }).show();
    }
  } catch (_) {}
  if (tray) { try { tray.setToolTip(`AgentWorks — installing v${updateState.version}…`); } catch (_) {} }

  // Brief delay so the installer forks before our pkill-prone quit.
  setTimeout(() => app.quit(), 1000);
}

// Renderer-driven install trigger (the in-app "Restart to install" button).
ipcMain.on('restart-to-install', () => {
  if (updateState.dmgPath) installDownloadedUpdate();
});

function killChildren() {
  if (workspaceProcess) {
    try {
      workspaceProcess.kill('SIGTERM');
    } catch (_) {}
    workspaceProcess = null;
  }
  if (agentProcess) {
    try {
      agentProcess.kill('SIGTERM');
    } catch (_) {}
    agentProcess = null;
  }
  if (agentPromptLogPruneInterval) {
    clearInterval(agentPromptLogPruneInterval);
    agentPromptLogPruneInterval = null;
  }
}

function showErrorAndExit(message) {
  dialog.showMessageBoxSync({
    type: 'error',
    title: 'AgentWorks',
    message: 'Startup failed',
    detail: message,
  });
  killChildren();
  app.exit(1);
}

function createWindow(initialUrl) {
  const targetUrl = withDesktopVersionCacheBust(initialUrl || `http://127.0.0.1:${dynamicAgentPort}`);
  console.log('[main] Creating main window for:', targetUrl);

  mainWindow = new BrowserWindow({
    width: 1400,
    height: 900,
    backgroundColor: '#1e1e1e', // Dark background to match theme and prevent white flash
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      nodeIntegration: false,
      contextIsolation: true,
    },
  });
  
  console.log('[main] Initializing window handlers...');

  mainWindow.webContents.on('did-start-loading', () => {
    console.log('[main] Window started loading:', targetUrl);
  });

  mainWindow.webContents.on('did-finish-load', () => {
    console.log('[main] Window finished loading:', mainWindow?.webContents.getURL());
  });

  mainWindow.webContents.on('did-fail-load', (_event, errorCode, errorDescription, validatedURL) => {
    // -3 is ERR_ABORTED (benign — e.g. a canceled/replaced subframe load).
    if (errorCode === -3) return;
    diagLog('[main] Window failed to load:', { errorCode, errorDescription, validatedURL });
  });

  mainWindow.webContents.on('render-process-gone', (_event, details) => {
    diagLog('[main] Renderer process gone:', details);
    // Auto-recover a crashed renderer so the user isn't stranded on a blank
    // screen having to discover Ctrl+R themselves.
    if (details && details.reason !== 'clean-exit' && mainWindow && !mainWindow.isDestroyed()) {
      diagLog('[main] Auto-reloading renderer after crash');
      try { mainWindow.webContents.reload(); } catch (e) { diagLog('[main] auto-reload failed:', String(e)); }
    }
  });

  mainWindow.webContents.on('unresponsive', () => {
    diagLog('[main] Window became unresponsive');
  });

  mainWindow.webContents.on('responsive', () => {
    diagLog('[main] Window became responsive again');
  });

  mainWindow.webContents.on('preload-error', (_event, preloadPath, error) => {
    diagLog('[main] Preload error:', preloadPath, String((error && error.stack) || error));
  });

  // window.open / target=_blank and plain <a href> navigations to anything
  // non-local open in the system browser; loopback/app navigation proceeds.
  // Shared with the other desktop shells (lib/externalNav.js).
  attachExternalNavigation(mainWindow.webContents, shell);

  const devUrl = process.env.DEV_URL;
  if (devUrl && process.env.ELECTRON_OPEN_DEVTOOLS === '1') {
    // DevTools is expensive on large workflow/event views; keep it opt-in.
    mainWindow.webContents.openDevTools({ mode: 'detach' });
  }

  // Always allow DevTools via Cmd+Shift+I / Ctrl+Shift+I
  mainWindow.webContents.on('before-input-event', (event, input) => {
    if ((input.meta || input.control) && input.shift && input.key === 'I') {
      mainWindow.webContents.toggleDevTools();
    }
  });

  mainWindow.loadURL(targetUrl);

  ['minimize', 'hide', 'restore', 'show', 'focus'].forEach((eventName) => {
    mainWindow.on(eventName, updateDockActivityAnimation);
  });

  if (process.env.RUNLOOP_DOCK_ACTIVITY_PREVIEW === '1') {
    dockActivityRunningCount = 1;
    setTimeout(() => {
      if (!mainWindow || mainWindow.isDestroyed()) return;
      if (process.env.RUNLOOP_DOCK_ACTIVITY_PREVIEW_MINIMIZE === '1') {
        mainWindow.minimize();
      }
      updateDockActivityAnimation();
    }, 1200);
  }
  
  mainWindow.on('closed', () => {
    console.log('[main] Main window closed');
    mainWindow = null;
    updateDockActivityAnimation();
  });
}

app.whenReady().then(async () => {
  migrateLegacyUserData();
  applyAppIcon();
  createMenu();
  createTray();
  
  // If DEV_URL is set (e.g. http://localhost:5173), skip spawning servers and just load that URL
  const devUrl = process.env.DEV_URL;
  if (devUrl) {
    console.log(`[main] DEV_URL detected: ${devUrl}. Skipping local server spawn.`);
    createWindow(devUrl);
    return;
  }
  
  const userDataPath = app.getPath('userData');

  // Resolve workspace-docs dir (prompts user on first launch if needed).
  // Cached in process.env so spawn helpers pick it up consistently.
  let resolvedDocsDir;
  try {
    resolvedDocsDir = await resolveDocsDir();
    process.env.RUNLOOP_DOCS_DIR = resolvedDocsDir;
    console.log(`[main] Workspace docs dir: ${resolvedDocsDir}`);
  } catch (err) {
    showErrorAndExit('Failed to resolve workspace folder: ' + (err.message || err));
    return;
  }

  // Prompt only when an existing provider key file needs a prior AUTH_SECRET.
  // Fresh workspaces get an app-generated secret so first launch stays quiet.
  const settings = loadSettings();
  if (!settings.authSecret && !process.env.AUTH_SECRET) {
    const providerKeysPath = path.join(resolvedDocsDir, 'config', 'provider-api-keys.json');
    const providerKeysState = inspectProviderKeysFileForStartup(providerKeysPath);
    if (providerKeysState.migratableKeys) {
      try {
        migrateProviderKeysToGeneratedSecret(providerKeysPath, providerKeysState.migratableKeys, settings);
      } catch (err) {
        diagLog('[main] Failed to migrate provider keys:', String(err && err.message ? err.message : err));
        process.env.AUTH_SECRET = DEFAULT_AUTH_SECRET;
      }
    } else if (providerKeysState.needsUnlock) {
      const entered = await promptAuthSecret('unlock');
      if (entered) {
        saveSettings({ ...settings, authSecret: entered });
        process.env.AUTH_SECRET = entered;
      }
    } else {
      persistGeneratedAuthSecret(settings);
    }
  } else if (settings.authSecret) {
    process.env.AUTH_SECRET = settings.authSecret;
  }

  // 2. Spawn servers (in sequence: Workspace first, then Agent)
  try {
    unifyAgentLogsDir(userDataPath);
    startAgentPromptLogPruning(userDataPath);
    console.log('[main] Spawning local servers...');
    await spawnWorkspace(userDataPath);
    await spawnAgent(userDataPath);
  } catch (err) {
    showErrorAndExit(err.message || String(err));
    return;
  }

  // 3. Wait for health
  try {
    console.log('[main] Waiting for backend health...');
    const agentHealthUrl = `http://127.0.0.1:${dynamicAgentPort}/api/health`;
    const workspaceHealthUrl = `http://127.0.0.1:${dynamicWorkspacePort}/health`;
    await waitForHealth(agentHealthUrl, workspaceHealthUrl);
    await clearFrontendCacheIfVersionChanged();
  } catch (err) {
    showErrorAndExit(err.message || String(err));
    return;
  }

  // 4. Create Window
  createWindow();
  checkForUpdates();
  // Re-check every 4 hours so long-running sessions notice new releases
  // without requiring a manual restart.
  setInterval(() => checkForUpdates(), 4 * 60 * 60 * 1000);
});

app.on('window-all-closed', () => {
  console.log('[main] window-all-closed');
  if (process.platform !== 'darwin') {
    app.quit();
  }
});

app.on('activate', () => {
  console.log('[main] activate');
  openMainWindow();
});

app.on('hide', updateDockActivityAnimation);
app.on('show', updateDockActivityAnimation);

app.on('before-quit', () => {
  console.log('[main] before-quit');
  stopDockActivityAnimation();
  killChildren();
});

app.on('will-quit', () => {
  console.log('[main] will-quit');
  killChildren();
});

app.on('quit', (_event, exitCode) => {
  console.log('[main] quit with exit code:', exitCode);
});

// A signal to the main process never runs the quit handlers above; without
// this the servers are orphaned. Shared with the other desktop shells.
installSignalShutdown({ app, stop: killChildren });

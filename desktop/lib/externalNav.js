'use strict';

// Anything that isn't the local app opens in the real browser rather than
// replacing (or popping up next to) the app window. Shared by every
// AgentWorks-family desktop shell.

function normalizeExternalUrl(rawUrl) {
  if (typeof rawUrl !== 'string') return null;
  const trimmed = rawUrl.trim();
  if (!trimmed) return null;
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol === 'http:' || parsed.protocol === 'https:' || parsed.protocol === 'mailto:') {
      return parsed.toString();
    }
  } catch (_) {
    return null;
  }
  return null;
}

function isLoopbackUrl(url) {
  return typeof url === 'string' && (url.startsWith('http://127.0.0.1') || url.startsWith('http://localhost'));
}

// isInternalNavUrl: same-window navigations the app itself performs.
function isInternalNavUrl(url) {
  return isLoopbackUrl(url) || url.startsWith('file://') || url.startsWith('app://') || url.startsWith('about:');
}

function makeOpenExternalUrl(shell) {
  return function openExternalUrl(rawUrl, source) {
    const externalUrl = normalizeExternalUrl(rawUrl);
    if (!externalUrl) {
      console.warn(`[main] Blocked unsupported external URL from ${source}:`, rawUrl);
      return;
    }
    shell.openExternal(externalUrl).catch((err) => {
      console.error(`[main] Failed to open external URL from ${source}`);
    });
  };
}

// attachExternalNavigation wires a BrowserWindow's webContents so that
// window.open / target=_blank and plain <a href> navigations to anything
// non-local open in the system browser, while loopback/app navigation
// proceeds normally.
function attachExternalNavigation(webContents, shell, { isInternal = isInternalNavUrl } = {}) {
  const openExternalUrl = makeOpenExternalUrl(shell);

  webContents.setWindowOpenHandler(({ url }) => {
    if (isLoopbackUrl(url)) return { action: 'allow' };
    const externalUrl = normalizeExternalUrl(url);
    if (externalUrl) openExternalUrl(externalUrl, 'window-open');
    else console.warn('[main] Blocking unsupported window-open URL:', url);
    return { action: 'deny' };
  });

  const redirectExternalNavigation = (event, url) => {
    if (isInternal(url)) return;
    event.preventDefault();
    const externalUrl = normalizeExternalUrl(url);
    if (externalUrl) openExternalUrl(externalUrl, 'will-navigate');
    else console.warn('[main] Blocking unsupported navigation URL:', url);
  };
  webContents.on('will-navigate', redirectExternalNavigation);
  webContents.on('will-redirect', redirectExternalNavigation);

  return openExternalUrl;
}

module.exports = { normalizeExternalUrl, isLoopbackUrl, isInternalNavUrl, makeOpenExternalUrl, attachExternalNavigation };

'use strict';

// Dedicated editor opening, deliberately NOT added to generic externalNav.
// No command URLs, extensions, hosts, auth fields or arbitrary query options.
function normalizeEditorUrl(raw) {
  if (typeof raw !== 'string' || raw.length > 16384) return null;
  try {
    const match = /^(vscode|cursor|zed):\/\/file(\/[^?#]*)$/.exec(raw);
    if (!match) return null;
    const originalPath = decodeURIComponent(match[2]);
    if (originalPath.split('/').some(p => p === '.' || p === '..')) return null;
    const uri = new URL(raw);
    if (uri.username || uri.password || uri.port || uri.hash) return null;
    const path = decodeURIComponent(uri.pathname);
    if (/[\x00-\x1f\x7f]/.test(path)) return null;
    if (['vscode:', 'cursor:', 'zed:'].includes(uri.protocol)) {
      if (uri.hostname !== 'file' || uri.search || !path.startsWith('/') || path === '/' || path.split('/').some(p => p === '..' || p === '.')) return null;
      return uri.toString();
    }
    return null;
  } catch (_) { return null; }
}

// No shell, user-supplied executable, app name, command or additional flags.
function jetbrainsArgs(target) {
  if (!target || typeof target.path !== 'string' || target.path.length > 16384 || !target.path.startsWith('/') || /[\x00-\x1f\x7f]/.test(target.path) || target.path.split('/').some(p => p === '..' || p === '.')) return null;
  if (!Number.isSafeInteger(target.line) || target.line < 0 || target.line > 10000000) return null;
  return ['-na', 'IntelliJ IDEA.app', '--args', ...(target.line ? ['--line', String(target.line)] : []), target.path];
}
// A project folder the server resolved, shown in Finder or File Explorer.
function revealFolderPath(raw) {
  if (typeof raw !== 'string' || raw.length > 16384 || /[\x00-\x1f\x7f]/.test(raw)) return null;
  const absolute = raw.startsWith('/') || /^[A-Za-z]:\\/.test(raw);
  if (!absolute || raw.split(/[\\/]/).some(p => p === '..' || p === '.')) return null;
  return raw;
}
function makeEditorOpener(shell, execFile) {
  return async function openEditor(payload) {
    if (payload?.reveal !== undefined) {
      const folder = revealFolderPath(payload.reveal);
      const rid = typeof payload?.requestId === 'string' && /^[a-zA-Z0-9:_-]{1,100}$/.test(payload.requestId) ? payload.requestId : 'unknown';
      const failed = folder ? await shell.openPath(folder) : 'unsupported';
      console.info(`[OPEN_EDITOR] request=${rid} op=reveal ok=${!failed}`);
      return failed ? { ok: false, error: 'This folder could not be opened.' } : { ok: true };
    }
    const uri = normalizeEditorUrl(payload?.url);
    const args = payload?.url === undefined ? jetbrainsArgs(payload?.jetbrains) : null;
    const id = typeof payload?.requestId === 'string' && /^[a-zA-Z0-9:_-]{1,100}$/.test(payload.requestId) ? payload.requestId : 'unknown';
    if ((!uri && !args) || (payload?.jetbrains && payload?.url !== undefined)) { console.warn(`[OPEN_EDITOR] request=${id} op=open ok=false reason=unsupported_url`); return { ok: false, error: 'This editor link is not supported.' }; }
    try {
      if (uri) await shell.openExternal(uri);
      else await new Promise((resolve, reject) => execFile('/usr/bin/open', args, { timeout: 10000 }, error => error ? reject(error) : resolve()));
      console.info(`[OPEN_EDITOR] request=${id} op=open ok=true`);
      return { ok: true };
    } catch (_) {
      console.warn(`[OPEN_EDITOR] request=${id} op=open ok=false reason=editor_unavailable`);
      return { ok: false, error: 'The editor could not open. Install it or choose another editor.' };
    }
  };
}
module.exports = { normalizeEditorUrl, jetbrainsArgs, revealFolderPath, makeEditorOpener };

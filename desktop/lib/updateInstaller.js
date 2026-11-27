'use strict';
const fs = require('fs');
const os = require('os');
const path = require('path');

// Use this release's installer and literal argv. Do not fetch executable code
// from main or interpolate app/cache paths into shell code.
function prepareDesktopUpdate(resources, executable, version, dmg) {
  if (!VERSION_RE.test(version)) throw new Error('Invalid update version');
  const stage = fs.mkdtempSync(path.join(os.tmpdir(), 'agentworks-update-'));
  const installer = path.join(stage, 'install.sh');
  fs.copyFileSync(path.join(resources, 'install.sh'), installer);
  return {
    command: '/bin/bash',
    args: ['-c', 'sleep 2; exec /bin/bash "$@"', 'agentworks-update', installer, '--update', '--version', releaseTag(version), '--dmg', dmg, '--app-dir', path.resolve(path.dirname(executable), '../../..')],
  };
}
// Match the running app architecture so Intel updates never fetch an ARM app.
function desktopUpdateAsset(version, arch) {
  if (!VERSION_RE.test(version)) throw new Error('Invalid update version');
  if (!['arm64', 'x64'].includes(arch)) throw new Error('Unsupported Mac architecture');
  return `AgentWorks-${version}-${arch}.dmg`;
}
// Releases are vX.Y.Z (version X.Y.Z). Nightly builds are GitHub pre-releases
// tagged nightly-X.Y.Z-nightly.N (version X.Y.Z-nightly.N). The nightly tag
// does not start with v<digit>, so apps without the nightly option and the
// installer never pick one up.
const VERSION_RE = /^\d+\.\d+\.\d+(-nightly\.\d+)?$/;
const NIGHTLY_TAG_RE = /^nightly-(\d+\.\d+\.\d+-nightly\.\d+)$/;
const RELEASE_TAG_RE = /^v(\d+\.\d+\.\d+)$/;

function releaseTag(version) {
  return version.includes('-nightly.') ? `nightly-${version}` : `v${version}`;
}

function parseVersion(version) {
  const m = /^(\d+)\.(\d+)\.(\d+)(?:-nightly\.(\d+))?$/.exec(String(version || ''));
  if (!m) return null;
  // A release outranks every nightly of the same X.Y.Z.
  return [Number(m[1]), Number(m[2]), Number(m[3]), m[4] === undefined ? Infinity : Number(m[4])];
}

// Returns true when version a is newer than b.
function isNewerVersion(a, b) {
  const pa = parseVersion(a);
  const pb = parseVersion(b);
  if (!pa) return false;
  if (!pb) return true;
  for (let i = 0; i < pa.length; i++) {
    if (pa[i] !== pb[i]) return pa[i] > pb[i];
  }
  return false;
}

// Pick the newest release the app should offer: releases always, nightly
// builds only when the person ticked "Get Nightly Builds".
function pickDesktopUpdate(releases, currentVersion, nightly) {
  let best = null;
  for (const r of Array.isArray(releases) ? releases : []) {
    if (!r || r.draft) continue;
    const tag = String(r.tag_name || '');
    const m = RELEASE_TAG_RE.exec(tag) || (nightly && r.prerelease ? NIGHTLY_TAG_RE.exec(tag) : null);
    if (!m) continue;
    if (RELEASE_TAG_RE.test(tag) && r.prerelease) continue;
    if (!best || isNewerVersion(m[1], best.version)) best = { release: r, version: m[1] };
  }
  if (!best || !isNewerVersion(best.version, currentVersion)) return null;
  return best;
}

module.exports = { prepareDesktopUpdate, desktopUpdateAsset, isNewerVersion, pickDesktopUpdate, releaseTag };

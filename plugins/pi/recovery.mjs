import { constants } from 'node:fs';
import { mkdir, lstat, readdir, open, unlink, rename } from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
const processStart = pid => {
  try { return execFileSync('ps', ['-p', String(pid), '-o', 'lstart='],
    { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'], timeout: 1000,
      env: { ...process.env, LC_ALL: 'C', TZ: 'UTC' } }).trim().split(/\s+/).join(' ') || undefined; }
  catch { return undefined; }
};
const started = processStart(process.pid);
const hash = value => createHash('sha256').update(value).digest('hex');

// Uncertainty metadata only: the bridge owns offers, credentials and bodies.
// Pi buffers custom entries until a conversation starts, so they cannot alone
// cover the bridge-response crash window. Each runtime owns a separate marker.
export class Recovery {
  constructor(root, session, lease) {
    this.root = path.join(root, 'pi-recovery');
    this.session = hash(session);
    this.lease = hash(lease);
    this.prefix = hash(JSON.stringify([session, lease])) + '-';
    this.file = path.join(this.root, this.prefix + randomUUID() + '.json');
    this.previous = [];
  }

  async prepare(accepted) {
    await mkdir(this.root, { mode: 0o700, recursive: true });
    const stat = await lstat(this.root);
    if (!stat.isDirectory() || stat.uid !== process.getuid() || stat.mode & 0o077)
      throw new Error('AgentWorks recovery directory is not private');
    const parent = await open(path.dirname(this.root), constants.O_RDONLY);
    try { await parent.sync(); } finally { await parent.close(); }
    let uncertain = false;
    for (const name of await readdir(this.root)) {
      // Only our UUID-named artifacts are eligible for housekeeping. A fresh
      // temporary file may belong to a concurrent writer.
      if (!/^[a-f0-9]{64}-[a-f0-9-]{36}\.json(?:\.[a-f0-9-]{36}\.tmp)?$/.test(name)) continue;
      const file = path.join(this.root, name);
      let handle;
      try { handle = await open(file, constants.O_RDONLY | constants.O_NOFOLLOW); }
      catch (error) { if (error.code === 'ENOENT') continue; throw error; }
      let state, recentMalformed = false, staleMalformed = false, temporary = name.endsWith('.tmp');
      try {
        const stat = await handle.stat();
        if (temporary && Date.now() - stat.mtimeMs < 60000) continue;
        try { state = JSON.parse(await handle.readFile('utf8')); }
        catch { state = {}; recentMalformed = Date.now() - stat.mtimeMs < 3000;
          staleMalformed = Date.now() - stat.mtimeMs >= 60000; }
        if (!state || typeof state !== 'object') state = {};
      } finally { await handle.close(); }
      if (recentMalformed) continue; // A legacy writer may still be filling its file.
      const currentLease = name.startsWith(this.prefix);
      if (!currentLease && state.session !== this.session && !staleMalformed && !temporary) continue;
      if (state.pid && state.pid !== process.pid) {
        try {
          process.kill(state.pid, 0);
          const actualStart = processStart(state.pid);
          if (!actualStart || !state.started || actualStart === state.started) continue;
        }
        catch (error) { if (error.code !== 'ESRCH') continue; }
      }
      if (temporary || !currentLease && staleMalformed) { await this.remove(file); continue; }
      if (!currentLease || !state.request && Array.isArray(state.ids) && state.ids.every(id => accepted.has(id))) {
        await this.remove(file);
      } else {
        uncertain = true;
        this.previous.push(file);
      }
    }
    return uncertain;
  }

  // Called only after the recovery notice was shown. Reporting ambiguity is
  // durable recovery progress, not native admission or permission to reinject.
  async acknowledge() {
    for (const file of this.previous) await this.remove(file);
    this.previous = [];
  }

  async save(ids = [], request = true) {
    if (!started) throw new Error('AgentWorks cannot identify this Pi process safely');
    const body = JSON.stringify({ request, ids, session: this.session, lease: this.lease, pid: process.pid, started });
    if (body === this.lastBody) return;
    const temp = this.file + '.' + randomUUID() + '.tmp';
    const handle = await open(temp, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
    try {
      await handle.writeFile(body); await handle.sync();
      await rename(temp, this.file); // Readers see a complete owner/state atomically.
    } finally { await handle.close(); await this.remove(temp); }
    const directory = await open(this.root, constants.O_RDONLY);
    try { await directory.sync(); } finally { await directory.close(); }
    this.lastBody = body;
  }

  async remove(file) {
    try { await unlink(file); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  }

  async retire() {
    for (const name of await readdir(this.root)) {
      if (name.startsWith(this.prefix) && name.endsWith('.json')) await this.remove(path.join(this.root, name));
    }
    this.lastBody = undefined;
  }
}

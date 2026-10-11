import { constants } from 'node:fs';
import { mkdir, lstat, readdir, open, unlink } from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import path from 'node:path';
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
      if (!name.endsWith('.json')) continue;
      const file = path.join(this.root, name);
      const handle = await open(file, constants.O_RDONLY | constants.O_NOFOLLOW);
      let state;
      try { state = JSON.parse(await handle.readFile('utf8')); }
      catch { state = {}; }
      finally { await handle.close(); }
      const currentLease = name.startsWith(this.prefix);
      if (!currentLease && state.session !== this.session) continue;
      if (state.pid && state.pid !== process.pid) {
        try { process.kill(state.pid, 0); continue; }
        catch (error) { if (error.code !== 'ESRCH') continue; }
      }
      if (!currentLease || !state.request && Array.isArray(state.ids) && state.ids.every(id => accepted.has(id))) {
        await unlink(file);
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

  async save(ids = []) {
    const body = JSON.stringify({ request: true, ids, session: this.session, lease: this.lease, pid: process.pid });
    if (body === this.lastBody) return;
    const first = this.lastBody === undefined;
    const handle = await open(this.file, constants.O_WRONLY | constants.O_CREAT | constants.O_TRUNC | constants.O_NOFOLLOW, 0o600);
    try { await handle.writeFile(body); await handle.sync(); }
    finally { await handle.close(); }
    if (first) {
      const directory = await open(this.root, constants.O_RDONLY);
      try { await directory.sync(); } finally { await directory.close(); }
    }
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

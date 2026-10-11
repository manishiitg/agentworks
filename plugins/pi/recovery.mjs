import { constants } from 'node:fs';
import { mkdir, lstat, readdir, open, unlink } from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import path from 'node:path';

// Only uncertainty metadata lives here. The common bridge still owns offers,
// credentials and message bodies. Pi buffers custom entries in empty sessions,
// so a session entry alone cannot cover the bridge-response crash window.
export class Recovery {
  constructor(root, session, lease) {
    this.root = path.join(root, 'pi-recovery');
    this.prefix = createHash('sha256').update(JSON.stringify([session, lease])).digest('hex') + '-';
    this.file = path.join(this.root, this.prefix + randomUUID() + '.json');
  }

  async prepare(accepted) {
    await mkdir(this.root, { mode: 0o700, recursive: true });
    const stat = await lstat(this.root);
    if (!stat.isDirectory() || stat.uid !== process.getuid() || stat.mode & 0o077)
      throw new Error('AgentWorks recovery directory is not private');
    let uncertain = false;
    for (const name of await readdir(this.root)) {
      if (!name.startsWith(this.prefix) || !name.endsWith('.json')) continue;
      const file = path.join(this.root, name);
      const handle = await open(file, constants.O_RDONLY | constants.O_NOFOLLOW);
      try {
        const state = JSON.parse(await handle.readFile('utf8'));
        if (Array.isArray(state.ids) && state.ids.every(id => accepted.has(id))) await unlink(file);
        else uncertain = true;
      } catch { uncertain = true; }
      finally { await handle.close(); }
    }
    return uncertain;
  }

  async save(ids) {
    const handle = await open(this.file, constants.O_WRONLY | constants.O_CREAT | constants.O_TRUNC | constants.O_NOFOLLOW, 0o600);
    try {
      await handle.writeFile(JSON.stringify(ids === undefined ? { request: true } : { ids }));
      await handle.sync();
    } finally { await handle.close(); }
    // Persist the directory entry as well as its body before releasing a request.
    const directory = await open(this.root, constants.O_RDONLY);
    try { await directory.sync(); } finally { await directory.close(); }
  }

  async clear() {
    try { await unlink(this.file); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
}

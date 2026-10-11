import { lstat } from 'node:fs/promises';
import http from 'node:http';
import { homedir } from 'node:os';
import path from 'node:path';

export function bridgeRoot() {
  return process.env.AGENTWORKS_BRIDGE_ROOT || path.join(
    process.platform === 'darwin' ? path.join(homedir(), 'Library', 'Caches') :
      process.env.XDG_CACHE_HOME || path.join(homedir(), '.cache'),
    'agentworks', 'bridge');
}

// The bridge owns credentials, remote SSE, authorization and the offer journal.
// This client can only talk to an existing private Unix socket.
export class LocalBridge {
  constructor(root = bridgeRoot()) { this.root = root; }

  async available() {
    try {
      const dir = await lstat(this.root);
      const socket = await lstat(path.join(this.root, 'bridge.sock'));
      return dir.isDirectory() && socket.isSocket() &&
        !(dir.mode & 0o077) && !(socket.mode & 0o077) &&
        dir.uid === process.getuid() && socket.uid === process.getuid();
    } catch { return false; }
  }

  async call(request, { signal, timeout = 3000 } = {}) {
    if (!await this.available()) throw new Error('AgentWorks local bridge is unavailable. Start agentworks bridge serve, then reconnect or attach in Pi.');
    if (signal?.aborted) throw new Error('AgentWorks local request cancelled');
    const body = JSON.stringify({ ...request, provider: 'pi' });
    return new Promise((resolve, reject) => {
      const req = http.request({
        socketPath: path.join(this.root, 'bridge.sock'), path: '/bridge', method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) },
        signal,
      }, res => {
        let size = 0;
        const chunks = [];
        res.on('data', chunk => {
          size += chunk.length;
          if (size > 4 * 1024 * 1024) req.destroy(new Error('AgentWorks local response too large'));
          else chunks.push(chunk);
        });
        res.on('error', () => reject(new Error('AgentWorks local response failed')));
        res.on('end', () => {
          try {
            const result = JSON.parse(Buffer.concat(chunks).toString('utf8'));
            // Never surface raw server errors: they may contain credential/config data.
            if (res.statusCode !== 200 || result.error) {
              const error = new Error('AgentWorks local bridge refused the request');
              // The current shared protocol has only an error string. Recognize
              // these exact APIError prefixes without surfacing its private tail.
              error.code = /^AgentWorks (receipt_refused|attachment_changed|not_admitted) \(HTTP 409\):/.exec(result.error || '')?.[1];
              if (result.error === 'message was not offered to this exact session') error.code = 'not_offered';
              error.terminalReceipt = Boolean(error.code);
              reject(error);
            } else resolve(result);
          } catch { reject(new Error('AgentWorks local bridge refused the request')); }
        });
      });
      const timer = setTimeout(() => req.destroy(new Error('AgentWorks local request timed out')), timeout);
      req.on('close', () => clearTimeout(timer));
      req.on('error', () => reject(new Error('AgentWorks local bridge request failed')));
      req.end(body);
    });
  }
}

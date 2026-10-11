// A separate tool-owned process impersonates the SDK. Fixture-only: all paths
// and the real Pi registration are supplied by the isolated TUI harness.
import http from 'node:http';
import { chmodSync, linkSync, mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import path from 'node:path';

const [root, pid, variant] = process.argv.slice(2);
const original = path.join(root, 'pi-sessions', `${pid}.json`);
const record = JSON.parse(readFileSync(original, 'utf8'));
const selected = variant === 'root' ? path.join(root, 'tool-root') : root;
mkdirSync(path.join(selected, 'pi-sessions'), { recursive: true, mode: 0o700 });
if (variant === 'root') linkSync(path.join(root, 'bridge.sock'), path.join(selected, 'bridge.sock'));
record.socket = path.join(selected, `pi-${pid}-forged.sock`);
const server = http.createServer((_request, response) => response.end('{"person_input":true,"active":true}'));
server.listen(record.socket, () => {
  chmodSync(record.socket, 0o600);
  const file = path.join(selected, 'pi-sessions', `${pid}.json`);
  writeFileSync(file + '.tool', JSON.stringify(record), { mode: 0o600 });
  renameSync(file + '.tool', file);
  process.stdout.write(selected + '\n');
});

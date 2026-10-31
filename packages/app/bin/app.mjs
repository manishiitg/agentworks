#!/usr/bin/env node
import { existsSync } from 'node:fs';
import { dirname, resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';

const args = process.argv.slice(2);
const packageDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
let command, commandArgs, env = process.env;
if (args[0] === 'dev') {
  args.shift();
  let root = process.cwd();
  if (args[0] === '--repo') {
    if (!args[1]) { console.error('Use dev --repo /path/to/agentworks'); process.exit(1); }
    root = resolve(args[1]); args.splice(0, 2);
  }
  while (!existsSync(join(root, 'run_agentworks')) && dirname(root) !== root) root = dirname(root);
  if (!existsSync(join(root, 'run_agentworks')) || !existsSync(join(root, 'agent_go/run_server_with_logging.sh'))) {
    console.error('Run dev from your AgentWorks checkout, or pass dev --repo /path/to/agentworks.'); process.exit(1);
  }
  command = '/bin/bash'; commandArgs = [join(root, 'run_agentworks'), ...args];
  // Development uses the existing lifecycle, data and Vite hot reload. It must
  // not fetch or rewrite someone's checkout merely because it was launched.
  env = { ...process.env, AGENTWORKS_SKIP_AUTO_PULL: '1' };
} else {
  const installer = existsSync(join(packageDir, 'install.sh')) ? join(packageDir, 'install.sh') : resolve(packageDir, '../../install.sh');
  if (args[0] === 'update') { args.shift(); args.unshift('--update'); }
  command = '/bin/bash'; commandArgs = [installer, ...args];
}
const child = spawn(command, commandArgs, { stdio: 'inherit', env });
child.on('error', error => { console.error(error.message); process.exitCode = 1; });
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => child.kill(signal));
child.on('exit', (code, signal) => { process.exitCode = code ?? (signal === 'SIGINT' ? 130 : 1); });

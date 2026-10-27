// The certified SDK contract and provisioning step for every installation.
// Import checks never create an agent or make a model request.
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const sdks = [
  { provider: 'claude-code', label: 'Claude', package: '@anthropic-ai/claude-agent-sdk', version: '0.3.311', entry: 'sdk.mjs', exported: 'query', variable: 'CLAUDE_AGENT_SDK_PATH' },
  { provider: 'cursor-cli', label: 'Cursor', package: '@cursor/sdk', version: '1.1.0', entry: 'dist/cjs/index.js', exported: 'Agent', variable: 'CURSOR_SDK_PATH' },
];
const probe = `
  const fs = await import('node:fs');
  const {pathToFileURL} = await import('node:url');
  const [entry, exported, metadata, version] = process.argv.slice(1);
  if (metadata && JSON.parse(fs.readFileSync(metadata, 'utf8')).version !== version) process.exit(1);
  const sdk = await import(pathToFileURL(entry).href);
  if (typeof (sdk[exported] ?? sdk.default?.[exported]) !== 'function') process.exit(1);
`;
function healthy(sdk, entry, metadata = '') {
  return spawnSync(process.execPath, ['--input-type=module', '-e', probe, entry, sdk.exported, metadata, sdk.version],
    { encoding: 'utf8', timeout: 45000, windowsHide: true }).status === 0;
}
function npmCommand() {
  // npm.cmd cannot be exec'd directly on Windows. Run its Node entry instead,
  // without a command shell (prefixes can contain spaces and metacharacters).
  const dirs = [...(process.env.PATH || process.env.Path || '').split(path.delimiter), path.dirname(process.execPath)];
  for (const dir of dirs) {
    const candidate = path.join(dir, 'node_modules', 'npm', 'bin', 'npm-cli.js');
    if (fs.existsSync(candidate)) return [process.execPath, [candidate]];
    if (process.platform !== 'win32') {
      try {
        const real = fs.realpathSync(path.join(dir, 'npm'));
        if (path.basename(real) === 'npm-cli.js') return [process.execPath, [real]];
        return [path.join(dir, 'npm'), []];
      } catch { /* next PATH entry */ }
    }
  }
  if (process.platform !== 'win32') return ['npm', []];
  throw new Error('Node.js and npm are needed. Install Node.js, then open AgentWorks again.');
}
function main(args) {
  const option = name => { const i = args.indexOf(name); return i < 0 ? '' : args[i + 1] || ''; };
  const prefix = option('--prefix');
  const provider = option('--provider');
  if (!path.isAbsolute(prefix) || (provider && !sdks.some(s => s.provider === provider))) throw new Error('Invalid SDK setup destination.');
  const modules = path.join(prefix, process.platform === 'win32' ? '' : 'lib', 'node_modules');
  const result = { entries: {}, ready: [], errors: [], node: process.execPath, npm: '' };
  let npm, globalRoot;
  const runNpm = npmArgs => {
    npm ||= npmCommand();
    result.npm = npm[1][0] || npm[0];
    return spawnSync(npm[0], [...npm[1], ...npmArgs], { encoding: 'utf8', timeout: 120000, windowsHide: true });
  };
  for (const sdk of sdks.filter(s => !provider || s.provider === provider)) {
    try {
      let entry = process.env[sdk.variable];
      if (entry) {
        entry = path.resolve(entry);
        if (!healthy(sdk, entry)) throw new Error('The configured component could not be loaded.');
      } else {
        const root = path.join(modules, sdk.package);
        entry = path.join(root, sdk.entry);
        let found = !args.includes('--force') && healthy(sdk, entry, path.join(root, 'package.json'));
        if (!found && args.includes('--prefer-global') && !args.includes('--force')) {
          if (globalRoot === undefined) {
            const probe = runNpm(['root', '--global']);
            globalRoot = probe.status === 0 ? probe.stdout.trim() : '';
          }
          if (globalRoot) {
            const globalPackage = path.join(globalRoot, sdk.package);
            const globalEntry = path.join(globalPackage, sdk.entry);
            if (healthy(sdk, globalEntry, path.join(globalPackage, 'package.json'))) {
              entry = globalEntry;
              found = true;
            }
          }
        }
        if (!found) {
          if (args.includes('--check-only')) throw new Error('A required component is missing. Update the app and try again.');
          const install = runNpm(['install', '--global', '--prefix', prefix, '--include=optional', '--no-audit', '--no-fund',
            '--allow-scripts=@anthropic-ai/claude-agent-sdk,@anthropic-ai/claude-code,esbuild', `${sdk.package}@${sdk.version}`]);
          if (install.status !== 0 || !healthy(sdk, entry, path.join(root, 'package.json'))) {
            throw new Error('Setup could not finish. Check your internet connection and try again.');
          }
        }
      }
      result.entries[sdk.variable] = entry;
      result.ready.push(sdk.label);
    } catch (error) {
      // npm output may contain proxy credentials. Never print it or a stack.
      result.errors.push(`${sdk.label}: ${error.message}`);
    }
  }
  console.log(JSON.stringify(result));
  process.exitCode = result.errors.length ? 1 : 0;
}
if (require.main === module) {
  try { main(process.argv.slice(2)); }
  catch { console.error('AI component setup could not start. Update AgentWorks and try again.'); process.exitCode = 1; }
}

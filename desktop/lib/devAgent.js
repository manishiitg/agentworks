'use strict';

const { spawnWithBootstrap, withoutDesktopBootstrap } = require('./connections');
const { waitForHealth } = require('./health');

// The shell has already resolved the workspace, API port and configuration.
// Unlike go run, a direct child preserves the private bootstrap pipe.
async function startDevelopmentAgent({ env, appOrigin, secret, authSecret, log }) {
  const api = new URL(env.MCP_AGENT_SERVER_URL);
  const app = new URL(appOrigin);
  const loopback = url => url.protocol === 'http:' && ['127.0.0.1', 'localhost'].includes(url.hostname);
  if (!loopback(api) || !loopback(app) || !authSecret) throw new Error('Start the local app with its desktop launcher.');
  const args = ['server', '--debug', '--host', api.hostname, '--port', api.port, '--log-level', 'debug',
    '--mcp-config', 'configs/mcp_servers_clean.json'];
  for (const [flag, name] of [['provider', 'DEEP_SEARCH_MAIN_LLM_PROVIDER'], ['model', 'DEEP_SEARCH_MAIN_LLM_MODEL'], ['temperature', 'DEEP_SEARCH_MAIN_LLM_TEMPERATURE']]) {
    if (env[name]) args.push('--' + flag, env[name]);
  }
  const child = spawnWithBootstrap(env.AGENTWORKS_DEV_AGENT_BINARY, args, {
    cwd: env.AGENTWORKS_DEV_AGENT_CWD,
    env: { ...withoutDesktopBootstrap(env), MCP_ENV_LOADED: '1' },
  }, { secret, host: api.host, app_origin: app.origin, auth_secret: authSecret });
  let spawnError;
  child.on('error', error => { spawnError = error; });
  child.stdout.on('data', data => log.write(data));
  child.stderr.on('data', data => log.write(data));
  try {
    await waitForHealth({ checks: [{ name: 'local backend', url: api.origin + '/api/health' }],
      isAlive: () => spawnError || child.exitCode !== null ? 'The local backend stopped during startup.' : null });
    return { child, port: Number(api.port) };
  } catch (error) { child.kill(); throw error; }
}
module.exports = { startDevelopmentAgent };

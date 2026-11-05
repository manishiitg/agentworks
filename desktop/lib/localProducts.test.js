'use strict';
const assert = require('node:assert/strict');
const test = require('node:test');
const { localProductEnv } = require('./localProducts');

test('desktop runs its own Vault and Brain, never an inherited Vault', () => {
  const inherited = {
    AGENTWORKS_DEPLOYMENT_MODE: 'server',
    AGENT_PRODUCTS: 'relays,example-app',
    AGENTWORKS_LOCAL_SERVER_PRODUCTS: '0',
    AGENTWORKS_ENABLED_PRODUCT_SURFACES: 'relays,knowledgebase,mcp-gateway,llm-gateway',
    AGENTWORKS_DEFAULT_PRODUCT_SURFACE: 'knowledgebase',
    CAPLAYER_SERVICE_URL: 'https://example.test',
    CAPLAYER_SERVICE_TOKEN: 'test-token',
    CAPLAYER_SERVICE_TOKEN_FILE: '/test/token',
    WORKSPACE_DOCS_PATH: '/existing/docs',
  };
  const env = localProductEnv(inherited, { url: 'http://127.0.0.1:45680', tokenFile: '/state/vault/service-token' });
  assert.equal(env.AGENTWORKS_DEPLOYMENT_MODE, 'local');
  assert.equal(env.AGENT_PRODUCTS, 'agentworks,work');
  assert.equal(env.AGENTWORKS_LOCAL_SERVER_PRODUCTS, '1');
  assert.equal(env.AGENTWORKS_ENABLED_PRODUCT_SURFACES, 'agentworks,work,mcp-gateway,knowledgebase');
  assert.equal(env.AGENTWORKS_DEFAULT_PRODUCT_SURFACE, 'agentworks');
  assert.equal(env.CAPLAYER_SERVICE_URL, 'http://127.0.0.1:45680');
  assert.equal(env.CAPLAYER_SERVICE_TOKEN, '');
  assert.equal(env.CAPLAYER_SERVICE_TOKEN_FILE, '/state/vault/service-token');
  assert.equal(env.WORKSPACE_DOCS_PATH, '/existing/docs');
  assert.equal(inherited.AGENTWORKS_DEPLOYMENT_MODE, 'server');
  // No Vault of its own (the Windows app today): Vault and Brain stay off.
  assert.equal(localProductEnv(inherited).AGENTWORKS_LOCAL_SERVER_PRODUCTS, '0');
  assert.equal(localProductEnv(inherited).CAPLAYER_SERVICE_URL, '');
});

test('DMG packages agent, workspace and Vault', () => {
  const resources = require('../package.json').build.mac.extraResources.map(resource => resource.to);
  assert.ok(resources.includes('agent-server'));
  assert.ok(resources.includes('workspace-server'));
  assert.ok(resources.includes('agentworks-vault'));
});

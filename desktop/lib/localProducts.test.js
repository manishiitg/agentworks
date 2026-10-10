'use strict';
const assert = require('node:assert/strict');
const test = require('node:test');
const { localProductEnv } = require('./localProducts');

test('desktop excludes server products and inherited Vault connections', () => {
  const inherited = {
    AGENTWORKS_DEPLOYMENT_MODE: 'server',
    AGENTWORKS_LOCAL_SERVER_PRODUCTS: '1',
    AGENTWORKS_ENABLED_PRODUCT_SURFACES: 'relays,knowledgebase,mcp-gateway,llm-gateway',
    AGENTWORKS_DEFAULT_PRODUCT_SURFACE: 'knowledgebase',
    CAPLAYER_SERVICE_URL: 'https://example.test',
    CAPLAYER_SERVICE_TOKEN: 'test-token',
    CAPLAYER_SERVICE_TOKEN_FILE: '/test/token',
    WORKSPACE_DOCS_PATH: '/existing/docs',
  };
  const env = localProductEnv(inherited);
  assert.equal(env.AGENTWORKS_DEPLOYMENT_MODE, 'local');
  assert.equal(env.AGENTWORKS_LOCAL_SERVER_PRODUCTS, '0');
  assert.equal(env.AGENTWORKS_ENABLED_PRODUCT_SURFACES, 'agentworks,work,code');
  assert.equal(env.AGENTWORKS_DEFAULT_PRODUCT_SURFACE, 'agentworks');
  assert.equal(env.CAPLAYER_SERVICE_URL, '');
  assert.equal(env.CAPLAYER_SERVICE_TOKEN, '');
  assert.equal(env.CAPLAYER_SERVICE_TOKEN_FILE, '');
  assert.equal(env.WORKSPACE_DOCS_PATH, '/existing/docs');
  assert.equal(inherited.AGENTWORKS_DEPLOYMENT_MODE, 'server');
});

test('DMG packages agent and workspace without a Vault sidecar', () => {
  const resources = require('../package.json').build.extraResources.map(resource => resource.to);
  assert.ok(resources.includes('agent-server'));
  assert.ok(resources.includes('workspace-server'));
  assert.ok(!resources.includes('vault-server'));
});

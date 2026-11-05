'use strict';

// The desktop installation owns this profile. Every Mac runs its own Vault and
// Brain (owner, 5 Nov), as the source launcher does with server products on
// (agent_go/run_server_with_logging.sh). Relays and Code stay off. Inherited
// server configuration must not point the agent at another Vault: only this
// installation's Vault, started before the agent, is used.
function localProductEnv(inherited = process.env, vault = {}) {
  // Without this installation's own Vault (the Windows app does not ship one yet) Vault and Brain stay off.
  const own = Boolean(vault.url);
  return {
    ...inherited,
    AGENTWORKS_DEPLOYMENT_MODE: 'local',
    AGENT_PRODUCTS: 'agentworks,work',
    AGENTWORKS_LOCAL_SERVER_PRODUCTS: own ? '1' : '0',
    AGENTWORKS_ENABLED_PRODUCT_SURFACES: own ? 'agentworks,work,mcp-gateway,knowledgebase' : 'agentworks,work',
    AGENTWORKS_DEFAULT_PRODUCT_SURFACE: 'agentworks',
    CAPLAYER_SERVICE_URL: vault.url || '',
    CAPLAYER_SERVICE_TOKEN: '',
    CAPLAYER_SERVICE_TOKEN_FILE: vault.tokenFile || '',
  };
}

module.exports = { localProductEnv };

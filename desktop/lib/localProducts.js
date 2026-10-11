'use strict';

// The desktop installation owns this profile. Inherited server configuration
// must not enable shared products or leave a dangling Vault connection.
function localProductEnv(inherited = process.env) {
  return {
    ...inherited,
    AGENTWORKS_DEPLOYMENT_MODE: 'local',
    AGENTWORKS_LOCAL_SERVER_PRODUCTS: '0',
    AGENTWORKS_ENABLED_PRODUCT_SURFACES: 'agentworks,work',
    AGENTWORKS_DEFAULT_PRODUCT_SURFACE: 'agentworks',
    CAPLAYER_SERVICE_URL: '',
    CAPLAYER_SERVICE_TOKEN: '',
    CAPLAYER_SERVICE_TOKEN_FILE: '',
  };
}

module.exports = { localProductEnv };

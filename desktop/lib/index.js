'use strict';

// Shared desktop shell mechanics.
module.exports = {
  ...require('./loginEnv'),
  ...require('./boundedLog'),
  ...require('./health'),
  ...require('./externalNav'),
  ...require('./shutdown'),
  ...require('./servers'),
};

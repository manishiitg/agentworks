'use strict';

const net = require('node:net');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const { createHash } = require('node:crypto');

function developmentSocketPath(checkout, apiOrigin) {
  const key = createHash('sha256').update(fs.realpathSync(checkout) + '\n' + new URL(apiOrigin).port).digest('hex').slice(0, 20);
  return path.join(os.homedir(), '.config', 'agentworks', 'desktop', key + '.sock');
}

// Only Electron's main process calls this. The response never crosses preload.
function readDevelopmentProof(socketPath, apiOrigin, appOrigin) {
  return new Promise((resolve, reject) => {
    try {
      const dir = fs.lstatSync(path.dirname(socketPath)), file = fs.lstatSync(socketPath);
      if (!dir.isDirectory() || (dir.mode & 0o777) !== 0o700 || dir.uid !== process.getuid() ||
          !file.isSocket() || (file.mode & 0o777) !== 0o600 || file.uid !== process.getuid()) throw new Error('Private desktop socket required.');
    } catch (error) { reject(error); return; }
    const socket = net.createConnection(socketPath);
    let data = '';
    const fail = () => { socket.destroy(); reject(new Error('Start the local backend to connect servers.')); };
    socket.setEncoding('utf8');
    socket.setTimeout(3000, fail);
    socket.on('error', fail);
    socket.on('connect', () => socket.end(JSON.stringify({ app_origin: appOrigin }) + '\n'));
    socket.on('data', chunk => { data += chunk; if (data.length > 4096) fail(); });
    socket.on('end', () => {
      socket.destroy();
      try {
        const result = JSON.parse(data);
        if (result.api_origin !== apiOrigin || result.app_origin !== appOrigin || !/^[a-f0-9]{64}$/.test(result.secret)) throw new Error('Wrong desktop backend.');
        resolve(result.secret);
      } catch (error) { reject(error); }
    });
  });
}

module.exports = { developmentSocketPath, readDevelopmentProof };

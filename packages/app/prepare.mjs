import { copyFileSync, chmodSync } from 'node:fs';
// Ship the exact installer with the npm version, never fetch a moving script.
copyFileSync(new URL('../../install.sh', import.meta.url), new URL('./install.sh', import.meta.url));
copyFileSync(new URL('../../LICENSE', import.meta.url), new URL('./LICENSE', import.meta.url));
chmodSync(new URL('./bin/app.mjs', import.meta.url), 0o755);

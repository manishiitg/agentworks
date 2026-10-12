// GitHub-only, offline fixture provider. Never invokes an installed person's
// CLI or changes their opt-in. Pin the SDK whose public statistics we use.
import { execFileSync } from 'node:child_process'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
const root = await mkdtemp(path.join(tmpdir(), 'bullpen-pi-sdk-'))
try {
  execFileSync('npm', ['install', '--prefix', root, '--no-audit', '--no-fund', '@earendil-works/pi-coding-agent@1.1.0'], { stdio: 'inherit', timeout: 180000 })
  execFileSync(process.execPath, ['--test', '--test-name-pattern=native SDK usage', '../plugins/pi/test/native.test.mjs'], {
    stdio: 'inherit', timeout: 60000,
    env: { ...process.env, PI_BIN: path.join(root, 'node_modules', '.bin', 'pi') },
  })
} finally { await rm(root, { recursive: true, force: true }) }

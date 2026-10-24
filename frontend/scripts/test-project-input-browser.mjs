import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
const root = fileURLToPath(new URL('../../', import.meta.url))
function run(command, args, cwd = root, env = {}) {
  const result = spawnSync(command, args, { cwd, env: { ...process.env, ...env }, stdio: 'inherit' })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(command + ' exited ' + result.status)
}
const node = path.join(root, 'packages/playwright')
const python = path.join(root, 'packages/playwright-python')
run('npm', ['ci', '--no-audit', '--no-fund'], node)
run('npx', ['playwright', 'install', '--with-deps', 'chromium'], node)
run('python3', ['-m', 'venv', '.venv'], python)
const py = path.join(python, '.venv/bin/python')
run(py, ['-m', 'pip', 'install', '-e', '.[pytest]'], python)
run(py, ['-m', 'playwright', 'install', '--with-deps', 'chromium'], python)
run('sudo', ['apt-get', 'install', '-y', 'ffmpeg'])
run('go', ['test', './cmd/server', '-run', '^(TestPlaywrightFixtureLive|TestPythonPlaywrightFixtureLive)$', '-count=1', '-v'], path.join(root, 'agent_go'), { GOWORK: 'off', AGENTWORKS_PLAYWRIGHT_LIVE_TEST: '1', AGENTWORKS_TEST_PYTHON: py })

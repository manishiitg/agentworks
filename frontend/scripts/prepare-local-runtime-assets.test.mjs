import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { expect, test } from 'vitest'
import { prepareLocalRuntimeAssets } from './prepare-local-runtime-assets.mjs'

// Regression for a local launch with no JSON renderer in either native static
// folder. Exercise the preparation/stamp/copy path without invoking a compiler.
test('local launch prepares missing runtimes, reuses current builds and repairs deleted native assets', async () => {
  const repo = await mkdtemp(path.join(os.tmpdir(), 'local-dashboard-assets-'))
  const frontendDir = path.join(repo, 'frontend')
  try {
    for (const dir of ['scripts', 'dashboard-runtime/src', 'src', 'dist']) await mkdir(path.join(frontendDir, dir), { recursive: true })
    await mkdir(path.join(repo, 'workspace/dashboards'), { recursive: true })
    for (const name of ['package.json', 'package-lock.json', 'scripts/prepare-local-runtime-assets.mjs', 'vite.report-preview.config.ts', 'dashboard-runtime/src/tsx-runtime.tsx', 'src/main.tsx']) await writeFile(path.join(frontendDir, name), 'fixture')
    await writeFile(path.join(repo, 'workspace/dashboards/components-catalog.json'), '{}')
    const calls = []
    const build = async script => {
      calls.push(script)
      for (const name of script === 'build:dashboard' ? ['dashboard-react.js', 'dashboard-react.css'] : ['report-preview.js']) await writeFile(path.join(frontendDir, 'dist', name), 'compiled ' + name)
    }
    const options = { frontendDir, staticDir: 'custom-static', build }
    await prepareLocalRuntimeAssets(options)
    expect(calls).toEqual(['build:dashboard', 'build:report-preview'])
    const native = path.join(repo, 'agent_go/static/dashboard-react.js')
    expect(await readFile(native, 'utf8')).toBe('compiled dashboard-react.js')
    expect(await readFile(path.join(frontendDir, 'public/dashboard-react.js'), 'utf8')).toBe('compiled dashboard-react.js')
    expect(await readFile(path.join(repo, 'agent_go/custom-static/dashboard-react.js'), 'utf8')).toBe('compiled dashboard-react.js')
    await rm(native)
    await prepareLocalRuntimeAssets(options)
    expect(calls).toHaveLength(2)
    expect(await readFile(native, 'utf8')).toBe('compiled dashboard-react.js')
    await writeFile(path.join(frontendDir, 'dist/dashboard-react.css'), '')
    await prepareLocalRuntimeAssets(options)
    expect(calls).toEqual(['build:dashboard', 'build:report-preview', 'build:dashboard'])
  } finally { await rm(repo, { recursive: true, force: true }) }
})

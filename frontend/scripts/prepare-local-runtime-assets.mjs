import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { copyFile, mkdir, readFile, readdir, stat, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

async function sourceDigest(inputs) {
  const hash = createHash('sha256')
  async function visit(file) {
    const info = await stat(file)
    if (info.isDirectory()) {
      for (const name of (await readdir(file)).sort()) {
        if (!['node_modules', 'dist', '.git'].includes(name)) await visit(path.join(file, name))
      }
    } else {
      hash.update(file); hash.update(await readFile(file))
    }
  }
  for (const input of inputs) await visit(input)
  return hash.digest('hex')
}

async function nonempty(file) {
  try { const info = await stat(file); return info.isFile() && info.size > 0 } catch { return false }
}

/** Prepare the browser runtimes needed by native preview and the development UI.
 * A content stamp avoids rebuilding on every Vite launch after type generation.
 */
export async function prepareLocalRuntimeAssets({ frontendDir, staticDir, build = script => execFileSync(process.platform === 'win32' ? 'npm.cmd' : 'npm', ['run', script], { cwd: frontendDir, stdio: 'inherit' }) }) {
  const repo = path.dirname(frontendDir)
  const stampPath = path.join(frontendDir, 'node_modules/.agentworks-runtime-assets.json')
  let stamps = {}
  try { stamps = JSON.parse(await readFile(stampPath, 'utf8')) } catch { /* first launch */ }
  const shared = ['package.json', 'package-lock.json', 'scripts/prepare-local-runtime-assets.mjs'].map(file => path.join(frontendDir, file))
  const groups = [
    { script: 'build:dashboard-json', files: ['dashboard-json.js', 'dashboard-json.css', 'dashboard-react.js'], inputs: [path.join(frontendDir, 'dashboard-json'), path.join(repo, 'workspace/dashboards/json-render-catalog.json')] },
    { script: 'build:report-preview', files: ['report-preview.js'], inputs: [path.join(frontendDir, 'src'), path.join(frontendDir, 'vite.report-preview.config.ts')] },
  ]
  const roots = new Set([path.join(repo, 'agent_go/static'), path.join(repo, 'agent_go/cmd/server/static'), path.join(frontendDir, 'public')])
  if (staticDir) roots.add(path.resolve(repo, 'agent_go', staticDir))
  for (const group of groups) {
    const digest = await sourceDigest([...shared, ...group.inputs])
    const outputs = group.files.map(name => path.join(frontendDir, 'dist', name))
    if (stamps[group.script] !== digest || !(await Promise.all(outputs.map(nonempty))).every(Boolean)) {
      console.log(`Preparing local runtime: ${group.script}`)
      await build(group.script)
    }
    for (const file of outputs) {
      if (!await nonempty(file)) throw new Error(`Local dashboard runtime is missing or empty: ${file}`)
      for (const root of roots) {
        await mkdir(root, { recursive: true })
        const target = path.join(root, path.basename(file))
        if (path.resolve(file) !== path.resolve(target)) await copyFile(file, target)
      }
    }
    stamps[group.script] = digest
  }
  await mkdir(path.dirname(stampPath), { recursive: true })
  await writeFile(stampPath, JSON.stringify(stamps))
  console.log('Local dashboard and preview runtimes are ready.')
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await prepareLocalRuntimeAssets({ frontendDir: fileURLToPath(new URL('..', import.meta.url)), staticDir: process.env.STATIC_DIR })
}

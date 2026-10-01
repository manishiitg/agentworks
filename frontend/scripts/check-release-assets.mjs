import { readdir, readFile, stat } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// Run against the packaged directory, including on the target host before
// switching releases. Merely finding the bundle in the source tree is insufficient.
// Reject obsolete standalone auth bundles, including unused chunks accidentally
// carried into a release. CapLayer must use the platform's existing auth flow.
async function rejectStandaloneGatewayLogin(root) {
  for (const entry of await readdir(root, { withFileTypes: true })) {
    const asset = path.join(root, entry.name)
    if (entry.isDirectory()) await rejectStandaloneGatewayLogin(asset)
    else if (entry.isFile() && /\.(js|html)$/.test(entry.name)) {
      const source = await readFile(asset, 'utf8')
      if (source.includes('Enter the admin token from the file path printed by the gateway launcher')) {
        throw new Error(`Release rejected: obsolete CapLayer token login in ${asset}. Rebuild the entire dist directory.`)
      }
    }
  }
}

export async function checkReleaseAssets(root) {
  for (const name of ['index.html', 'report-preview.js']) {
    const asset = path.join(root, name)
    let info
    try { info = await stat(asset) } catch { /* diagnosed below */ }
    if (!info?.isFile() || info.size === 0) {
      throw new Error(`Release rejected: missing or empty ${asset}. Run npm run build in frontend/ and package the entire dist directory. The agent STATIC_DIR must point to this directory.`)
    }
  }
  await rejectStandaloneGatewayLogin(root)
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const root = path.resolve(process.argv[2] || 'dist')
  try {
    await checkReleaseAssets(root)
    console.log(`Release assets verified: ${root}`)
  } catch (error) {
    console.error(error.message)
    process.exitCode = 1
  }
}

import fs from 'node:fs'
import path from 'node:path'
import type { Plugin } from 'vite'

// Trusted build input only. This is never read from runtime/user configuration.
// Imports keep their public module identity so shared platform modules are reused.
export function productSourceOverlay(root: string, manifestPath = process.env.AGENTWORKS_FRONTEND_SOURCE_OVERLAY): Plugin[] {
  if (!manifestPath) return []
  const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8')) as {
    version: number; product: string; files: Record<string, string>
  }
  if (manifest.version !== 1 || !/^[a-z][a-z0-9-]*$/.test(manifest.product) || !manifest.files || Array.isArray(manifest.files)) {
    throw new Error('Invalid private product source overlay')
  }
  const files = new Map<string, string>()
  for (const [relative, source] of Object.entries(manifest.files)) {
    if (!/^src\/(?:[a-zA-Z0-9_-]+\/)*[a-zA-Z0-9_.-]+\.(?:ts|tsx)$/.test(relative) ||
        typeof source !== 'string' || !path.isAbsolute(source) || !fs.statSync(source).isFile()) {
      throw new Error(`Invalid product source mapping: ${relative}`)
    }
    files.set(path.resolve(root, relative), source)
  }
  if (files.size === 0) throw new Error('Private product source overlay is empty')
  return [{
    name: `agentworks-private-source-${manifest.product}`,
    enforce: 'pre',
    load(id) {
      const source = files.get(id.split('?', 1)[0])
      return source ? fs.readFileSync(source, 'utf8') : null
    },
  }]
}

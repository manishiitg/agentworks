import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { afterEach, expect, it } from 'vitest'
import { productSourceOverlay } from './productSourceOverlay'

const roots: string[] = []
afterEach(() => { for (const root of roots.splice(0)) fs.rmSync(root, { recursive: true, force: true }) })
function fixture(files: Record<string, string>) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'product-overlay-')); roots.push(root)
  const source = path.join(root, 'private.tsx'); fs.writeFileSync(source, 'export const privateSource = true')
  const manifest = path.join(root, 'overlay.json')
  fs.writeFileSync(manifest, JSON.stringify({ version: 1, product: 'relays', files: Object.fromEntries(Object.entries(files).map(([key, value]) => [key, value === 'source' ? source : value])) }))
  return { root, source, manifest }
}
it('keeps public builds independent', () => { expect(productSourceOverlay('/public', '')).toEqual([]) })
it('loads private code at the existing public module identity', () => {
  const f = fixture({ 'src/components/Relay.tsx': 'source' })
  const [plugin] = productSourceOverlay(f.root, f.manifest)
  const load = plugin.load as (id: string) => string | null
  expect(load(path.join(f.root, 'src/components/Relay.tsx'))).toContain('privateSource')
  expect(load(path.join(f.root, 'src/shared.ts'))).toBeNull()
  fs.unlinkSync(f.source)
  expect(() => load(path.join(f.root, 'src/components/Relay.tsx'))).toThrow()
})
it('rejects missing code and traversal without falling back', () => {
  for (const files of [{ '../vite.config.ts': 'source' }, { 'src/Relay.tsx': '/does-not-exist.tsx' }]) {
    const f = fixture(files)
    expect(() => productSourceOverlay(f.root, f.manifest)).toThrow()
  }
})

import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { expect, test } from 'vitest'
import { checkReleaseAssets } from './check-release-assets.mjs'

test('reject missing/empty preview bundle and accept complete release', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'release-assets-'))
  try {
    await expect(checkReleaseAssets(root)).rejects.toThrow(/index.html/)
    await writeFile(path.join(root, 'index.html'), '<html></html>')
    await expect(checkReleaseAssets(root)).rejects.toThrow(/report-preview.js/)
    await writeFile(path.join(root, 'report-preview.js'), '')
    await expect(checkReleaseAssets(root)).rejects.toThrow(/report-preview.js/)
    await writeFile(path.join(root, 'report-preview.js'), 'console.log("preview")')
    await checkReleaseAssets(root)
    const stale = path.join(root, 'caplayer-old.js')
    await writeFile(stale, 'Enter the admin token from the file path printed by the gateway launcher')
    await expect(checkReleaseAssets(root)).rejects.toThrow(/obsolete CapLayer token login/)
    await rm(stale)
    await checkReleaseAssets(root)
  } finally { await rm(root, { recursive: true, force: true }) }
})

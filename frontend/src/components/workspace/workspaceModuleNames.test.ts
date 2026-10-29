import { readdirSync } from 'node:fs'
import { expect, it } from 'vitest'

it('keeps workspace module names distinct for extensionless imports on macOS', () => {
  // Vite resolves .ts before .tsx. On a case-insensitive filesystem a helper
  // called chatFileReview.ts shadowed the lazy ChatFileReview.tsx component.
  const modules = new Map<string, string[]>()
  for (const file of readdirSync('src/components/workspace')) {
    if (!/\.tsx?$/.test(file)) continue
    const name = file.replace(/\.tsx?$/, '').toLowerCase()
    modules.set(name, [...(modules.get(name) ?? []), file])
  }
  expect([...modules.values()].filter(files => files.length > 1)).toEqual([])
})

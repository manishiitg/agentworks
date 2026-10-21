import { readFile, writeFile } from 'node:fs/promises'
import { catalogDocument } from '../catalog.mjs'
const output = new URL('../../../workspace/dashboards/json-render-catalog.json', import.meta.url)
const body = JSON.stringify(catalogDocument(), null, 2) + '\n'
if (process.argv.includes('--check')) {
  if (await readFile(output, 'utf8') !== body) throw Error('Dashboard catalog differs from pinned upstream definitions; regenerate it before releasing.')
} else if (process.argv.includes('--stdout')) process.stdout.write(body)
else await writeFile(output, body)

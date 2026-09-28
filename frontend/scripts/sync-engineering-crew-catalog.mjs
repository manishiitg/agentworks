import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const repoRoot = path.resolve(frontendRoot, '..')
const source = fs.readFileSync(path.join(frontendRoot, 'src/products/work/engineeringSpecialists.ts'), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { engineeringSpecialists } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

if (engineeringSpecialists.length !== 7) throw new Error(`Expected seven Engineering Crew templates, found ${engineeringSpecialists.length}`)
const ids = new Set()
const catalog = engineeringSpecialists.map(({ id, version, name, role, purpose, selectedSkills, files, setupPath }) => {
  if (ids.has(id)) throw new Error(`Duplicate Engineering Crew template ${id}`)
  ids.add(id)
  const setup = JSON.parse(files[setupPath])
  if (setup.template_id !== id || setup.template_version !== version || setup.checks.length < 5 || setup.checks.length > 10) {
    throw new Error(`Invalid Engineering Crew setup for ${id}`)
  }
  return { id, version, name, role, purpose, selected_skills: selectedSkills, files }
})
const destination = path.join(repoRoot, 'playbooks/crew-agents/engineering/catalog.json')
const content = `${JSON.stringify({ schema_version: 1, agents: catalog }, null, 2)}\n`
if (process.argv.includes('--check')) {
  if (!fs.existsSync(destination) || fs.readFileSync(destination, 'utf8') !== content) {
    throw new Error('Engineering Crew catalog is stale; run npm run sync:engineering-crew-catalog')
  }
  console.log(`Verified ${catalog.length} Engineering Crew agents`)
} else {
  fs.mkdirSync(path.dirname(destination), { recursive: true })
  fs.writeFileSync(destination, content)
  console.log(`Synced ${catalog.length} Engineering Crew agents to ${path.relative(repoRoot, destination)}`)
}

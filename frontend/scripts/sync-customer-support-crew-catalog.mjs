import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const repoRoot = path.resolve(frontendRoot, '..')
const source = fs.readFileSync(path.join(frontendRoot, 'src/products/work/supportSpecialists.ts'), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { supportSpecialists } = await import('data:text/javascript;base64,' + Buffer.from(compiled).toString('base64'))
const expansionSource = fs.readFileSync(path.join(frontendRoot, 'src/products/work/categoryExpansionSpecialists.ts'), 'utf8')
const expansionCompiled = ts.transpileModule(expansionSource, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { categoryExpansionSpecialists } = await import('data:text/javascript;base64,' + Buffer.from(expansionCompiled).toString('base64'))

if (supportSpecialists.length !== 4) throw new Error('Expected four Customer Support Crew templates, found ' + supportSpecialists.length)
const templates = [...supportSpecialists, ...categoryExpansionSpecialists.filter(item => item.category === 'Customer Support')]
if (templates.length !== 5) throw new Error('Expected five Customer Support Crew templates, found ' + templates.length)
const ids = new Set()
const catalog = templates.map(({ id, version, name, role, purpose, selectedSkills, files, setupPath }) => {
  if (ids.has(id)) throw new Error('Duplicate Customer Support Crew template ' + id)
  ids.add(id)
  const setup = JSON.parse(files[setupPath])
  if (setup.template_id !== id || setup.template_version !== version || setup.checks.length !== 9 || setup.completed_steps.length !== 0) {
    throw new Error('Invalid Customer Support Crew setup for ' + id)
  }
  return { id, version, name, role, purpose, selected_skills: selectedSkills, files }
})
const destination = path.join(repoRoot, 'playbooks/crew-agents/customer-support/catalog.json')
const content = JSON.stringify({ schema_version: 1, agents: catalog }, null, 2) + '\n'
if (process.argv.includes('--check')) {
  if (!fs.existsSync(destination) || fs.readFileSync(destination, 'utf8') !== content) {
    throw new Error('Customer Support Crew catalog is stale; run npm run sync:customer-support-crew-catalog')
  }
  console.log('Verified ' + catalog.length + ' Customer Support Crew agents')
} else {
  fs.mkdirSync(path.dirname(destination), { recursive: true })
  fs.writeFileSync(destination, content)
  console.log('Synced ' + catalog.length + ' Customer Support Crew agents to ' + path.relative(repoRoot, destination))
}

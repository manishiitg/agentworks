import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const repoRoot = path.resolve(frontendRoot, '..')
const source = fs.readFileSync(path.join(frontendRoot, 'src/products/work/productSpecialists.ts'), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { productSpecialists } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const expansionSource = fs.readFileSync(path.join(frontendRoot, 'src/products/work/categoryExpansionSpecialists.ts'), 'utf8')
const expansionCompiled = ts.transpileModule(expansionSource, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { categoryExpansionSpecialists } = await import(`data:text/javascript;base64,${Buffer.from(expansionCompiled).toString('base64')}`)

if (productSpecialists.length !== 1) throw new Error(`Expected one Product Crew template, found ${productSpecialists.length}`)
const templates = [...productSpecialists, ...categoryExpansionSpecialists.filter(item => item.category === 'Product')]
if (templates.length !== 5) throw new Error(`Expected five Product Crew templates, found ${templates.length}`)
const catalog = templates.map(({ id, version, name, role, purpose, selectedSkills, files, setupPath }) => {
  const setup = JSON.parse(files[setupPath])
  if (setup.template_id !== id || setup.template_version !== version || setup.checks.length !== 9 || setup.completed_steps.length !== 0) {
    throw new Error(`Invalid Product Crew setup for ${id}`)
  }
  return { id, version, name, role, purpose, selected_skills: selectedSkills, files }
})
const destination = path.join(repoRoot, 'playbooks/crew-agents/product/catalog.json')
const content = `${JSON.stringify({ schema_version: 1, agents: catalog }, null, 2)}\n`
if (process.argv.includes('--check')) {
  if (!fs.existsSync(destination) || fs.readFileSync(destination, 'utf8') !== content) {
    throw new Error('Product Crew catalog is stale; run npm run sync:product-crew-catalog')
  }
  console.log(`Verified ${catalog.length} Product Crew agents`)
} else {
  fs.mkdirSync(path.dirname(destination), { recursive: true })
  fs.writeFileSync(destination, content)
  console.log(`Synced ${catalog.length} Product Crew agents to ${path.relative(repoRoot, destination)}`)
}

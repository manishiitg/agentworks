import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const repoRoot = path.resolve(frontendRoot, '..')
const sourcePath = path.join(frontendRoot, 'src/products/work/crewTemplates.ts')
const source = ts.createSourceFile(sourcePath, fs.readFileSync(sourcePath, 'utf8'), ts.ScriptTarget.Latest, true)

function property(node, name) {
  const entry = node.properties.find(item => ts.isPropertyAssignment(item) && item.name.getText(source) === name)
  return entry?.initializer
}

function stringProperty(node, name) {
  const value = property(node, name)
  if (!value || !ts.isStringLiteral(value)) throw new Error(`Expected static ${name} in Finance Crew template`)
  return value.text
}

const templates = []
function visit(node) {
  if (ts.isVariableDeclaration(node) && node.name.getText(source) === 'crewTemplates' && node.initializer && ts.isArrayLiteralExpression(node.initializer)) {
    for (const entry of node.initializer.elements) {
      if (!ts.isObjectLiteralExpression(entry) || stringProperty(entry, 'category') !== 'Finance') continue
      const id = stringProperty(entry, 'id')
      const versionNode = property(entry, 'version')
      if (!versionNode || !ts.isNumericLiteral(versionNode)) throw new Error(`Invalid version for ${id}`)
      const templateRoot = path.join(frontendRoot, 'src/products/work/templates', id)
      const setupName = id === 'finance-analyst' ? 'TEMPLATE_SETUP.md' : 'SETUP.md'
      const setupPath = id === 'finance-analyst' ? 'TEMPLATE_SETUP.json' : `templates/${id}/TEMPLATE_SETUP.json`
      const guidePath = id === 'finance-analyst' ? 'TEMPLATE_SETUP.md' : `templates/${id}/SETUP.md`
      const files = {
        [`skills/${id}/SKILL.md`]: fs.readFileSync(path.join(templateRoot, 'SKILL.md'), 'utf8'),
        [guidePath]: fs.readFileSync(path.join(templateRoot, setupName), 'utf8'),
        [setupPath]: fs.readFileSync(path.join(templateRoot, 'TEMPLATE_SETUP.json'), 'utf8'),
      }
      const setup = JSON.parse(files[setupPath])
      if (setup.template_id !== id || setup.template_version !== Number(versionNode.text) || setup.checks.length < 5 || setup.checks.length > 10) {
        throw new Error(`Invalid setup checklist for ${id}`)
      }
      templates.push({
        id,
        version: Number(versionNode.text),
        name: stringProperty(entry, 'name'),
        role: stringProperty(entry, 'role'),
        purpose: stringProperty(entry, 'purpose'),
        selected_skills: [id],
        files,
      })
    }
  }
  ts.forEachChild(node, visit)
}
visit(source)
if (templates.length !== 5) throw new Error(`Expected five core Finance Crew templates, found ${templates.length}`)

const packSource = fs.readFileSync(path.join(frontendRoot, 'src/products/work/billingPacks.ts'), 'utf8')
const compiledPacks = ts.transpileModule(packSource, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { billingPacks } = await import('data:text/javascript;base64,' + Buffer.from(compiledPacks).toString('base64'))
if (billingPacks.length !== 4) throw new Error(`Expected four Billing capability packs, found ${billingPacks.length}`)
for (const pack of billingPacks) {
  const setup = JSON.parse(pack.files[pack.setupPath])
  if (setup.template_id !== pack.id || setup.template_version !== pack.version || setup.checks.length !== 9 || setup.completed_steps.length !== 0) {
    throw new Error(`Invalid Billing pack setup for ${pack.id}`)
  }
  if (templates.some(item => item.id === pack.id)) throw new Error(`Duplicate Finance template ${pack.id}`)
  templates.push({
    id: pack.id, version: pack.version, name: pack.name, role: pack.role, purpose: pack.purpose,
    selected_skills: pack.selectedSkills, files: pack.files,
  })
}

const destination = path.join(repoRoot, 'playbooks/crew-agents/finance/catalog.json')
const content = `${JSON.stringify({ schema_version: 1, agents: templates }, null, 2)}\n`
if (process.argv.includes('--check')) {
  if (!fs.existsSync(destination) || fs.readFileSync(destination, 'utf8') !== content) {
    throw new Error('Finance Crew catalog is stale; run npm run sync:finance-crew-catalog')
  }
  console.log(`Verified ${templates.length} Finance Crew agents`)
} else {
  fs.mkdirSync(path.dirname(destination), { recursive: true })
  fs.writeFileSync(destination, content)
  console.log(`Synced ${templates.length} Finance Crew agents to ${path.relative(repoRoot, destination)}`)
}

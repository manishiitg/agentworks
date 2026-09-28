import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const repoRoot = path.resolve(frontendRoot, '..')
const source = fs.readFileSync(path.join(frontendRoot, 'src/components/playbooks/playbookCatalog.ts'), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { PLAYBOOK_CATALOG } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

function manifestsUnder(directory) {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const target = path.join(directory, entry.name)
    if (entry.isDirectory()) return manifestsUnder(target)
    return entry.name === 'playbook.json' ? [JSON.parse(fs.readFileSync(target, 'utf8'))] : []
  })
}

function category(hierarchy) {
  const platform = hierarchy.indexOf('Agentic Engineering Platform')
  return platform >= 0 && platform + 1 < hierarchy.length ? hierarchy[platform + 1] : hierarchy.at(-1) || 'Other'
}

const manifests = manifestsUnder(path.join(repoRoot, 'playbooks/agentic-engineering-platform'))
const byId = new Map(PLAYBOOK_CATALOG.map(item => [item.id, item]))
const errors = []
if (byId.size !== PLAYBOOK_CATALOG.length) errors.push('Frontend catalog contains duplicate IDs')
for (const manifest of manifests) {
  const projected = byId.get(manifest.id)
  if (!projected) {
    errors.push(`Missing frontend catalog entry: ${manifest.id}`)
    continue
  }
  const fields = {
    title: manifest.title,
    // The UI may use a shorter editorial description; identity, version,
    // routing, and setup metadata must remain an exact manifest projection.
    version: manifest.version,
    category: category(manifest.hierarchy),
    order: manifest.order,
    inputCount: manifest.setup_inputs.length,
    toolCount: manifest.recommended_tools.length,
    teamScope: manifest.team_scope,
    agentSlots: manifest.agent_slots || [],
    handoffs: manifest.handoffs || [],
    setupChecks: manifest.setup_checks || [],
  }
  for (const [field, expected] of Object.entries(fields)) {
    const actual = projected[field] ?? (Array.isArray(expected) ? [] : undefined)
    if (JSON.stringify(actual) !== JSON.stringify(expected)) errors.push(`${manifest.id}: ${field} differs from playbook.json`)
  }
}
for (const id of byId.keys()) {
  if (!manifests.some(manifest => manifest.id === id)) errors.push(`Frontend-only catalog entry: ${id}`)
}
if (errors.length) {
  throw new Error(`Playbook catalog projection is stale:\n${errors.map(error => `- ${error}`).join('\n')}`)
}
console.log(`Verified ${manifests.length} frontend Playbook entries against manifests`)

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const repoRoot = path.resolve(frontendRoot, '..')
const depthText = fs.readFileSync(path.join(frontendRoot, 'src/products/work/websiteGrowthDepth.ts'), 'utf8')
const specialistText = fs.readFileSync(path.join(frontendRoot, 'src/products/work/websiteGrowthSpecialists.ts'), 'utf8')
const depthImport = "import type { WebsiteGrowthSpecialistId } from './websiteGrowthSpecialists'\n"
const specialistImport = "import { websiteGrowthDepth } from './websiteGrowthDepth'\n"
if (!depthText.includes(depthImport) || !specialistText.includes(specialistImport)) {
  throw new Error('Website Growth catalog source imports changed; update the sync script')
}
const depthSource = depthText.replace(depthImport, '')
const specialistSource = specialistText.replace(specialistImport, '')
const source = depthSource + '\n' + specialistSource
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const { websiteGrowthSpecialists } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

const starterRoot = path.join(frontendRoot, 'src/products/work/templates/website-growth-starter')
const starterFiles = {
  'skills/website-growth-starter/SKILL.md': fs.readFileSync(path.join(starterRoot, 'SKILL.md'), 'utf8'),
  'templates/website-growth-starter/SETUP.md': fs.readFileSync(path.join(starterRoot, 'SETUP.md'), 'utf8'),
  'templates/website-growth-starter/TEMPLATE_SETUP.json': fs.readFileSync(path.join(starterRoot, 'TEMPLATE_SETUP.json'), 'utf8'),
}
const starter = {
  id: 'website-growth-starter', version: 1, name: 'Website Growth Starter',
  role: 'Website growth strategist for this business',
  purpose: 'Audit the business website, find evidence-backed opportunities for relevant traffic, and guide a measurable 30-day growth plan.',
  selectedSkills: ['website-growth-starter'], files: starterFiles,
}
const catalog = [starter, ...websiteGrowthSpecialists].map(({ id, version, name, role, purpose, selectedSkills, files }) => ({
  id, version, name, role, purpose, selected_skills: selectedSkills, files,
}))
const destination = path.join(repoRoot, 'playbooks/crew-agents/website-growth/catalog.json')
const content = `${JSON.stringify({ schema_version: 1, agents: catalog }, null, 2)}\n`
if (process.argv.includes('--check')) {
  if (!fs.existsSync(destination) || fs.readFileSync(destination, 'utf8') !== content) {
    throw new Error('Website Growth Crew catalog is stale; run npm run sync:website-growth-catalog')
  }
  console.log(`Verified ${catalog.length} Website Growth Crew agents`)
} else {
  fs.mkdirSync(path.dirname(destination), { recursive: true })
  fs.writeFileSync(destination, content)
  console.log(`Synced ${catalog.length} Website Growth Crew agents to ${path.relative(repoRoot, destination)}`)
}

// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const files = vi.hoisted(() => new Map<string, string>())
const updatePlannerFile = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ agentApi: {
  getPlannerFileContent: async (path: string) => {
    const content = files.get(path)
    if (content === undefined) throw { response: { status: 404 } }
    return { data: { content } }
  }, updatePlannerFile,
}, getApiBaseUrl: () => '', getAuthToken: () => null }))

import { removeProductProjectTemplate } from '../../platform/chat/productProjects'
import { WorkTemplateSetup } from './WorkTemplateSetup'
import { crewTemplates } from './crewTemplates'
import { installWorkSessionTemplate, parseSessionManifest, removeWorkSessionTemplate, type WorkSession } from './workSessions'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('removes a template from the chat row, retries a failed save and preserves Crew work on reattachment', async () => {
  files.clear()
  const path = 'Crew/template-removal-regression'
  const finance = crewTemplates.find(template => template.id === 'finance-analyst')!
  const tax = crewTemplates.find(template => template.id === 'tax-export')!
  const product = { schema_version: 1, product: 'work', id: 'template-removal', title: 'Finance Crew', description: 'My work', session_id: 'work:project:template-removal', identity: { name: 'Finance Crew', role: 'My role' }, templates: [{ id: finance.id, version: finance.version }, { id: tax.id, version: tax.version }] }
  const runtime = { capabilities: { selected_skills: [finance.id, tax.id, 'my-custom-skill'], selected_servers: ['my-server'], selected_secrets: ['MY_SECRET'] }, schedules: [{ id: 'keep-schedule' }] }
  files.set(`${path}/product.json`, JSON.stringify(product))
  files.set(`${path}/workflow.json`, JSON.stringify(runtime))
  for (const template of [finance, tax]) for (const [name, content] of Object.entries(template.files)) files.set(`${path}/${name}`, content)
  const setup = JSON.parse(files.get(`${path}/${finance.setupPath}`)!)
  setup.completed_steps = ['identity', 'skill']
  files.set(`${path}/${finance.setupPath}`, JSON.stringify(setup))
  files.set(`${path}/skills/finance-analyst/SKILL.md`, 'My edited finance skill')
  files.set(`${path}/chats/saved.md`, 'Existing chat')
  const retainedFiles = new Map([...files].filter(([name]) => !name.endsWith('/product.json') && !name.endsWith('/workflow.json')))
  updatePlannerFile.mockImplementation(async (name: string, content: string) => { files.set(name, content); return {} })
  const initial = { ...parseSessionManifest(JSON.stringify(product), path)!, selectedSkills: runtime.capabilities.selected_skills }
  let current = initial
  function ChatRows() {
    const [session, setSession] = useState(initial)
    return <>{session.templates.map(installed => {
      const template = crewTemplates.find(item => item.id === installed.id)!
      return <WorkTemplateSetup key={template.id} template={template} workspacePath={path} chatReady={false}
        onStartSetup={async () => {}} onRemove={async () => {
          current = await removeWorkSessionTemplate(session, template.id)
          setSession(current)
        }} />
    })}</>
  }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<ChatRows />))
    // Runtime deselection succeeds, but a receipt write fails: retain the row and error.
    updatePlannerFile.mockImplementationOnce(async (name: string, content: string) => { files.set(name, content); return {} })
    updatePlannerFile.mockRejectedValueOnce(new Error('Could not save template removal'))
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Remove Finance Analyst template"]')!.click())
    expect(host.textContent).toContain('Could not save template removal')
    expect(host.querySelector('[aria-label="Finance Analyst setup status"]')).not.toBeNull()
    expect(JSON.parse(files.get(`${path}/product.json`)!).templates).toHaveLength(2)
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Finance Analyst setup status"] [aria-label="Refresh setup status"]')!.click())
    expect(host.textContent).toContain('Could not save template removal')
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Remove Finance Analyst template"]')!.click())
    expect(host.querySelector('[aria-label="Finance Analyst setup status"]')).toBeNull()
    expect(host.querySelector('[aria-label="Tax Export Preparer setup status"]')).not.toBeNull()
    expect(current.selectedSkills).toEqual([tax.id, 'my-custom-skill'])
    const saved = JSON.parse(files.get(`${path}/product.json`)!)
    expect(parseSessionManifest(JSON.stringify(saved), path)!.templates).toEqual([{ id: tax.id, version: tax.version }])
    expect(saved.identity).toEqual(product.identity)
    expect(saved.description).toBe(product.description)
    expect(JSON.parse(files.get(`${path}/workflow.json`)!)).toMatchObject({ capabilities: { selected_skills: [tax.id, 'my-custom-skill'], selected_servers: ['my-server'], selected_secrets: ['MY_SECRET'] }, schedules: runtime.schedules })
    expect(new Map([...files].filter(([name]) => retainedFiles.has(name)))).toEqual(retainedFiles)
    const reattached = await installWorkSessionTemplate(current, finance.id)
    expect(reattached.templates.map(template => template.id)).toEqual([tax.id, finance.id])
    expect(new Map([...files].filter(([name]) => retainedFiles.has(name)))).toEqual(retainedFiles)
    // Shared Run readers cannot mutate another owner's templates.
    await expect(removeWorkSessionTemplate({ ...initial, shared: { ownerId: 'owner', triggers: [], schedules: [] } } as WorkSession, finance.id)).rejects.toThrow('Only the Crew owner')
  } finally {
    await act(async () => root.unmount()); host.remove(); vi.resetAllMocks()
  }
})

it('serializes removal of multiple rows and keeps shared skills until their last template is removed', async () => {
  files.clear()
  const path = 'Crew/concurrent-removal'
  const product = { schema_version: 1, product: 'work', id: 'concurrent', title: 'Crew', session_id: 'work:concurrent', templates: [{ id: 'first', version: 1 }, { id: 'second', version: 1 }] }
  files.set(`${path}/product.json`, JSON.stringify(product))
  files.set(`${path}/workflow.json`, JSON.stringify({ capabilities: { selected_skills: ['first-skill', 'second-skill', 'shared-skill', 'my-skill'] } }))
  updatePlannerFile.mockImplementation(async (name: string, content: string) => { files.set(name, content); return {} })
  const session = parseSessionManifest(JSON.stringify(product), path)!
  const skills = (id: string) => [`${id}-skill`, 'shared-skill']
  try {
    const first = removeProductProjectTemplate(session, 'first', skills, 'Remove first', 'workflow.json')
    const second = removeProductProjectTemplate(session, 'second', skills, 'Remove second', 'workflow.json')
    expect((await first).selectedSkills).toEqual(['second-skill', 'shared-skill', 'my-skill'])
    expect((await second).selectedSkills).toEqual(['my-skill'])
    expect(JSON.parse(files.get(`${path}/product.json`)!).templates).toEqual([])
    expect(JSON.parse(files.get(`${path}/workflow.json`)!).capabilities.selected_skills).toEqual(['my-skill'])
  } finally { vi.resetAllMocks() }
})

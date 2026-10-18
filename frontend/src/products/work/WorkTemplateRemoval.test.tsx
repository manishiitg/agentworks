// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const changeProjectTemplate = vi.hoisted(() => vi.fn())
const updatePlannerFile = vi.hoisted(() => vi.fn())
const getPlannerFileContent = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ agentApi: {
  getPlannerFileContent,
  changeProjectTemplate, updatePlannerFile,
}, getApiBaseUrl: () => '', getAuthToken: () => null }))

import { WorkTemplateSetup } from './WorkTemplateSetup'
import { crewTemplates } from './crewTemplates'
import { CODE_PRODUCT } from './projectProduct'
import { installWorkSessionTemplate, parseSessionManifest, removeWorkSessionTemplate, type WorkSession } from './workSessions'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('retries a failed removal and displays the server template state for a Code-origin Crew without manifest writes', async () => {
  const template = crewTemplates.find(item => item.id === 'finance-analyst')!
  const initial = parseSessionManifest(JSON.stringify({ schema_version: 1, product: 'code', id: 'demo', title: 'Crew', session_id: 'code:project:demo', templates: [{ id: template.id, version: template.version }] }), 'Crew/demo', undefined, CODE_PRODUCT)!
  getPlannerFileContent.mockResolvedValue({ data: { content: template.files[template.setupPath] } })
  let current = initial
  function ChatRows() {
    const [session, setSession] = useState(initial)
    return <>{session.templates.map(installed => <WorkTemplateSetup key={installed.id} template={template} workspacePath="Crew/demo" chatReady={false}
      onStartSetup={async () => {}} onRemove={async () => { current = await removeWorkSessionTemplate(session, template.id); setSession(current) }} />)}</>
  }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    changeProjectTemplate.mockRejectedValueOnce(new Error('Could not save template removal'))
    await act(async () => root.render(<ChatRows />))
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Remove Finance Analyst template"]')!.click())
    expect(host.textContent).toContain('Could not save template removal')
    expect(host.querySelector('[aria-label="Finance Analyst setup status"]')).not.toBeNull()
    changeProjectTemplate.mockResolvedValueOnce({ templates: [], selected_skills: ['keep'] })
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Remove Finance Analyst template"]')!.click())
    expect(host.querySelector('[aria-label="Finance Analyst setup status"]')).toBeNull()
    expect(current.selectedSkills).toEqual(['keep'])
    expect(changeProjectTemplate).toHaveBeenLastCalledWith('code', 'demo', 'remove_template', template.id)
    changeProjectTemplate.mockResolvedValueOnce({ templates: [{ id: template.id, version: template.version }], selected_skills: ['keep', template.id] })
    const reattached = await installWorkSessionTemplate(current, template.id)
    expect(reattached.selectedSkills).toEqual(['keep', template.id])
    expect(changeProjectTemplate).toHaveBeenLastCalledWith('code', 'demo', 'install_template', template.id)
    expect(updatePlannerFile).not.toHaveBeenCalled()
    await expect(removeWorkSessionTemplate({ ...initial, shared: { ownerId: 'owner', triggers: [], schedules: [] } } as WorkSession, template.id)).rejects.toThrow('Only the Crew owner')
    expect(changeProjectTemplate).toHaveBeenCalledTimes(3)
  } finally { await act(async () => root.unmount()); host.remove(); vi.resetAllMocks() }
})

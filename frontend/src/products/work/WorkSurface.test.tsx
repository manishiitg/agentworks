// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CreateWorkProjectDialog } from './CreateWorkProjectDialog'
import { normalizeWorkWorkspaceView, WorkChatTabs } from './WorkSurface'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { agentApi } from '../../services/api'
import { isWorkWorkspaceViewEnabled } from './workViewGating'
import { routeView } from '../../utils/productRoutes'
import { crewTemplates, type CrewTemplateId } from './crewTemplates'

vi.mock('../../utils/chatDeliveryTelemetry', async original => ({ ...await original<typeof import('../../utils/chatDeliveryTelemetry')>(), recordTabReorderTelemetry: vi.fn() }))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
// RunsOnPicker loads the provider manifest in an effect. This runner has no working
// localStorage, so the real store's late write became an unhandled rejection after the
// test ended. A store with the manifest already loaded starts no request at all.
const llmState = vi.hoisted(() => ({ providerManifest: [], providerManifestLoaded: true, loadProviderManifest: vi.fn(), setShowLLMModal: vi.fn() }))
vi.mock('../../stores/useLLMStore', () => ({
  useLLMStore: Object.assign((selector: (state: typeof llmState) => unknown) => selector(llmState), { getState: () => llmState }),
}))

describe('fresh deep links keep their view', () => {
  it('does not resolve a phantom Secrets view to project identity', () => {
    expect(normalizeWorkWorkspaceView('secrets')).toBeNull()
  })
  afterEach(() => { window.history.pushState(null, '', '/') })

  it('resolves a Crew schedules link instead of the identity default', () => {
    window.history.pushState(null, '', '/crew/projects/example?view=schedules')
    expect(routeView('work', 'example', normalizeWorkWorkspaceView, 'identity')).toBe('schedules')
  })

  it('keeps a route view only while its panel is enabled', () => {
    expect(isWorkWorkspaceViewEnabled('schedules', new Set(['schedules']))).toBe(true)
    expect(isWorkWorkspaceViewEnabled('schedules', new Set(['files']))).toBe(false)
    expect(isWorkWorkspaceViewEnabled('costs', new Set(['files']))).toBe(false)
  })

  it('resolves Code shell and costs links instead of Files', () => {
    window.history.pushState(null, '', '/code/projects/example?view=shell')
    expect(routeView('code', 'example', normalizeWorkWorkspaceView, 'files')).toBe('shell')
    window.history.pushState(null, '', '/code/projects/example?view=costs')
    expect(routeView('code', 'example', normalizeWorkWorkspaceView, 'files')).toBe('costs')
  })
})

describe('CreateWorkProjectDialog', () => {
  let container: HTMLDivElement | null = null

  afterEach(() => {
    container?.remove()
    container = null
  })

  it('requires a name and submits trimmed project details', async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const onCreate = vi.fn()

    await act(async () => {
      root.render(<CreateWorkProjectDialog onClose={() => {}} onCreate={onCreate} submitting={false} error={null} />)
    })

    const submit = container.querySelector('[data-testid="work-create-project-submit"]') as HTMLButtonElement
    expect(container.textContent).toContain('Create a Crew member')
    expect(submit.textContent).toContain('Create Crew member')
    expect(submit.disabled).toBe(true)

    const name = container.querySelector('[data-testid="work-create-project-name-input"]') as HTMLInputElement
    const icon = container.querySelector('[data-testid="work-create-project-icon-input"]') as HTMLInputElement
    const description = container.querySelector('textarea') as HTMLTextAreaElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(icon, '🚀')
      icon.dispatchEvent(new Event('input', { bubbles: true }))
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(name, '  Customer portal  ')
      name.dispatchEvent(new Event('input', { bubbles: true }))
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(description, '  Build and maintain the portal.  ')
      description.dispatchEvent(new Event('input', { bubbles: true }))
    })

    expect(submit.disabled).toBe(false)
    await act(async () => { submit.click() })
    expect(onCreate).toHaveBeenCalledWith('Customer portal', 'Build and maintain the portal.', '🚀', undefined, undefined)

    await act(async () => { root.unmount() })
  })

  it('previews the Finance Analyst template and submits its identity', async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const onCreate = vi.fn()
    await act(async () => {
      root.render(<CreateWorkProjectDialog onClose={() => {}} onCreate={onCreate} submitting={false} error={null} />)
    })

    const search = container.querySelector('[aria-label="Search Crew templates"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'Finance Analyst')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      (container!.querySelector('[data-testid="work-template-finance-analyst"]') as HTMLInputElement).click()
    })
    expect((container.querySelector('[data-testid="work-create-project-name-input"]') as HTMLInputElement).value).toBe('Finance Analyst')
    expect(container.textContent).toContain('Connections, schedules, triggers, functions, and Automations are not activated.')
    await act(async () => { (container!.querySelector('[data-testid="work-create-project-submit"]') as HTMLButtonElement).click() })
    expect(onCreate).toHaveBeenCalledWith(
      'Finance Analyst',
      expect.stringContaining('Analyze authorized finance records'),
      '📊',
      'finance-analyst',
      undefined,
    )
    await act(async () => { root.unmount() })
  })

  it('offers the Website Growth category and starter template', async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const onCreate = vi.fn()
    await act(async () => {
      root.render(<CreateWorkProjectDialog onClose={() => {}} onCreate={onCreate} submitting={false} error={null} />)
    })
    expect(container.textContent).toContain('Website Growth')
    const search = container.querySelector('[aria-label="Search Crew templates"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'Website Growth Starter')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.textContent).toContain('Website Growth Starter')
    await act(async () => {
      (container!.querySelector('[data-testid="work-template-website-growth-starter"]') as HTMLInputElement).click()
    })
    expect((container.querySelector('[data-testid="work-create-project-name-input"]') as HTMLInputElement).value).toBe('Website Growth Starter')
    await act(async () => { (container!.querySelector('[data-testid="work-create-project-submit"]') as HTMLButtonElement).click() })
    expect(onCreate).toHaveBeenCalledWith('Website Growth Starter', expect.stringContaining('Audit the business website'), '🌱', 'website-growth-starter', undefined)
    await act(async () => { root.unmount() })
  })

  it('browses QA and Security beneath Engineering without changing their template IDs', async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<CreateWorkProjectDialog onClose={() => {}} onCreate={vi.fn()} submitting={false} error={null} />)
    })
    const category = container.querySelector('[aria-label="Filter template category"]') as HTMLSelectElement
    expect([...category.options].map(option => option.value)).toContain('Engineering')
    expect([...category.options].map(option => option.value)).not.toContain('QA')
    expect([...category.options].map(option => option.value)).not.toContain('Security')
    await act(async () => {
      category.value = 'Engineering'
      category.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.textContent).toContain('13 results')
    expect(container.textContent).toContain('Engineering · QA ·')
    expect(container.textContent).toContain('Engineering · Security ·')
    await act(async () => { root.unmount() })
  })

  it('finds the Sales lead intake template and keeps message delivery pending', async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const onCreate = vi.fn()
    await act(async () => {
      root.render(<CreateWorkProjectDialog onClose={() => {}} onCreate={onCreate} submitting={false} error={null} />)
    })
    const search = container.querySelector('[aria-label="Search Crew templates"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'Lead Intake Qualifier')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.textContent).toContain('1 result')
    await act(async () => {
      (container!.querySelector('[data-testid="work-template-lead-intake-qualifier"]') as HTMLInputElement).click()
    })
    expect(container.textContent).toContain('Connections, schedules, triggers, functions, and Automations are not activated.')
    await act(async () => { (container!.querySelector('[data-testid="work-create-project-submit"]') as HTMLButtonElement).click() })
    expect(onCreate).toHaveBeenCalledWith('Lead Intake & Qualifier', expect.stringContaining('Turn an authorized inbound enquiry'), '📥', 'lead-intake-qualifier', undefined)
    await act(async () => { root.unmount() })
  })

  it('finds Customer Success onboarding without activating a customer action', async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const onCreate = vi.fn()
    await act(async () => {
      root.render(<CreateWorkProjectDialog onClose={() => {}} onCreate={onCreate} submitting={false} error={null} />)
    })
    const search = container.querySelector('[aria-label="Search Crew templates"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'milestone register')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.querySelector('[data-testid="work-template-customer-onboarding-coordinator"]')).not.toBeNull()
    await act(async () => {
      (container!.querySelector('[data-testid="work-template-customer-onboarding-coordinator"]') as HTMLInputElement).click()
    })
    expect(container.textContent).toContain('Connections, schedules, triggers, functions, and Automations are not activated.')
    await act(async () => { (container!.querySelector('[data-testid="work-create-project-submit"]') as HTMLButtonElement).click() })
    expect(onCreate).toHaveBeenCalledWith('Customer Onboarding Coordinator', expect.stringContaining('Turn an authorized new-customer handoff'), '🚀', 'customer-onboarding-coordinator', undefined)
    await act(async () => { root.unmount() })
  })

  it('finds a finance specialist by a tool the customer already uses', async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const onCreate = vi.fn()
    await act(async () => {
      root.render(<CreateWorkProjectDialog onClose={() => {}} onCreate={onCreate} submitting={false} error={null} />)
    })

    const search = container.querySelector('[aria-label="Search Crew templates"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'Ramp')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.textContent).toContain('1 result')
    await act(async () => {
      (container!.querySelector('[data-testid="work-template-spend-payables-coordinator"]') as HTMLInputElement).click()
    })
    expect(container.textContent).toContain('exports work first')
    await act(async () => { (container!.querySelector('[data-testid="work-create-project-submit"]') as HTMLButtonElement).click() })
    expect(onCreate).toHaveBeenCalledWith('Spend & Payables Coordinator', expect.stringContaining('Review proposed purchases, bills and spend'), '🧮', 'spend-payables-coordinator', undefined)
    await act(async () => { root.unmount() })
  })

  it('keeps a large catalog browsable without losing the selected template', async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const templates = Array.from({ length: 100 }, (_, index) => ({
      ...crewTemplates[0],
      id: `catalog-${index}` as CrewTemplateId,
      name: `Template ${String(index).padStart(3, '0')}`,
      category: index < 40 ? 'Finance' : index < 65 ? 'Operations' : index < 80 ? 'Sales' : index < 90 ? 'Marketing' : index < 95 ? 'Customer Support' : 'Engineering',
    }))

    await act(async () => {
      root.render(<CreateWorkProjectDialog onClose={() => {}} onCreate={() => {}} submitting={false} error={null} templates={templates} />)
    })
    expect(container.textContent).toContain('100 available')
    expect(container.querySelectorAll('[data-testid^="work-template-"]')).toHaveLength(12)

    await act(async () => {
      (Array.from(container!.querySelectorAll('button')).find(button => button.textContent?.startsWith('Show more')) as HTMLButtonElement).click()
    })
    expect(container.querySelectorAll('[data-testid^="work-template-"]')).toHaveLength(24)

    await act(async () => {
      const category = container!.querySelector('[aria-label="Filter template category"]') as HTMLSelectElement
      category.value = 'Finance'
      category.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.textContent).toContain('40 results')
    expect(container.querySelectorAll('[data-testid^="work-template-"]')).toHaveLength(12)

    await act(async () => {
      (container!.querySelector('[data-testid="work-template-catalog-0"]') as HTMLInputElement).click()
    })
    const search = container.querySelector('[aria-label="Search Crew templates"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'nothing matches this')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.textContent).toContain('No results')
    expect(container.textContent).toContain('Selected: Template 000')
    expect((container.querySelector('[data-testid="work-create-project-name-input"]') as HTMLInputElement).value).toBe('Template 000')

    await act(async () => { root.unmount() })
  })
})


describe('Crew native tab dragging', () => {
  it('uses the sidebar server order and counts agents without counting schedule or bot activity views', async () => {
    const make = (id: string, position = 0, activity = false) => ({ tabId: id, sessionId: `session-${id}`, name: id, createdAt: id === 'z' ? 1 : 900, config: {}, metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileIsMainChat: id === 'main', agentProfileConversationKey: id === 'main' ? 'crew:chat:root-main' : `crew:chat:${id}`, agentProfileTabPosition: position, ...(activity ? { isViewOnly: true, isScheduledRun: id === 'schedule', isBotRun: id === 'bot' } : {}) } } as ChatTab)
    useChatStore.setState({ chatTabs: { z: make('z'), bot: make('bot', 0, true), main: make('main'), a: make('a'), schedule: make('schedule', 0, true), b: make('b', 1) }, activeTabId: 'b' })
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkChatTabs projectId="crew" projectName="Crew" canonicalTabId="main" profileId="work" allowSideChats={false} />))
      const ids = (selector: string, attribute: string) => [...host.querySelectorAll(selector)].map(node => node.getAttribute(attribute))
      expect(ids('[data-chat-tab-id]', 'data-chat-tab-id')).toEqual(['main', 'b', 'a', 'bot', 'schedule'])
      const menu = host.querySelector<HTMLButtonElement>('[data-testid="work-more-tabs"]')!
      expect(menu.textContent).toBe('Agents · 4')
      await act(async () => menu.click())
      expect(ids('[data-chat-list-id]', 'data-chat-list-id')).toEqual(['main', 'b', 'a', 'bot', 'schedule', 'z'])
      expect(host.querySelector('[aria-label="Agents"] [title="Close activity view"]')).not.toBeNull()
      expect(host.querySelector('[aria-label="Close Crew"]')).toBeNull()
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it.each(['a', 'b'])('keeps a drag source and drop target working while %s is selected', async active => {
    const make = (id: string, position: number) => ({ tabId: id, sessionId: `session-${id}`, name: id, createdAt: position, config: {}, metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileIsMainChat: id === 'main', agentProfileConversationKey: id === 'main' ? 'crew' : `crew:chat:${id}`, agentProfileTabPosition: position } } as ChatTab)
    useChatStore.setState({ chatTabs: { main: make('main', 0), a: make('a', 1), b: make('b', 2) }, activeTabId: active })
    const list = vi.spyOn(agentApi, 'listAgentProfileSideChats').mockResolvedValue([])
    const order = vi.spyOn(agentApi, 'orderAgentProfileSideChats').mockResolvedValue([{ conversation_key: 'crew:chat:b', session_id: 'session-b', name: 'b', position: 1 }, { conversation_key: 'crew:chat:a', session_id: 'session-a', name: 'a', position: 2 }])
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkChatTabs projectId="crew" projectName="Crew" canonicalTabId="main" profileId="work" allowSideChats />))
      const strip = host.querySelector('[data-testid="work-chat-tab-strip"]')!
      expect(strip.querySelector('.lucide-grip-vertical')).toBeNull()
      expect(strip.querySelector('[aria-label="Close a"]')).toBeNull()
      expect(strip.querySelector('[title="Make this the main chat"]')).toBeNull()
      const dataTransfer = { setData: vi.fn(), effectAllowed: '', dropEffect: '' }
      const gesture = (kind: string, node: Element) => { const event = new Event(kind, { bubbles: true, cancelable: true }); Object.defineProperty(event, 'dataTransfer', { value: dataTransfer }); node.dispatchEvent(event) }
      await act(async () => gesture('dragstart', host.querySelector('[data-chat-tab-id="a"]')!))
      expect(dataTransfer.setData).toHaveBeenCalledWith('application/x-agentworks-chat-key', 'crew:chat:a')
      await act(async () => gesture('dragover', host.querySelector('[data-chat-tab-id="b"]')!))
      await act(async () => gesture('drop', host.querySelector('[data-chat-tab-id="b"]')!))
      expect(order).toHaveBeenCalledWith('work', 'crew', ['crew:chat:b', 'crew:chat:a'])
      const menu = host.querySelector<HTMLButtonElement>('[data-testid="work-more-tabs"]')!
      expect(menu.textContent).toContain('Agents · 3')
      await act(async () => menu.click())
      expect(host.querySelector('[aria-label="Agents"]')?.textContent).toContain('a')
      expect(host.querySelector('[aria-label="Move a earlier"]')).not.toBeNull()
      expect(host.querySelector('[aria-label="Close a"]')).not.toBeNull()
    } finally { await act(async () => root.unmount()); host.remove(); list.mockRestore(); order.mockRestore() }
  })
})

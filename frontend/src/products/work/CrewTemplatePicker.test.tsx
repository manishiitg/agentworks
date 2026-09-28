// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CrewTemplatePicker } from './CrewTemplatePicker'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('Crew Identity template browse', () => {
  let container: HTMLDivElement | null = null

  afterEach(() => {
    container?.remove()
    container = null
  })

  it('combines browse category and search while excluding installed templates', async () => {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const onInstallTemplate = vi.fn(async () => {})

    await act(async () => {
      root.render(<CrewTemplatePicker
        projectTemplates={[{ id: 'incident-investigator', version: 1 }]}
        onInstallTemplate={onInstallTemplate}
        onError={vi.fn()}
      />)
    })

    const category = container.querySelector('[aria-label="Filter template category"]') as HTMLSelectElement
    expect([...category.options].map(option => option.value)).toContain('Engineering')
    expect([...category.options].map(option => option.value)).not.toContain('QA')
    expect([...category.options].map(option => option.value)).not.toContain('Security')
    await act(async () => {
      category.value = 'Engineering'
      category.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.textContent).toContain('12 results')
    expect(container.querySelector('[data-testid="identity-template-incident-investigator"]')).toBeNull()
    expect(container.querySelector('[data-testid="identity-template-browser-journey-qa-analyst"]')).not.toBeNull()

    const search = container.querySelector('[aria-label="Search Crew templates"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'performance')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.textContent).toContain('1 result')
    const performance = container.querySelector('[data-testid="identity-template-performance-investigator"]') as HTMLDivElement
    expect(performance).not.toBeNull()
    expect(container.querySelector('[data-testid="identity-template-browser-journey-qa-analyst"]')).toBeNull()
    await act(async () => { (performance.querySelector('button') as HTMLButtonElement).click() })
    expect(onInstallTemplate).toHaveBeenCalledWith('performance-investigator')

    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'no matching template')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.textContent).toContain('No templates match these filters.')
    await act(async () => {
      (Array.from(container!.querySelectorAll('button')).find(button => button.textContent === 'Clear filters') as HTMLButtonElement).click()
    })
    expect(category.value).toBe('all')
    expect(search.value).toBe('')
    await act(async () => { root.unmount() })
  })
})

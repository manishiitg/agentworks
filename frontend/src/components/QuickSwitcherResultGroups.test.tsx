// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { QuickSwitcherResultGroups } from './QuickSwitcherResultGroups'
import { quickSwitcherArtifactID, quickSwitcherGroups, quickSwitcherMatches, quickSwitcherMatchRank, uniqueQuickSwitcherArtifacts, type SearchResultRow } from '../utils/quickSwitcherResults'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('PLAT-856-568: whatsapp replaces same-slug artifact rows, ranks its panel first and keeps group statuses at the bottom', async () => {
  // Native directory slugs are workspace-local: all four may be "overview".
  const directory = ['SEO analyst', 'AgentWorks', 'X post drafts', 'X post drafts'].map((label, i) => ({ workspace: `Crew/project-${i}`, dashboard_id: 'overview', document_path: 'index.html', label }))
  const artifacts = uniqueQuickSwitcherArtifacts([...directory, directory[0]]).map(row => ({ ...row, id: quickSwitcherArtifactID(row), type: 'artifact', subtitle: 'Crew', searchText: 'whatsapp hidden path' }))
  expect(artifacts).toHaveLength(4)
  expect(new Set(artifacts.map(row => row.id)).size).toBe(4)
  const inventory: SearchResultRow[] = [artifacts[0], { id: 'panel:whatsapp', type: 'panel', label: 'WhatsApp', subtitle: 'Panel' }, artifacts[1], { id: 'schedule:whatsapp', type: 'schedule', label: 'WhatsApp summary', subtitle: 'Daily' }, artifacts[2], { id: 'artifact:match', type: 'artifact', label: 'WhatsApp report', subtitle: 'Crew' }, artifacts[3], { id: 'artifact:match2', type: 'artifact', label: 'WhatsApp notes', subtitle: 'Crew' }]
  const footers = { artifact: <button>Some artifacts could not load. Retry</button>, message: <button>No matches in messages searched so far · Search older messages</button> }
  const view = (query: string) => {
    const ranked = inventory.filter(row => !query || quickSwitcherMatches(row, query)).sort((a, b) => quickSwitcherMatchRank(a, query) - quickSwitcherMatchRank(b, query))
    const rows = quickSwitcherGroups(ranked).flatMap(group => group.rows)
    return <QuickSwitcherResultGroups rows={rows} footers={footers} renderRow={row => <div role="option" data-id={row.id}>{row.label}</div>} />
  }
  const host = document.createElement('div'), root = createRoot(host)
  const options = () => [...host.querySelectorAll<HTMLElement>('[role="option"]')]
  const group = (label: string) => host.querySelector<HTMLElement>(`section[aria-label="${label}"]`)!
  try {
    await act(async () => root.render(view('')))
    expect(options().filter(row => row.textContent === 'X post drafts')).toHaveLength(2) // Distinct artifacts, not title-deduped.
    await act(async () => root.render(view('whatsapp')))
    expect(options().map(row => row.textContent)).toEqual(['WhatsApp', 'WhatsApp summary', 'WhatsApp report', 'WhatsApp notes'])
    for (const label of ['SEO analyst', 'AgentWorks', 'X post drafts']) expect(options().some(row => row.textContent === label)).toBe(false)
    expect([...host.querySelectorAll('h3')].filter(row => row.textContent === 'Artifacts')).toHaveLength(1)
    expect([...host.querySelectorAll('h3')].filter(row => row.textContent === 'Schedules')).toHaveLength(1)
    const artifactGroup = group('Artifacts')
    expect(artifactGroup.lastElementChild).toBe(artifactGroup.querySelector('button'))
    const messageGroup = group('In messages · this workspace')
    expect(messageGroup).toBe([...host.querySelectorAll('section')].at(-1))
    expect(messageGroup.lastElementChild).toBe(messageGroup.querySelector('button'))
    // No stale results after another query; a loaded hit's footer stays with it.
    await act(async () => root.render(<QuickSwitcherResultGroups rows={[{ id: 'message:1', type: 'message', label: 'Crew · agent', subtitle: 'whatsapp' }]} footers={{ message: <button>Search older messages</button> }} renderRow={row => <div role="option">{row.subtitle}</div>} />))
    expect(group('Artifacts')).toBeNull()
    expect(host.textContent).not.toContain('Some artifacts could not load. Retry')
    expect(host.querySelector('section')?.lastElementChild).toBe(host.querySelector('button'))
  } finally { await act(async () => root.unmount()) }
})

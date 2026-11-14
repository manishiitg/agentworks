import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import { QuickSwitcherResultGroups } from './QuickSwitcherResultGroups'
import { quickSwitcherArtifactID, quickSwitcherGroups, quickSwitcherMatches, quickSwitcherMatchRank, uniqueQuickSwitcherArtifacts, type SearchResultRow } from '../utils/quickSwitcherResults'

afterEach(cleanup)

it('PLAT-856-568: whatsapp replaces same-slug artifact rows, ranks its panel first and keeps group statuses at the bottom', () => {
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
  const { rerender } = render(view(''))
  expect(screen.getAllByRole('option', { name: 'X post drafts' })).toHaveLength(2) // Different artifacts, not title-deduped.
  rerender(view('whatsapp'))
  expect(screen.getAllByRole('option').map(row => row.textContent)).toEqual(['WhatsApp', 'WhatsApp summary', 'WhatsApp report', 'WhatsApp notes'])
  for (const label of ['SEO analyst', 'AgentWorks', 'X post drafts']) expect(screen.queryByRole('option', { name: label })).toBeNull()
  expect(screen.getAllByRole('heading', { name: 'Artifacts' })).toHaveLength(1)
  expect(screen.getAllByRole('heading', { name: 'Schedules' })).toHaveLength(1)
  const artifactGroup = screen.getByRole('group', { name: 'Artifacts' })
  expect(artifactGroup.lastElementChild).toBe(within(artifactGroup).getByRole('button'))
  const messageGroup = screen.getByRole('group', { name: 'In messages · this workspace' })
  expect(messageGroup).toBe(screen.getAllByRole('group').at(-1))
  expect(messageGroup.lastElementChild).toBe(within(messageGroup).getByRole('button'))
  // No stale results after another query, and a loaded hit's footer stays in
  // its message group instead of splitting navigation or artifact results.
  rerender(<QuickSwitcherResultGroups rows={[{ id: 'message:1', type: 'message', label: 'Crew · agent', subtitle: 'whatsapp' }]} footers={{ message: <button>Search older messages</button> }} renderRow={row => <div role="option">{row.subtitle}</div>} />)
  expect(screen.queryByRole('group', { name: 'Artifacts' })).toBeNull()
  expect(screen.queryByText('Some artifacts could not load. Retry')).toBeNull()
  expect(screen.getByRole('group').lastElementChild).toBe(screen.getByRole('button', { name: 'Search older messages' }))
})

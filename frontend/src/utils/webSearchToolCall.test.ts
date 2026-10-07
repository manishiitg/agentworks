import { describe, expect, it } from 'vitest'
import { parseWebSearchToolCall } from './webSearchToolCall'

// Shapes copied from real recorded calls (2026-10-07); see webSearchToolCall.ts.
describe('parseWebSearchToolCall', () => {
  it('reads Claude Code WebSearch links into result rows', () => {
    const view = parseWebSearchToolCall({
      name: 'WebSearch',
      args: '{"query":"Hexalog logistics AI platform India funding","mode":"standard"}',
      result: 'Web search results for query: "Hexalog logistics AI platform India funding"\n\n' +
        'Links: [{"title":"Hexalog Raises USD 4 Mn [Seed]","url":"https://india.entrepreneur.com/news/501840"},' +
        '{"title":"app.dealroom.co","url":"https://app.dealroom.co/companies/hexalog"},' +
        '{"title":"bad","url":"javascript:alert(1)"}]\n\nBased on the search results, see https://ignored.example.com',
    })
    expect(view?.query).toBe('Hexalog logistics AI platform India funding')
    expect(view?.results).toEqual([
      { title: 'Hexalog Raises USD 4 Mn [Seed]', url: 'https://india.entrepreneur.com/news/501840', domain: 'india.entrepreneur.com' },
      { title: 'app.dealroom.co', url: 'https://app.dealroom.co/companies/hexalog', domain: 'app.dealroom.co' },
    ])
  })

  it('extracts plain http(s) links from AGY search_web text', () => {
    const view = parseWebSearchToolCall({
      name: 'search_web',
      args: '{"query":"agentworks"}',
      result: 'Top results:\n- AgentWorks home (https://www.agentworks.dev/).\n- Docs: https://docs.agentworks.dev/start, and https://www.agentworks.dev/',
    })
    expect(view?.query).toBe('agentworks')
    expect(view?.results.map(row => [row.url, row.domain])).toEqual([
      ['https://www.agentworks.dev/', 'agentworks.dev'],
      ['https://docs.agentworks.dev/start', 'docs.agentworks.dev'],
    ])
  })

  it('shows a Codex web_search as its query, and a URL "query" as an opened page', () => {
    // Structured Codex: the start event has no args; the provider's end result
    // (multi-llm-provider-go codexWebSearchResultText, real 0.160.1 search) carries both.
    const codexResult = 'Web search results for query: "latest Go release version site:go.dev"\n\n' +
      'Links: [{"title":"Go 1.27 Release Notes - The Go Programming Language","url":"https://go.dev/doc/go1.27"},' +
      '{"title":"Release History - The Go Programming Language","url":"https://go.dev/doc/devel/release"}]'
    const codex = parseWebSearchToolCall({ name: 'web_search', args: '', result: codexResult })
    expect(codex).toMatchObject({ query: 'latest Go release version site:go.dev', opened: [] })
    expect(codex?.results.map(r => r.url)).toEqual(['https://go.dev/doc/go1.27', 'https://go.dev/doc/devel/release'])
    expect(parseWebSearchToolCall({ name: 'web_search', args: '{"query":"codex cli release notes"}' }))
      .toMatchObject({ query: 'codex cli release notes', results: [], opened: [] })
    expect(parseWebSearchToolCall({ name: 'web_search', args: '{"query":"https://pi.dev/"}' }))
      .toMatchObject({ query: '', opened: ['https://pi.dev/'] })
    // Not a search: the generic tool card stays.
    expect(parseWebSearchToolCall({ name: 'search_web_llm', args: '{"query":"x"}' })).toBeNull()
  })

  it('unwraps Cursor CallDynamicTool WebSearch and reads its markdown links', () => {
    const view = parseWebSearchToolCall({
      name: 'CallDynamicTool',
      args: '{"arguments":{"search_term":"cursor-agent CLI latest version"},"namespace":"cursor","toolName":"WebSearch"}',
      result: 'Title: Web search results\nContent: Links:\n1. [CLI Changelog | Cursor Docs](https://cursor.com/docs/cli/changelog)\n',
    })
    expect(view?.query).toBe('cursor-agent CLI latest version')
    expect(view?.results).toEqual([{ title: 'CLI Changelog | Cursor Docs', url: 'https://cursor.com/docs/cli/changelog', domain: 'cursor.com' }])
  })
})

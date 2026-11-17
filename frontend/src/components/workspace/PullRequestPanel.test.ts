import { describe, expect, it } from 'vitest'
import { fixCommentsMessage, mergeMessage, pullRequestStatusLine } from './PullRequestPanel'

describe('PullRequestPanel words', () => {
  it('says where a pull request stands in plain words', () => {
    expect(pullRequestStatusLine({ status: { state: 'open', checks: 'passing', review: 'approved' }, mergeable: true })).toBe('Checks passing · Approved · Ready to merge')
    expect(pullRequestStatusLine({ status: { state: 'open', checks: 'failing', failing_checks: ['a', 'b'], review: 'none' }, mergeable: false })).toBe('2 checks failing · No review yet')
    expect(pullRequestStatusLine({ status: { state: 'open', review: 'changes_requested', conflicts: true }, mergeable: false })).toBe('Changes requested · Has conflicts')
    expect(pullRequestStatusLine({ status: { state: 'merged' }, mergeable: false })).toBe('Merged')
  })

  it('asks the agent to fix every open comment with its file and line', () => {
    const text = fixCommentsMessage({ repository: 'acme/app', number: 4, url: 'https://github.com/acme/app/pull/4', head: 'chat/x' }, [
      { path: 'a.go', line: 3, author: 'rev', body: 'rename\nthis' },
      { author: 'rev', body: 'Overall fine', review: 'changes_requested' },
      { path: 'a.go', line: 3, author: 'dev', body: 'done', reply: true },
    ])
    expect(text).toContain('- a.go:3 — rev: rename this')
    expect(text).toContain('- Review summary — rev: Overall fine')
    expect(text).not.toContain('done')
  })

  it('leaves the merge method to the person, asked by the agent', () => {
    expect(mergeMessage({ repository: 'acme/app', number: 4, url: 'u', base: 'main' })).toMatch(/ask me which way to merge/)
  })
})

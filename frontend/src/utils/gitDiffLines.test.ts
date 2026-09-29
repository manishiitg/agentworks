import { describe, expect, it } from 'vitest'
import { allLinesAdded, parseGitDiffLines } from './gitDiffLines'

const diff = (body: string) => `diff --git a/f.ts b/f.ts\nindex 1..2 100644\n--- a/f.ts\n+++ b/f.ts\n${body}`

describe('parseGitDiffLines', () => {
  it('marks added, modified and deleted lines by their line in the new file', () => {
    const result = parseGitDiffLines(diff([
      '@@ -1,6 +1,7 @@',
      ' keep 1',
      '-old 2',
      '+new 2',
      ' keep 3',
      '+added 4',
      ' keep 5',
      '-gone 6',
      ' keep 7',
    ].join('\n')))
    expect(result.modified).toEqual([2])
    expect(result.added).toEqual([4])
    expect(result.deleted).toEqual([6])
  })

  it('handles several hunks and a deletion at the end of a hunk', () => {
    const result = parseGitDiffLines(diff([
      '@@ -1,2 +1,3 @@',
      ' a',
      '+b',
      ' c',
      '@@ -10,3 +11,2 @@',
      ' x',
      '-y',
      ' z',
    ].join('\n')))
    expect(result.added).toEqual([2])
    expect(result.deleted).toEqual([12])
  })

  it('ignores file headers, "no newline" markers and an empty diff', () => {
    expect(parseGitDiffLines('')).toEqual({ added: [], modified: [], deleted: [] })
    const result = parseGitDiffLines(diff('@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+b\n\\ No newline at end of file'))
    expect(result.modified).toEqual([1])
  })

  it('treats every line of an untracked file as added', () => {
    expect(allLinesAdded('a\nb\nc\n').added).toEqual([1, 2, 3])
    expect(allLinesAdded('').added).toEqual([])
  })
})

import { expect, it } from 'vitest'
import { describeToolStep, summarizeSteps } from './describeToolStep'

// #3307: chat steps read as plain sentences from each provider's own tool input.
it('describes Claude, Codex and unknown tool steps in plain words', () => {
  const cases: [Parameters<typeof describeToolStep>[0], string][] = [
    [{ name: 'Bash', args: '{"command":"npx vitest run","description":"Run unit tests"}' }, 'Ran "Run unit tests"'],
    [{ name: 'exec_command', args: '{"cmd":["git","status"]}' }, 'Ran git status'],
    [{ name: 'Read', args: '{"file_path":"/repo/src/ChatArea.tsx"}' }, 'Read ChatArea.tsx'],
    [{ name: 'Write', args: '{"file_path":"/repo/notes.md"}', result: 'File created successfully' }, 'Created notes.md'],
    [{ name: 'Write', args: '{"file_path":"/repo/notes.md"}', result: 'The file has been updated' }, 'Edited notes.md'],
    [{ name: 'apply_patch', args: '{"input":"*** Begin Patch\\n*** Update File: a/x.ts\\n*** Add File: a/y.ts\\n*** End Patch"}' }, 'Edited 2 files'],
    [{ name: 'WebSearch', args: '{"query":"nested tables"}' }, 'Searched the web for "nested tables"'],
    [{ name: 'createIssue', server: 'github' }, 'Used create issue in github'],
    [{ name: 'Read', args: 'not json' }, 'Read a file'],
  ]
  for (const [input, line] of cases) expect(describeToolStep(input).done).toBe(line)
})

it('counts a file edited twice once in the summary', () => {
  const steps = [
    describeToolStep({ name: 'Read', args: '{"file_path":"a.ts"}' }),
    describeToolStep({ name: 'Edit', args: '{"file_path":"a.ts"}' }),
    describeToolStep({ name: 'Edit', args: '{"file_path":"a.ts"}' }),
    describeToolStep({ name: 'Bash', args: '{"command":"npm test"}' }),
  ]
  expect(summarizeSteps(steps)).toBe('Read 1 file, edited 1 file, ran 1 command')
})

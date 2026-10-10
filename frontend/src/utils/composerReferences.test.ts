import { describe, expect, it } from 'vitest'
import { fileReferenceRanges, formatFileReference, getComposerTrigger, reconcileFileReferences, removeFileReferences, replaceComposerTrigger } from './composerReferences'

const triggerAtEnd = (text: string) => getComposerTrigger(text, text.length)

describe('composer triggers', () => {
  it.each(['https://example.com/pulse', 'Check https://example.com/pulse', 'name@example.com', '/usr/local', 'C:\\work\\file', 'hello/world', '# Heading', '@file.md then ordinary text', '/review.md'])('keeps %s as ordinary text', text => {
    expect(triggerAtEnd(text)).toBeNull()
  })
  it.each([
    ['/', '/', ''], ['/pulse', '/', 'pulse'], ['@file.md now /pulse', '/', 'pulse'],
    ['@Workflow/latency-report', '@', 'Workflow/latency-report'], ['Compare @报告.md', '@', '报告.md'],
    ['Compare @"My Report', '@', 'My Report'], ['#workflow', '#', 'workflow'],
  ])('recognizes only the current token in %s', (text, kind, query) => {
    expect(triggerAtEnd(text)).toMatchObject({ kind, query, end: text.length })
  })
  it('uses the caret and replaces a complete token without losing surrounding text', () => {
    const text = 'Compare @old/file.md with yesterday'
    const trigger = getComposerTrigger(text, 'Compare @old'.length)!
    expect(trigger.query).toBe('old')
    expect(replaceComposerTrigger(text, trigger, '@new/file.md ')).toEqual({ text: 'Compare @new/file.md  with yesterday', caret: 21 })
    expect(getComposerTrigger(text, 9, 12)).toBeNull()
    expect(getComposerTrigger('/usr/local', 4)).toBeNull()
    expect(getComposerTrigger('https://example.com/pulse', 24)).toBeNull()
  })
  it('allows quoted filenames with spaces and escaped quotes', () => {
    const path = 'reports/My "Great" Report.md'
    const reference = formatFileReference(path)
    expect(fileReferenceRanges(reference + ' compare', path)).toEqual([{ start: 0, end: reference.length }])
    expect(triggerAtEnd(reference)).toBeNull()
    const trigger = getComposerTrigger(reference + ' suffix', reference.length - 1)!
    expect(trigger.query).toBe(path)
    expect(replaceComposerTrigger(reference + ' suffix', trigger, '@other')).toMatchObject({ text: '@other suffix' })
  })
})

describe('reference attachment reconciliation', () => {
  const files = [{ path: 'plan.json' }, { path: 'report.md' }, { path: 'uploaded.pdf' }]
  it('removes deleted references together, preserving independent attachments', () => {
    expect(reconcileFileReferences('@plan.json @report.md', '', files)).toEqual([{ path: 'uploaded.pdf' }])
    expect(reconcileFileReferences('@plan.json', '@plan.jso', files)).toEqual(files.slice(1))
  })
  it('keeps a file until its last reference is removed and avoids prefix collisions', () => {
    expect(reconcileFileReferences('@plan.json @plan.json', '@plan.json', files)).toEqual(files)
    expect(reconcileFileReferences('@plan.json', '@plan.json.bak', files)).toEqual(files.slice(1))
    expect(reconcileFileReferences('@plan.json', '@plan.json. Continue here', files)).toEqual(files)
    expect(removeFileReferences('Read @plan.json and @plan.json.bak; @plan.json!', 'plan.json')).toBe('Read  and @plan.json.bak; !')
  })
  it('removes quoted and legacy references for a filename containing spaces', () => {
    expect(removeFileReferences('Compare @"My Report.md" with @My Report.md tomorrow', 'My Report.md')).toBe('Compare  with  tomorrow')
  })
})

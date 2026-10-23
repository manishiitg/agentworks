import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import NotificationInstructions from './NotificationInstructions'

describe('NotificationInstructions', () => {
  it.each(['Run summary', 'Autopilot review'])('exposes the complete %s instructions in a native disclosure', title => {
    const instructions = 'First line\n' + 'Detailed instruction. '.repeat(100) + '\nLast line'
    const html = renderToStaticMarkup(<NotificationInstructions title={title} instructions={instructions} />)
    expect(html).toContain('<details')
    expect(html).toContain('group w-full min-w-0 text-xs')
    expect(html).not.toContain('sm:pl-')
    expect(html).not.toContain('<details open')
    expect(html).toContain('<summary')
    expect(html).toContain('Show full instructions')
    expect(html).toContain('Show less')
    expect(html).toContain('for ' + title)
    expect(html).toContain('whitespace-pre-wrap break-words')
    expect(html).toContain(instructions + '</p>')
  })
})

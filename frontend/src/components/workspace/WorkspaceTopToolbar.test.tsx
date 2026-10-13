import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { WorkspaceTopToolbar } from './WorkspaceTopToolbar'

describe('WorkspaceTopToolbar', () => {
  it('wraps below desktop width so every control stays reachable, and stays one row at desktop width', () => {
    render(<WorkspaceTopToolbar><span>tabs</span><span>toolbar</span></WorkspaceTopToolbar>)
    const bar = screen.getByText('tabs').parentElement!
    expect(bar.className).toContain('flex-wrap')
    expect(bar.className).toContain('md:flex-nowrap')
    expect(bar.className).not.toMatch(/(?<!md:)flex-nowrap/)
  })
})

import { describe, expect, it } from 'vitest'
import { selectableProducts } from './selectableProducts'

const all = ['agentworks', 'code', 'dominion', 'sparkquill', 'video-studio', 'work']

describe('products an admin can grant', () => {
  it('offers only the deployment’s main products', () => {
    expect(selectableProducts(all, ['code'])).toEqual(['code'])
    expect(selectableProducts(all, ['agentworks', 'work', 'code'])).toEqual(['agentworks', 'code', 'work'])
  })
  it('never offers the dedicated products on a shared deployment, even when enabled', () => {
    expect(selectableProducts(all, ['agentworks', 'work', 'code', 'dominion', 'sparkquill', 'video-studio'])).toEqual(['agentworks', 'code', 'work'])
  })
  it('a dedicated product deployment still offers its own product', () => {
    expect(selectableProducts(all, ['dominion'])).toEqual(['dominion'])
    expect(selectableProducts(all, ['video-studio'])).toEqual(['video-studio'])
  })
  it('offers nothing the server does not host', () => {
    expect(selectableProducts(['code'], ['agentworks', 'code'])).toEqual(['code'])
    expect(selectableProducts([], ['code'])).toEqual([])
  })
})

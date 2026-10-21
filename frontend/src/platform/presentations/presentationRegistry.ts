import type { ComponentType } from 'react'
import type { PresentationRendererProps } from './PresentationRenderer'

const renderers = new Map<string, ComponentType<PresentationRendererProps>>()

export function registerPresentationRenderer(kind: string, renderer: ComponentType<PresentationRendererProps>) {
  if (!kind.trim()) throw new Error('presentation kind is required')
  renderers.set(kind, renderer)
}

export function getPresentationRenderer(kind: string): ComponentType<PresentationRendererProps> | undefined {
  return renderers.get(kind)
}

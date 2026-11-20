import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { EditorIcon } from './EditorIcons'

it('draws a mark for each editor the server offers and a fallback for others', () => {
  for (const id of ['vscode', 'cursor', 'zed', 'jetbrains', 'something-new']) {
    expect(renderToStaticMarkup(<EditorIcon id={id} />)).toContain('<svg')
  }
})

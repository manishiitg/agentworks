const CONTEXT_PLACEHOLDER = /\{\{context\}\}/g
// A line that is only a label for the placeholder, e.g.
// "Additional context from me (optional): {{context}}".
const LABELLED_PLACEHOLDER_LINE = /^[^\n]*:[ \t]*\{\{context\}\}[ \t]*$/
const BARE_PLACEHOLDER_LINE = /^[ \t]*\{\{context\}\}[ \t]*$/
const LABEL_LINE = /:[ \t]*$/

// renderCommandPrompt fills a slash command's `{{context}}` placeholder.
// With no context, a line that only labels the placeholder is dropped
// instead of left dangling: an agent reads a trailing "Additional context
// from me:" as a message cut off mid-sentence and asks for the rest
// rather than running the command.
export function renderCommandPrompt(template: string, context: string): string {
  const value = context.trim()
  if (value) {
    // Commands saved before {{context}} was enforced still get the typed text.
    if (!template.includes('{{context}}')) return `${template.trim()}\n\n${value}`
    return template.replace(CONTEXT_PLACEHOLDER, value).trim()
  }

  const lines = template.split('\n')
  const kept: string[] = []
  for (const line of lines) {
    if (LABELLED_PLACEHOLDER_LINE.test(line)) continue
    if (BARE_PLACEHOLDER_LINE.test(line)) {
      // "Label:\n{{context}}" — drop the label line above too.
      if (kept.length > 0 && LABEL_LINE.test(kept[kept.length - 1])) kept.pop()
      continue
    }
    kept.push(line.replace(CONTEXT_PLACEHOLDER, ''))
  }
  return kept.join('\n').replace(/\n{3,}/g, '\n\n').trim()
}

// PLAT-746: command names may contain spaces. Match the longest registered
// prefix on a word boundary, leaving the remaining text as its arguments.
export function resolveSlashCommandInput<T>(input: string, find: (name: string) => T | undefined): { command: T; name: string; args: string } | undefined {
  const value = input.trim()
  const boundaries = [...value.matchAll(/\s+/g)].map(match => match.index!)
  for (const end of [value.length, ...boundaries.reverse()]) {
    const name = value.slice(0, end)
    const command = find(name)
    if (command) return { command, name, args: value.slice(end).trim() }
  }
  return undefined
}

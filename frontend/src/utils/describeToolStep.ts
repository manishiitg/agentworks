// Plain-language step lines for the chat (#3307): "Read ChatArea.tsx",
// "Ran "Run unit tests"", "Searched the web for …". The exact tool name and
// input stay one click away in the step's details.

export type StepKind = 'read' | 'edit' | 'create' | 'run' | 'search' | 'web' | 'open' | 'helper' | 'plan' | 'other'

export interface StepDescription {
  kind: StepKind
  /** Past tense, for a finished step. */
  done: string
  /** Present tense, for the step running now. */
  live: string
  /** Files this step read or wrote, by name, for the summary. */
  files: string[]
}

export interface StepInput {
  name: string
  server?: string
  args?: string
  result?: string
}

const MAX_TARGET = 70

function parseArgs(raw?: string): Record<string, unknown> {
  if (!raw) return {}
  try {
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {}
  } catch {
    return {}
  }
}

function arg(args: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const value = args[key]
    if (typeof value === 'string' && value.trim()) return value.trim().replace(/\s+/g, ' ')
    if (Array.isArray(value) && value.length && value.every(part => typeof part === 'string')) return value.join(' ').trim()
  }
  return ''
}

function short(text: string): string {
  return text.length > MAX_TARGET ? `${text.slice(0, MAX_TARGET - 1)}…` : text
}

export function fileName(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, '')
  return trimmed.split(/[\\/]/).pop() || trimmed
}

function hostOf(url: string): string {
  try { return new URL(url).host || url } catch { return url }
}

function humanize(name: string): string {
  return name.replace(/([a-z])([A-Z])/g, '$1 $2').replace(/[_-]+/g, ' ').trim().toLowerCase()
}

function step(kind: StepKind, done: string, live: string, files: string[] = []): StepDescription {
  return { kind, done, live, files }
}

function patchFiles(text: string): { added: string[]; changed: string[] } {
  const added: string[] = [], changed: string[] = []
  for (const line of text.split('\n')) {
    const add = /^\*\*\* Add File: (.+)$/.exec(line)
    const other = /^\*\*\* (?:Update|Delete) File: (.+)$/.exec(line)
    if (add) added.push(fileName(add[1].trim()))
    else if (other) changed.push(fileName(other[1].trim()))
  }
  return { added, changed }
}

function many(count: number, one: string, more: string): string {
  return count === 1 ? one : `${count} ${more}`
}

export function describeToolStep({ name, server, args: rawArgs, result }: StepInput): StepDescription {
  const args = parseArgs(rawArgs)
  const n = name.toLowerCase()
  const path = arg(args, 'file_path', 'notebook_path', 'path', 'target_file', 'filename')
  const file = path ? fileName(path) : ''

  switch (n) {
    case 'read': case 'read_file': case 'view': case 'notebookread': case 'cat':
      return file ? step('read', `Read ${file}`, `Reading ${file}`, [file]) : step('read', 'Read a file', 'Reading a file')
    case 'edit': case 'multiedit': case 'notebookedit': case 'edit_file': case 'str_replace': case 'str_replace_editor': case 'search_replace':
      return file ? step('edit', `Edited ${file}`, `Editing ${file}`, [file]) : step('edit', 'Edited a file', 'Editing a file')
    case 'write': case 'write_file': case 'create_file': {
      // Write also replaces existing files; the tool's own reply says which.
      const updated = /updated|overwr/i.test(result || '')
      if (!file) return step(updated ? 'edit' : 'create', updated ? 'Edited a file' : 'Created a file', 'Writing a file')
      return updated ? step('edit', `Edited ${file}`, `Writing ${file}`, [file]) : step('create', `Created ${file}`, `Writing ${file}`, [file])
    }
    case 'apply_patch': {
      // Keep the patch's line breaks: arg() folds whitespace.
      const patch = typeof args.input === 'string' ? args.input : typeof args.patch === 'string' ? args.patch : rawArgs || ''
      const { added, changed } = patchFiles(patch)
      const files = [...changed, ...added]
      if (!files.length) return step('edit', 'Edited files', 'Editing files')
      if (files.length === 1) return added.length ? step('create', `Created ${files[0]}`, `Creating ${files[0]}`, files) : step('edit', `Edited ${files[0]}`, `Editing ${files[0]}`, files)
      return step('edit', `Edited ${files.length} files`, `Editing ${files.length} files`, files)
    }
    case 'bash': case 'exec_command': case 'shell': case 'run_shell_command': case 'terminal': case 'run_terminal_cmd': case 'execute_shell_command': {
      const description = arg(args, 'description')
      if (description) return step('run', `Ran "${short(description)}"`, `Running "${short(description)}"`)
      const command = arg(args, 'command', 'cmd')
      return command ? step('run', `Ran ${short(command)}`, `Running ${short(command)}`) : step('run', 'Ran a command', 'Running a command')
    }
    case 'grep': case 'glob': case 'grep_search': case 'codebase_search': case 'file_search': case 'search_files': case 'find': case 'ls': case 'list_dir': case 'list_files': {
      const pattern = arg(args, 'pattern', 'query', 'regex', 'glob_pattern')
      if (pattern) return step('search', `Searched for "${short(pattern)}"`, `Searching for "${short(pattern)}"`)
      return file ? step('search', `Looked in ${file}`, `Looking in ${file}`) : step('search', 'Searched the files', 'Searching the files')
    }
    case 'websearch': case 'web_search': {
      const query = arg(args, 'query', 'q')
      return query ? step('web', `Searched the web for "${short(query)}"`, `Searching the web for "${short(query)}"`) : step('web', 'Searched the web', 'Searching the web')
    }
    case 'webfetch': case 'web_fetch': case 'fetch': {
      const url = arg(args, 'url')
      return url ? step('open', `Opened ${hostOf(url)}`, `Opening ${hostOf(url)}`) : step('open', 'Opened a web page', 'Opening a web page')
    }
    case 'task': case 'agent': case 'spawnagent': {
      const about = arg(args, 'description', 'name', 'prompt')
      return about ? step('helper', `Started helper "${short(about)}"`, `Helper working on "${short(about)}"`) : step('helper', 'Started a helper', 'Helper working')
    }
    case 'todowrite': case 'update_plan':
      return step('plan', 'Updated the plan', 'Updating the plan')
  }
  const label = humanize(name) || 'a tool'
  const where = server && !/^api[-_]bridge$/.test(server) ? ` in ${server}` : ''
  return step('other', `Used ${label}${where}`, `Using ${label}${where}`)
}

/** One line for a finished group of steps: what was read, changed and run. */
export function summarizeSteps(steps: StepDescription[]): string {
  const unique = (kind: StepKind) => {
    const of = steps.filter(item => item.kind === kind)
    // The same file edited twice counts once; steps without a file name count each.
    return new Set(of.flatMap(item => item.files)).size + of.filter(item => !item.files.length).length
  }
  const count = (kind: StepKind) => steps.filter(item => item.kind === kind).length
  const parts: string[] = []
  const read = unique('read'), edited = unique('edit'), created = unique('create')
  if (read) parts.push(`read ${many(read, '1 file', 'files')}`)
  if (count('search')) parts.push('searched the files')
  if (count('web') || count('open')) parts.push('searched the web')
  if (edited) parts.push(`edited ${many(edited, '1 file', 'files')}`)
  if (created) parts.push(`created ${many(created, '1 file', 'files')}`)
  if (count('helper')) parts.push(`used ${many(count('helper'), '1 helper', 'helpers')}`)
  if (count('run')) parts.push(`ran ${many(count('run'), '1 command', 'commands')}`)
  if (count('other')) parts.push(`used ${many(count('other'), '1 tool', 'tools')}`)
  if (!parts.length) parts.push(`${steps.length} ${steps.length === 1 ? 'step' : 'steps'}`)
  const line = parts.join(', ')
  return line.charAt(0).toUpperCase() + line.slice(1)
}

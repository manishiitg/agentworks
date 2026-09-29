// Which lines of the working file a unified diff (against HEAD) touches, for
// the editor's change gutter: green added, blue modified, red where lines
// were deleted. Line numbers are those of the current file.

export interface GitLineChanges {
  added: number[]
  modified: number[]
  /** The line a deletion sits above (deleted lines are no longer in the file). */
  deleted: number[]
}

export function parseGitDiffLines(diff: string): GitLineChanges {
  const changes: GitLineChanges = { added: [], modified: [], deleted: [] }
  let newLine = 0
  let inHunk = false
  let addRun: number[] = []
  let deletedCount = 0

  const flush = () => {
    if (addRun.length > 0 && deletedCount > 0) changes.modified.push(...addRun)
    else if (addRun.length > 0) changes.added.push(...addRun)
    else if (deletedCount > 0) changes.deleted.push(Math.max(1, newLine))
    addRun = []
    deletedCount = 0
  }

  for (const line of diff.split('\n')) {
    const hunk = /^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line)
    if (hunk) {
      flush()
      newLine = Number(hunk[1])
      inHunk = true
      continue
    }
    if (!inHunk || line.startsWith('\\')) continue
    if (line.startsWith('+')) {
      addRun.push(newLine++)
    } else if (line.startsWith('-')) {
      deletedCount++
    } else {
      flush()
      newLine++
    }
  }
  flush()
  return changes
}

/** Every line of a new (untracked) file counts as added. */
export function allLinesAdded(content: string): GitLineChanges {
  const count = content === '' ? 0 : content.replace(/\n$/, '').split('\n').length
  return { added: Array.from({ length: count }, (_, index) => index + 1), modified: [], deleted: [] }
}

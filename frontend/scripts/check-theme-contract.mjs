import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'
import ts from 'typescript'

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
// Authored documents, code palettes and game artwork deliberately own their look.
// This is a styling audit, never an authority or content-security boundary.
export function themeIsland(file) {
  return /(?:^|\/)(?:appearance|themes|reportWidgets)\//.test(file) || /(?:Renderer|ChatCards|ChatWidgets|ChatVisual|ChatDiagram|ChatProcessDiagram|ChatDeveloperWidgets|DiffCode|SyntaxHighlightedCode|PlainMarkdown)\.(?:ts|tsx)$/.test(file) || /\/TerminalCenter\.tsx$/.test(file)
}
const rawNeutral = /(?:^|[\s:!'"`])(?:bg|text|border|ring|divide|outline|fill|stroke|placeholder)-(?:gray|slate|zinc|neutral|stone)(?:-\d{2,3})(?:\/\d+)?\b|(?:^|[\s:!'"`])(?:bg|text|border|ring|divide|outline|fill|stroke|placeholder)-(?:white|black)(?:\/\d+)?\b|(?:^|[\s:!'"`])(?:bg|text|border|ring|divide|outline|fill|stroke|placeholder)-\[#[\da-fA-F]{3,8}\]/g
export function themeViolations(source, file, changedLines) {
  if (themeIsland(file)) return []
  const tree = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, file.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
  const errors = []
  const visit = node => {
    if (ts.isStringLiteralLike(node) || ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) {
      const text = node.getText(tree)
      for (const match of text.matchAll(rawNeutral)) {
        const line = tree.getLineAndCharacterOfPosition(node.getStart(tree) + match.index).line + 1
        if (!changedLines || changedLines.has(line)) errors.push({ file, line, token: match[0].trim().replace(/^["'`]/, '') })
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(tree)
  return errors
}
export function changedLineRanges(diff) {
  const files = new Map(); let file, line = 0
  for (const text of diff.split('\n')) {
    if (text.startsWith('+++ b/')) { file = text.slice(6); files.set(file, new Set()); continue }
    const hunk = /^@@ .* \+(\d+)(?:,\d+)? @@/.exec(text)
    if (hunk) { line = Number(hunk[1]); continue }
    if (text.startsWith('+') && file) { files.get(file).add(line++); continue }
    if (!text.startsWith('-') && !text.startsWith('\\')) line++
  }
  return files
}
function walk(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap(entry => entry.isDirectory() ? walk(path.join(dir,entry.name)) : [path.join(dir,entry.name)])
}
export function checkThemeContract() {
  const errors = []
  // Always enforce the shared-control library, even for release source without git.
  for (const file of walk(path.join(frontend,'src/components/ui'))) {
    if (!/\.(?:ts|tsx)$/.test(file) || /\.(?:test|spec)\./.test(file)) continue
    const name = path.relative(frontend,file).replaceAll(path.sep,'/')
    errors.push(...themeViolations(fs.readFileSync(file,'utf8'),name))
  }
  // No legacy baseline/count: new app code must use the same semantic contract.
  let diff
  try {
    const base = execFileSync('git',['merge-base',process.env.THEME_CONTRACT_BASE || 'origin/dev','HEAD'],{cwd:frontend,encoding:'utf8',stdio:['ignore','pipe','ignore']}).trim()
    diff = execFileSync('git',['diff','--no-ext-diff','--unified=0',base,'HEAD','--','frontend/src'],{cwd:path.dirname(frontend),encoding:'utf8',maxBuffer:16*1024*1024})
  } catch { console.log('Theme audit: git change-level audit unavailable; shared controls are checked.') }
  if (diff) for (const [file,lines] of changedLineRanges(diff)) {
    if (!/\.(?:ts|tsx)$/.test(file) || /\.(?:test|spec)\./.test(file) || file.includes('/components/ui/')) continue
    const full = path.resolve(path.dirname(frontend),file)
    if (fs.existsSync(full)) errors.push(...themeViolations(fs.readFileSync(full,'utf8'),file,lines))
  }
  if (errors.length) {
    for (const error of errors) console.error(`${error.file}:${error.line}: ${error.token} — use semantic theme roles (frontend/THEMING.md).`)
    throw new Error(`Theme contract: ${errors.length} hard-coded styles found.`)
  }
  console.log('Theme contract: shared controls and changed app styles use semantic roles.')
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { checkThemeContract() } catch (error) { console.error(error.message); process.exitCode=1 }
}

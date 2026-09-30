import {
  File, FileArchive, FileAudio, FileCode, FileImage, FileJson, FileSpreadsheet, FileTerminal, FileText, FileVideo, Folder, FolderOpen,
  type LucideIcon,
} from 'lucide-react'

type FileIconSpec = { icon: LucideIcon; className: string }

const CODE = { icon: FileCode, className: 'text-sky-500' }
const BY_EXTENSION: Record<string, FileIconSpec> = {
  ts: CODE, tsx: CODE, js: CODE, jsx: CODE, mjs: CODE, cjs: CODE, py: { icon: FileCode, className: 'text-yellow-500' },
  go: { icon: FileCode, className: 'text-cyan-500' }, rs: { icon: FileCode, className: 'text-orange-500' },
  java: CODE, kt: CODE, rb: { icon: FileCode, className: 'text-red-500' }, php: CODE, c: CODE, h: CODE, cpp: CODE, cs: CODE, swift: CODE,
  html: { icon: FileCode, className: 'text-orange-500' }, css: { icon: FileCode, className: 'text-blue-500' }, scss: CODE, vue: CODE, svelte: CODE,
  sql: { icon: FileCode, className: 'text-violet-500' },
  json: { icon: FileJson, className: 'text-amber-500' }, jsonl: { icon: FileJson, className: 'text-amber-500' },
  yaml: { icon: FileJson, className: 'text-rose-400' }, yml: { icon: FileJson, className: 'text-rose-400' }, toml: { icon: FileJson, className: 'text-rose-400' },
  lock: { icon: FileJson, className: 'text-muted-foreground' },
  md: { icon: FileText, className: 'text-blue-400' }, markdown: { icon: FileText, className: 'text-blue-400' }, txt: { icon: FileText, className: 'text-muted-foreground' },
  pdf: { icon: FileText, className: 'text-red-500' }, docx: { icon: FileText, className: 'text-blue-600' },
  csv: { icon: FileSpreadsheet, className: 'text-emerald-500' }, xls: { icon: FileSpreadsheet, className: 'text-emerald-600' }, xlsx: { icon: FileSpreadsheet, className: 'text-emerald-600' },
  png: { icon: FileImage, className: 'text-purple-500' }, jpg: { icon: FileImage, className: 'text-purple-500' }, jpeg: { icon: FileImage, className: 'text-purple-500' },
  gif: { icon: FileImage, className: 'text-purple-500' }, webp: { icon: FileImage, className: 'text-purple-500' }, svg: { icon: FileImage, className: 'text-amber-500' },
  mp4: { icon: FileVideo, className: 'text-pink-500' }, webm: { icon: FileVideo, className: 'text-pink-500' }, mov: { icon: FileVideo, className: 'text-pink-500' },
  mp3: { icon: FileAudio, className: 'text-pink-400' }, wav: { icon: FileAudio, className: 'text-pink-400' }, m4a: { icon: FileAudio, className: 'text-pink-400' },
  zip: { icon: FileArchive, className: 'text-amber-600' }, gz: { icon: FileArchive, className: 'text-amber-600' }, tar: { icon: FileArchive, className: 'text-amber-600' },
  sh: { icon: FileTerminal, className: 'text-emerald-500' }, bash: { icon: FileTerminal, className: 'text-emerald-500' }, zsh: { icon: FileTerminal, className: 'text-emerald-500' },
}

/** VS Code-style icon for a tree row: open/closed folder, or the file's type. */
export function FileTypeIcon({ name, folder, open, image }: { name: string; folder?: boolean; open?: boolean; image?: boolean }) {
  if (folder) {
    const Icon = open ? FolderOpen : Folder
    return <Icon aria-hidden="true" className="h-4 w-4 shrink-0 text-amber-500/90" />
  }
  const ext = name.includes('.') ? name.split('.').pop()!.toLowerCase() : ''
  const spec = BY_EXTENSION[ext] ?? (image ? BY_EXTENSION.png : { icon: File, className: 'text-muted-foreground' })
  const Icon = spec.icon
  return <Icon aria-hidden="true" className={`h-4 w-4 shrink-0 ${spec.className}`} />
}

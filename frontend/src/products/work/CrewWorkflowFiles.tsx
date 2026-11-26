import { useEffect, useState } from 'react'
import { FileCode2 } from 'lucide-react'
import { crewCodeFilesApi, type CrewCodeFile } from '../../api/crewCodeFiles'
import { PageCard, PageRow, RowButton } from '../../components/automation/AutomationKit'
import { taskFileName } from './CrewWorkflowTasks'

const MAIN_FILES: Record<string, string> = {
  'main.py': 'Starts the workflow and picks which task to run.',
  'script.json': 'Its name, description, inputs and saved settings.',
}

const sizeLabel = (file: CrewCodeFile) => `${file.lines} line${file.lines === 1 ? '' : 's'}`

/**
 * The Files tab: everything in the workflow's code folder, in three groups. The main files, then one file per task
 * (named after the task), then any other file. Open shows the file in the project's Files viewer (#4151).
 */
export function CrewWorkflowFiles({ workspacePath, tasks, descriptions = {}, onOpenFile }: {
  workspacePath: string
  tasks: string[]
  /** One plain line per task, keyed by task name. */
  descriptions?: Record<string, string>
  onOpenFile: (file: string) => void
}) {
  const [files, setFiles] = useState<CrewCodeFile[] | null>(() => crewCodeFilesApi.peek(workspacePath) ?? null)
  useEffect(() => {
    let current = true
    crewCodeFilesApi.get(workspacePath).then(list => { if (current) setFiles(list) }).catch(() => { if (current) setFiles([]) })
    return () => { current = false }
  }, [workspacePath])
  if (files === null) return <PageCard title="Files" testId="crew-workflow-files"><p className="px-4 py-3 text-sm text-muted-foreground">Loading files…</p></PageCard>
  const byName = new Map(files.map(file => [file.name, file]))
  const main = Object.keys(MAIN_FILES).filter(name => byName.has(name))
  const taskRows = tasks.map(task => ({ task, file: byName.get(taskFileName(task)) })).filter((row): row is { task: string; file: CrewCodeFile } => !!row.file)
  const used = new Set([...main, ...taskRows.map(row => row.file.name)])
  const other = files.filter(file => !used.has(file.name))
  const open = (file: string) => <RowButton title={`Open ${file}`} onClick={() => onOpenFile(file)}>Open</RowButton>
  const lead = <span className="grid h-8 w-8 place-items-center rounded-lg bg-muted"><FileCode2 aria-hidden="true" className="h-4 w-4" /></span>
  if (!files.length) return <PageCard title="Files" testId="crew-workflow-files"><p className="px-4 py-3 text-sm text-muted-foreground">No files found in this workflow's code folder.</p></PageCard>
  return <div className="space-y-4" data-testid="crew-workflow-files">
    <p className="px-1 text-xs text-muted-foreground" data-testid="crew-workflow-files-count">{files.length} file{files.length === 1 ? '' : 's'} in this workflow's code folder.</p>
    {main.length > 0 && <PageCard title="Main files" hint="What starts the workflow and holds its settings.">
      {main.map(name => <PageRow key={name} testId={`file-${name}`} lead={lead} title={name} lines={[MAIN_FILES[name], sizeLabel(byName.get(name)!)]} action={open(name)} />)}
    </PageCard>}
    {taskRows.length > 0 && <PageCard title="Task files" hint="One file for each task.">
      {taskRows.map(({ task, file }) => <PageRow key={file.name} testId={`file-${file.name}`} lead={lead} title={file.name} lines={[`Task: ${task}`, descriptions[task], sizeLabel(file)]} action={open(file.name)} />)}
    </PageCard>}
    {other.length > 0 && <PageCard title="Other files" hint="Helpers and anything else the workflow keeps.">
      {other.map(file => <PageRow key={file.name} testId={`file-${file.name}`} lead={lead} title={file.name} lines={[sizeLabel(file)]} action={open(file.name)} />)}
    </PageCard>}
  </div>
}

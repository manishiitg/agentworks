import React, { useRef, useEffect, useCallback, useState } from 'react'
import Editor from '@monaco-editor/react'
import type { OnChange, OnMount } from '@monaco-editor/react'
import type { editor } from 'monaco-editor'
import { useTheme } from '../../hooks/useTheme'
import type { GitLineChanges } from '../../utils/gitDiffLines'

let pythonFormattingProviderRegistered = false

const normalizeLineEndings = (value: string): string => value.replace(/\r\n?/g, '\n')

const formatPythonCode = (value: string): string => {
  const normalized = normalizeLineEndings(value)
  const lines = normalized.split('\n')
  const formatted: string[] = []
  let previousLineBlank = false

  for (const line of lines) {
    const indentationMatch = line.match(/^\s*/)
    const rawIndentation = indentationMatch?.[0] || ''
    const indentation = rawIndentation.replace(/\t/g, '    ')
    const content = line.slice(rawIndentation.length).replace(/[ \t]+$/g, '')
    const nextLine = `${indentation}${content}`
    const isBlank = nextLine.trim().length === 0

    if (isBlank) {
      if (!previousLineBlank) {
        formatted.push('')
      }
      previousLineBlank = true
      continue
    }

    formatted.push(nextLine)
    previousLineBlank = false
  }

  return formatted.join('\n').replace(/\n{3,}/g, '\n\n').trimEnd()
}

interface FileEditorProps {
  value: string
  filepath: string
  /** Read-only unless an editing surface turns it off. */
  readOnly?: boolean
  onChange?: (value: string) => void
  height?: string
  onMount?: (editor: editor.IStandaloneCodeEditor) => void
  /** Git change markers for the gutter (green added, blue modified, red deleted). */
  lineChanges?: GitLineChanges
}

export const FileEditor: React.FC<FileEditorProps> = ({
  value,
  filepath,
  readOnly = true,
  onChange,
  height = '100%',
  onMount,
  lineChanges,
}) => {
  const editorRef = useRef<editor.IStandaloneCodeEditor | null>(null)
  const decorationsRef = useRef<editor.IEditorDecorationsCollection | null>(null)
  const [editorReady, setEditorReady] = useState(false)
  const { theme } = useTheme()

  // Detect language from file extension
  const getLanguage = (path: string): string => {
    const ext = path.toLowerCase().split('.').pop() || ''
    const languageMap: Record<string, string> = {
      'json': 'json',
      'md': 'markdown',
      'markdown': 'markdown',
      'js': 'javascript',
      'jsx': 'javascript',
      'ts': 'typescript',
      'tsx': 'typescript',
      'py': 'python',
      'go': 'go',
      'java': 'java',
      'c': 'c',
      'cpp': 'cpp',
      'cs': 'csharp',
      'php': 'php',
      'rb': 'ruby',
      'sql': 'sql',
      'html': 'html',
      'css': 'css',
      'yaml': 'yaml',
      'yml': 'yaml',
      'xml': 'xml',
      'sh': 'shell',
      'bash': 'shell',
      'zsh': 'shell',
      'txt': 'plaintext'
    }
    return languageMap[ext] || 'plaintext'
  }

  const language = getLanguage(filepath)

  // Map theme to Monaco theme
  const getMonacoTheme = (theme: string): string => {
    if (theme === 'dark' || theme === 'dark-plus') {
      return 'vs-dark'
    }
    return 'vs'
  }

  const handleEditorDidMount: OnMount = (editor, monaco) => {
    editorRef.current = editor
    setEditorReady(true)

    if (!pythonFormattingProviderRegistered) {
      monaco.languages.registerDocumentFormattingEditProvider('python', {
        provideDocumentFormattingEdits(model) {
          const original = model.getValue()
          const formatted = formatPythonCode(original)

          if (formatted === original) {
            return []
          }

          return [{
            range: model.getFullModelRange(),
            text: formatted,
          }]
        },
      })

      pythonFormattingProviderRegistered = true
    }

    // Configure editor options
    editor.updateOptions({
      fontSize: 14,
      minimap: { enabled: true },
      wordWrap: 'on',
      lineNumbers: 'on',
      folding: true,
      scrollBeyondLastLine: false,
      automaticLayout: true,
      formatOnPaste: false,
      formatOnType: false
    })

    if (onMount) {
      onMount(editor)
    }
  }

  // Git change markers in the gutter.
  useEffect(() => {
    const instance = editorRef.current
    if (!instance || !editorReady) return
    const decorate = (lines: number[], className: string) => lines.map(line => ({
      range: { startLineNumber: line, startColumn: 1, endLineNumber: line, endColumn: 1 },
      options: { isWholeLine: true, linesDecorationsClassName: className },
    }))
    const next = lineChanges
      ? [
        ...decorate(lineChanges.added, 'git-gutter-added'),
        ...decorate(lineChanges.modified, 'git-gutter-modified'),
        ...decorate(lineChanges.deleted, 'git-gutter-deleted'),
      ]
      : []
    if (decorationsRef.current) decorationsRef.current.set(next)
    else decorationsRef.current = instance.createDecorationsCollection(next)
  }, [lineChanges, editorReady])

  const handleChange: OnChange = (next) => {
    if (onChange && next !== undefined) onChange(next)
  }

  // Expose a best-effort formatter to parent consumers.
  const formatDocument = useCallback(() => {
    if (!editorRef.current) {
      return
    }

    const currentValue = editorRef.current.getValue()

    if (language === 'json') {
      try {
        const parsed = JSON.parse(currentValue)
        editorRef.current.setValue(JSON.stringify(parsed, null, 2))
      } catch (error) {
        console.error('Failed to format JSON:', error)
        return
      }
    }

    if (language === 'python') {
      editorRef.current.setValue(formatPythonCode(currentValue))
    }

    editorRef.current.getAction('editor.action.formatDocument')?.run()
  }, [language])

  // Expose format helpers via the editor instance for existing consumers.
  useEffect(() => {
    if (editorRef.current) {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      ;(editorRef.current as any).formatJSON = formatDocument
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      ;(editorRef.current as any).formatDocument = formatDocument
    }
  }, [formatDocument])

  return (
    <div className="h-full w-full">
      <Editor
        height={height}
        language={language}
        value={value}
        onChange={handleChange}
        theme={getMonacoTheme(theme)}
        onMount={handleEditorDidMount}
        options={{
          readOnly,
          minimap: { enabled: true },
          wordWrap: 'on',
          lineNumbers: 'on',
          folding: true,
          scrollBeyondLastLine: false,
          automaticLayout: true,
          formatOnPaste: false,
          formatOnType: false,
          fontSize: 14,
          fontFamily: 'Menlo, Monaco, "Courier New", monospace',
          tabSize: 2,
          insertSpaces: true,
          detectIndentation: false,
          guides: {
            indentation: true,
          },
          bracketPairColorization: {
            enabled: true,
          }
        }}
      />
    </div>
  )
}

export default FileEditor

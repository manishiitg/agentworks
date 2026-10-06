import { useState, useRef } from 'react'
import { Loader2, CheckCircle, AlertCircle, Download, ExternalLink, Upload, FileArchive } from 'lucide-react'
import { skillsApi } from '../../api/skills'
import type { ValidateSkillResponse } from '../../types/skills'

interface SkillImportDialogProps {
  workspacePath: string
  onClose: () => void
  onSuccess: () => void
}

type ImportMethod = 'github' | 'zip'

export default function SkillImportDialog({ workspacePath, onClose, onSuccess }: SkillImportDialogProps) {
  const [importMethod, setImportMethod] = useState<ImportMethod>('github')
  const [url, setUrl] = useState('')
  const [isPrivate, setIsPrivate] = useState(false)
  const [pat, setPat] = useState('')
  const [selectedFile, setSelectedFile] = useState<File | null>(null)
  const [isValidating, setIsValidating] = useState(false)
  const [isImporting, setIsImporting] = useState(false)
  const [validationResult, setValidationResult] = useState<ValidateSkillResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const handleMethodChange = (method: ImportMethod) => {
    setImportMethod(method)
    setValidationResult(null)
    setError(null)
  }

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (file) {
      if (!file.name.toLowerCase().endsWith('.zip')) {
        setError('Please select a .zip file')
        setSelectedFile(null)
        return
      }
      setSelectedFile(file)
      setValidationResult(null)
      setError(null)
    }
  }

  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    const file = e.dataTransfer.files?.[0]
    if (file) {
      if (!file.name.toLowerCase().endsWith('.zip')) {
        setError('Please select a .zip file')
        setSelectedFile(null)
        return
      }
      setSelectedFile(file)
      setValidationResult(null)
      setError(null)
    }
  }

  const handleDragOver = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault()
  }

  const handleValidate = async () => {
    if (importMethod === 'github') {
      if (!url.trim()) {
        setError('Please enter a GitHub URL')
        return
      }
    } else {
      if (!selectedFile) {
        setError('Please select a zip file')
        return
      }
    }

    setIsValidating(true)
    setError(null)
    setValidationResult(null)

    try {
      const result = importMethod === 'github'
        ? await skillsApi.validateSkill({ github_url: url.trim(), ...(pat ? { github_token: pat } : {}) }, workspacePath)
        : await skillsApi.validateSkillZip(selectedFile!, workspacePath)
      setValidationResult(result)
      if (!result.valid) {
        setError(result.error || 'Invalid skill')
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Validation failed')
    } finally {
      setIsValidating(false)
    }
  }

  const handleImport = async () => {
    if (importMethod === 'github') {
      if (!url.trim()) {
        setError('Please enter a GitHub URL')
        return
      }
    } else {
      if (!selectedFile) {
        setError('Please select a zip file')
        return
      }
    }

    setIsImporting(true)
    setError(null)

    try {
      const result = importMethod === 'github'
        ? await skillsApi.importSkill({ github_url: url.trim(), ...(pat ? { github_token: pat } : {}) }, workspacePath)
        : await skillsApi.importSkillZip(selectedFile!, workspacePath)
      if (result.success) {
        onSuccess()
      } else {
        setError(result.error || 'Import failed')
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Import failed')
    } finally {
      setIsImporting(false)
    }
  }

  const canValidate = importMethod === 'github' ? url.trim() !== '' : selectedFile !== null

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50 p-4">
      <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg p-6 shadow-xl w-full max-w-lg">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
            Import Skill
          </h3>
          <button
            onClick={onClose}
            className="text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
          >
            ✕
          </button>
        </div>

        <div className="space-y-4">
          {/* Tab Buttons */}
          <div className="flex border-b border-gray-200 dark:border-gray-700">
            <button
              onClick={() => handleMethodChange('github')}
              className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
                importMethod === 'github'
                  ? 'border-primary text-primary'
                  : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'
              }`}
            >
              GitHub URL
            </button>
            <button
              onClick={() => handleMethodChange('zip')}
              className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
                importMethod === 'zip'
                  ? 'border-primary text-primary'
                  : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'
              }`}
            >
              Upload Zip
            </button>
          </div>

          {/* GitHub URL Input */}
          {importMethod === 'github' && (
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
                GitHub Folder URL
              </label>
              <input
                type="text"
                value={url}
                onChange={(e) => {
                  setUrl(e.target.value)
                  setValidationResult(null)
                  setError(null)
                }}
                placeholder="https://github.com/user/repo/tree/main/skills/my-skill"
                className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-md bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100 placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-primary focus:border-transparent"
              />
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                Paste a URL to a skill folder containing SKILL.md
              </p>

              <label className="flex items-center gap-2 mt-3 cursor-pointer">
                <input
                  type="checkbox"
                  checked={isPrivate}
                  onChange={(e) => {
                    setIsPrivate(e.target.checked)
                    if (!e.target.checked) setPat('')
                  }}
                  className="w-4 h-4 rounded border-gray-300 dark:border-gray-600 text-primary focus:ring-primary"
                />
                <span className="text-sm text-gray-700 dark:text-gray-300">Private repository</span>
              </label>

              {isPrivate && (
                <div className="mt-2">
                  <input
                    type="password"
                    value={pat}
                    onChange={(e) => setPat(e.target.value)}
                    placeholder="ghp_..."
                    className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-md bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100 placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-primary focus:border-transparent"
                  />
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    Personal Access Token with repo scope. Your token is only used for this request and is not stored anywhere.
                  </p>
                </div>
              )}
            </div>
          )}

          {/* Zip Upload */}
          {importMethod === 'zip' && (
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
                Zip File
              </label>
              <div
                onDrop={handleDrop}
                onDragOver={handleDragOver}
                onClick={() => fileInputRef.current?.click()}
                className={`border-2 border-dashed rounded-lg p-6 text-center cursor-pointer transition-colors ${
                  selectedFile
                    ? 'border-primary bg-primary/10'
                    : 'border-gray-300 dark:border-gray-600 hover:border-primary/60'
                }`}
              >
                <input
                  ref={fileInputRef}
                  type="file"
                  accept=".zip"
                  onChange={handleFileSelect}
                  className="hidden"
                />
                {selectedFile ? (
                  <div className="flex items-center justify-center gap-2">
                    <FileArchive className="w-6 h-6 text-muted-foreground" />
                    <span className="text-sm text-gray-900 dark:text-gray-100 font-medium">
                      {selectedFile.name}
                    </span>
                  </div>
                ) : (
                  <>
                    <Upload className="w-8 h-8 mx-auto text-gray-400 dark:text-gray-500 mb-2" />
                    <p className="text-sm text-gray-600 dark:text-gray-400">
                      Drop a .zip file here or click to browse
                    </p>
                  </>
                )}
              </div>
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                Zip file should contain SKILL.md at root or in a single folder
              </p>
            </div>
          )}

          {/* Error Message */}
          {error && (
            <div className="flex items-center gap-2 p-3 bg-red-50 dark:bg-red-900/30 border border-red-200 dark:border-red-800 rounded-md">
              <AlertCircle className="w-5 h-5 text-red-500 flex-shrink-0" />
              <span className="text-sm text-red-700 dark:text-red-300">{error}</span>
            </div>
          )}

          {/* Validation Result */}
          {validationResult && validationResult.valid && (
            <div className="p-3 bg-green-50 dark:bg-green-900/30 border border-green-200 dark:border-green-800 rounded-md space-y-2">
              <div className="flex items-center gap-2">
                <CheckCircle className="w-5 h-5 text-green-500" />
                <span className="text-sm font-medium text-green-700 dark:text-green-300">
                  Valid skill found
                </span>
              </div>

              {validationResult.frontmatter && (
                <div className="space-y-1 pl-7">
                  <p className="text-sm text-green-800 dark:text-green-200">
                    <span className="font-medium">Name:</span> {validationResult.frontmatter.name}
                  </p>
                  <p className="text-sm text-green-800 dark:text-green-200">
                    <span className="font-medium">Description:</span> {validationResult.frontmatter.description}
                  </p>
                  {validationResult.frontmatter.allowed_tools && validationResult.frontmatter.allowed_tools.length > 0 && (
                    <p className="text-sm text-green-800 dark:text-green-200">
                      <span className="font-medium">Tools:</span> {validationResult.frontmatter.allowed_tools.join(', ')}
                    </p>
                  )}
                </div>
              )}

              {validationResult.files && validationResult.files.length > 0 && (
                <div className="pl-7">
                  <p className="text-xs text-green-600 dark:text-green-400">
                    Files: {validationResult.files.join(', ')}
                  </p>
                </div>
              )}

              {validationResult.exists && (
                <div className="flex items-center gap-2 p-2 bg-amber-50 dark:bg-amber-900/30 border border-amber-200 dark:border-amber-800 rounded-md">
                  <AlertCircle className="w-4 h-4 text-amber-500 flex-shrink-0" />
                  <span className="text-sm text-amber-700 dark:text-amber-300">
                    A skill with this name already exists. Importing will overwrite it.
                  </span>
                </div>
              )}
            </div>
          )}

          {/* Actions */}
          <div className="flex items-center justify-between pt-4 border-t border-gray-200 dark:border-gray-700">
            <a
              href="https://skills.sh"
              target="_blank"
              rel="noopener noreferrer"
              className="text-sm text-primary hover:underline flex items-center gap-1"
            >
              <ExternalLink className="w-3 h-3" />
              Browse skills on skills.sh
            </a>

            <div className="flex items-center gap-2">
              <button
                onClick={handleValidate}
                disabled={isValidating || !canValidate}
                className="px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300 bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 dark:hover:bg-gray-600 rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2"
              >
                {isValidating ? (
                  <>
                    <Loader2 className="w-4 h-4 animate-spin" />
                    Validating...
                  </>
                ) : (
                  'Validate'
                )}
              </button>

              <button
                onClick={handleImport}
                disabled={isImporting || !validationResult?.valid}
                className="px-4 py-2 text-sm font-medium text-primary-foreground bg-primary hover:bg-primary/90 rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2"
              >
                {isImporting ? (
                  <>
                    <Loader2 className="w-4 h-4 animate-spin" />
                    Importing...
                  </>
                ) : (
                  <>
                    <Download className="w-4 h-4" />
                    Import
                  </>
                )}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

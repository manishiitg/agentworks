import { useMemo, useState } from 'react'
import * as XLSX from 'xlsx'

interface XlsxRendererProps {
  data: ArrayBuffer
}

export function XlsxRenderer({ data }: XlsxRendererProps) {
  const [page, setPage] = useState(0)
  const [activeSheet, setActiveSheet] = useState(0)

  const workbook = useMemo(() => {
    try {
      return XLSX.read(data, { type: 'array', sheetRows: 10001 })
    } catch (err) {
      console.error('Failed to parse XLSX:', err)
      return null
    }
  }, [data])

  const sheetData = useMemo(() => {
    if (!workbook) return []
    const sheetName = workbook.SheetNames[activeSheet]
    if (!sheetName) return []
    const sheet = workbook.Sheets[sheetName]
    // header: 1 returns array of arrays
    return XLSX.utils.sheet_to_json<string[]>(sheet, { header: 1, defval: '' })
  }, [workbook, activeSheet])

  if (!workbook) {
    return (
      <p className="text-red-500 dark:text-red-400 text-sm">
        Failed to parse XLSX file. The file may be corrupted.
      </p>
    )
  }

  const headers = sheetData[0] || []
  const rows = sheetData.slice(1)

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-400">
          <span className="font-medium">XLSX Spreadsheet</span>
          <span className="text-xs bg-emerald-100 dark:bg-emerald-900 text-emerald-800 dark:text-emerald-200 px-2 py-0.5 rounded">
            {rows.length} preview rows (up to 10,000 per sheet)
          </span>
          <span className="text-xs bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-300 px-2 py-0.5 rounded">
            {headers.length} columns
          </span>
        </div>
      </div>

      {/* Sheet tabs */}
      {workbook.SheetNames.length > 1 && (
        <div className="flex gap-1 border-b border-gray-200 dark:border-gray-700">
          {workbook.SheetNames.map((name, i) => (
            <button
              key={name}
              onClick={() => { setActiveSheet(i); setPage(0) }}
              className={`px-3 py-1.5 text-xs rounded-t border border-b-0 transition-colors ${
                i === activeSheet
                  ? 'bg-white dark:bg-gray-900 text-blue-600 dark:text-blue-400 border-gray-200 dark:border-gray-700 font-medium'
                  : 'bg-gray-50 dark:bg-gray-800 text-gray-500 dark:text-gray-400 border-transparent hover:bg-gray-100 dark:hover:bg-gray-700'
              }`}
            >
              {name}
            </button>
          ))}
        </div>
      )}

      <div className="flex items-center gap-3 text-xs"><button disabled={!page} onClick={() => setPage(page - 1)}>Previous</button><span>Page {page + 1} of {Math.max(1, Math.ceil(rows.length / 200))}</span><button disabled={(page + 1) * 200 >= rows.length} onClick={() => setPage(page + 1)}>Next</button></div>
      <div className="overflow-x-auto border border-gray-200 dark:border-gray-700 rounded-lg">
        <table className="min-w-full text-xs">
          <thead>
            <tr>
              {headers.map((header, i) => (
                <th
                  key={i}
                  className="sticky top-0 px-3 py-2 text-left font-semibold text-gray-700 dark:text-gray-200 bg-gray-100 dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700 whitespace-nowrap"
                >
                  {String(header)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.slice(page * 200, (page + 1) * 200).map((row, ri) => (
              <tr
                key={ri}
                className={`${
                  ri % 2 === 0
                    ? 'bg-white dark:bg-gray-900'
                    : 'bg-gray-50 dark:bg-gray-850'
                } hover:bg-blue-50 dark:hover:bg-gray-800`}
              >
                {headers.map((_, ci) => (
                  <td
                    key={ci}
                    className="px-3 py-1.5 text-gray-700 dark:text-gray-300 border-b border-gray-100 dark:border-gray-800 whitespace-nowrap max-w-xs truncate"
                    title={String(row[ci] ?? '')}
                  >
                    {String(row[ci] ?? '')}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
        {rows.length === 0 && (
          <p className="text-center text-gray-500 dark:text-gray-400 text-xs py-4">
            This sheet is empty.
          </p>
        )}
      </div>
    </div>
  )
}

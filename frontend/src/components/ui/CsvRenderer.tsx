import { useMemo, useState } from 'react'
import Papa from 'papaparse'

interface CsvRendererProps {
  content: string
}

export function CsvRenderer({ content }: CsvRendererProps) {
  const [page, setPage] = useState(0)
  const [sortColumn, setSortColumn] = useState<number | null>(null)
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('asc')
  const [filter, setFilter] = useState('')

  const parsed = useMemo(() => {
    const result = Papa.parse<string[]>(content, {
      skipEmptyLines: true,
    })
    return result.data
  }, [content])

  const headers = parsed[0] || []
  const rows = parsed.slice(1)

  const filteredRows = useMemo(() => {
    if (!filter) return rows
    const lowerFilter = filter.toLowerCase()
    return rows.filter(row =>
      row.some(cell => cell?.toLowerCase().includes(lowerFilter))
    )
  }, [rows, filter])

  const sortedRows = useMemo(() => {
    if (sortColumn === null) return filteredRows
    return [...filteredRows].sort((a, b) => {
      const aVal = a[sortColumn] || ''
      const bVal = b[sortColumn] || ''
      // Try numeric sort first
      const aNum = Number(aVal)
      const bNum = Number(bVal)
      if (!isNaN(aNum) && !isNaN(bNum)) {
        return sortDirection === 'asc' ? aNum - bNum : bNum - aNum
      }
      return sortDirection === 'asc'
        ? aVal.localeCompare(bVal)
        : bVal.localeCompare(aVal)
    })
  }, [filteredRows, sortColumn, sortDirection])

  const handleSort = (colIndex: number) => {
    if (sortColumn === colIndex) {
      setSortDirection(d => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortColumn(colIndex)
      setSortDirection('asc')
    }
  }

  if (!parsed.length) {
    return <p className="text-gray-500 dark:text-gray-400 text-sm">Empty CSV file</p>
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-400">
          <span className="font-medium">CSV File</span>
          <span className="text-xs bg-emerald-100 dark:bg-emerald-900 text-emerald-800 dark:text-emerald-200 px-2 py-0.5 rounded">
            {rows.length} rows
          </span>
          <span className="text-xs bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-300 px-2 py-0.5 rounded">
            {headers.length} columns
          </span>
        </div>
        <input
          type="text"
          placeholder="Filter rows..."
          value={filter}
          onChange={e => { setFilter(e.target.value); setPage(0) }}
          className="text-xs px-2 py-1 rounded border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-800 dark:text-gray-200 focus:outline-none focus:ring-1 focus:ring-blue-500 w-48"
        />
      </div>
      <div className="flex items-center gap-3 text-xs"><button disabled={!page} onClick={() => setPage(page - 1)}>Previous</button><span>Page {page + 1} of {Math.max(1, Math.ceil(sortedRows.length / 200))}</span><button disabled={(page + 1) * 200 >= sortedRows.length} onClick={() => setPage(page + 1)}>Next</button></div>
      <div className="overflow-x-auto border border-gray-200 dark:border-gray-700 rounded-lg">
        <table className="min-w-full text-xs">
          <thead>
            <tr>
              {headers.map((header, i) => (
                <th
                  key={i}
                  onClick={() => handleSort(i)}
                  className="sticky top-0 px-3 py-2 text-left font-semibold text-gray-700 dark:text-gray-200 bg-gray-100 dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700 cursor-pointer hover:bg-gray-200 dark:hover:bg-gray-700 select-none whitespace-nowrap"
                >
                  {header}
                  {sortColumn === i && (
                    <span className="ml-1">{sortDirection === 'asc' ? '▲' : '▼'}</span>
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sortedRows.slice(page * 200, (page + 1) * 200).map((row, ri) => (
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
                    title={row[ci] || ''}
                  >
                    {row[ci] || ''}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
        {filter && sortedRows.length === 0 && (
          <p className="text-center text-gray-500 dark:text-gray-400 text-xs py-4">
            No rows match the filter.
          </p>
        )}
      </div>
    </div>
  )
}

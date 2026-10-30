import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { LogsHeader } from './LogsHeader'

describe('LogsHeader', () => {
  it('renders the run picker below the header buttons', () => {
    const html = renderToStaticMarkup(
      <LogsHeader
        runFolderOptions={['run-1', 'run-2']}
        runFolderInfos={[]}
        selectedRunFolder="run-1"
        setSelectedRunFolder={() => {}}
        loading={false}
        loadLogs={() => {}}
      />,
    )
    const refreshIndex = html.indexOf('aria-label="Refresh logs and run-folder list"')
    const pickerIndex = html.indexOf('aria-label="Execution run"')
    expect(refreshIndex).toBeGreaterThanOrEqual(0)
    expect(pickerIndex).toBeGreaterThan(refreshIndex)
  })
})

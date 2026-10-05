import React from 'react'
import { createRoot } from 'react-dom/client'
import { ChromeExtensionConnection } from '../../src/components/workflow/ChromeExtensionConnection'
import '../../src/index.css'
createRoot(document.getElementById('root')!).render(<div style={{maxWidth: 640, margin: '20px auto', border: '1px solid #ddd'}}><ChromeExtensionConnection workspacePath="Workflow/CRM" onSelectionChange={() => {}} /></div>)

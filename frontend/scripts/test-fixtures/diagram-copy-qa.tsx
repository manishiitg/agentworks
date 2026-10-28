import React from 'react'
import { createRoot } from 'react-dom/client'
import '../../src/index.css'
import { ChatDiagram } from '../../src/components/ui/ChatDiagram'

if (new URLSearchParams(location.search).has('light')) document.documentElement.classList.remove('dark')
const nodes = Array.from({ length: 8 }, (_, index) => ({ id: `step-${index}`, title: index ? `Step ${index + 1}` : 'Café — योजना', description: 'A complete diagram keeps the last step, even below the scroll area.', emphasis: index === 3 }))
createRoot(document.getElementById('root')!).render(<main className="mx-auto max-w-3xl bg-background p-3 text-foreground"><ChatDiagram spec={{ type: 'diagram', title: 'Copy a complete diagram', nodes, edges: nodes.slice(1).map((node, index) => ({ from: nodes[index].id, to: node.id, label: index === 2 ? 'Continue' : undefined })) }} /></main>)

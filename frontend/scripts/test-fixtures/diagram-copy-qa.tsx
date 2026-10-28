import React from 'react'
import { createRoot } from 'react-dom/client'
import '../../src/index.css'
import { ChatElement } from '../../src/components/ui/ChatElement'
import { parseChatElement } from '../../src/components/ui/chatElementSpec'

const params = new URLSearchParams(location.search)
if (params.has('light')) document.documentElement.classList.remove('dark')
const actors = [{ id: 'person', title: 'Person' }, { id: 'agent', title: 'AI teammate' }, { id: 'app', title: 'Connected app' }]
const nodes = Array.from({ length: 8 }, (_, index) => ({ id: `step-${index}`, title: index ? `Step ${index + 1}` : 'Café — योजना', description: 'A complete diagram keeps the last step, even below the scroll area.', emphasis: index === 3 }))
const type = params.get('type') || 'diagram'
const source = type === 'swimlane' ? { type, title: 'Who does each step', lanes: actors, steps: nodes.map((node, index) => ({ ...node, lane: actors[index % actors.length].id })) }
  : type === 'sequence' ? { type, title: 'Questions, work and replies', participants: actors, messages: Array.from({ length: 12 }, (_, index) => ({ from: actors[index % actors.length].id, to: actors[index % 4 === 3 ? index % actors.length : (index + 1) % actors.length].id, label: index === 11 ? 'Final reply — योजना' : `Message ${index + 1}: read the relevant records and return the result`, reply: index % 2 === 1 })) }
    : { type, title: 'Copy a complete diagram', nodes, edges: nodes.slice(1).map((node, index) => ({ from: nodes[index].id, to: node.id, label: index === 2 ? 'Continue' : undefined })) }
const spec = parseChatElement(JSON.stringify(source))!
createRoot(document.getElementById('root')!).render(<main className="mx-auto max-w-3xl bg-background p-3 text-foreground"><ChatElement spec={spec} renderMarkdown={body => body} /></main>)

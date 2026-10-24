import React, { useState } from 'react'
import { createRoot } from 'react-dom/client'
import '../../src/index.css'
import { ChatTabGroups } from '../../src/components/chat/ChatTabGroups'
import { ChatTabPill } from '../../src/components/chat/ChatTabPill'
import type { ChatTab } from '../../src/stores/useChatStore'
import type { ProductSurface } from '../../src/products/productSurfaceConfig'

document.documentElement.classList.toggle('dark', new URLSearchParams(location.search).get('theme') !== 'light')
const names = ['Builder', 'notion updates', 'general bugs', 'coach', 'ab-testing-system', 'Testing', 'Builder']
const tabs = names.map((name, index) => ({
  tabId: String(index), name, config: {}, createdAt: index,
  metadata: { agentProfileTabOwnerID: index < 5 ? 'mine' : 'other', agentProfileTabOwnerName: index < 5 ? 'my.account@example.com' : 'alex.taylor@example.com', agentProfileTabCanWrite: index < 5 },
})) as ChatTab[]

function Strip({ surface, label }: { surface: ProductSurface; label: string }) {
  const [active, setActive] = useState('4')
  return <section data-surface={surface} className="min-w-0 py-4">
    <h2 className="mb-2 text-xs text-muted-foreground">{label}</h2>
    <div className="flex min-w-0 items-center gap-1 border-b border-border">
      <div data-owner-strip className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
        <ChatTabGroups tabs={tabs}>{tab => <ChatTabPill tab={tab} isActive={active === tab.tabId} isBlank={false} canClose={false} productSurface={tab.tabId === '0' ? surface : undefined} onTabClick={setActive} onCloseTab={() => {}} />}</ChatTabGroups>
      </div>
    </div>
  </section>
}
createRoot(document.getElementById('root')!).render(<main className="min-h-screen bg-background px-4 py-6 text-foreground">
  <Strip surface="work" label="Crew" /><Strip surface="agentworks" label="Goal" /><Strip surface="relays" label="Relay" />
</main>)

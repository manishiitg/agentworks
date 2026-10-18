import { useId, useState } from 'react'
import { ArrowRight, BarChart3, Check, Eye, FileInput, MessageCircle, Presentation, Users } from 'lucide-react'
import { CartesianGrid, Line, LineChart, ResponsiveContainer, XAxis } from 'recharts'

// This example is deliberately local: no project queries, model calls or actions.
const exampleSeries = [
  { day: 'Mon', value: 4 }, { day: 'Tue', value: 7 }, { day: 'Wed', value: 6 },
  { day: 'Thu', value: 11 }, { day: 'Fri', value: 15 },
]
const examples = [
  { id: 'data', label: 'Live data', icon: BarChart3 },
  { id: 'actions', label: 'Forms & actions', icon: FileInput },
  { id: 'stories', label: 'Stories & media', icon: Presentation },
] as const
const story = [
  { title: 'What changed this week?', body: 'The sample team completed more work as the week progressed.', detail: '4 on Monday → 15 on Friday' },
  { title: 'Explore the detail', body: 'Bring charts, research, documents and explanations into the same view.', detail: 'A briefing your team can explore' },
  { title: 'Tell the story your way', body: 'Your dashboard can include interactive presentations, animation and video.', detail: 'Choose the style in chat' },
]

export function DashboardStarterPreview() {
  const [example, setExample] = useState<typeof examples[number]['id']>('data')
  const [period, setPeriod] = useState<'all' | 'recent'>('all')
  const [topic, setTopic] = useState('Weekly team report')
  const [previewedTopic, setPreviewedTopic] = useState('')
  const [slide, setSlide] = useState(0)
  const topicId = useId()
  const panelId = useId()
  const currentStory = story[slide]
  return <section aria-label="Try dashboard examples" className="mb-7 overflow-hidden rounded-xl border border-border bg-muted/15 text-left">
    <div className="flex flex-wrap items-center justify-between gap-1 border-b border-border px-4 py-2.5 text-[10px] text-muted-foreground">
      <span>Explore what you can build</span><span>Demo · sample data</span>
    </div>
    <div role="group" aria-label="Dashboard examples" className="flex gap-1 border-b border-border p-1.5">
      {examples.map(({ id, label, icon: Icon }) => <button key={id} type="button" aria-pressed={example === id} aria-controls={panelId} onClick={() => setExample(id)} className={`flex min-w-0 flex-1 items-center justify-center gap-1.5 rounded-md px-1 py-2 text-[11px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${example === id ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
        <Icon aria-hidden="true" className="hidden h-3.5 w-3.5 shrink-0 sm:block" />{label}
      </button>)}
    </div>
    <div id={panelId} className="min-h-[218px] p-4">
      {example === 'data' && <>
        <div className="flex items-center justify-between gap-2">
          <p className="text-xs font-medium">Work completed</p>
          <select aria-label="Example chart period" value={period} onChange={event => setPeriod(event.target.value as 'all' | 'recent')} className="rounded border border-border bg-background px-2 py-1 text-[10px] text-muted-foreground">
            <option value="all">This week</option><option value="recent">Last 3 days</option>
          </select>
        </div>
        <div className="mt-2 h-28 w-full" role="img" aria-label={period === 'all' ? 'Sample chart: work completed rises from 4 on Monday to 15 on Friday' : 'Sample chart: work completed rises from 6 on Wednesday to 15 on Friday'}>
          <ResponsiveContainer width="100%" height="100%" minWidth={0}>
            <LineChart data={period === 'all' ? exampleSeries : exampleSeries.slice(-3)} margin={{ top: 10, left: 8, right: 12, bottom: 0 }} accessibilityLayer={false}>
              <CartesianGrid vertical={false} stroke="hsl(var(--border))" />
              <XAxis dataKey="day" tick={{ fill: 'hsl(var(--muted-foreground))', fontSize: 10 }} axisLine={false} tickLine={false} interval={0} padding={{ left: 12, right: 12 }} />
              <Line dataKey="value" type="monotone" stroke="hsl(var(--primary))" strokeWidth={2} dot={{ r: 3 }} isAnimationActive={false} />
            </LineChart>
          </ResponsiveContainer>
        </div>
        <p className="mt-3 text-[11px] leading-5 text-muted-foreground">Filter live charts, explore records and ask your agent about any value.</p>
      </>}
      {example === 'actions' && <form onSubmit={event => { event.preventDefault(); setPreviewedTopic(topic.trim()) }}>
        <label htmlFor={topicId} className="text-xs font-medium">Report to prepare</label>
        <div className="mt-2 flex gap-2">
          <input id={topicId} value={topic} onChange={event => { setTopic(event.target.value); setPreviewedTopic('') }} maxLength={80} className="min-w-0 flex-1 rounded-md border border-border bg-background px-2.5 py-2 text-xs" />
          <button type="submit" disabled={!topic.trim()} className="shrink-0 rounded-md border border-primary/30 bg-primary/10 px-3 py-2 text-xs text-foreground hover:bg-primary/20 disabled:opacity-50">Try demo</button>
        </div>
        <div aria-live="polite" className="mt-3 min-h-12 rounded-md bg-background/60 px-3 py-2.5 text-[11px] leading-5 text-muted-foreground">
          {previewedTopic ? <><Check aria-hidden="true" className="mr-1 inline h-3.5 w-3.5 text-primary" />Sample result: “{previewedTopic}” is ready to review.</> : 'Try a form without running an agent or changing project data.'}
        </div>
        <p className="mt-3 text-[11px] leading-5 text-muted-foreground">Collect inputs, edit records and run your published functions from buttons.</p>
      </form>}
      {example === 'stories' && <>
        <div className="flex items-center gap-2 text-[10px] text-muted-foreground"><Presentation aria-hidden="true" className="h-3.5 w-3.5" />Example briefing · {slide + 1} of {story.length}</div>
        <h3 className="mt-3 text-base font-semibold">{currentStory.title}</h3>
        <p className="mt-2 text-xs leading-5 text-muted-foreground">{currentStory.body}</p>
        <div className="mt-4 flex items-center justify-between gap-2 border-t border-border pt-3">
          <span className="text-[10px] text-muted-foreground">{currentStory.detail}</span>
          <button type="button" onClick={() => setSlide((slide + 1) % story.length)} aria-label={slide === story.length - 1 ? 'Restart example briefing' : 'Next example slide'} className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><ArrowRight aria-hidden="true" className="h-4 w-4" /></button>
        </div>
      </>}
    </div>
    <div className="space-y-3 border-t border-border px-4 py-3">
      <div className="flex items-center gap-2 text-[10px] text-muted-foreground"><MessageCircle aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />Add agent chat inside your dashboard.</div>
      <div className="flex items-start gap-2">
        <Users aria-hidden="true" className="mt-0.5 h-3.5 w-3.5 shrink-0 text-primary" />
        <div>
          <p className="text-[11px] font-medium text-foreground">Share with your team</p>
          <p className="mt-1 text-[10px] leading-4 text-muted-foreground"><Eye aria-hidden="true" className="mr-1 inline h-3 w-3" />Dashboard + read access to project files and results.</p>
          <p className="mt-0.5 text-[10px] leading-4 text-muted-foreground">Project editing stays with its owners and editors.</p>
        </div>
      </div>
    </div>
  </section>
}

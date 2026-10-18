import type { ReactNode } from 'react'

function StarterGuide({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return <div className="flex h-full min-h-0 items-center justify-center overflow-y-auto px-6 py-8">
    <div className="w-full max-w-lg text-center">
      {children}
      <h2 className="text-2xl font-semibold tracking-tight text-foreground">{title}</h2>
      <p className="mx-auto mt-3 max-w-sm text-sm leading-6 text-muted-foreground">{description}</p>
    </div>
  </div>
}

export function CrewStarterGuide() {
  return <StarterGuide title="Build an agent for your team" description="Add memory, skills and functions. Share your agent with your team and schedule its work.">
    <figure className="mx-auto mb-7 w-full max-w-sm">
      <svg viewBox="0 0 420 156" className="h-auto w-full" role="img" aria-label="Build an agent with memory, skills and functions, then share it with your team and schedule its work. Example: a shared reporting agent runs every Monday.">
        <g fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" className="text-muted-foreground">
          <path d="M131 65h27m-5-4 5 4-5 4M265 65h27m-5-4 5 4-5 4" strokeOpacity=".4" />
        </g>
        <g fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className="text-primary">
          <circle cx="74" cy="51" r="15" className="fill-primary/5" />
          <path d="M48 96v-5a26 26 0 0 1 52 0v5" />
          <rect x="176" y="33" width="48" height="61" rx="5" className="fill-primary/5" />
          <path d="M187 47h26m-26 9h26m-26 9h17" strokeOpacity=".5" />
          <rect x="204" y="71" width="39" height="28" rx="5" className="fill-background" />
          <path d="m216 79-5 6 5 6m15-12 5 6-5 6m-7-13-3 14" />
          <circle cx="334" cy="49" r="12" className="fill-primary/5" />
          <circle cx="359" cy="52" r="10" strokeOpacity=".6" />
          <path d="M313 93v-9a21 21 0 0 1 42 0v9m2-24a17 17 0 0 1 19 17" />
          <circle cx="367" cy="93" r="15" className="fill-background" />
          <path d="M367 84v9l6 4" />
        </g>
        <g fill="currentColor" textAnchor="middle" fontSize="12" className="text-muted-foreground">
          <text x="74" y="139">Your agent</text><text x="210" y="139" className="fill-foreground">Skills &amp; functions</text><text x="343" y="139">Share &amp; schedule</text>
        </g>
      </svg>
      <figcaption className="mt-1 text-[11px] text-muted-foreground/70">Example: a reporting agent your team can use, running every Monday.</figcaption>
    </figure>
  </StarterGuide>
}

export function GoalStarterGuide() {
  return <StarterGuide title="What do you want to achieve?" description="Set a measurable outcome. Your agents work, check progress and improve the plan.">
    <figure className="mx-auto mb-7 w-full max-w-sm">
      <svg viewBox="0 0 420 156" className="h-auto w-full" role="img" aria-label="Example: set a target of 20 qualified leads per week; agents work and review results repeatedly while tracking progress toward the target">
        <g fill="none" stroke="currentColor" strokeWidth="1.5" className="text-muted-foreground">
          <rect x="32" y="28" width="82" height="75" rx="6" className="fill-muted/40" />
          <path d="M126 65h26m-5-4 5 4-5 4M268 65h26m-5-4 5 4-5 4" strokeOpacity=".4" strokeLinecap="round" strokeLinejoin="round" />
        </g>
        <g fill="currentColor" textAnchor="middle" className="text-muted-foreground">
          <text x="73" y="65" fontSize="24" className="fill-foreground">20</text><text x="73" y="84" fontSize="9">leads / week</text>
        </g>
        <g fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className="text-primary">
          <path d="M186 38a35 35 0 0 1 54 26l-6-6m6 6 5-7M234 92a35 35 0 0 1-54-26l6 6m-6-6-5 7" strokeOpacity=".7" />
          <rect x="196" y="48" width="28" height="35" rx="4" className="fill-primary/5" />
          <path d="m202 59 3 3 5-6m-8 16h16" />
          <path d="M310 31v69h77" strokeOpacity=".4" />
          <path d="M316 40h68" strokeDasharray="3 4" strokeOpacity=".5" />
          <path d="m319 88 16-9 14 3 16-19 17-9" strokeWidth="2" />
          <circle cx="382" cy="54" r="3" className="fill-primary" />
        </g>
        <g fill="currentColor" textAnchor="middle" fontSize="12" className="text-muted-foreground">
          <text x="73" y="139">Your target</text><text x="210" y="139" className="fill-foreground">Work & review</text><text x="349" y="139">Track progress</text>
        </g>
      </svg>
      <figcaption className="mt-1 text-[11px] text-muted-foreground/70">Example: find qualified leads and improve the plan each week.</figcaption>
    </figure>
  </StarterGuide>
}

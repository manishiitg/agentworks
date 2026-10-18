/** A concrete example, rather than a diagram of the project's current Flow. */
export function FlowStarterIllustration() {
  return (
    <figure className="mx-auto mb-7 w-full max-w-sm">
      <svg viewBox="0 0 420 156" className="h-auto w-full" role="img" aria-label="Example: a Flow reads an invoice and adds its details as a new spreadsheet row">
        {/* An incoming invoice becomes a structured spreadsheet row. */}
        <g className="text-muted-foreground" fill="none" stroke="currentColor" strokeWidth="1.5">
          <path d="M46 18h44l16 16v72a6 6 0 0 1-6 6H46a6 6 0 0 1-6-6V24a6 6 0 0 1 6-6Z" className="fill-muted/40" />
          <path d="M90 18v12a4 4 0 0 0 4 4h12" opacity=".5" />
          <path d="M52 56h42M52 65h29M52 83h42" strokeLinecap="round" opacity=".45" />
        </g>
        <g fill="currentColor" className="text-muted-foreground">
          <text x="52" y="44" fontSize="8" letterSpacing="1">INVOICE</text>
          <text x="52" y="98" fontSize="10">$240.00</text>
        </g>
        {/* One continuous path through a few steps. */}
        <g fill="none" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          <path d="M120 65h34m-5-4 5 4-5 4M266 65h34m-5-4 5 4-5 4" className="stroke-muted-foreground/40" />
          <path d="M178 65h21m22 0h21" className="stroke-primary/60" />
          <g className="fill-background stroke-primary/70">
            <rect x="158" y="54" width="22" height="22" rx="7" />
            <rect x="199" y="54" width="22" height="22" rx="7" />
            <rect x="240" y="54" width="22" height="22" rx="7" />
          </g>
          <g className="fill-primary" stroke="none">
            <circle cx="169" cy="65" r="2.5" />
            <circle cx="210" cy="65" r="2.5" />
            <circle cx="251" cy="65" r="2.5" />
          </g>
        </g>
        <g fill="none" stroke="currentColor" strokeWidth="1.5" className="text-primary">
          <rect x="309" y="31" width="80" height="68" rx="6" className="fill-primary/5" strokeOpacity=".5" />
          <path d="M310 49h78M310 65h78M334 32v66M361 32v66" strokeOpacity=".35" />
          <rect x="310" y="66" width="78" height="16" className="fill-primary/15" stroke="none" />
          <g strokeLinecap="round" strokeLinejoin="round">
            <path d="M316 40h10M341 40h12M368 40h12M316 57h10M341 57h12M368 57h12M316 74h10M341 74h12M368 74h12" strokeOpacity=".6" />
          </g>
          <circle cx="383" cy="96" r="10" className="fill-background" strokeOpacity=".7" />
          <path d="m379 96 3 3 5-6" strokeLinecap="round" strokeLinejoin="round" />
        </g>
        <g fill="currentColor" textAnchor="middle" fontSize="12" className="text-muted-foreground">
          <text x="73" y="139">New invoice</text>
          <text x="210" y="139" className="fill-foreground">Your Flow</text>
          <text x="349" y="139">Spreadsheet updated</text>
        </g>
      </svg>
      <figcaption className="mt-1 text-[11px] text-muted-foreground/70">Example: read each invoice and add its details to a spreadsheet.</figcaption>
    </figure>
  )
}

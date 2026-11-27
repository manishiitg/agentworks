// Shared by the current Crew and the other expanded Crews: same density,
// with larger touch targets in the phone drawer. Indented with a subtle
// vertical guide line so threads visually nest under their project row
// even when a project has only a single Main thread (#4436).
export const sidebarAgentListClass = 'mb-2 ml-3.5 space-y-px border-l border-border pb-1 pl-2'
export const sidebarAgentRowClass = 'flex h-[30px] w-full min-w-0 items-center rounded-md pl-2 pr-7 text-left text-[12.5px] leading-5 max-md:min-h-10'
// A group is a small heading inside its project, smaller than the agent rows
// (#3369). Its own size: the row's text-[13px] would win over an appended one.
export const sidebarGroupHeaderClass = 'flex h-6 w-full min-w-0 items-center gap-1.5 rounded-md pl-2 pr-7 text-left text-[11px] font-medium uppercase leading-4 tracking-wide text-muted-foreground/80 hover:bg-muted hover:text-foreground max-md:min-h-10'
export const sidebarAgentDisclosureClass = 'flex h-7 w-full items-center gap-1.5 rounded-md px-2 text-left text-xs text-muted-foreground/80 hover:bg-muted hover:text-foreground max-md:min-h-10'

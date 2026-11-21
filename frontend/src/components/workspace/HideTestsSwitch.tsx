/** "Hide tests" on the Changes tab: hides test files from the list (utils/hideTests). */
export function HideTestsSwitch({ hide, onChange }: { hide: boolean; onChange: (hide: boolean) => void }) {
  return <button type="button" role="switch" aria-checked={hide} onClick={() => onChange(!hide)} title="Hide files named like tests (login.test.ts, login_test.go)"
    className="inline-flex items-center gap-1.5 rounded px-1 py-0.5 text-xs text-muted-foreground hover:text-foreground">
    <span aria-hidden="true" className={`relative h-3.5 w-6 rounded-full transition-colors ${hide ? 'bg-primary' : 'bg-muted-foreground/40'}`}>
      <span className={`absolute top-0.5 h-2.5 w-2.5 rounded-full bg-background transition-all ${hide ? 'left-3' : 'left-0.5'}`} />
    </span>
    Hide tests
  </button>
}

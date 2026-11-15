export function themeIsland(file: string): boolean
export function themeViolations(source: string, file: string, changedLines?: Set<number>): {file: string;line: number;token: string}[]
export function changedLineRanges(diff: string): Map<string, Set<number>>
export function checkThemeContract(): void

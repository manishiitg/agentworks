/** Browser hover text uses only purpose metadata already admitted for this row. */
export function agentPurposeTooltip(row: {purpose?: string; examples?: string[]}, detail?: string): string {
 return [row.purpose?.trim(), row.examples?.length ? `Ask me to:\n${row.examples.map(example=>`• ${example}`).join('\n')}` : '', detail].filter(Boolean).join('\n\n')
}

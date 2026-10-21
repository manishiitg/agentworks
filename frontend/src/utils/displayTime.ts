let configuredDisplayTimeZone: string | undefined

export const setDisplayTimeZone = (zone?: string | null): void => {
  configuredDisplayTimeZone = zone?.trim() || undefined
}

export const getDisplayTimeZone = (): string | undefined => {
  const zone = configuredDisplayTimeZone
  if (!zone) return undefined
  try {
    new Intl.DateTimeFormat(undefined, { timeZone: zone }).format()
    return zone
  } catch {
    return undefined
  }
}

export const formatDeploymentDateTime = (value?: string | number | Date | null): string => {
  if (value === undefined || value === null || value === '') return ''
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString(undefined, {
    timeZone: getDisplayTimeZone(),
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    timeZoneName: getDisplayTimeZone() ? 'short' : undefined,
  })
}

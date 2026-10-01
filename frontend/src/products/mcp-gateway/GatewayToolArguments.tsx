/** Raw JSON schema display shared by server and group tool cards. */
export function GatewayToolArguments({ schema, rawSchema }: { schema?: string | null; rawSchema?: Record<string, unknown> }) {
  let schemaText = 'Schema not provided.'
  if (rawSchema) schemaText = JSON.stringify(rawSchema, null, 2)
  else if (schema) {
    try { schemaText = JSON.stringify(JSON.parse(atob(schema)), null, 2) }
    catch { schemaText = 'Schema could not be displayed.' }
  }
  return <pre className="w-full max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted/30 p-3 font-mono text-xs" aria-label="Input JSON schema">{schemaText}</pre>
}

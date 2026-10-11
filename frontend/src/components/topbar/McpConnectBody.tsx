import { CliMcpSetupPanel } from '../integrations/CliMcpSetupPanel'

/**
 * How a local or hosted AI agent connects to this server over MCP. For admins
 * and Code reviewers it also explains the code:review tools, which are used
 * from that agent, outside Code.
 */
export default function McpConnectBody({ codeReview }: { codeReview: boolean }) {
  return (
    <div className="space-y-4">
      {codeReview && (
        <div className="rounded-md border border-border bg-muted/40 p-3 text-sm text-muted-foreground">
          <p className="font-medium text-foreground">Code review</p>
          <p className="mt-1">
            The approval screen lists <b>Review every Code workspace</b> for your account. Once you allow it, your agent can
            then list every Code workspace, see its cost by person, and read its chats and files, read-only. Every call is
            recorded in the Code review audit log.
          </p>
        </div>
      )}
      <div className="rounded-md border border-border bg-muted/40 p-3 text-sm text-muted-foreground">
        <p className="font-medium text-foreground">Crew authoring access</p>
        <p className="mt-1">Existing connections with permission to edit your Crews can also run sandboxed commands,
          control their browser, change their database and use their connected tools. Your current role, Crew ownership
          and connection limits apply. Calls appear in the owner-only Crew execution history. Revoke a connection here
          if you no longer want it to have that access.</p>
      </div>
      <div className="rounded-md border border-border bg-muted/40 p-3 text-sm text-muted-foreground">
        <p className="font-medium text-foreground">Builder authoring access</p>
        <p className="mt-1">Existing connections approved for Builder chat can also edit workflow plans directly,
          without a model turn. Current owner, editor and team rules, connection limits, native validation, revisions
          and the plan changelog apply. Revoke a connection here if you no longer want it to have that access.</p>
      </div>
      <CliMcpSetupPanel />
    </div>
  )
}

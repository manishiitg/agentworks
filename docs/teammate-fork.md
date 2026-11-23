# Copy a teammate's agent to your Mac

Choose **Fork to my Mac…** beside an agent in the Team monitor, or in the header
while viewing that teammate's chat. The app creates a new private agent in your
own local copy of the same Crew. It uses your own AI account. The original is
unchanged; project files, skills, sign-ins, schedules and audience are not copied.

The server checks the current Team membership, audience and chat visibility at
creation, download and completion. **Hidden chats cannot be forked**, even if the
agent is listed and can answer questions. The action remains visible and explains
this refusal. Leaving the Team, narrowing the audience, hiding the chat or rebinding
the source invalidates an unfinished recipient-initiated copy.

Copying uses the latest complete, validated source upload and reports its saved
time. It never reads live from the maker's computer, the virtual responder's chat,
or another teammate's question thread. An offline maker can therefore be copied
only when that source upload is ready. Mac app conversations use their portable
source dialogue. Claude/Codex terminal sources also retain their independent native
context, with the existing bounded recent dialogue window for display and for
continuing in a different program. Server app sources are not supported by this
operation.

After a refusal, **Request a copy via my agent…** opens a draft in your own chat; it sends no request, grants no access and creates no offer. Your agent can coordinate with the maker using the existing Needs-you/AgentBridge flow. Only the maker's own authenticated agent or app may create an offer.

If no complete source is available, ask its maker to use **Send a copy to a
teammate…**. That existing maker-initiated flow still requires the recipient to
accept; it does not delete the original.

Agents on the recipient's Mac use the same handler through `team_sync` action
`fork_from` (`workspace team_fork_from` over MCP), naming the teammate's exact
`agent_key` from Team agents. A cloud-only caller is directed to the person's
connected Mac. Current Crew token scopes still apply. Source export is logged
and shown in Team Activity.

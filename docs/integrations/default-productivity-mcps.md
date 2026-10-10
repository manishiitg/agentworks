# Default productivity MCP connections

The shared local catalog and the server A/server C release catalogs include the
following direct-provider integrations. Catalog presence makes a connection
available to configure; it does not authorize an account or select it for every
workflow. Existing user overlays remain separate.

| Service | Provider endpoint | Setup documentation |
| --- | --- | --- |
| Notion | `https://mcp.notion.com/mcp` | [Notion](https://developers.notion.com/guides/mcp/overview) |
| Canva | `https://mcp.canva.com/mcp` | [Canva](https://www.canva.dev/docs/mcp/) |
| Airtable | `https://mcp.airtable.com/mcp` | [Airtable](https://support.airtable.com/articles/9897799762-using-the-airtable-mcp-server) |
| Todoist | `https://ai.todoist.net/mcp` | [Todoist](https://developer.todoist.com/api/v1/) |
| Asana | `https://mcp.asana.com/v2/mcp` | [Asana](https://developers.asana.com/docs/using-asanas-mcp-server) |
| ClickUp | `https://mcp.clickup.com/mcp` | [ClickUp](https://developer.clickup.com/docs/connect-an-ai-assistant-to-clickups-mcp-server) |
| Atlassian | `https://mcp.atlassian.com/v2/mcp` | [Atlassian](https://developer.atlassian.com/cloud/rovo-mcp/guides/getting-started/) |
| Dropbox | `https://mcp.dropbox.com/mcp` | [Dropbox](https://help.dropbox.com/integrations/connect-dropbox-mcp-server) |
| Miro | `https://mcp.miro.com/` | [Miro](https://developers.miro.com/docs/connecting-to-miro-mcp) |
| Figma | `https://mcp.figma.com/mcp` | [Figma](https://developers.figma.com/docs/figma-mcp-server/remote-server-installation/) |

## Verification and sign-in

On 2026-09-11, the seven newly included server entries (Todoist through Figma)
were checked against provider documentation and public OAuth protected-resource
and authorization-server metadata. The shared catalog's older Asana, Atlassian
and Miro URLs were updated. Authorization, token and registration URLs come from
that public metadata; no customer account was connected during verification.

Asana v2 publishes no dynamic-registration endpoint: it requires a separately
registered OAuth app and provider-specific setup. Do not restore its obsolete
`/register` URL or claim it is a one-click connection. Other entries publish
registration metadata, but successful user consent/tool execution has not been
verified here. Dropbox's documentation also describes app prerequisites; follow
the provider's current setup requirements if dynamic registration is unavailable.

The connector UI already has descriptions and categories for these services.
`TestConsumerCatalogsStayInSync` validates the shipped JSON using the production
config loader and checks that new release entries match the shared catalog.
Adding more integrations should repeat provider verification and add them to the
release catalogs deliberately, rather than copying every public directory hit.

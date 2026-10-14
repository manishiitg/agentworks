# Channel connectors

`agent_go/cmd/server/services/BotConnector` is the shared transport interface. The session manager and event filter reuse conversation lifecycle, completion events, progress, user-input handling, and errors. Each connector owns its SDK, credentials, incoming event decoding, and formatter.

Implement `Capabilities() ChannelCapabilities` explicitly. Zero values disable features:

| Capability | Shared behavior |
| --- | --- |
| Threads | Read and continue channel threads |
| MessageEdits | Update an existing reply or progress message |
| StreamingReplies | Stream assistant text; also requires MessageEdits |
| Reactions | Add/remove accepted and processing indicators |
| MessageDeletion | Remove temporary progress; implement DeleteMessage too |
| ProgressUpdates | Send one temporary status message; requires edits or deletion |
| WorkflowProgress | Permit workflow progress when the route requests full details |

Slack enables all features. WhatsApp enables WorkflowProgress, while leaving periodic status messages and in-place streaming disabled. Declare only features actually implemented by the connector. Translate common reaction names into the platform's supported emoji representation inside the adapter.

Progress is cleared through the existing completion signal, cancellation, and blocking user-input events. If deletion fails, an editable message becomes a terminal status. No transport-specific completion detector is needed.

To add Discord or Telegram:

1. Implement BotConnector and the channel formatter; declare capabilities.
2. Normalize incoming events into BotIncomingMessage and ThreadID, and register the connector with the existing manager.
3. Integrate that channel's authenticated route/settings adapter. Capabilities describe presentation, not authorization; preserve target-scoped Run grants and account/route checks.
4. Wire any builder tools and guidance through the existing product.yaml policies.
5. Test actual transport behavior and authorization. The capability tests exercise Discord, Telegram, and an arbitrary channel name using mock transports; they do not constitute live connectors.

`ChannelHistory` advertises an optional `ChannelHistoryReader` interface. The caller authorizes the source channel; the adapter enforces a bounded time/message/byte budget. Slack supports it. WhatsApp currently does not. History attachment JSON stays opaque to the shared interface.

A channel is a Share adapter: it reaches the product's `ask` or its functions through the same server path as every other channel, and only formats and delivers. See [Build, Share, Use](design/build_share_use.md).

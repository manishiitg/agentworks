# Models

Use the live `models` menu. A project has one place to choose a model: each chat (`scope=tab`, the same as the model menu in the message box). An empty engine uses the project's fallback: the owner's last-used model. When the project's owner picks a model for a chat, that also becomes the fallback for chats with no model of their own (schedules, agent messages, shared asks and the project's Users). A new chat starts on the model of the chat it was opened from. `scope=project` is for a workflow's models; for a project it only reads the fallback. Native chats bind their own project and tab; external MCP takes the existing project and chat IDs from discovery. Choose only an offered account, provider, model and reasoning level. List/catalog/capabilities/test actions appear only on surfaces that already offered them. A chat model change applies from the next message; do not claim this answer already uses it. Read back the saved choice.

For configuration details and runtime effects, Read [the detailed procedure](references/configuration.md).

A chat's context use, cost and plan use are in the Context section at the end of the right pane. An unknown figure stays hidden; never estimate one from the conversation length.
On a computer, project agents appear in the open sidebar; the header shows the current agent. Closing the sidebar shows all agent tabs in the header. There is no separate layout setting.

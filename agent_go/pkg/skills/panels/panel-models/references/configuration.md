# Model configuration

In Crew chat, use `manage_crew_model(action="list")` for the offered providers,
models, available personal/company accounts and this chat's saved choice. When the person asks, use `action="set"`
with an offered engine, model, reasoning level and `connection_id` for the chosen
personal or company account. Each owner chooses for their own named tab; this
does not lend their personal account to other owners or change Crew integrations. An empty engine returns this
chat to the Crew's default. The change applies from the next message, including
messages that reach this chat from another channel; it does not change other
chats or the Crew default. Do not claim the current reply already uses it.

For provider or model configuration exposed through tools, discover the admitted
configuration tools and load their schemas. Never read or edit raw `config/`
files for LLM/provider configuration. The configured provider/model is the
runtime's identity; model self-identification does not override it.

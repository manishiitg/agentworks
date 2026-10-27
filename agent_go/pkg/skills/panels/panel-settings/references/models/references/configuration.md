# Model configuration

In Crew chat, use `models(action="list", scope="tab")` for the offered providers,
models, available personal/company accounts and this chat's saved choice. When the person asks, use `models action=set scope=tab`
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

Project defaults use `models action=get/set scope=project`; send the `models` object with the requested changes. Existing Builder and account checks still apply. Use catalog/list only when those actions appear in this connection’s menu. `settings action=chat` changes the personal Context display only.

## Who can use a company AI account

An admin chooses **Everyone in the company**, **Only admins**, or **Chosen people** in AI models & accounts. There is one Save; only Chosen people opens the searchable people list. Admins always have access. This choice applies across products and does not share projects, personal accounts or credentials.

Over MCP, read `account action=availability account=<provider>` before changing it. Use `account action=set_availability account=<provider> available_to="all"`, `"admins"`, or `{"users":[person IDs]}` for the requested audience, then read it back. Get person IDs from `account action=users_list`; do not invent product groups. Both actions require an admin and `users:manage`, and use the same server checks as the screen. An installation-pinned choice must be changed by the installation administrator. Leave models, limits and saved logins unchanged unless asked.

A default model may use a restricted company account. People outside its audience must choose another available account or ask an admin to add them; never silently switch accounts or native login folders to bypass a refusal.

# Product links

Copy the address bar after opening the Crew or view you want to share. A link identifies the destination; the recipient still needs their own sign-in and access.

| Destination | Path |
| --- | --- |
| Crews / one Crew | `/crew`, `/crew/projects/<id>` |
| Brain / a folder | `/brain`, `/brain?folder=<relative-folder>` |
| Vault | `/vault` |
| LLM Gateway, where installed | `/llm-gateway` |
| AgentBridge / connection instructions | `/agentbridge`, `/agentbridge/connect` |

Workflows are inside their Crew. Open the workflow there and copy its actual address for Steps, Runs or a specific result. Use the same approach for dashboards and files instead of guessing an internal view or route name. Brain folder names are relative to Brain and encoded in the query, not server filesystem paths.

An explicit link selects its destination when the browser opens it. Sign-in retains the destination, and missing or inaccessible items remain unavailable under the backend's access rules. A copied dashboard, file or report URL does not change those rules.

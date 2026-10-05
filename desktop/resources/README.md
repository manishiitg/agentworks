# Server binaries for development

Place the following binaries here to run the Electron app locally (unpackaged):

- **agent-server** — built from `agent_go` (root has `server` subcommand):
  ```bash
  cd agent_go && GOOS=darwin GOARCH=arm64 go build -o ../desktop/resources/agent-server .
  ```
- **workspace-server** — built from `workspace` (rename the planner binary):
  ```bash
  cd workspace && GOOS=darwin GOARCH=arm64 go build -o ../desktop/resources/workspace-server .
  ```
- **vault-server** — the Vault gateway, built from `mcp-gateway` (run by default; `AGENTWORKS_LOCAL_VAULT=0` opts out):
  ```bash
  cd mcp-gateway && GOWORK=off go build -o ../desktop/resources/vault-server ./cmd/server
  ```

When packaged with electron-builder, these are copied from `resources/` into the app's `Contents/Resources/`.

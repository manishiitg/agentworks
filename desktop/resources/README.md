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

When packaged with electron-builder, these are copied from `resources/` into the app's `Contents/Resources/`.

The local app includes workflows, Crew and Code. Relays, Brain, Vault and LLM Gateway are server products. The DMG does not package or start a Vault sidecar.

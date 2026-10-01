# Local filesystem and memory MCPs

These are the official [filesystem](https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem) and [memory](https://github.com/modelcontextprotocol/servers/tree/main/src/memory) reference servers, not custom mock tools. Version `2026.8.31` is pinned for both packages. They need Node.js and no external account.

The small development bridge forwards their original tool schemas and calls from stdio to Streamable HTTP. It binds only to `127.0.0.1`; production gateway startup does not launch it.

## Start

From the `mcp-gateway` directory:

```sh
npm install --prefix /tmp/caplayer-reference-mcps/runtime --save-exact @modelcontextprotocol/server-filesystem@2026.8.31 @modelcontextprotocol/server-memory@2026.8.31
go run ./cmd/local-mcps -data-dir /tmp/caplayer-reference-mcps -runtime-dir /tmp/caplayer-reference-mcps/runtime
```

| Server | Local URL | Storage | Discovered tools |
| --- | --- | --- | --- |
| Filesystem | `http://127.0.0.1:18164/filesystem/mcp` | Only `/tmp/caplayer-reference-mcps/files` | 14 |
| Memory | `http://127.0.0.1:18164/memory/mcp` | `/tmp/caplayer-reference-mcps/memory.jsonl` | 9 |

The filesystem server creates a sample `README.md` once. Existing test files are preserved. Memory is the reference knowledge graph: entities, relations and observations. Both keep data across bridge restarts; gateway governance remains in memory and must be reconnected after a gateway restart.

## Connect to CapLayer

Start your loopback gateway with `GATEWAY_ALLOW_PRIVATE_UPSTREAMS=1` so it can dial these loopback HTTP endpoints. This setting applies only to the local test gateway, not the production configuration. Keep normal gateway authentication enabled.

Use MCP servers → Add custom servers with the two URLs above, or run:

```sh
python3 scripts/connect-local-mcps.py --gateway http://127.0.0.1:18163 --admin-token-file /tmp/caplayer-ui-review/admin-token
```

The helper reads the existing private token file, adds missing connectors and resyncs existing ones. It accepts only loopback origins. An admin connection approves the initial discovered tool definitions automatically. The helper does not publish policies, assign memberships or create group credentials. Existing connections are resynced; newly added or changed definitions then require review. The browser uses the normal product login at `http://127.0.0.1:18162/`.

## Easy tests

1. Expand each connected server and inspect tools/schemas. Initial definitions are already approved by the admin connection; assign selected tools to a group to grant access.
2. Use filesystem `list_directory` and `read_text_file` on the test folder/README. `write_file` and `edit_file` provide write tests.
3. Create a read-only group for those read tools; test that writes are denied. Add a `path` condition to further restrict a tool. File access is also bounded by the reference server's allowed directory.
4. Use memory `create_entities`, `add_observations`, `search_nodes` and `read_graph` to test separate read/write grants.
5. The README contains a synthetic email for regex PII tests. Observe allowed/denied calls in Audit when calling through the gateway.

The CapLayer setup assistant inspects schemas and drafts permissions; it does not itself run these upstream tools. Use a gateway MCP client to test the published policy.

## Verified locally

- Both reference servers initialized and their 23 tools appeared in the CapLayer connected-server UI.
- Filesystem MCP write/read round trip succeeded in the test directory.
- Reading a path outside that directory was denied by the reference server.
- Memory entity creation and search succeeded; the smoke-test entity was then removed.
- Re-running the connection helper resynced existing connectors without creating duplicates.

Repeat the opt-in test while the bridge is running:

```sh
CAPLAYER_LOCAL_REFERENCE_TEST_URL=http://127.0.0.1:18164 CAPLAYER_LOCAL_REFERENCE_TEST_DATA=/tmp/caplayer-reference-mcps go test ./cmd/local-mcps -v -count=1
```

## Local OAuth test

Use the official Memory MCP behind a synthetic loopback OAuth provider to test the existing product sign-in, dynamic client registration, PKCE, and refresh without a real account:

```sh
python3 scripts/local-oauth-memory.py \
  --source-config ../agent_go/configs/mcp_servers_clean.json \
  --output-config /tmp/caplayer-oauth-mcp.json
```

Keep the reference bridge running on port 18164. The fixture binds to `127.0.0.1:18165` and writes the copied catalog plus `Local OAuth Memory`; it does not modify the source config. Run the product API with `--mcp-config /tmp/caplayer-oauth-mcp.json`. Run the gateway with `GATEWAY_CATALOG_PATH=/tmp/caplayer-oauth-mcp.json`, `GATEWAY_PRODUCT_URL` set to the product origin, matching service credentials, and private upstreams enabled for local testing only.

In MCP servers, find Local OAuth Memory and choose **Connect with OAuth**. Approve the clearly labeled local test page. The server should connect with 9 tools and no group grants. After 20 seconds, Refresh tool list should work without signing in again. `GET http://127.0.0.1:18165/health` reports authorization, refresh, and authenticated MCP request counts without credentials.

The fixture is test code with an in-memory authorization store; restarting it invalidates its test registrations and tokens. Use a fresh test registration or delete only the `Local OAuth Memory` test client cache before reconnecting. Never deploy the fixture or use production data in its reference memory store.

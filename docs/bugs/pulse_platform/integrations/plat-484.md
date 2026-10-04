# PLAT-484: Existing Code MCP discovery skill contract test fails

State: open. Found during PLAT-483 verification; not part of the Gmail setup change.
Priority: P2.

`TestCodeSkillOptionsStayPrivateAndRefreshWithoutMutatingBuiltins` in
`agent_go/internal/codeproduct/feature_discovery_test.go` fails with
`canonical MCP discovery/timing contract missing`. Its Code MCP branch expects
the skill description to contain `tool is missing or fails`, content to contain
`search_tools(server_name=`, and content not to contain `works right away`.

Reproduced on a clean owned detached worktree at unchanged `origin/main`
`1bb1781eb`, using:

```sh
cd agent_go
GOWORK=off go test ./internal/codeproduct -run '^TestCodeSkillOptionsStayPrivateAndRefreshWithoutMutatingBuiltins$' -count=1
```

The failure is independent of the Gmail setup feature. Determine the intended
current private MCP discovery contract, then reconcile the canonical skill and
test. No behaviour decision or production fix was made for this issue.

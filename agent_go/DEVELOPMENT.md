# Development notes

## Building: the engine libraries are folders in this repository

`mcpagent/` and `multi-llm-provider-go/` live in this repository next to
`agent_go/`. `agent_go/go.mod` (and `workspace/go.mod`) replace their modules
with those folders, so a build always uses the checked-out library code: change
a library and the app in the same pull request, with no version to bump.

## Pre-commit hook

The shared pre-commit hook currently has a `cd agent_go` path bug that fails
when committing from *inside* `agent_go` (see the `fix/pre-commit-hook-worktree-root`
branch). Until that lands, if a commit is blocked only by the hook's build step
— and `go build ./...` + `go vet ./...` pass locally and `gitleaks` is clean —
`git commit --no-verify` is acceptable.

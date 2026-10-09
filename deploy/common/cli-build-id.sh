#!/usr/bin/env bash
# cli_build_id REPO_ROOT WORKSPACE_ROOT GOWORK: a short id of exactly the source the AgentWorks CLI is built from (its own package
# and every package of this repository it imports), taken from git's tree hashes at HEAD. It changes only when the CLI's source
# does, so a deploy that touches only server code leaves it alone and nobody is asked to update their CLI for nothing. Prints
# nothing when it cannot be computed (the CLI then falls back to comparing whole-repository revisions).
cli_build_id() {
  local repo="$1" workspace="$2" gowork="${3:-}" dirs entries d line
  dirs="$(cd "$workspace" && GOWORK="$gowork" go list -deps -f '{{if not .Standard}}{{.Dir}}{{end}}' "$repo/agent_go/cmd/agentworks" 2>/dev/null | grep "^$repo/" | sed "s#^$repo/##" | sort -u)" || return 0
  [[ -n "$dirs" ]] || return 0
  entries=""
  while IFS= read -r d; do
    line="$(git -C "$repo" ls-tree HEAD "$d" 2>/dev/null)" || return 0
    [[ -n "$line" ]] || return 0
    entries+="$line"$'\n'
  done <<<"$dirs"
  printf '%s' "$entries" | sha256sum | cut -c1-16
}

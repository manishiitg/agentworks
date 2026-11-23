#!/usr/bin/env bash
# Real SDK bridge tests for every registered Codex capability, including bounded
# calls. Capture resets reviews; inspect the JSON output before signing/verify.
set -euo pipefail
cd "$(dirname "$0")/.."
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.26.0}"
case "${1:-capture}" in
 capture)
   selector="$(go run ./cmd/coding-agent-tests -provider Codex)"
   RUN_MCPAGENT_REAL_BRIDGE_E2E=1 RUN_MCPAGENT_STEER_E2E=1 \
     MCPAGENT_REAL_BRIDGE_ONLY=codex MLP_AGENT_REVIEW_CAPTURE=1 \
     go test ./agent -run "$selector" -v -count=1 -timeout=40m
   RUN_CLI_SANDBOX_CONTRACT=1 CLI_SANDBOX_CONTRACT_PROVIDERS=Codex \
     go test ./agent -run '^TestCLISandboxContract$' -v -count=1 -timeout=15m
   ;;
 verify)
   go test ./agent -run '^(TestAgentReviewsApproved|TestLayer2P0RegistryConsistent|TestLayer2ProviderSetsDerived|TestLayer2P0AgentReviewedEvidence|TestProviderAccountContinuationCannotCrossAccounts)$' -count=1
   ;;
 *) echo "usage: $0 [capture|verify]" >&2; exit 2 ;;
esac

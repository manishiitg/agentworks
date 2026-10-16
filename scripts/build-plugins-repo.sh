#!/usr/bin/env bash
# Assemble the small public plugins-only marketplace repo from this checkout:
# the plugin packages, shared skills and marketplace manifests, nothing
# else (no server code, no deployment material). Usage: build-plugins-repo.sh OUT_DIR
set -euo pipefail
out="${1:?usage: build-plugins-repo.sh OUT_DIR}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
rm -rf "$out"
mkdir -p "$out/.claude-plugin" "$out/.cursor-plugin" "$out/.agents/plugins"
cp -R "$root/plugins" "$out/plugins"
cp "$root/.claude-plugin/marketplace.json" "$out/.claude-plugin/marketplace.json"
cp "$root/.cursor-plugin/marketplace.json" "$out/.cursor-plugin/marketplace.json"
cp "$root/.agents/plugins/marketplace.json" "$out/.agents/plugins/marketplace.json"
[ -f "$root/LICENSE" ] && cp "$root/LICENSE" "$out/LICENSE"
find "$out" -name node_modules -prune -exec rm -rf {} + 2>/dev/null || true
cat > "$out/README.md" <<'README'
# AgentWorks plugins

Plugins that connect Claude Code, Codex, Muse, Pi and Cursor chats to
[AgentWorks](https://github.com/manishiitg/agentworks) (AgentBridge messaging).
This repository is generated from `plugins/` and the marketplace manifests in
the main repository on every change; send fixes there, not here.

```sh
claude plugin marketplace add manishiitg/agentworks-plugins
claude plugin install agentworks@agentworks

codex plugin marketplace add manishiitg/agentworks-plugins
codex plugin add agentworks-codex@agentworks

muse plugins marketplace add agentworks https://github.com/manishiitg/agentworks-plugins.git
muse plugins install agentworks-muse@agentworks

cursor-agent plugin marketplace add https://github.com/manishiitg/agentworks-plugins.git
```

Cursor CLI also supports loading `plugins/cursor` with `--plugin-dir`. Review
its [project-hook setup and tested modes](plugins/cursor/README.md) before
attaching; desktop messaging has separate limitations.

Already installed from the main repository? It keeps working. To switch to this
smaller source, remove the old marketplace and add this one (see each plugin's
README).
README
echo "built $(find "$out" -type f | wc -l) files, $(du -sk "$out" | cut -f1) KiB"

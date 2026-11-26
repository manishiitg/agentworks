#!/bin/sh

set -eu

VERSION=${1:-}
ASSET_DIR=${2:-dist}
OUTPUT=${3:-server.json}
REPOSITORY=${REPOSITORY:-manishiitg/llm-provider-mcp}

if [ -z "$VERSION" ]; then
    printf 'usage: %s VERSION [ASSET_DIR] [OUTPUT]\n' "$0" >&2
    exit 2
fi

command -v jq >/dev/null 2>&1 || {
    printf 'jq is required\n' >&2
    exit 1
}

packages='[]'
found=0

for bundle in "$ASSET_DIR"/llm-provider-mcp_*.mcpb; do
    [ -f "$bundle" ] || continue
    found=1
    filename=$(basename "$bundle")
    if command -v sha256sum >/dev/null 2>&1; then
        checksum=$(sha256sum "$bundle" | awk '{print $1}')
    else
        checksum=$(shasum -a 256 "$bundle" | awk '{print $1}')
    fi
    identifier="https://github.com/${REPOSITORY}/releases/download/v${VERSION}/${filename}"
    package=$(jq -n \
        --arg identifier "$identifier" \
        --arg checksum "$checksum" \
        '{registryType:"mcpb",identifier:$identifier,fileSha256:$checksum,transport:{type:"stdio"}}')
    packages=$(printf '%s' "$packages" | jq --argjson package "$package" '. + [$package]')
done

if [ "$found" -ne 1 ]; then
    printf 'no MCP bundles found in %s\n' "$ASSET_DIR" >&2
    exit 1
fi

jq \
    --arg version "$VERSION" \
    --arg repository "https://github.com/${REPOSITORY}" \
    --argjson packages "$packages" \
    '.version = $version | .repository.url = $repository | .packages = $packages' \
    packaging/registry/server.template.json > "$OUTPUT.tmp"
mv "$OUTPUT.tmp" "$OUTPUT"

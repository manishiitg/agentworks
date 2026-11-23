#!/bin/sh

set -eu

REPOSITORY="manishiitg/llm-provider-mcp"
MODULE="github.com/${REPOSITORY}"
DEFAULT_INSTALL_DIR="${HOME}/.local/bin"
INSTALL_DIR="${LLM_PROVIDER_MCP_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"
VERSION="${LLM_PROVIDER_MCP_VERSION:-latest}"
SOURCE_BINARY="${LLM_PROVIDER_MCP_BINARY:-}"
CLIENT=""
PROVIDERS="${LLM_PROVIDER_MCP_ALLOWED_PROVIDERS:-}"
WORKSPACE=""
SKIP_SMOKE_TEST=0
NON_INTERACTIVE=0
TMP_DIR=""

usage() {
    cat <<'EOF'
Download llm-provider-mcp and run its setup wizard.

Usage:
  install-mcp.sh [options]

Options:
  --client CLIENT       none, codex, claude, or both (default: ask on a TTY)
  --providers LIST      Comma-separated delegation target IDs (default: detected CLIs)
  --workspace PATH      Optional allowed delegation workspace root
  --install-dir PATH    Binary install directory (default: ~/.local/bin)
  --version VERSION     Release tag, main, or latest (default: latest)
  --from-binary PATH    Install an already-built binary instead of downloading
  --skip-smoke-test     Skip the MCP initialize and tools/list check
  --non-interactive     Do not prompt; binary-only registration unless --client is set
  -h, --help            Show this help

Examples:
  install-mcp.sh
  install-mcp.sh --client codex --providers cursor-cli --non-interactive
  install-mcp.sh --client claude --providers pi-cli --non-interactive
EOF
}

log() {
    printf '%s\n' "$*"
}

warn() {
    printf 'Warning: %s\n' "$*" >&2
}

die() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

cleanup() {
    if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
        rm -rf "$TMP_DIR"
    fi
}

trap cleanup 0 HUP INT TERM

while [ "$#" -gt 0 ]; do
    case "$1" in
        --client)
            [ "$#" -ge 2 ] || die "--client requires a value"
            CLIENT=$2
            shift 2
            ;;
        --providers)
            [ "$#" -ge 2 ] || die "--providers requires a value"
            PROVIDERS=$2
            shift 2
            ;;
        --workspace)
            [ "$#" -ge 2 ] || die "--workspace requires a value"
            WORKSPACE=$2
            shift 2
            ;;
        --install-dir)
            [ "$#" -ge 2 ] || die "--install-dir requires a value"
            INSTALL_DIR=$2
            shift 2
            ;;
        --version)
            [ "$#" -ge 2 ] || die "--version requires a value"
            VERSION=$2
            shift 2
            ;;
        --from-binary)
            [ "$#" -ge 2 ] || die "--from-binary requires a value"
            SOURCE_BINARY=$2
            shift 2
            ;;
        --skip-smoke-test)
            SKIP_SMOKE_TEST=1
            shift
            ;;
        --non-interactive)
            NON_INTERACTIVE=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "unknown option: $1"
            ;;
    esac
done

case "$INSTALL_DIR" in
    '~') INSTALL_DIR=$HOME ;;
    '~/'*) INSTALL_DIR="${HOME}/${INSTALL_DIR#~/}" ;;
esac
case "$VERSION" in
    *[!A-Za-z0-9._-]*) die "--version contains unsupported characters" ;;
esac
[ -n "$VERSION" ] || die "--version cannot be empty"

mkdir -p "$INSTALL_DIR"
INSTALL_DIR=$(CDPATH= cd "$INSTALL_DIR" && pwd -P)
BINARY_PATH="${INSTALL_DIR}/llm-provider-mcp"
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/llm-provider-mcp-install.XXXXXX")

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
        return
    fi
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
        return
    fi
    die "sha256sum or shasum is required to verify release archives"
}

release_asset_name() {
    os=$(uname -s)
    arch=$(uname -m)
    case "$os" in
        Darwin) os=darwin ;;
        Linux) os=linux ;;
        *) return 1 ;;
    esac
    case "$arch" in
        x86_64|amd64) arch=amd64 ;;
        arm64|aarch64) arch=arm64 ;;
        *) return 1 ;;
    esac
    printf 'llm-provider-mcp_%s_%s.tar.gz\n' "$os" "$arch"
}

install_release() {
    [ "$VERSION" != "main" ] || return 1
    asset=$(release_asset_name) || return 1
    if [ "$VERSION" = "latest" ]; then
        base_url="https://github.com/${REPOSITORY}/releases/latest/download"
    else
        base_url="https://github.com/${REPOSITORY}/releases/download/${VERSION}"
    fi
    archive="${TMP_DIR}/${asset}"
    checksums="${TMP_DIR}/checksums.txt"
    if ! curl -fsSL "${base_url}/${asset}" -o "$archive"; then
        return 1
    fi
    curl -fsSL "${base_url}/checksums.txt" -o "$checksums" || \
        die "release archive exists but checksums.txt could not be downloaded"
    expected=$(awk -v name="$asset" '$2 == name {print $1}' "$checksums")
    [ -n "$expected" ] || die "release checksum is missing for $asset"
    actual=$(sha256_file "$archive")
    [ "$actual" = "$expected" ] || die "release checksum verification failed for $asset"
    mkdir -p "${TMP_DIR}/release"
    tar -xzf "$archive" -C "${TMP_DIR}/release" llm-provider-mcp
    [ -f "${TMP_DIR}/release/llm-provider-mcp" ] || \
        die "release archive does not contain llm-provider-mcp"
    cp "${TMP_DIR}/release/llm-provider-mcp" "$BINARY_PATH"
    chmod 0755 "$BINARY_PATH"
    log "Installed release binary: $BINARY_PATH"
    return 0
}

install_with_go() {
    command -v go >/dev/null 2>&1 || \
        die "no compatible release archive was found and Go 1.25.12+ is not installed"
    go_version=$(go version | awk '{print $3}' | sed 's/^go//')
    go_major=$(printf '%s' "$go_version" | cut -d. -f1 | sed 's/[^0-9].*//')
    go_minor=$(printf '%s' "$go_version" | cut -d. -f2 | sed 's/[^0-9].*//')
    go_patch=$(printf '%s' "$go_version" | cut -d. -f3 | sed 's/[^0-9].*//')
    [ -n "$go_major" ] && [ -n "$go_minor" ] || die "could not parse Go version: $go_version"
    [ -n "$go_patch" ] || go_patch=0
    if [ "$go_major" -lt 1 ] || \
       { [ "$go_major" -eq 1 ] && [ "$go_minor" -lt 25 ]; } || \
       { [ "$go_major" -eq 1 ] && [ "$go_minor" -eq 25 ] && [ "$go_patch" -lt 12 ]; }; then
        die "Go 1.25.12+ is required for source installation; found $go_version"
    fi
    go_ref=$VERSION
    if [ "$go_ref" = "latest" ]; then
        go_ref=main
    fi
    log "No release archive found; building ${MODULE}/cmd/llm-provider-mcp@${go_ref} with Go."
    GOBIN="$INSTALL_DIR" go install "${MODULE}/cmd/llm-provider-mcp@${go_ref}"
    [ -x "$BINARY_PATH" ] || die "go install did not create $BINARY_PATH"
    log "Installed source build: $BINARY_PATH"
}

install_existing_binary() {
    case "$SOURCE_BINARY" in
        /*) source_path=$SOURCE_BINARY ;;
        *) source_path="${PWD}/${SOURCE_BINARY}" ;;
    esac
    [ -f "$source_path" ] || die "source binary does not exist: $source_path"
    [ -x "$source_path" ] || die "source binary is not executable: $source_path"
    if [ "$source_path" != "$BINARY_PATH" ]; then
        cp "$source_path" "$BINARY_PATH"
    fi
    chmod 0755 "$BINARY_PATH"
    log "Installed local binary: $BINARY_PATH"
}

prepare_macos_binary() {
    [ "$(uname -s)" = "Darwin" ] || return 0
    command -v codesign >/dev/null 2>&1 || \
        die "codesign is required to prepare the macOS binary"

    signature_info=$(codesign -dv --verbose=2 "$BINARY_PATH" 2>&1 || true)
    case "$signature_info" in
        *"Signature=adhoc"*|*"code object is not signed at all"*|"")
            codesign --force --sign - "$BINARY_PATH" >/dev/null 2>&1 || \
                die "failed to create a local macOS code signature for $BINARY_PATH"
            codesign --verify --strict "$BINARY_PATH" >/dev/null 2>&1 || \
                die "macOS code-signature verification failed for $BINARY_PATH"
            log "Prepared macOS code signature: $BINARY_PATH"
            ;;
        *)
            codesign --verify --strict "$BINARY_PATH" >/dev/null 2>&1 || \
                die "the installed macOS binary has an invalid code signature"
            ;;
    esac
}

if [ -n "$SOURCE_BINARY" ]; then
    install_existing_binary
elif ! install_release; then
    install_with_go
fi

prepare_macos_binary

set -- setup --binary "$BINARY_PATH"
[ -z "$CLIENT" ] || set -- "$@" --client "$CLIENT"
[ -z "$PROVIDERS" ] || set -- "$@" --providers "$PROVIDERS"
[ -z "$WORKSPACE" ] || set -- "$@" --workspace "$WORKSPACE"
[ "$SKIP_SMOKE_TEST" -eq 0 ] || set -- "$@" --skip-smoke-test

if [ "$NON_INTERACTIVE" -eq 1 ]; then
    set -- "$@" --non-interactive
    "$BINARY_PATH" "$@"
elif [ -r /dev/tty ] && [ -w /dev/tty ]; then
    "$BINARY_PATH" "$@" </dev/tty
else
    warn "no interactive terminal detected; continuing without host registration"
    "$BINARY_PATH" "$@" --non-interactive
fi

case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) ;;
    *) warn "$INSTALL_DIR is not on PATH; registered MCP clients use the absolute binary path" ;;
esac

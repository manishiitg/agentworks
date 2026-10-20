#!/usr/bin/env bash
# Activate a shared release from a product-scoped job directory.
set -euo pipefail
JOB="$1"
PRODUCT="$(cat "$JOB/product")"
# deploy.sh ships the product's config (from the private deployments repository) with the job.
if [[ -f "$JOB/product-config.tgz" ]]; then
  install -d -m 0700 "$JOB/product-config"
  tar -xzf "$JOB/product-config.tgz" -C "$JOB/product-config"
  export PRODUCT_CONFIG_DIR="$JOB/product-config"
fi
REMOTE_APP="/srv/$PRODUCT"
export PATH="$HOME/.local/go/bin:$HOME/.local/bin:/usr/local/bin:/usr/bin:/bin"
case "$(uname -sm)" in
  'Linux x86_64') BUILD_ARCH=amd64 ;;
  'Linux aarch64') BUILD_ARCH=arm64 ;;
  *) echo 'Expected Linux x86_64 or aarch64.' >&2; exit 1 ;;
esac
export BUILD_ARCH
[[ "$(whoami)" == "$PRODUCT" ]] || { echo "Must run as the $PRODUCT account, not $(whoami)" >&2; exit 1; }

# Serialize complete deployments (clone + build + activate + prune). This box
# also runs other products under their own separate accounts/locks; this lock
# only ever guards concurrent runs of THIS product's deploy against itself.
exec 9>"$REMOTE_APP/deploy.lock"
flock -n 9 || { echo "Another $PRODUCT deployment is already running." >&2; exit 1; }
trap 'rm -rf "$JOB"' EXIT

# Prebuilt release (PLAT-426): nothing to install, clone or compile. The build holds the three repositories' source, so the
# activation script that runs is the one from the very revisions the build was made from.
[[ -f "$JOB/prebuilt" ]] || { echo "A prebuilt release job is required." >&2; exit 2; }
  PREBUILT="$(cat "$JOB/prebuilt")"
  # The shared build folder, or this product's own downloaded copy (PREBUILT_DELIVERY=fetch, a host of its own).
  [[ "$PREBUILT" =~ ^/srv/_builds/[0-9a-f]{8}(-arm64)?-[0-9]{14}$ || "$PREBUILT" =~ ^/srv/$PRODUCT/prebuilt/[0-9a-f]{8}(-arm64)?-[0-9]{14}$ ]] \
    || { echo "Unexpected prebuilt path: $PREBUILT" >&2; exit 1; }
  bash "$PREBUILT/source/mcp-agent-builder-go/deploy/rootless-linux/build-and-activate.sh" "$PREBUILT/source" "$PRODUCT" --prebuilt "$PREBUILT"
  exit $?

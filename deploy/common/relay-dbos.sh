#!/usr/bin/env bash
# The interpreter is owned by the deployment account; slots may only read it.
# Keep each requirements version at a stable path across release activation.
relay_dbos_prepare() (
  set -euo pipefail
  umask 022
  local repo_root="$1" app_dir="$2" requirements digest runtime expected
  requirements="$repo_root/agent_go/pkg/relaypython/requirements-dbos.txt"
  digest="$(sha256sum "$requirements" | awk '{print $1}')"
  expected="$(sed -n 's/^dbos==//p' "$requirements")"
  [[ -n "$expected" ]] || { echo 'Missing pinned DBOS version' >&2; exit 1; }
  install -d -m 0755 "$app_dir/runtime"
  exec 9>"$app_dir/runtime/.relay-dbos-install.lock"
  flock 9
  runtime="$app_dir/runtime/relay-dbos-${digest:0:16}"
  if [[ ! -f "$runtime/.ready" ]]; then
    # Only incomplete installations are replaced. Ready runtimes may belong to
    # an active invocation and must never be upgraded underneath its process.
    [[ ! -e "$runtime" ]] || rm -rf -- "$runtime"
    python3 -m venv "$runtime"
    "$runtime/bin/python" -m pip install --disable-pip-version-check --no-cache-dir -r "$requirements" >&2
    "$runtime/bin/python" -m pip check >&2
    "$runtime/bin/python" -I -c 'import dbos, importlib.metadata, sys; assert importlib.metadata.version("dbos") == sys.argv[1]' "$expected"
    chmod -R u+rwX,go+rX,go-w "$runtime"
    printf '%s\n' "$digest" > "$runtime/.ready"
  fi
  "$runtime/bin/python" -I -c 'import dbos, importlib.metadata, sys; assert importlib.metadata.version("dbos") == sys.argv[1]' "$expected"
  printf '%s\n' "$runtime/bin/python"
)

#!/usr/bin/env bash
# Linux end-to-end test of the slot shell chain (PLAT-478), in a throwaway container. Run from the repository root:
#
#   docker run --rm --privileged -v "$PWD/workspace:/src/new:ro" -v "$PWD/deploy/common:/src/deploy:ro" [-v <old workspace>:/src/old:ro] \
#     -v "$(go env GOMODCACHE):/go/pkg/mod" golang:1.26-bookworm bash /src/new/slotcheck/container_e2e.sh
#
# It builds a slot host the way provision-slots.sh does (service account vs, slot accounts slot01/slot02/slot50 with
# their groups, root-owned slotctl and allow-list, the sudo rule, the slot table root:vs 0640, a docs tree with two
# users, a workflow, an app-owned 0700 browser profile holding a cookie file) and then, as the service account:
#   1. the real Landlock launcher, run as slot01 with a policy that grants the 0700 profile: the old launcher (from
#      /src/old, if mounted) refuses with SANDBOX_UNAVAILABLE, the new one starts the command (SANDBOX_GRANT_SKIPPED);
#   2. the real self-test (bin/slotcheck) on the fixed layout: everything passes;
#   3. the self-test with releases/ 0700: FAIL runner-reachable, exit 1;
#   5. TestRealSlotChain: a Crew command starts; the slot cannot read the browser profile, another user's tree, the
#      app's .env, or a world-readable file outside its grants (so it ran confined);
#   6. deploy/common/slotcheck.sh (if /src/deploy is mounted), the wrapper a deploy runs: passes, prints no secret;
#      then: the new code created no browser profile folder;
#   4. (last, it creates profile folders as it goes) the self-test built against the OLD grant builder and launcher
#      (if /src/old is mounted, e.g. `git archive origin/main~N workspace`): the probes FAIL with the RTS error, exit 1.
set -euo pipefail
export GOFLAGS=-mod=mod GOTOOLCHAIN=local CGO_ENABLED=0
apt-get update >/dev/null && apt-get install -y sudo tmux >/dev/null

APP=/srv/vs DOCS=/srv/vs/data/docs LIBEXEC=/usr/local/libexec/agentworks ETC=/etc/agentworks
REL="$APP/releases/r1"
PROFILE_ROOT="$APP/state/browser-profile"
PROFILE="$PROFILE_ROOT-projects/project-3bdc30503fa28a4f--browser"

groupadd vs && useradd -m -g vs -s /bin/bash vs
groupadd slotshared
for s in slot01 slot02 slot50; do
  groupadd "$s"
  useradd -M -N -g "$s" -d "$APP/slots/home/$s" -s /usr/sbin/nologin "$s"
  usermod -aG "$s" vs
  usermod -aG slotshared "$s"
done
usermod -aG slotshared vs

echo "==> building"
mkdir -p "$REL/bin" /build
cp -R /src/new /build/new
(cd /build/new && go build -o "$REL/bin/video-studio-landlock-runner" ./cmd/landlock-runner \
  && go build -o "$REL/bin/slotctl" ./cmd/slotctl && go build -o "$REL/bin/slotcheck" ./cmd/slotcheck \
  && go test -c -o "$REL/bin/slotcheck.test" ./slotcheck)
if [[ -d /src/old ]]; then
  # The old launcher, and the self-test built on the old grant builder (origin/main's security package).
  cp -R /src/old /build/old
  cp -R /build/new/slotcheck /build/old/slotcheck && rm -f /build/old/slotcheck/*_test.go
  mkdir -p /build/old/cmd/slotcheck && cp /build/new/cmd/slotcheck/*.go /build/old/cmd/slotcheck/
  cp /build/new/slots/config.go /build/old/slots/config.go
  mkdir -p "$APP/releases/r0/bin"
  (cd /build/old && go build -o "$APP/releases/r0/bin/video-studio-landlock-runner" ./cmd/landlock-runner \
    && go build -o "$APP/releases/r0/bin/slotcheck" ./cmd/slotcheck)
fi

echo "==> host layout (as provision-slots.sh lays it out)"
install -d -o vs -g vs -m 0711 "$APP" "$APP/data" "$DOCS" "$APP/releases" "$APP/slots" "$APP/slots/home" "$APP/slots/state" "$APP/slots/run"
chown -R vs:vs "$APP/releases" && chmod 0711 "$APP/releases" "$APP/releases"/* "$APP/releases"/*/bin
chmod 0755 "$APP/releases"/*/bin/*
install -d -o vs -g vs -m 0700 "$APP/state" "$PROFILE_ROOT" "$PROFILE_ROOT-projects" "$PROFILE"
echo COOKIE > "$PROFILE/Cookies" && chown vs:vs "$PROFILE/Cookies" && chmod 0600 "$PROFILE/Cookies"
cat > "$APP/.env" <<ENV
ENVFILE=1
AGENTWORKS_SLOTS=optin
AGENTWORKS_SLOTCTL=$LIBEXEC/slotctl
AGENTWORKS_SLOTCTL_CONFIG=$LIBEXEC/slotctl.json
AGENTWORKS_SLOTS_FILE=$ETC/slots.json
AGENT_BROWSER_SHARED_PROFILE=$PROFILE_ROOT
GLOBAL_SECRET_TEST_ONLY=never-printed-value
ENV
chown vs:vs "$APP/.env" && chmod 0600 "$APP/.env"
for s in slot01 slot02 slot50; do
  install -d -o "$s" -g "$s" -m 0700 "$APP/slots/home/$s"
  install -d -o vs -g "$s" -m 2770 "$APP/slots/state/$s" "$APP/slots/run/$s"
done
install -d -o vs -g slotshared -m 2770 "$DOCS/Workflow" "$DOCS/Workflow/wf1"
install -d -o vs -g vs -m 0755 "$DOCS/Workflow/wf2" && echo PUBLIC > "$DOCS/Workflow/wf2/public.txt" && chmod 0644 "$DOCS/Workflow/wf2/public.txt"
install -d -o vs -g vs -m 0711 "$DOCS/_users"
for pair in u1:slot01 u2:slot02; do
  u="${pair%%:*}" s="${pair##*:}"
  mkdir -p "$DOCS/_users/$u/Chats/Work/projects" "$DOCS/_users/$u/Chats/Code/projects/${u}code"
  mkdir -p "$DOCS/_users/$u/Chats/Work/projects/${u}crew"
done
echo own > "$DOCS/_users/u1/Chats/Work/projects/u1crew/own.txt"
echo SECRET > "$DOCS/_users/u2/Chats/Work/projects/u2crew/secret.txt"
for pair in u1:slot01 u2:slot02; do
  u="${pair%%:*}" s="${pair##*:}"
  chown -R "vs:$s" "$DOCS/_users/$u" && chmod -R g+rwX,o-rwx "$DOCS/_users/$u" && find "$DOCS/_users/$u" -type d -exec chmod g+s {} +
done

install -d -o root -g root -m 0755 "$LIBEXEC"
install -o root -g root -m 0755 "$REL/bin/slotctl" "$LIBEXEC/slotctl"
cat > "$LIBEXEC/slotctl.json" <<JSON
{"slot_prefix":"slot","allowed_exec":["$APP/releases/*/bin/video-studio-landlock-runner","/usr/bin/tmux","/usr/bin/chmod"],
 "allowed_cwd":["$DOCS","$APP/slots"],"slot_run_root":"$APP/slots/run","slot_state_root":"$APP/slots/state","docs_root":"$DOCS","slot_table":"$ETC/slots.json"}
JSON
chmod 0644 "$LIBEXEC/slotctl.json"
install -d -o root -g vs -m 0750 "$ETC" && chmod o+x "$ETC"
printf '{"slots":{"slot01":"u1","slot02":"u2"}}\n' | install -o root -g vs -m 0640 /dev/stdin "$ETC/slots.json"
cat > /etc/sudoers.d/agentworks-slots <<SUDO
Defaults:vs !requiretty
Defaults:vs env_reset
Defaults:vs secure_path="/usr/bin:/bin"
Runas_Alias AGENTWORKS_SLOTS = slot01, slot02, slot50
vs ALL=(AGENTWORKS_SLOTS) NOPASSWD: $LIBEXEC/slotctl exec, $LIBEXEC/slotctl exec --request-file *
SUDO
chmod 0440 /etc/sudoers.d/agentworks-slots && visudo -cf /etc/sudoers.d/agentworks-slots >/dev/null

as_vs() {
  sudo -u vs env -i PATH=/usr/bin:/bin HOME=/home/vs AGENTWORKS_SLOTS=optin AGENTWORKS_SLOTCTL="$LIBEXEC/slotctl" \
    AGENTWORKS_SLOTCTL_CONFIG="$LIBEXEC/slotctl.json" AGENTWORKS_SLOTS_FILE="$ETC/slots.json" \
    AGENT_BROWSER_SHARED_PROFILE="$PROFILE_ROOT" WORKSPACE_DOCS_PATH="$DOCS" AGENTWORKS_STATE_ROOT="$APP/state" "$@"
}
fails=0
expect() { # expect <exit code> <description> -- command...
  local want="$1" what="$2" rc=0; shift 3
  "$@" || rc=$?
  if [[ "$rc" == "$want" ]]; then echo "OK   $what (exit $rc)"; else echo "BAD  $what: exit $rc, wanted $want"; fails=$((fails + 1)); fi
}

echo "==> 1. the launcher, as slot01, with the 0700 browser profile in its grants"
policy=/tmp/policy.json
printf '{"read_paths":[],"write_paths":["%s"],"work_dir":"%s"}\n' "$PROFILE" "$DOCS" > "$policy" && chmod 0644 "$policy"
cp "$policy" /tmp/policy-old.json && chmod 0644 /tmp/policy-old.json
if [[ -x "$APP/releases/r0/bin/video-studio-landlock-runner" ]]; then
  out="$(sudo -u slot01 "$APP/releases/r0/bin/video-studio-landlock-runner" --config /tmp/policy-old.json -- /bin/sh -c 'pwd' 2>&1 || true)"
  echo "old launcher: $out"
  [[ "$out" == *"SANDBOX_UNAVAILABLE: inspect Landlock path: stat $PROFILE: permission denied"* ]] && echo "OK   old launcher refuses the command (the RTS failure)" || { echo "BAD  old launcher did not reproduce the RTS failure"; fails=$((fails + 1)); }
fi
out="$(sudo -u slot01 "$REL/bin/video-studio-landlock-runner" --config "$policy" -- /bin/sh -c 'pwd; cat '"$PROFILE"'/Cookies' 2>&1 || true)"
echo "new launcher: $out"
[[ "$out" == *"SANDBOX_GRANT_SKIPPED: $PROFILE"* && "$out" == *"$DOCS"* && "$out" != *COOKIE* ]] && echo "OK   new launcher starts the command, skips the grant, the cookie stays unreadable" || { echo "BAD  new launcher"; fails=$((fails + 1)); }

echo "==> 2. self-test, fixed layout"
expect 0 "self-test passes on the fixed layout" -- as_vs "$REL/bin/slotcheck" --docs "$DOCS" --app "$APP"
as_vs "$REL/bin/slotcheck" --docs "$DOCS" --app "$APP" || true

echo "==> 3. self-test, releases/ 0700 (RTS layer 2)"
chmod 0700 "$APP/releases"
expect 1 "self-test fails when releases/ is 0700" -- as_vs "$REL/bin/slotcheck" --docs "$DOCS" --app "$APP"
as_vs "$REL/bin/slotcheck" --docs "$DOCS" --app "$APP" | grep '^FAIL' | head -3 || true
chmod 0711 "$APP/releases"

echo "==> the self-test changed nothing in the users' folders"
if find "$DOCS/_users" "$DOCS/Workflow" -name .sandbox-cache | grep -q .; then
  echo "BAD  the self-test created .sandbox-cache in a user's or workflow folder"; fails=$((fails + 1))
else
  echo "OK   no .sandbox-cache created by the self-test"
fi

echo "==> 5a. a real tmux server for slot01 (PLAT-480, F1)"
SOCK="$APP/slots/run/slot01/tmux.sock"
sudo -u slot01 env SHELL=/bin/sh tmux -S "$SOCK" new-session -d -s probe "sleep 3000"
expect 0 "slot01 itself reaches its tmux server" -- sudo -u slot01 tmux -S "$SOCK" list-sessions
# Control: the launcher alone (Landlock, no private view) does NOT stop a confined command reaching the server.
printf '{"read_paths":[],"write_paths":["%s"],"work_dir":"%s"}\n' "$DOCS" "$DOCS" > /tmp/policy-tmux.json && chmod 0644 /tmp/policy-tmux.json
out="$(sudo -u slot01 "$REL/bin/video-studio-landlock-runner" --config /tmp/policy-tmux.json -- /bin/sh -c "tmux -S $SOCK list-sessions" 2>&1 || true)"
echo "control (launcher without the hide): $out"
[[ "$out" == *probe* ]] && echo "OK   control: Landlock alone leaves the socket reachable (the hole this step closes)" || { echo "BAD  control did not reproduce the hole: $out"; fails=$((fails + 1)); }

install -d -o vs -g slot01 -m 2770 "$APP/slots/run/slot01/shells" "$APP/slots/run/slot01/shells/e2e" "$APP/slots/run/slot01/shells/other"
echo OTHER > "$APP/slots/run/slot01/shells/other/secret" && chown vs:slot01 "$APP/slots/run/slot01/shells/other/secret"
echo "==> 5. the chain, negative reads"
expect 0 "TestRealSlotChain" -- as_vs env AGENTWORKS_SLOT_CHAIN_E2E=1 E2E_DOCS="$DOCS" E2E_APP="$APP" E2E_PROFILE="$PROFILE" \
  E2E_OTHER_PROJECT="$DOCS/_users/u2/Chats/Work/projects/u2crew" E2E_CREW="$DOCS/_users/u1/Chats/Work/projects/u1crew" \
  E2E_NOT_GRANTED="$DOCS/Workflow/wf2/public.txt" E2E_TMUX_SOCKET="$SOCK" E2E_RUN_KEEP="$APP/slots/run/slot01/shells/e2e" E2E_RUNNER="$REL/bin/video-studio-landlock-runner" \
  AGENTWORKS_LANDLOCK_RUNNER="$REL/bin/video-studio-landlock-runner" "$REL/bin/slotcheck.test" -test.run TestRealSlotChain -test.v

if [[ -f /src/deploy/slotcheck.sh ]]; then
  echo "==> 6. the deploy wrapper (deploy/common/slotcheck.sh), as a deploy runs it"
  cp /src/deploy/slotcheck.sh /src/deploy/admission_scan.py "$REL/" && chmod 0644 "$REL/slotcheck.sh" "$REL/admission_scan.py"
  ln -sfn "$REL" "$APP/current"
  out="$(sudo -u vs env -i PATH=/usr/bin:/bin HOME=/home/vs bash "$APP/current/slotcheck.sh" --app "$APP" --docs "$DOCS" --product vs 2>&1)"; rc=$?
  printf '%s\n' "$out" | grep -E '^(==>|slot self-test|secret admission|INFO|WARN|FAIL)' || true
  [[ "$rc" == 0 && "$out" =~ slot\ self-test:\ [0-9]+\ passed,\ 0\ failed && "$out" != *never-printed-value* ]] && echo "OK   deploy wrapper passes and prints no secret value" || { echo "BAD  deploy wrapper (exit $rc)"; fails=$((fails + 1)); }
fi

echo "==> the new code created nothing under the browser profile roots"
# (The sandbox's own start-up probe, run as the service account, creates the empty -users/-workflows/-projects roots;
# a running service has them already. No browser's profile folder may appear.)
[[ "$(ls "$PROFILE_ROOT-projects")" == "$(basename "$PROFILE")" && -z "$(ls -A "$PROFILE_ROOT-workflows" 2>/dev/null)" ]] && echo "OK   no profile folder created" || { ls -la "$PROFILE_ROOT-projects"; echo "BAD  the self-test created browser profile folders"; fails=$((fails + 1)); }

if [[ -x "$APP/releases/r0/bin/slotcheck" ]]; then
  # Last: the old grant builder creates profile folders as it goes (scopeBrowser's MkdirAll), the new one does not.
  echo "==> 4. self-test on the OLD grant builder and launcher (RTS layer 3)"
  expect 1 "self-test fails on the old code" -- as_vs "$APP/releases/r0/bin/slotcheck" --docs "$DOCS" --app "$APP"
  as_vs "$APP/releases/r0/bin/slotcheck" --docs "$DOCS" --app "$APP" | grep -E '^(FAIL|slot self-test)' || true
fi

echo "container e2e: $fails failure(s)"
exit "$fails"

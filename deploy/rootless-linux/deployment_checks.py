"""Fail-closed rootless-Linux deployment checks; never print environment contents.

Generalized from deploy/cf/deployment_checks.py (confida). Product-specific
values come from PRODUCT and EXPECTED_PUBLIC_URL in the environment, both
already exported by build-and-activate.sh before this runs.
"""
import argparse
import json
import os
import urllib.request
from pathlib import Path
import platform
import pwd
import re
import subprocess
import sys

PRODUCT = os.environ.get("PRODUCT", "")
EXPECTED_PUBLIC_URL = os.environ.get("EXPECTED_PUBLIC_URL", "")
ENV_FILE = Path(f"/srv/{PRODUCT}/.env")
UNIT = f"{PRODUCT}-agent"


def check_environment_file(path=ENV_FILE):
    if not EXPECTED_PUBLIC_URL:
        return
    values = []
    for line in path.read_text().splitlines():
        match = re.match(r"^\s*PUBLIC_URL\s*=(.*)$", line)
        if match:
            value = match.group(1).strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                value = value[1:-1]
            values.append(value)
    if values != [EXPECTED_PUBLIC_URL]:
        raise ValueError(f"{path} must contain exactly one PUBLIC_URL={EXPECTED_PUBLIC_URL}")


def check_process_environment(raw):
    if not EXPECTED_PUBLIC_URL:
        return
    values = [entry for entry in raw.split(b"\0") if entry.startswith(b"PUBLIC_URL=")]
    if values != [f"PUBLIC_URL={EXPECTED_PUBLIC_URL}".encode()]:
        raise ValueError(f"running {UNIT} has missing, duplicate, or incorrect PUBLIC_URL")


def env_file_values(path=ENV_FILE):
    values = {}
    for line in path.read_text().splitlines():
        match = re.match(r"^\s*([A-Z_][A-Z0-9_]*)\s*=(.*)$", line)
        if match:
            value = match.group(2).strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                value = value[1:-1]
            values.setdefault(match.group(1), []).append(value)
    return values


# gog (Gmail connector) must use its encrypted file keyring on headless boxes.
# On "auto" it picks the service user's gnome-keyring over D-Bus, which has no
# unlocked default collection, and every Gmail connect fails with
# "store token: set token: Object does not exist at path /".
def check_gog_keyring_file(path=ENV_FILE):
    values = env_file_values(path)
    if values.get("GOG_KEYRING_BACKEND") != ["file"]:
        raise ValueError(f"{path} must contain exactly one GOG_KEYRING_BACKEND=file")
    passwords = values.get("GOG_KEYRING_PASSWORD", [])
    if len(passwords) != 1 or not passwords[0]:
        raise ValueError(f"{path} must contain exactly one non-empty GOG_KEYRING_PASSWORD")


def check_gog_keyring_process(raw):
    entries = raw.split(b"\0")
    if [e for e in entries if e.startswith(b"GOG_KEYRING_BACKEND=")] != [b"GOG_KEYRING_BACKEND=file"]:
        raise ValueError(f"running {UNIT} does not have GOG_KEYRING_BACKEND=file")
    passwords = [e for e in entries if e.startswith(b"GOG_KEYRING_PASSWORD=")]
    if len(passwords) != 1 or passwords[0] == b"GOG_KEYRING_PASSWORD=":
        raise ValueError(f"running {UNIT} has a missing or empty GOG_KEYRING_PASSWORD")


def systemctl_value(property_name):
    return subprocess.check_output(
        ["systemctl", "--user", "show", UNIT, f"--property={property_name}", "--value"],
        text=True, timeout=10,
    ).strip()


def check_user_slots(env_path=ENV_FILE, users_path=None):
    """A server with more than one account runs every account in its own Linux slot.

    Citymall went live on 2026-10-09 with five accounts and no slots at all (every person's commands ran as one Linux user, with
    the server's secrets readable to it), and nothing failed: slotcheck skipped itself ("slots are not enabled on this host").
    So this check is part of every deploy. It needs the product's own env file and its user directory; a server with a single
    account is a personal install and needs no slots.
    """
    users_path = users_path or Path(f"/srv/{PRODUCT}/data/docs/config/users.json")
    if not users_path.exists():
        return
    users = [u for u in json.loads(users_path.read_text()).get("users", []) if not u.get("disabled") and u.get("id")]
    if len(users) < 2:
        return
    values = env_file_values(env_path)
    mode = (values.get("AGENTWORKS_SLOTS") or [""])[-1].lower()
    if mode not in ("on", "optin"):
        raise ValueError(
            f"this server has {len(users)} accounts but AGENTWORKS_SLOTS is not on, so everyone's commands run as one Linux user. "
            "Fix: set SLOTS_ENABLED=true and \"AGENTWORKS_SLOTS=on\" in the product's product.env and deploy again"
        )
    table = Path((values.get("AGENTWORKS_SLOTS_FILE") or ["/etc/agentworks/slots.json"])[-1])
    held = set(json.loads(table.read_text()).get("slots", {}).values())
    without = sorted(u.get("email") or u.get("username") or u["id"] for u in users if u["id"] not in held)
    if without and mode == "optin":
        # Opt-in is a rollout state (server C, Dominion): accounts without a slot still run as the service account. Say so loudly.
        print(f"WARN {len(without)} of {len(users)} accounts have no slot yet (AGENTWORKS_SLOTS=optin): {', '.join(without)}")
        return
    if without:
        raise ValueError(
            f"{len(without)} of {len(users)} accounts have no slot: {', '.join(without)}. "
            f"Fix: as root on the host, run: PRODUCT={PRODUCT} bash -s -- ensure < deploy/common/provision-slots.sh"
        )


def check_private_tmp(env_path=ENV_FILE, restrict_path="/proc/sys/kernel/apparmor_restrict_unprivileged_userns", fetch=None):
    """Where the host restricts unprivileged user namespaces, the workspace service must still get a private /tmp.

    The service starts the launcher in namespaces it creates itself; AppArmor puts that child in its restricted profile unless the
    service binary has a `userns` exception, and then private /tmp is unavailable and any command whose folder rules hide a path
    inside a granted folder fails with SANDBOX_UNAVAILABLE (Citymall, 2026-10-09). The workspace health says so.
    """
    try:
        restricted = Path(restrict_path).read_text().strip() == "1"
    except OSError:
        return
    if not restricted:
        return
    values = env_file_values(env_path)
    base = (values.get("WORKSPACE_API_URL") or [""])[-1].rstrip("/")
    if not base:
        return
    health = json.loads((fetch or (lambda url: urllib.request.urlopen(url, timeout=10).read()))(base + "/health"))
    detail = str(health.get("shell_sandbox", {}).get("detail", ""))
    if "private /tmp unavailable" in detail:
        raise ValueError(
            "this host restricts unprivileged user namespaces and the workspace service cannot create one (private /tmp is "
            "unavailable), so shell commands with hidden folders fail. Fix: as root run: "
            f"PRODUCT={PRODUCT} bash -s -- userns < deploy/common/provision-slots.sh, then restart {PRODUCT}-workspace"
        )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=["preflight", "running"])
    args = parser.parse_args()
    if not PRODUCT:
        raise ValueError("PRODUCT must be set in the environment before running this check")
    if platform.system() != "Linux" or pwd.getpwuid(os.getuid()).pw_name != PRODUCT:
        raise ValueError(f"deployment checks must run as the {PRODUCT} account on Linux")
    check_environment_file()
    check_gog_keyring_file()
    if EXPECTED_PUBLIC_URL and str(ENV_FILE) not in systemctl_value("EnvironmentFiles").split():
        raise ValueError(f"{UNIT} must load {ENV_FILE} through EnvironmentFile")
    if args.phase == "running":
        pid = int(systemctl_value("MainPID"))
        if pid <= 0:
            raise ValueError(f"{UNIT} has no running main process")
        environ = Path(f"/proc/{pid}/environ").read_bytes()
        check_process_environment(environ)
        check_gog_keyring_process(environ)
        check_user_slots()
        check_private_tmp()
    print(f"PASS {args.phase}: {PRODUCT} deployment configuration")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)

#!/usr/bin/env bash
# Publish a finished build to the PUBLIC GitHub repo that only holds builds (PLAT-426), so servers download it themselves.
#
#   publish-build.sh <build-dir>     publish (idempotent): release build-<builder8>-<mcpagent8>-<provider8> with the assets
#                                    build.tar.gz (the whole build folder), build-rts.tar.gz (the trimmed copy RTS needs),
#                                    manifest.json, SHA256SUMS; then keep only the newest 8 build releases per architecture
#   publish-build.sh --list          the build releases on GitHub (anonymous, read-only)
#
# Runs on the build host after a build (build-release.sh, best effort; `./deploy.sh publish`). Needs only python3, tar and gzip.
# The token (fine-grained, Contents read+write on the one repo) comes from GH_TOKEN in the environment or from the file
# ~/.config/agentworks/builds.env (BUILDS_ENV_FILE) as a line GH_TOKEN=...; that file must be mode 0600 and owned by the user
# running this, or it is refused. Without a token the script prints one message, publishes nothing and exits 0.
# The last stdout line on success is RELEASE_TAG=<tag>. The token is never printed.
# Settings: GH_BUILDS_REPO (manishiitg/agentworks-builds), GH_API_URL, GH_UPLOAD_URL, BUILDS_KEEP_RELEASES (8).
set -euo pipefail
exec python3 - "$@" <<'PY'
import hashlib, http.client, json, os, re, shutil, subprocess, sys, tempfile, time, urllib.error, urllib.parse, urllib.request

REPO = os.environ.get("GH_BUILDS_REPO", "manishiitg/agentworks-builds")
API = os.environ.get("GH_API_URL", "https://api.github.com").rstrip("/")
UPLOAD = os.environ.get("GH_UPLOAD_URL", "https://uploads.github.com").rstrip("/")
KEEP = int(os.environ.get("BUILDS_KEEP_RELEASES", "8"))
TAG_RE = re.compile(r"^build-[0-9a-f]{8}-[0-9a-f]{8}-[0-9a-f]{8}(-arm64)?$")
ASSETS = ("build.tar.gz", "build-rts.tar.gz", "manifest.json", "SHA256SUMS")
MAX_ASSET = 2 * 1024**3 - 1
REPOS = ("mcp-agent-builder-go", "mcpagent", "multi-llm-provider-go")
# Left out of the copy RTS needs: the two library sources and the CLI downloads (RTS never used them). Same as ship_build_to_rts.
RTS_EXCLUDE = ("source/mcpagent", "source/multi-llm-provider-go", "downloads")


class Fatal(Exception):
    pass


def say(msg):
    print(msg, flush=True)


def token_file():
    return os.environ.get("BUILDS_ENV_FILE") or os.path.join(os.path.expanduser("~"), ".config", "agentworks", "builds.env")


def load_token():
    """GH_TOKEN from the environment, else from a private file. None when there is none; Fatal for a file others can read."""
    value = os.environ.get("GH_TOKEN", "").strip()
    if value:
        return value
    path = token_file()
    if not os.path.isfile(path):
        return None
    st = os.stat(path)
    if st.st_mode & 0o077:
        raise Fatal(f"refusing {path}: mode {st.st_mode & 0o777:04o} lets other users read the token; run: chmod 600 {path}")
    if st.st_uid != os.geteuid():
        raise Fatal(f"refusing {path}: it belongs to another user than the one running this")
    for line in open(path, encoding="utf-8"):
        line = line.strip()
        if line.startswith("export "):
            line = line[7:].strip()
        if line.startswith("GH_TOKEN="):
            return line[len("GH_TOKEN="):].strip().strip("'\"") or None
    return None


class Github:
    def __init__(self, token):
        self.token = token

    def headers(self, extra=None):
        h = {"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "agentworks-publish-build"}
        if self.token:
            h["Authorization"] = "Bearer " + self.token
        h.update(extra or {})
        return h

    def call(self, method, url, body=None, ok=(200, 201, 204), allow=()):
        """One API call with retries on 5xx and network errors. Returns (status, json or None); `allow` statuses are returned, others raise."""
        data = json.dumps(body).encode() if body is not None else None
        last = None
        for attempt in range(4):
            req = urllib.request.Request(url if url.startswith("http") else API + url, data=data, method=method, headers=self.headers({"Content-Type": "application/json"} if data else None))
            try:
                with urllib.request.urlopen(req, timeout=60) as resp:
                    raw = resp.read()
                    return resp.status, (json.loads(raw) if raw else None)
            except urllib.error.HTTPError as err:
                raw = err.read()
                if err.code in allow:
                    return err.code, (json.loads(raw) if raw[:1] in (b"{", b"[") else None)
                last = f"GitHub {method} {urllib.parse.urlsplit(url).path} -> HTTP {err.code}: {raw[:200].decode('utf-8', 'replace')}"
                if err.code < 500:
                    break
            except (urllib.error.URLError, OSError, http.client.HTTPException) as err:
                last = f"GitHub {method} {urllib.parse.urlsplit(url).path}: {err}"
            time.sleep(2 * (attempt + 1))
        raise Fatal(last)

    def releases(self):
        out, page = [], 1
        while True:
            _, chunk = self.call("GET", f"/repos/{REPO}/releases?per_page=100&page={page}")
            out += chunk
            if len(chunk) < 100:
                return out
            page += 1

    def upload(self, release_id, path, name):
        size = os.path.getsize(path)
        if size > MAX_ASSET:
            raise Fatal(f"{name} is {size} bytes, over the 2 GB asset limit")
        parts = urllib.parse.urlsplit(UPLOAD)
        conn_cls = http.client.HTTPSConnection if parts.scheme == "https" else http.client.HTTPConnection
        target = f"{parts.path}/repos/{REPO}/releases/{release_id}/assets?name={urllib.parse.quote(name)}"
        last = None
        for attempt in range(3):
            conn = conn_cls(parts.netloc, timeout=900)
            try:
                with open(path, "rb") as handle:
                    conn.request("POST", target, body=handle, headers=self.headers({"Content-Type": "application/octet-stream", "Content-Length": str(size)}))
                    resp = conn.getresponse()
                    raw = resp.read()
                if resp.status in (200, 201):
                    return
                last = f"upload of {name} -> HTTP {resp.status}: {raw[:200].decode('utf-8', 'replace')}"
                if resp.status < 500 and resp.status != 422:
                    break
            except (OSError, http.client.HTTPException) as err:
                last = f"upload of {name}: {err}"
            finally:
                conn.close()
            # a failed attempt can leave a half-registered asset under the same name: remove it before retrying
            self.drop_assets(release_id, only=name)
            time.sleep(3 * (attempt + 1))
        raise Fatal(last)

    def drop_assets(self, release_id, only=None):
        _, assets = self.call("GET", f"/repos/{REPO}/releases/{release_id}/assets?per_page=100")
        for asset in assets or []:
            if only is None or asset["name"] == only:
                self.call("DELETE", f"/repos/{REPO}/releases/assets/{asset['id']}", allow=(404,))


def preflight(gh):
    """Before the first upload: prove the token can really write to the builds repo, by creating and deleting a tiny DRAFT release (it needs
    Contents write; a draft makes no tag and is never visible). Writes only to the builds repo, never to another repository."""
    tag = "preflight-" + os.urandom(6).hex()
    status, rel = gh.call("POST", f"/repos/{REPO}/releases", {"tag_name": tag, "name": tag, "body": "write check", "draft": True, "prerelease": True, "make_latest": "false"}, allow=(403, 404))
    if status in (403, 404):
        raise Fatal(f"the token cannot write to {REPO}: it needs Contents: Read and write on that repository only")
    gh.call("DELETE", f"/repos/{REPO}/releases/{rel['id']}", allow=(404,))


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def read_revisions(build):
    revs = {}
    path = os.path.join(build, "SOURCE_REVISIONS")
    if not os.path.isfile(path):
        raise Fatal(f"{path} is missing: not a build folder")
    for line in open(path, encoding="utf-8"):
        key, _, value = line.strip().partition("=")
        if key in REPOS:
            revs[key] = value
    for repo in REPOS:
        if not re.fullmatch(r"[0-9a-f]{40}", revs.get(repo, "")):
            raise Fatal(f"SOURCE_REVISIONS has no valid revision for {repo}")
    return revs


def release_body(name, revs, manifest_sha):
    lines = [f"build: {name}"] + [f"{repo}={revs[repo]}" for repo in REPOS] + [f"manifest-sha256: {manifest_sha}"]
    return "\n".join(lines) + "\n"


def body_manifest(release):
    m = re.search(r"^manifest-sha256: ([0-9a-f]{64})$", release.get("body") or "", re.M)
    return m.group(1) if m else None


def complete(release, manifest_sha):
    got = {a["name"]: a for a in release.get("assets", []) if a.get("state") == "uploaded" and a.get("size", 0) > 0}
    return body_manifest(release) == manifest_sha and all(name in got for name in ASSETS)


def tar_gz(parent, name, dest, excludes=()):
    """tar | gzip of one folder into dest; the archive's top folder is the build name."""
    cmd = ["tar", "-C", parent] + [f"--exclude={name}/{e}" for e in excludes] + ["-cf", "-", name]
    env = dict(os.environ, COPYFILE_DISABLE="1")
    gz = [shutil.which("pigz") or "gzip", "-3"]
    with open(dest, "wb") as out:
        tar = subprocess.Popen(cmd, stdout=subprocess.PIPE, env=env)
        zip_ = subprocess.Popen(gz, stdin=tar.stdout, stdout=out)
        tar.stdout.close()
        if zip_.wait() != 0 or tar.wait() != 0:
            raise Fatal(f"could not archive {name}")


def publish(build):
    build = os.path.abspath(build)
    manifest = os.path.join(build, "manifest.json")
    if not os.path.isfile(manifest):
        raise Fatal(f"{manifest} is missing: not a finished build")
    name = os.path.basename(build)
    revs = read_revisions(build)
    with open(manifest, encoding="utf-8") as handle:
        arch = json.load(handle).get("arch")
    if arch not in ("x86_64", "aarch64"):
        raise Fatal(f"unsupported build architecture: {arch}")
    tag = "build-" + "-".join(revs[r][:8] for r in REPOS) + ("-arm64" if arch == "aarch64" else "")
    manifest_sha = sha256_file(manifest)
    token = load_token()
    gh = Github(token)
    status, release = gh.call("GET", f"/repos/{REPO}/releases/tags/{tag}", allow=(404,))
    if status == 404:
        release = None
    if not token:
        if release and complete(release, manifest_sha):
            say(f"build {name} is already on GitHub as {tag}")
            say(f"RELEASE_TAG={tag}")
        else:
            say(f"publish skipped: no GitHub token (put GH_TOKEN=... in {token_file()}, mode 600); servers will be sent this build the old way")
        return
    if release and complete(release, manifest_sha):
        say(f"build {name} is already published as {tag}")
    else:
        preflight(gh)
        parent = os.path.dirname(build)
        for stale in os.listdir(parent):
            if stale.startswith(".publish.") and time.time() - os.path.getmtime(os.path.join(parent, stale)) > 86400:
                shutil.rmtree(os.path.join(parent, stale), ignore_errors=True)
        tmp = tempfile.mkdtemp(prefix=".publish.", dir=parent)
        try:
            say(f"publishing build {name} as {tag}")
            tar_gz(parent, name, os.path.join(tmp, "build.tar.gz"))
            tar_gz(parent, name, os.path.join(tmp, "build-rts.tar.gz"), RTS_EXCLUDE)
            shutil.copy(manifest, os.path.join(tmp, "manifest.json"))
            with open(os.path.join(tmp, "SHA256SUMS"), "w") as sums:
                for asset in ASSETS[:3]:
                    sums.write(f"{sha256_file(os.path.join(tmp, asset))}  {asset}\n")
            body = release_body(name, revs, manifest_sha)
            if release is None:
                status, release = gh.call("POST", f"/repos/{REPO}/releases", {
                    "tag_name": tag, "name": tag, "body": body, "draft": False, "prerelease": True, "make_latest": "false"}, allow=(422,))
                if status == 422:  # created by someone else a moment ago
                    _, release = gh.call("GET", f"/repos/{REPO}/releases/tags/{tag}")
            if release.get("prerelease") is not True or release.get("body") != body:
                gh.call("PATCH", f"/repos/{REPO}/releases/{release['id']}", {"body": body, "prerelease": True, "make_latest": "false"})
            # A half-uploaded release is repaired by starting its assets over: SHA256SUMS goes last and completes it.
            gh.drop_assets(release["id"])
            for asset in ASSETS:
                gh.upload(release["id"], os.path.join(tmp, asset), asset)
                say(f"  uploaded {asset} ({os.path.getsize(os.path.join(tmp, asset)) // 1024} KB)")
            _, check = gh.call("GET", f"/repos/{REPO}/releases/{release['id']}")
            if not complete(check, manifest_sha):
                raise Fatal(f"release {tag} is not complete after the upload")
        finally:
            shutil.rmtree(tmp, ignore_errors=True)
    prune(gh, tag)
    say(f"RELEASE_TAG={tag}")


def prune(gh, current):
    """Keep the newest KEEP releases for this architecture; delete older releases and their tags."""
    builds = sorted((r for r in gh.releases() if TAG_RE.match(r["tag_name"]) and r["tag_name"].endswith("-arm64") == current.endswith("-arm64")), key=lambda r: r["created_at"], reverse=True)
    for old in builds[KEEP:]:
        if old["tag_name"] == current:
            continue
        gh.call("DELETE", f"/repos/{REPO}/releases/{old['id']}", allow=(404,))
        gh.call("DELETE", f"/repos/{REPO}/git/refs/tags/{urllib.parse.quote(old['tag_name'])}", allow=(404, 422))
        say(f"pruned old build release {old['tag_name']}")


def list_releases():
    gh = Github(None)
    rows = sorted((r for r in gh.releases() if TAG_RE.match(r["tag_name"])), key=lambda r: r["created_at"], reverse=True)
    if not rows:
        say(f"No build releases in github.com/{REPO}")
    for r in rows:
        manifest = body_manifest(r)
        state = "complete" if manifest and complete(r, manifest) else "INCOMPLETE"
        say(f"{r['tag_name']}  {r['created_at'][:16].replace('T', ' ')}  manifest {(manifest or '?')[:8]}  {state}")


def main(argv):
    try:
        if argv == ["--list"]:
            list_releases()
        elif len(argv) == 1 and not argv[0].startswith("-"):
            publish(argv[0])
        else:
            print("usage: publish-build.sh <build-dir> | --list", file=sys.stderr)
            return 2
    except Fatal as err:
        print(f"publish-build: {err}", file=sys.stderr)
        return 1
    return 0


sys.exit(main(sys.argv[1:]))
PY

"""A fake of the GitHub REST API and release downloads, just enough for publish-build.sh and fetch-build.sh tests (PLAT-426)."""
import json
import re
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit

REPO = "manishiitg/agentworks-builds"
TOKEN = "fake-token-for-tests-only"


class FakeGithub:
    def __init__(self, require_token=TOKEN):
        self.releases, self.tags, self.assets, self.log = [], set(), {}, []
        self.next_id = 1
        self.require_token = require_token
        self.deny_writes = False        # a token without Contents write: releases POST answers 404
        self.drafts_created = 0
        self.fail_upload_of = None      # asset name whose upload dies half way (once)
        self.download_mode = {}         # (tag, asset) -> "truncate-once" | "truncate-always"
        self.downloads = []
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), self._handler())
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def close(self):
        self.server.shutdown()
        self.server.server_close()

    def env(self):
        return {"GH_API_URL": self.url, "GH_UPLOAD_URL": self.url + "/upload", "BUILDS_DOWNLOAD_BASE": self.url + "/download"}

    def new_id(self):
        self.next_id += 1
        return self.next_id

    def release_json(self, rel):
        out = dict(rel)
        out["assets"] = [dict(a, id=i) for i, a in self.assets.items() if a["release"] == rel["id"]]
        for a in out["assets"]:
            a.pop("data", None)
        return out

    def seed(self, tag, created):
        rel = {"id": self.new_id(), "tag_name": tag, "name": tag, "body": "", "prerelease": True, "created_at": created}
        self.releases.append(rel)
        self.tags.add(tag)
        return rel

    def _handler(self):
        fake = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *a):
                pass

            def reply(self, code, body=None, raw=None, headers=None):
                data = raw if raw is not None else (json.dumps(body).encode() if body is not None else b"")
                self.send_response(code)
                self.send_header("Content-Length", str(len(data)))
                for k, v in (headers or {}).items():
                    self.send_header(k, v)
                self.end_headers()
                self.wfile.write(data)

            def authed(self, write):
                fake.log.append((self.command, self.path, "auth" if self.headers.get("Authorization") else "anon"))
                if write and fake.require_token and self.headers.get("Authorization") != f"Bearer {fake.require_token}":
                    self.reply(401, {"message": "Bad credentials"})
                    return False
                return True

            def body(self):
                n = int(self.headers.get("Content-Length") or 0)
                return self.rfile.read(n)

            def do_GET(self):
                parts = urlsplit(self.path)
                path = parts.path
                if path.startswith("/download/"):
                    return self.download(path)
                if not self.authed(False):
                    return
                if path == f"/repos/{REPO}/releases":
                    q = parse_qs(parts.query)
                    per, page = int(q["per_page"][0]), int(q["page"][0])
                    rows = [fake.release_json(r) for r in fake.releases]
                    return self.reply(200, rows[(page - 1) * per:page * per])
                m = re.fullmatch(rf"/repos/{REPO}/releases/tags/(.+)", path)
                if m:
                    for r in fake.releases:
                        if r["tag_name"] == m.group(1):
                            return self.reply(200, fake.release_json(r))
                    return self.reply(404, {"message": "Not Found"})
                m = re.fullmatch(rf"/repos/{REPO}/releases/(\d+)/assets", path)
                if m:
                    return self.reply(200, fake.release_json(next(r for r in fake.releases if r["id"] == int(m.group(1))))["assets"])
                m = re.fullmatch(rf"/repos/{REPO}/releases/(\d+)", path)
                if m:
                    return self.reply(200, fake.release_json(next(r for r in fake.releases if r["id"] == int(m.group(1)))))
                self.reply(404, {"message": "Not Found"})

            def do_POST(self):
                path = urlsplit(self.path).path
                if not self.authed(True):
                    return
                if path == f"/repos/{REPO}/releases":
                    req = json.loads(self.body())
                    if fake.deny_writes:
                        return self.reply(404, {"message": "Not Found"})
                    if req.get("draft"):
                        fake.drafts_created += 1
                        rel = {"id": fake.new_id(), "tag_name": req["tag_name"], "draft": True}
                        fake.drafts = getattr(fake, "drafts", []) + [rel]
                        return self.reply(201, rel)
                    if req["tag_name"] in {r["tag_name"] for r in fake.releases}:
                        return self.reply(422, {"message": "Validation Failed"})
                    assert req.get("make_latest") == "false" and req.get("prerelease") is True and req.get("draft") is False, req
                    rel = {"id": fake.new_id(), "tag_name": req["tag_name"], "name": req["name"], "body": req["body"],
                           "prerelease": True, "created_at": f"2026-10-04T10:{len(fake.releases):02d}:00Z"}
                    fake.releases.append(rel)
                    fake.tags.add(rel["tag_name"])
                    return self.reply(201, fake.release_json(rel))
                m = re.fullmatch(rf"/upload/repos/{REPO}/releases/(\d+)/assets", path)
                if m:
                    name = parse_qs(urlsplit(self.path).query)["name"][0]
                    rid, size = int(m.group(1)), int(self.headers["Content-Length"])
                    if any(a["release"] == rid and a["name"] == name for a in fake.assets.values()):
                        self.body()
                        return self.reply(422, {"message": "already_exists"})
                    if fake.fail_upload_of == name:      # dies half way: a half-registered asset stays behind
                        fake.fail_upload_of = None
                        self.rfile.read(size // 2)
                        fake.assets[fake.new_id()] = {"release": rid, "name": name, "state": "starter", "size": 0, "data": b""}
                        self.close_connection = True
                        return
                    data = self.rfile.read(size)
                    fake.assets[fake.new_id()] = {"release": rid, "name": name, "state": "uploaded", "size": len(data), "data": data}
                    return self.reply(201, {"name": name})
                self.reply(404, {"message": "Not Found"})

            def do_PATCH(self):
                if not self.authed(True):
                    return
                m = re.fullmatch(rf"/repos/{REPO}/releases/(\d+)", urlsplit(self.path).path)
                req = json.loads(self.body())
                for r in fake.releases:
                    if m and r["id"] == int(m.group(1)):
                        assert req.get("make_latest") == "false"
                        r.update({k: v for k, v in req.items() if k in ("body", "prerelease")})
                        return self.reply(200, fake.release_json(r))
                self.reply(404, {"message": "Not Found"})

            def do_DELETE(self):
                path = urlsplit(self.path).path
                if not self.authed(True):
                    return
                m = re.fullmatch(rf"/repos/{REPO}/releases/assets/(\d+)", path)
                if m:
                    fake.assets.pop(int(m.group(1)), None)
                    return self.reply(204)
                m = re.fullmatch(rf"/repos/{REPO}/releases/(\d+)", path)
                if m:
                    fake.drafts = [d for d in getattr(fake, "drafts", []) if d["id"] != int(m.group(1))]
                    fake.releases = [r for r in fake.releases if r["id"] != int(m.group(1))]
                    fake.assets = {i: a for i, a in fake.assets.items() if a["release"] != int(m.group(1))}
                    return self.reply(204)
                m = re.fullmatch(rf"/repos/{REPO}/git/refs/tags/(.+)", path)
                if m:
                    fake.tags.discard(m.group(1))
                    return self.reply(204)
                self.reply(404, {"message": "Not Found"})

            def download(self, path):
                _, _, tag, name = path.split("/", 3)
                fake.downloads.append((tag, name, self.headers.get("Range")))
                rel = next((r for r in fake.releases if r["tag_name"] == tag), None)
                asset = next((a for a in fake.assets.values() if rel and a["release"] == rel["id"] and a["name"] == name), None)
                if not asset:
                    return self.reply(404, {"message": "Not Found"})
                data, start = asset["data"], 0
                rng = self.headers.get("Range")
                mode = fake.download_mode.get((tag, name))
                if rng:
                    start = int(re.match(r"bytes=(\d+)-", rng).group(1))
                    if start >= len(data):
                        return self.reply(416)
                    self.send_response(206)
                    self.send_header("Content-Range", f"bytes {start}-{len(data) - 1}/{len(data)}")
                else:
                    self.send_response(200)
                body = data[start:]
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                if mode == "truncate-always" or (mode == "truncate-once" and not rng):
                    self.wfile.write(body[: len(body) // 2])
                    self.wfile.flush()
                    self.close_connection = True
                    return
                self.wfile.write(body)

        return Handler

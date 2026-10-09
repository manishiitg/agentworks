"""PLAT-426: publish-build.sh and fetch-build.sh against a local fake of the GitHub API (no network, no real token)."""
from pathlib import Path
import hashlib
import json
import os
import stat
import subprocess
import tarfile
import tempfile
import unittest

from fake_github import FakeGithub, TOKEN

COMMON = Path(__file__).resolve().parent
PUBLISH = COMMON / "publish-build.sh"
FETCH = COMMON / "fetch-build.sh"
REVS = {"mcp-agent-builder-go": "a" * 40, "mcpagent": "b" * 40, "multi-llm-provider-go": "c" * 40}
TAG = "build-aaaaaaaa-bbbbbbbb-cccccccc"


def make_build(parent, revs=REVS, payload="one", arch="x86_64"):
    name = revs["mcp-agent-builder-go"][:8] + "-20261004000000"
    d = Path(parent) / name
    (d / "bin").mkdir(parents=True)
    (d / "source/mcpagent").mkdir(parents=True)
    (d / "source/multi-llm-provider-go").mkdir(parents=True)
    (d / "source/mcp-agent-builder-go").mkdir(parents=True)
    (d / "downloads").mkdir()
    (d / "bin/agent").write_text(payload)
    (d / "source/mcpagent/lib.go").write_text("package x")
    (d / "source/multi-llm-provider-go/p.go").write_text("package y")
    (d / "source/mcp-agent-builder-go/main.go").write_text("package z")
    (d / "downloads/cli.tgz").write_text("cli")
    (d / "SOURCE_REVISIONS").write_text("".join(f"{k}={v}\n" for k, v in revs.items()))
    (d / "manifest.json").write_text(json.dumps({"schema": 1, "name": name, "revisions": revs, "arch": arch, "payload": payload}))
    return d


class Base(unittest.TestCase):
    def setUp(self):
        self.gh = FakeGithub()
        self.addCleanup(self.gh.close)
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)
        (self.root / "builds").mkdir()
        self.build = make_build(self.root / "builds")
        self.home = self.root / "home"
        self.home.mkdir()

    def env_file(self, mode=0o600, token=TOKEN):
        d = self.home / ".config/agentworks"
        d.mkdir(parents=True, exist_ok=True)
        f = d / "builds.env"
        f.write_text(f"GH_TOKEN={token}\n")
        f.chmod(mode)
        return f

    def run_script(self, script, *args, env=None):
        base = {k: v for k, v in os.environ.items() if k not in ("GH_TOKEN", "GITHUB_TOKEN")}
        base.update(self.gh.env(), HOME=str(self.home))
        base.update(env or {})
        return subprocess.run(["bash", str(script), *args], capture_output=True, text=True, env=base)

    def publish(self, build=None, **kw):
        return self.run_script(PUBLISH, str(build or self.build), **kw)

    def names(self, tag=TAG):
        rel = next(r for r in self.gh.releases if r["tag_name"] == tag)
        return sorted(a["name"] for a in self.gh.assets.values() if a["release"] == rel["id"] and a["state"] == "uploaded")


class PublishTest(Base):
    def test_creates_the_release_with_all_assets_and_never_marks_it_latest(self):
        self.env_file()
        r = self.publish()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(f"RELEASE_TAG={TAG}", r.stdout)
        self.assertEqual(self.names(), ["SHA256SUMS", "build-rts.tar.gz", "build.tar.gz", "manifest.json"])
        rel = self.gh.releases[0]
        self.assertTrue(rel["prerelease"])
        manifest_sha = hashlib.sha256((self.build / "manifest.json").read_bytes()).hexdigest()
        self.assertIn(f"manifest-sha256: {manifest_sha}", rel["body"])
        for repo, rev in REVS.items():
            self.assertIn(f"{repo}={rev}", rel["body"])
        data = {a["name"]: a["data"] for a in self.gh.assets.values()}
        self.assertEqual(data["manifest.json"], (self.build / "manifest.json").read_bytes())
        sums = dict(reversed(line.split("  ")) for line in data["SHA256SUMS"].decode().splitlines())
        for name in ("build.tar.gz", "build-rts.tar.gz", "manifest.json"):
            self.assertEqual(sums[name + "\n" if False else name], hashlib.sha256(data[name]).hexdigest())
        self.assertNotIn(TOKEN, r.stdout + r.stderr)

    def archive_names(self, asset):
        path = self.root / asset
        path.write_bytes(next(a["data"] for a in self.gh.assets.values() if a["name"] == asset))
        with tarfile.open(path) as t:
            return t.getnames()

    def test_full_archive_holds_everything_and_the_rts_one_leaves_out_sources_and_downloads(self):
        self.env_file()
        self.publish()
        name = self.build.name
        full, rts = self.archive_names("build.tar.gz"), self.archive_names("build-rts.tar.gz")
        self.assertIn(f"{name}/manifest.json", full)
        self.assertIn(f"{name}/source/mcpagent/lib.go", full)
        self.assertIn(f"{name}/downloads/cli.tgz", full)
        self.assertIn(f"{name}/manifest.json", rts)
        self.assertIn(f"{name}/bin/agent", rts)
        self.assertIn(f"{name}/source/mcp-agent-builder-go/main.go", rts)
        self.assertFalse([n for n in rts if "/source/mcpagent" in n or "/source/multi-llm-provider-go" in n or "/downloads" in n], rts)

    def test_rerun_is_idempotent(self):
        self.env_file()
        self.publish()
        uploads = [e for e in self.gh.log if e[0] == "POST" and "/upload/" in e[1]]
        r = self.publish()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("already published", r.stdout)
        self.assertEqual([e for e in self.gh.log if e[0] == "POST" and "/upload/" in e[1]], uploads)
        self.assertEqual(len(self.gh.releases), 1)

    def test_arm_release_does_not_replace_or_prune_the_x86_release(self):
        self.env_file()
        self.assertEqual(self.publish().returncode, 0)
        x86_assets = {key: a["data"] for key, a in self.gh.assets.items()}
        arm = make_build(self.root / "arm", arch="aarch64")
        result = self.publish(arm, env={"BUILDS_KEEP_RELEASES": "1"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"RELEASE_TAG={TAG}-arm64", result.stdout)
        self.assertEqual({r["tag_name"] for r in self.gh.releases}, {TAG, TAG + "-arm64"})
        self.assertTrue(all(self.gh.assets[key]["data"] == data for key, data in x86_assets.items()))
        # Downloading an ARM tag must also pass the transport's tag validation.
        fetched = self.root / "fetched-arm"
        digest = hashlib.sha256((arm / "manifest.json").read_bytes()).hexdigest()
        result = self.run_script(FETCH, TAG + "-arm64", "build.tar.gz", digest, str(fetched))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads((fetched / "manifest.json").read_text())["arch"], "aarch64")

    def test_half_uploaded_release_is_repaired(self):
        self.env_file()
        self.gh.fail_upload_of = "build-rts.tar.gz"
        r = self.publish()
        self.assertEqual(r.returncode, 0, r.stderr)           # the retry inside the run already repairs a dropped upload
        self.assertEqual(self.names(), ["SHA256SUMS", "build-rts.tar.gz", "build.tar.gz", "manifest.json"])
        # and a release left incomplete by an earlier run (an asset missing) is completed by the next run
        rel = self.gh.releases[0]
        victim = next(i for i, a in self.gh.assets.items() if a["name"] == "SHA256SUMS")
        del self.gh.assets[victim]
        r = self.publish()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn("already published", r.stdout)
        self.assertEqual(self.names(), ["SHA256SUMS", "build-rts.tar.gz", "build.tar.gz", "manifest.json"])
        self.assertEqual(len(self.gh.releases), 1)
        self.assertEqual(rel["id"], self.gh.releases[0]["id"])

    def test_rebuild_of_the_same_revisions_replaces_the_assets(self):
        self.env_file()
        self.publish()
        other = make_build(self.root / "builds2", payload="two")
        r = self.publish(other)
        self.assertEqual(r.returncode, 0, r.stderr)
        manifest = next(a["data"] for a in self.gh.assets.values() if a["name"] == "manifest.json")
        self.assertEqual(manifest, (other / "manifest.json").read_bytes())
        self.assertEqual(len(self.gh.releases), 1)

    def test_prunes_to_the_newest_eight_releases_and_deletes_their_tags(self):
        self.env_file()
        for i in range(10):
            self.gh.seed(f"build-{i:08x}-{i:08x}-{i:08x}", f"2026-09-{10 + i:02d}T00:00:00Z")
        self.gh.seed("not-a-build-release", "2020-01-01T00:00:00Z")
        r = self.publish()
        self.assertEqual(r.returncode, 0, r.stderr)
        builds = [x["tag_name"] for x in self.gh.releases if x["tag_name"].startswith("build-")]
        self.assertEqual(len(builds), 8)
        self.assertIn(TAG, builds)
        self.assertNotIn("build-00000000-00000000-00000000", builds)
        self.assertNotIn("build-00000000-00000000-00000000", self.gh.tags)
        self.assertIn("not-a-build-release", [x["tag_name"] for x in self.gh.releases])

    def test_token_file_with_loose_permissions_is_refused_and_nothing_is_called(self):
        for mode in (0o644, 0o640, 0o604):
            self.env_file(mode)
            r = self.publish()
            self.assertNotEqual(r.returncode, 0)
            self.assertIn("refusing", r.stderr)
            self.assertIn("chmod 600", r.stderr)
            self.assertNotIn(TOKEN, r.stdout + r.stderr)
        self.assertEqual(self.gh.log, [])

    def test_without_a_token_it_skips_with_one_message_and_exits_zero(self):
        r = self.publish()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("publish skipped: no GitHub token", r.stdout)
        self.assertEqual(len([l for l in r.stdout.splitlines() if l.strip()]), 1)
        self.assertNotIn("RELEASE_TAG", r.stdout)
        self.assertEqual([e for e in self.gh.log if e[0] != "GET"], [])

    def test_without_a_token_an_already_published_build_is_still_reported(self):
        self.env_file()
        self.publish()
        os.remove(self.home / ".config/agentworks/builds.env")
        r = self.publish()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(f"RELEASE_TAG={TAG}", r.stdout)

    def test_token_from_the_environment_works_and_is_never_printed(self):
        r = self.publish(env={"GH_TOKEN": TOKEN})
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertNotIn(TOKEN, r.stdout + r.stderr)

    def test_a_rejected_token_fails_clearly_without_echoing_it(self):
        self.env_file(token="wrong-token")
        r = self.publish()
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("HTTP 401", r.stderr)
        self.assertNotIn("wrong-token", r.stdout + r.stderr)

    def test_a_write_preflight_runs_before_the_first_upload_and_leaves_no_draft(self):
        self.env_file()
        r = self.publish()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.gh.drafts_created, 1)
        self.assertEqual(getattr(self.gh, "drafts", []), [])
        first_upload = next(i for i, e in enumerate(self.gh.log) if "/upload/" in e[1])
        self.assertTrue(any(e[0] == "POST" and e[1].endswith("/releases") for e in self.gh.log[:first_upload]))
        self.gh.log.clear()
        self.assertEqual(self.publish().returncode, 0)      # nothing to upload: no write at all
        self.assertEqual([e for e in self.gh.log if e[0] != "GET"], [])
        self.assertTrue(all(p == f"/repos/manishiitg/agentworks-builds" or p.startswith("/repos/manishiitg/agentworks-builds/") or p.startswith("/upload/repos/manishiitg/agentworks-builds/") for _, p, _ in self.gh.log))

    def test_a_token_that_cannot_write_is_refused_before_anything_is_uploaded(self):
        self.env_file()
        self.gh.deny_writes = True
        r = self.publish()
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("the token cannot write to manishiitg/agentworks-builds: it needs Contents: Read and write on that repository only", r.stderr)
        self.assertEqual([e for e in self.gh.log if "/upload/" in e[1]], [])
        self.assertEqual(self.gh.releases, [])
        self.assertNotIn(TOKEN, r.stdout + r.stderr)

    def test_list_is_anonymous(self):
        self.env_file()
        self.publish()
        before = len(self.gh.log)
        r = self.run_script(PUBLISH, "--list")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(TAG, r.stdout)
        self.assertIn("complete", r.stdout)
        self.assertTrue(all(e[2] == "anon" for e in self.gh.log[before:]))


class FetchTest(Base):
    def setUp(self):
        super().setUp()
        self.env_file()
        self.assertEqual(self.publish().returncode, 0)
        self.gh.log, self.gh.downloads = [], []
        self.sha = hashlib.sha256((self.build / "manifest.json").read_bytes()).hexdigest()
        self.dest = self.root / "server/job/build"

    def fetch(self, asset="build-rts.tar.gz", sha=None, tag=TAG):
        return self.run_script(FETCH, tag, asset, sha or self.sha, str(self.dest))

    def leftovers(self):
        parent = self.dest.parent
        return sorted(p.name for p in parent.iterdir()) if parent.exists() else []

    def test_good_download_lands_in_dest_and_matches_the_build(self):
        r = self.fetch()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual((self.dest / "manifest.json").read_bytes(), (self.build / "manifest.json").read_bytes())
        self.assertTrue((self.dest / "bin/agent").is_file())
        self.assertFalse((self.dest / "downloads").exists())
        self.assertEqual(self.leftovers(), ["build"])
        self.assertTrue(all(d[2] is None for d in self.gh.downloads))
        self.assertTrue(all(e[2] == "anon" for e in self.gh.log))  # no credential is sent anywhere

    def test_full_asset_has_the_downloads_too(self):
        r = self.fetch("build.tar.gz")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue((self.dest / "downloads/cli.tgz").is_file())

    def test_hash_mismatch_is_refused_and_leaves_nothing_behind(self):
        r = self.fetch(sha="0" * 64)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("REFUSING", r.stderr)
        self.assertFalse(self.dest.exists())
        self.assertEqual(self.leftovers(), [])

    def test_tampered_manifest_in_the_download_is_refused(self):
        for a in self.gh.assets.values():
            if a["name"] == "build-rts.tar.gz":
                src = self.root / "t.tar.gz"
                src.write_bytes(a["data"])
                ex = self.root / "ex"
                ex.mkdir()
                with tarfile.open(src) as t:
                    t.extractall(ex)
                (ex / self.build.name / "manifest.json").write_text("{}")
                with tarfile.open(src, "w:gz") as t:
                    t.add(ex / self.build.name, arcname=self.build.name)
                a["data"], a["size"] = src.read_bytes(), src.stat().st_size
        r = self.fetch()
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("REFUSING", r.stderr)
        self.assertEqual(self.leftovers(), [])

    def test_truncated_download_is_resumed(self):
        self.gh.download_mode[(TAG, "build-rts.tar.gz")] = "truncate-once"
        r = self.fetch()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(any(d[2] and d[2].startswith("bytes=") for d in self.gh.downloads), self.gh.downloads)
        self.assertEqual((self.dest / "manifest.json").read_bytes(), (self.build / "manifest.json").read_bytes())
        self.assertEqual(self.leftovers(), ["build"])

    def test_download_that_stays_truncated_fails_and_leaves_nothing(self):
        self.gh.download_mode[(TAG, "build-rts.tar.gz")] = "truncate-always"
        r = self.fetch()
        self.assertNotEqual(r.returncode, 0)
        self.assertFalse(self.dest.exists())
        self.assertEqual(self.leftovers(), [])

    def test_missing_release_fails_at_once_and_bad_arguments_are_refused(self):
        r = self.fetch(tag="build-11111111-22222222-33333333")
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(len([d for d in self.gh.downloads if d[0] == "build-11111111-22222222-33333333"]), 1)
        self.assertEqual(self.leftovers(), [])
        for args in (("bad;tag", "build.tar.gz", self.sha), (TAG, "evil.tar.gz", self.sha), (TAG, "build.tar.gz", "abc")):
            r = self.run_script(FETCH, *args, str(self.dest))
            self.assertEqual(r.returncode, 2, args)

    def test_existing_dest_is_refused(self):
        self.dest.mkdir(parents=True)
        r = self.fetch()
        self.assertEqual(r.returncode, 2)
        self.assertEqual(list(self.dest.iterdir()), [])


if __name__ == "__main__":
    unittest.main()

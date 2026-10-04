"""PLAT-426: a prebuilt release is refused unless its manifest matches this host and every file."""
from pathlib import Path
import contextlib
import importlib.util
import io
import os
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("release_manifest", HERE / "release_manifest.py")
manifest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(manifest)

REVS = {"mcp-agent-builder-go": "a" * 40, "mcpagent": "b" * 40, "multi-llm-provider-go": "c" * 40}


def make_build(root, name="aaaaaaaa-20261004000000"):
    root = Path(root) / name
    (root / "bin/lib").mkdir(parents=True)
    (root / "frontend/assets").mkdir(parents=True)
    (root / "bin/agent").write_bytes(b"\x7fELF-agent")
    (root / "bin/agent").chmod(0o755)
    (root / "bin/lib/libfake.so").write_bytes(b"lib")
    (root / "frontend/index.html").write_text("<html></html>")
    (root / "frontend/assets/app.js").write_text("x=1")
    (root / "source/mcpagent").mkdir(parents=True)
    (root / "source/mcpagent/main.go").write_text("package main")
    os.symlink("app.js", root / "frontend/assets/latest.js")
    (root / "SOURCE_REVISIONS").write_text("".join(f"{k}={v}\n" for k, v in REVS.items()))
    manifest.create(root, name, REVS, build_seconds=42)
    return root


class ManifestTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.tmp = Path(self._tmp.name)
        self.build = make_build(self.tmp)
        # the test host may not be glibc: pin the manifest's value so the checks below are deterministic
        data = manifest.load(self.build)
        data["glibc"] = "2.39"
        data["arch"] = "x86_64"
        data["os"] = "linux"
        (self.build / "manifest.json").write_text(__import__("json").dumps(data))
        self.host = dict(arch="x86_64", glibc="2.39")

    def verify(self, **kw):
        args = dict(self.host)
        args.update(kw)
        return manifest.verify(self.build, **args)

    def refused(self, fragment, **kw):
        with self.assertRaises(manifest.Refused) as caught:
            self.verify(**kw)
        self.assertIn(fragment, str(caught.exception))

    def test_intact_build_verifies_and_records_revisions(self):
        data = self.verify()
        self.assertEqual(data["revisions"], REVS)
        self.assertEqual(data["build_seconds"], 42)
        self.assertIn("bin/agent", data["files"])
        self.assertEqual(data["symlinks"], {"frontend/assets/latest.js": "app.js"})

    def test_hash_mismatch_is_refused(self):
        (self.build / "frontend/assets/app.js").write_text("x=2")  # same size, other content
        self.refused("hash mismatch for frontend/assets/app.js")

    def test_size_mismatch_is_refused(self):
        (self.build / "bin/lib/libfake.so").write_bytes(b"longer")
        self.refused("size mismatch")

    def test_missing_file_is_refused(self):
        (self.build / "bin/lib/libfake.so").unlink()
        self.refused("missing file bin/lib/libfake.so")

    def test_unlisted_file_is_refused(self):
        (self.build / "bin/extra").write_text("surprise")
        self.refused("unlisted file bin/extra")

    def test_lost_executable_bit_is_refused(self):
        (self.build / "bin/agent").chmod(0o644)
        self.refused("executable bit")

    def test_changed_symlink_is_refused(self):
        (self.build / "frontend/assets/latest.js").unlink()
        os.symlink("elsewhere.js", self.build / "frontend/assets/latest.js")
        self.refused("points somewhere else")

    def test_wrong_architecture_is_refused(self):
        self.refused("build is for x86_64, this host is aarch64", arch="aarch64")

    def test_build_for_another_os_is_refused(self):
        data = manifest.load(self.build)
        data["os"] = "darwin"
        (self.build / "manifest.json").write_text(__import__("json").dumps(data))
        self.refused("not linux")

    def test_older_glibc_is_refused_newer_is_accepted(self):
        self.refused("needs glibc 2.39 or newer, this host has 2.35", glibc="2.35")
        self.verify(glibc="2.39")
        self.verify(glibc="2.41")
        self.refused("cannot read this host's glibc", glibc="")

    def test_manifest_hash_announced_by_the_build_host_is_checked(self):
        good = manifest.sha256_file(self.build / "manifest.json")
        self.verify(expected_manifest_sha256=good)
        self.refused("does not match the hash announced", expected_manifest_sha256="0" * 64)

    def test_missing_manifest_is_refused(self):
        (self.build / "manifest.json").unlink()
        self.refused("not a complete build")

    def test_trimmed_copy_needs_an_explicit_skip_prefix(self):
        (self.build / "source/mcpagent/main.go").unlink()
        self.refused("missing file source/mcpagent/main.go")
        self.verify(skip_prefixes=("source/mcpagent/",))
        # a skipped prefix does not excuse a file that is present but changed
        (self.build / "source/mcpagent/main.go").write_text("package mxin")
        self.refused("hash mismatch for source/mcpagent/main.go", skip_prefixes=("source/mcpagent/",))

    def test_cli_exit_codes(self):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            self.assertEqual(manifest.main(["verify", str(self.build), "--host-arch", "x86_64", "--host-glibc", "2.39"]), 0)
            (self.build / "bin/agent").write_bytes(b"tampered!!")
            self.assertEqual(manifest.main(["verify", str(self.build), "--host-arch", "x86_64", "--host-glibc", "2.39"]), 1)
        self.assertIn("REFUSED", err.getvalue())


class FindBuildTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)
        self.old = make_build(self.root, "11111111-20261001000000")
        revs = {"mcp-agent-builder-go": "2" * 40, "mcpagent": "b" * 40, "multi-llm-provider-go": "c" * 40}
        self.new = self.root / "22222222-20261004000000"
        self.new.mkdir()
        (self.new / "f").write_text("x")
        manifest.create(self.new, self.new.name, revs)

    def test_find_by_name_prefix_and_revision_returns_the_three_revisions(self):
        for query in ("11111111-20261001000000", "11111111", "aaaaaaaaaa"):
            name, shas = manifest.find_build(self.root, query)
            self.assertEqual(name, "11111111-20261001000000")
            self.assertEqual(shas, [REVS["mcp-agent-builder-go"], REVS["mcpagent"], REVS["multi-llm-provider-go"]])

    def test_ambiguous_unknown_and_short_queries_are_refused(self):
        for query in ("bbbbbbbbbb", "ffffffffff", "11"):  # the mcpagent revision is shared by both builds
            with self.subTest(query=query), self.assertRaises(manifest.Refused):
                manifest.find_build(self.root, query)

    def test_list_marks_pinned_builds(self):
        (self.root / ".pinned").mkdir()
        (self.root / ".pinned" / self.old.name).touch()
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            manifest.main(["list", str(self.root)])
        lines = {line.split()[0]: line for line in out.getvalue().splitlines()[1:]}
        self.assertTrue(lines[self.old.name].endswith("pinned"))
        self.assertFalse(lines[self.new.name].endswith("pinned"))


if __name__ == "__main__":
    unittest.main()

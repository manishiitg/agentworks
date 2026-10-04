"""build-release.sh --prune-only (PLAT-426): old builds are removed by default; the newest, pinned and very recent ones stay."""
import os
import platform
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("build-release.sh")


@unittest.skipUnless(platform.system() == "Linux" and platform.machine() == "x86_64", "build-release.sh runs on Linux x86_64 only")
class PruneBuildsTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)

    def make(self, name, age_seconds):
        build = self.root / name
        build.mkdir()
        (build / "manifest.json").write_text("{}")
        (build / "marker").write_text("x")
        stamp = time.time() - age_seconds
        os.utime(build / "manifest.json", (stamp, stamp))
        return build

    def prune(self, *extra):
        result = subprocess.run(["bash", str(SCRIPT), "--prune-only", "--builds-dir", str(self.root), *extra], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result.stdout

    def remaining(self):
        return sorted(d.name for d in self.root.iterdir() if d.is_dir() and not d.name.startswith("."))

    def test_default_keeps_only_the_newest_pinned_and_very_recent_builds(self):
        hours = 7200
        self.make("aaaaaaaa-20260101000000", hours)
        self.make("bbbbbbbb-20260102000000", hours)
        self.make("cccccccc-20260103000000", hours)       # pinned
        self.make("dddddddd-20260104000000", 300)         # 5 minutes old: an activation may still be reading it
        self.make("eeeeeeee-20260105000000", 10)          # the newest
        (self.root / ".pinned").mkdir()
        (self.root / ".pinned" / "cccccccc-20260103000000").touch()
        out = self.prune()
        self.assertEqual(self.remaining(), ["cccccccc-20260103000000", "dddddddd-20260104000000", "eeeeeeee-20260105000000"])
        self.assertIn("pruning old build aaaaaaaa-20260101000000", out)
        self.assertIn("pruning old build bbbbbbbb-20260102000000", out)

    def test_old_builds_go_once_they_are_past_the_recent_window(self):
        self.make("aaaaaaaa-20260101000000", 7200)
        self.make("bbbbbbbb-20260102000000", 7200)
        self.prune()
        self.assertEqual(self.remaining(), ["bbbbbbbb-20260102000000"])

    def test_keep_can_be_raised_and_other_folders_are_left_alone(self):
        self.make("aaaaaaaa-20260101000000", 7200)
        self.make("bbbbbbbb-20260102000000", 7200)
        (self.root / "not-a-build").mkdir()
        (self.root / "not-a-build" / "file").write_text("x")
        self.prune("--keep", "2")
        self.assertEqual(self.remaining(), ["aaaaaaaa-20260101000000", "bbbbbbbb-20260102000000", "not-a-build"])

    def test_a_directory_without_a_manifest_is_never_removed(self):
        (self.root / "cccccccc-20260103000000").mkdir()   # looks like a build, has no manifest: maybe being written
        self.make("aaaaaaaa-20260101000000", 7200)
        self.make("bbbbbbbb-20260102000000", 7200)
        self.prune()
        self.assertEqual(self.remaining(), ["bbbbbbbb-20260102000000", "cccccccc-20260103000000"])


if __name__ == "__main__":
    unittest.main()

"""#3035: both build launchers refuse a new build before it exhausts the host."""
import contextlib
import io
import os
import subprocess
from pathlib import Path
import shutil
import sys
import unittest
from unittest.mock import patch


class BuildDiskCheckTest(unittest.TestCase):
    def test_build_and_verify_warn_and_refuse_before_compilation(self):
        for name in ("build-release.sh", "verify-change.sh"):
            source = Path(__file__).with_name(name).read_text()
            code = source.split("<<'DISK_CHECK'\n", 1)[1].split("\nDISK_CHECK", 1)[0]
            # The launcher must invoke the guard, not just define it.
            self.assertIn("\n  check_build_disk\n", source)
            for used, free, warning, status in (
                (60, 40, False, None),
                (85, 15, True, None),
                (90, 10, True, None),
                (91, 9, True, 2),
                (100, 0, True, 2),
            ):
                with self.subTest(launcher=name, free=free):
                    out = io.StringIO()
                    usage = shutil._ntuple_diskusage(100 * 1024**3, used * 1024**3, free * 1024**3)
                    with patch.dict(os.environ, {}, clear=True), patch.object(sys, "argv", [name, "example-build-root"]), patch("shutil.disk_usage", return_value=usage), contextlib.redirect_stderr(out):
                        if status is None:
                            exec(compile(code, name, "exec"), {})
                        else:
                            with self.assertRaises(SystemExit) as caught:
                                exec(compile(code, name, "exec"), {})
                            self.assertEqual(caught.exception.code, status)
                    self.assertEqual("WARNING:" in out.getvalue(), warning)
                    self.assertEqual("Build not started:" in out.getvalue(), status is not None)

    def test_operator_override_is_forwarded_validated_and_logged(self):
        helper = str(Path(__file__).with_name("build-once.sh"))
        for value, okay in (("0", True), ("2.5", True), ("10", True), ("", False), ("-1", False), ("nan", False), ("1; echo injected", False)):
            with self.subTest(value=value):
                env = dict(os.environ, BUILD_DISK_MIN_FREE_GIB=value)
                result = subprocess.run(["bash", "-c", 'source "$1"; build_disk_override_arg', "test", helper], env=env, text=True, capture_output=True)
                self.assertEqual(result.returncode == 0, okay, result.stderr)
                if okay:
                    self.assertEqual(result.stdout, " --setenv=BUILD_DISK_MIN_FREE_GIB=" + value)
        for name in ("build-release.sh", "verify-change.sh"):
            source = Path(__file__).with_name(name).read_text()
            code = source.split("<<'DISK_CHECK'\n", 1)[1].split("\nDISK_CHECK", 1)[0]
            out = io.StringIO()
            usage = shutil._ntuple_diskusage(100 * 1024**3, 98 * 1024**3, 2 * 1024**3)
            with patch.dict(os.environ, {"BUILD_DISK_MIN_FREE_GIB": "1"}, clear=True), patch.object(sys, "argv", [name, "example-build-root"]), patch("shutil.disk_usage", return_value=usage), contextlib.redirect_stderr(out):
                exec(compile(code, name, "exec"), {})
            self.assertIn("event=operator_override minimum_free_gib=1", out.getvalue())
            self.assertIn("WARNING:", out.getvalue())
            self.assertNotIn("Build not started:", out.getvalue())


if __name__ == "__main__":
    unittest.main()

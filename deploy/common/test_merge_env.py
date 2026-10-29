"""The host .env follows the product's managed environment, including removals (PLAT-789)."""
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from merge_env import merge, parse_entries  # noqa: E402


class MergeEnvTest(unittest.TestCase):
    def test_updates_listed_keys_and_appends_new_ones(self):
        managed = parse_entries("/bin", ["A=2", "NEW=x"])
        out = merge("A=1\nOTHER=keep\nPATH=old\n", managed, set())
        self.assertEqual(out, "A=2\nOTHER=keep\nPATH=/bin\nNEW=x\n")

    def test_a_key_the_deploy_wrote_before_and_no_longer_lists_is_removed(self):
        managed = parse_entries("/bin", ["A=1"])
        out = merge("A=1\nWHATSAPP_DEBUG=true\nPATH=/bin\n", managed, {"A", "PATH", "WHATSAPP_DEBUG"})
        self.assertNotIn("WHATSAPP_DEBUG", out)
        self.assertIn("A=1", out)

    def test_a_line_added_by_hand_is_never_removed(self):
        managed = parse_entries("/bin", ["A=1"])
        out = merge("A=1\nBY_HAND=1\nPATH=/bin\n", managed, {"A", "PATH"})
        self.assertIn("BY_HAND=1", out)

    def test_the_first_run_has_no_history_and_removes_nothing(self):
        managed = parse_entries("/bin", ["A=1"])
        out = merge("STALE=1\nA=0\nPATH=x\n", managed, set())
        self.assertIn("STALE=1", out)

    def test_secrets_come_from_a_file_and_an_empty_path_is_left_alone(self):
        # The AWS host keeps PATH in its unit files and passes its global secrets as a file, never on a command line.
        import tempfile
        from merge_env import main
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            (tmp / ".env").write_text("PATH=/unit\nTOKEN=old\n")
            (tmp / "secrets").write_text("TOKEN=new\n\n")
            main([str(tmp / ".env"), str(tmp / "out"), str(tmp / "keys"), "", "--from-file", str(tmp / "secrets"), "A=1"])
            self.assertEqual((tmp / "out").read_text(), "PATH=/unit\nTOKEN=new\nA=1\n")
            self.assertEqual((tmp / "keys").read_text().split(), ["A", "TOKEN"])

    def test_first_run_removes_a_global_secret_no_longer_provided(self):
        # The old AWS deploy stripped every GLOBAL_SECRET_* line before writing the current set; a deleted global secret
        # must leave .env at the next deploy even before .env.managed-keys exists. Without --from-file (rootless) it stays.
        import tempfile
        from merge_env import main
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            (tmp / ".env").write_text("GLOBAL_SECRET_OLD=x\nGLOBAL_SECRET_KEEP=1\nBY_HAND=1\n")
            (tmp / "secrets").write_text("GLOBAL_SECRET_KEEP=2\n")
            main([str(tmp / ".env"), str(tmp / "out"), str(tmp / "keys"), "", "--from-file", str(tmp / "secrets")])
            self.assertEqual((tmp / "out").read_text(), "GLOBAL_SECRET_KEEP=2\nBY_HAND=1\n")
            main([str(tmp / ".env"), str(tmp / "rootless"), str(tmp / "keys2"), "/bin", "A=1"])
            self.assertIn("GLOBAL_SECRET_OLD=x", (tmp / "rootless").read_text())

    def test_a_duplicated_managed_key_is_written_once(self):
        managed = parse_entries("/bin", ["A=1"])
        out = merge("A=0\nA=9\nPATH=/bin\n", managed, set())
        self.assertEqual(out.count("A="), 1)


if __name__ == "__main__":
    unittest.main()

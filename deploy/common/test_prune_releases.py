import importlib.util
from pathlib import Path
import tempfile
import time
import os
import unittest
from unittest.mock import patch
from io import BytesIO
import runpy

spec = importlib.util.spec_from_file_location('prune', Path(__file__).with_name('prune-releases.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ReleaseCleanupTest(unittest.TestCase):
    def test_confida_incomplete_release_and_latest_staged_candidate(self):
        with tempfile.TemporaryDirectory() as tmp:
            app = Path(tmp).resolve()
            names = ['confida-abcdef0-20260910120000', 'confida-abcdef1-20260910120000', 'confida-abcdef2-20260910120000']
            for name in names:
                (app / 'releases' / name).mkdir(parents=True)
            (app / 'current').symlink_to(app / 'releases' / names[0])
            proc = app / 'proc'
            proc.mkdir()
            self.assertEqual(module.prune(app, apply=True, proc=proc, keep=[names[2]]), [names[1]])
            self.assertTrue((app / 'releases' / names[2]).exists())
            self.assertEqual(module.prune(app, apply=True, proc=proc), [names[2]])
            with self.assertRaises(ValueError):
                module.prune(app, apply=True, proc=proc, keep=['../data'])

    def test_unreadable_process_path_does_not_abort_cleanup_and_newest_are_kept(self):
        # Server A 2026-10-05: one unreadable /proc/<pid>/root of another account aborted every cleanup, so 27 releases
        # (34 GB) piled up and the disk filled. The cleanup must go on, and keep the newest releases as a margin.
        with tempfile.TemporaryDirectory() as tmp:
            app = Path(tmp).resolve()
            names = [f'abcdef{i}-2026100{i}120000' for i in range(1, 6)]
            for name in names:
                (app / 'releases' / name).mkdir(parents=True)
            (app / 'current').symlink_to(app / 'releases' / names[4])
            proc = app / 'proc'
            (proc / '123').mkdir(parents=True)
            (proc / '123' / 'cmdline').write_text('/proc/999/root/srv/run')
            real = os.path.realpath

            def realpath(path, *args, **kwargs):
                if str(path).startswith('/proc/999/'):
                    raise PermissionError(13, 'Permission denied', str(path))
                return real(path, *args, **kwargs)

            with patch.object(module.os.path, 'realpath', side_effect=realpath):
                removed = module.prune(app, apply=True, proc=proc, keep_newest=2)
            self.assertEqual(sorted(removed), sorted(names[:3]))
            self.assertTrue((app / 'releases' / names[3]).exists() and (app / 'releases' / names[4]).exists())

    def test_health_requires_json_from_every_service(self):
        with patch.object(module, 'urlopen', side_effect=[BytesIO(b'{"status":"healthy"}'), BytesIO(b'{"status":"unhealthy"}')]) as request:
            with self.assertRaises(RuntimeError):
                module.wait_for_health(['http://agent/api/health', 'http://workspace/health'], timeout=0)
            self.assertEqual(request.call_count, 2)
        with patch.object(module, 'urlopen', return_value=BytesIO(b'<html>SPA fallback</html>')):
            with self.assertRaises(RuntimeError):
                module.wait_for_health(['http://agent/health'], timeout=0)
        with patch.object(module, 'urlopen', return_value=BytesIO(b'{"status":"healthy"}')):
            module.wait_for_health(['http://agent/api/health'], timeout=0)

    def test_failed_health_does_not_remove_releases(self):
        with tempfile.TemporaryDirectory() as tmp:
            app = Path(tmp)
            old = app / 'releases/abcdef0-20260910120000'
            current = app / 'releases/abcdef1-20260910120000'
            old.mkdir(parents=True)
            current.mkdir()
            (app / 'current').symlink_to(current)
            args = ['prune-releases.py', str(app), '--apply', '--health-url', 'http://agent/api/health']
            with patch('sys.argv', args), patch('urllib.request.urlopen', return_value=BytesIO(b'{}')), patch('time.monotonic', side_effect=[0, 181]):
                with self.assertRaises(RuntimeError):
                    runpy.run_path(str(Path(__file__).with_name('prune-releases.py')), run_name='__main__')
            self.assertTrue(old.is_dir())
            self.assertTrue(current.is_dir())

    def test_healthy_release_with_failed_cleanup_is_only_a_warning(self):
        with tempfile.TemporaryDirectory() as tmp:
            app = Path(tmp)
            args = ['prune-releases.py', str(app), '--apply', '--health-url', 'http://agent/api/health']
            # No releases/current layout: prune() raises, but the health check passed.
            with patch('sys.argv', args), patch('urllib.request.urlopen', return_value=BytesIO(b'{"status":"healthy"}')):
                runpy.run_path(str(Path(__file__).with_name('prune-releases.py')), run_name='__main__')

    def test_health_timeout_defaults_past_first_boot(self):
        self.assertGreaterEqual(module.DEFAULT_HEALTH_TIMEOUT, 180)

    def test_only_unused_release_copies_are_removed(self):
        with tempfile.TemporaryDirectory() as tmp:
            app = Path(tmp)
            releases = app / 'releases'
            releases.mkdir()
            names = [f'{i:07x}-20260910120000' for i in range(5)]
            for name in names:
                (releases / name).mkdir()
            (app / 'current').symlink_to(releases / names[0])
            (releases / names[3] / '.deploying').touch()
            (app / 'data').mkdir()
            (releases / 'user-data').mkdir()
            (releases / 'abcdef0-20260910120000').symlink_to(app / 'data')
            proc = app / 'proc'
            (proc / '123').mkdir(parents=True)
            (proc / '123' / 'exe').symlink_to(releases / names[1] / 'bin/agent')
            (proc / '123' / 'cmdline').write_text(f'python\0{releases / names[2]}/bridge.py\0')
            self.assertEqual(module.prune(app, proc=proc), [names[4]])
            self.assertTrue((releases / names[4]).exists())
            self.assertEqual(module.prune(app, apply=True, proc=proc), [names[4]])
            self.assertFalse((releases / names[4]).exists())
            self.assertTrue((app / 'data').is_dir())
            self.assertTrue((releases / 'user-data').is_dir())
            self.assertTrue(all((releases / name).is_dir() for name in names[:4]))

    def test_a_stale_deploying_marker_does_not_pin_a_release(self):
        with tempfile.TemporaryDirectory() as tmp:
            app = Path(tmp)
            releases = app / 'releases'
            releases.mkdir()
            names = [f'{i:07x}-20260910120000' for i in range(3)]
            for name in names:
                (releases / name).mkdir()
            (app / 'current').symlink_to(releases / names[0])
            fresh, stale = releases / names[1] / '.deploying', releases / names[2] / '.deploying'
            fresh.touch()
            stale.touch()
            old = time.time() - module.DEPLOYING_MARKER_MAX_AGE_SECONDS - 60
            os.utime(stale, (old, old))
            proc = app / 'proc'
            proc.mkdir()
            self.assertEqual(module.prune(app, apply=True, proc=proc), [names[2]])
            self.assertTrue((releases / names[1]).is_dir(), 'a deploy in progress stays protected')
            self.assertFalse((releases / names[2]).exists(), 'a marker left by a finished deploy must not keep the release')

    def test_legacy_named_releases_and_running_scripts(self):
        with tempfile.TemporaryDirectory() as tmp:
            app = Path(tmp).resolve()
            for name in ('current-build', 'old-theme-fix', 'live-browser-panel'):
                (app / 'releases' / name / 'frontend').mkdir(parents=True)
                (app / 'releases' / name / 'frontend/index.html').touch()
            (app / 'current').symlink_to(app / 'releases/current-build')
            proc = app / 'proc'
            (proc / '1').mkdir(parents=True)
            (proc / '1/maps').write_text(f'000 000 000 {app}/releases/live-browser-panel/lib/browser.so')
            self.assertEqual(module.prune(app, apply=True, proc=proc), ['old-theme-fix'])
            self.assertTrue((app / 'releases/live-browser-panel').is_dir())

    def test_invalid_current_aborts_cleanup(self):
        with tempfile.TemporaryDirectory() as tmp:
            app = Path(tmp)
            (app / 'releases').mkdir()
            (app / 'data').mkdir()
            (app / 'current').symlink_to(app / 'data')
            with self.assertRaises(RuntimeError):
                module.prune(app, apply=True)


if __name__ == '__main__':
    unittest.main()

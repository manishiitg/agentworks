#!/usr/bin/env python3
"""Remove inactive deployment copies; dry-run unless --apply is supplied."""
import argparse
import fcntl
import os
from pathlib import Path
import re
import shutil
import json
import sys
import time
from urllib.request import urlopen

# A `.deploying` marker protects a release that is still being built or uploaded. A deploy that exits after the
# release went live but before its last line (a false failure) leaves the marker behind, and a marker that old
# must not pin the release forever (server C kept 15 releases, 14 GB, this way).
DEPLOYING_MARKER_MAX_AGE_SECONDS = 6 * 3600

RELEASE_NAME = re.compile(r"(?:[a-z][a-z0-9-]*-)?[0-9a-f]{7,40}-[0-9]{14}\Z")


# A first boot can run one-time state carry-over and chat migration before the
# listener opens (about a minute on server A), so wait well past that.
DEFAULT_HEALTH_TIMEOUT = 180
# Besides the live release, keep the previous one: a margin for processes that cannot be inspected and a fast rollback.
DEFAULT_KEEP_NEWEST = 2


def wait_for_health(urls, timeout=DEFAULT_HEALTH_TIMEOUT):
    deadline = time.monotonic() + timeout
    while True:
        try:
            for url in urls:
                with urlopen(url, timeout=5) as response:
                    if json.load(response).get('status') != 'healthy':
                        raise ValueError('service is not healthy')
            return
        except (OSError, ValueError, AttributeError) as error:
            if time.monotonic() >= deadline:
                raise RuntimeError('health check failed; releases were not pruned') from error
            time.sleep(1)


def active_releases(releases, proc=Path('/proc')):
    referenced = set()
    pattern = re.compile(re.escape(str(releases)) + r'''/([^/\s\x00"']+)(?:/|\s|\x00|$)''')
    for process in proc.iterdir():
        if not process.name.isdigit():
            continue
        for name in ('exe', 'cwd', 'cmdline', 'maps'):
            try:
                value = os.readlink(process / name) if name in ('exe', 'cwd') else (process / name).read_text(errors='replace')
            except (OSError, UnicodeError):
                continue  # Processes can exit during the scan; other users may be inaccessible.
            referenced.update(pattern.findall(value))
            # A process may use an alias such as /var -> /private/var or an
            # app symlink. Compare canonical absolute paths as well.
            for path in re.findall(r'''/[^\s\x00"']+''', value):
                try:
                    resolved = os.path.realpath(path)
                except OSError:
                    # Another account's /proc/<pid>/root and similar cannot be resolved (slot accounts). Before this
                    # was caught, one such path aborted the whole cleanup and server A kept every release (34 GB, disk full).
                    continue
                referenced.update(pattern.findall(resolved))
    return referenced


def deploying_in_progress(release, now=None):
    marker = release / '.deploying'
    try:
        age = (time.time() if now is None else now) - marker.stat().st_mtime
    except OSError:
        return False
    return age < DEPLOYING_MARKER_MAX_AGE_SECONDS


def newest_releases(releases, count):
    """The `count` newest release directories by the timestamp in their name (modification time for older names)."""
    def stamp(release):
        match = re.search(r'-([0-9]{14})\Z', release.name)
        return (match.group(1) if match else time.strftime('%Y%m%d%H%M%S', time.gmtime(release.stat().st_mtime)), release.name)
    candidates = [release for release in releases.iterdir() if release.is_dir() and not release.is_symlink()]
    return {release.name for release in sorted(candidates, key=stamp, reverse=True)[:max(count, 0)]}


def prune(app, apply=False, proc=Path('/proc'), keep=(), keep_newest=0):
    app = app.resolve(strict=True)
    releases = (app / 'releases').resolve(strict=True)
    current = (app / 'current').resolve(strict=True)
    if current.parent != releases or not current.is_dir():
        raise RuntimeError('current must point to a directory directly inside releases')
    for name in keep:
        if name in ('.', '..') or Path(name).name != name or (releases / name).resolve(strict=True).parent != releases:
            raise ValueError('--keep must name a release directly inside releases')
    removed = []
    with (app / '.release-cleanup.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        # Processes of other accounts cannot always be inspected, so the newest releases are kept as a margin
        # (and as the quick rollback) on top of the active one and anything a readable process still uses.
        protected = active_releases(releases, proc) | {current.name} | set(keep) | newest_releases(releases, keep_newest)
        for release in sorted(releases.iterdir()):
            if release.is_symlink() or not release.is_dir():
                continue
            # Older manual deployments used descriptive names. Require an
            # actual application artifact before treating those as releases.
            if not (RELEASE_NAME.fullmatch(release.name) or
                    any((release / 'bin' / binary).is_file() for binary in ('video-studio-agent', 'confida-agent', 'dominion-agent')) or
                    (release / 'frontend/index.html').is_file()):
                continue
            if release.name in protected or deploying_in_progress(release):
                print('keep', release.name)
                continue
            # Recheck the symlink before removal in case another deployment swapped it.
            if (app / 'current').resolve(strict=True) == release:
                continue
            print('remove' if apply else 'would remove', release.name)
            if apply:
                shutil.rmtree(release)
            removed.append(release.name)
    return removed


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('app', type=Path)
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--keep', action='append', default=[], help='Preserve this explicitly staged release for this cleanup only')
    parser.add_argument('--keep-newest', type=int, default=DEFAULT_KEEP_NEWEST, help='Always keep this many of the newest releases (the live one included)')
    parser.add_argument('--only-if-free-below-gb', type=float, help='Make room before a deploy: do nothing unless the disk has less free space than this')
    parser.add_argument('--health-url', action='append', default=[], help='Require a healthy JSON response before cleanup (repeat for each service)')
    parser.add_argument('--health-timeout', type=int, default=DEFAULT_HEALTH_TIMEOUT, help='Seconds to wait for every --health-url to report healthy')
    args = parser.parse_args()
    if args.only_if_free_below_gb is not None:
        free_gb = shutil.disk_usage(args.app).free / 1e9
        if free_gb >= args.only_if_free_below_gb:
            print(f'{free_gb:.1f} GB free: no cleanup needed before the deploy')
            sys.exit(0)
        print(f'only {free_gb:.1f} GB free: removing old release copies before the deploy')
    # An agent that never becomes healthy fails the deployment.
    wait_for_health(args.health_url, timeout=args.health_timeout)
    # The release is live and healthy; failing to delete old copies is only a
    # warning so a cleanup problem never reports a good deployment as failed.
    try:
        print('release copies removed:' if args.apply else 'unused release copies:', len(prune(args.app, args.apply, keep=args.keep, keep_newest=args.keep_newest)))
    except (OSError, RuntimeError, ValueError) as error:
        print(f'warning: release cleanup skipped: {error}', file=sys.stderr)

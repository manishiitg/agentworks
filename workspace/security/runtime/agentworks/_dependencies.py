"""Install declared packages inside the script's existing sandbox cache."""
from contextlib import contextmanager
import errno
import hashlib
import importlib.util
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time


@contextmanager
def entry_lock(path):
    with path.open("a+b") as lock:
        if os.name == "nt":
            import msvcrt
            lock.write(b"\0")
            lock.flush()
            while True:
                lock.seek(0)
                try:
                    msvcrt.locking(lock.fileno(), msvcrt.LK_NBLCK, 1)
                    break
                except OSError as error:
                    if error.errno not in (errno.EACCES, errno.EDEADLK):
                        raise
                    time.sleep(0.1)
            try:
                yield
            finally:
                lock.seek(0)
                msvcrt.locking(lock.fileno(), msvcrt.LK_UNLCK, 1)
        else:
            import fcntl
            fcntl.flock(lock, fcntl.LOCK_EX)
            yield


def prepare():
    requirements = Path(os.environ["SCRIPT_SOURCE_DIR"]) / "requirements.txt"
    target = Path(os.environ["WORKFLOW_CODE_DEPS"])
    digest = hashlib.sha256(requirements.read_bytes()).hexdigest()
    if target.name != digest:
        raise RuntimeError("The saved package requirements changed after launch. Start a new run from the Crew chat.")
    target.parent.mkdir(parents=True, exist_ok=True)
    with entry_lock(target.with_suffix(".lock")):
        if target.is_dir():
            print("[packages] Reusing this script's installed requirements.", flush=True)
            return
        uv = Path(__file__).resolve().parent.parent / ("uv.exe" if os.name == "nt" else "uv")
        if uv.is_file():
            name = "uv"
            installer = [str(uv), "pip", "install", "--python", sys.executable, "--no-python-downloads", "--no-progress"]
        elif importlib.util.find_spec("pip") is not None:
            name = "pip"
            # pip scans every interpreter import location for metadata. Do not
            # make it scan the caller's PYTHONPATH (which can contain a parent
            # scratch directory outside this command's existing read grant).
            installer = [sys.executable, "-I", "-m", "pip", "install", "--no-input", "--disable-pip-version-check", "--no-compile"]
        else:
            raise RuntimeError("Python package setup is unavailable: uv and pip are missing. Ask your administrator to update the app's Python tools, then run it again.")
        temporary = Path(tempfile.mkdtemp(prefix=digest + ".install-", dir=target.parent))
        try:
            print(f"[packages] Installing this script's requirements with {name}.", flush=True)
            subprocess.run([*installer, "--target", str(temporary), "--requirement", str(requirements)], check=True)
            # Normal program writes cannot replace installed package files.
            # The owner can deliberately change their own modes; this is not
            # an isolation boundary against a hostile same-UID program.
            for directory, folders, files in os.walk(temporary, topdown=False):
                for name in files:
                    path = Path(directory) / name
                    path.chmod(0o550 if path.stat().st_mode & 0o111 else 0o440)
                for name in folders:
                    (Path(directory) / name).chmod(0o550)
            temporary.chmod(0o550)
            temporary.rename(target)
        finally:
            if temporary.exists():
                # Restore our unfinished temporary entry's write bits before
                # cleanup; a failed install never becomes a reusable entry.
                for directory, _, _ in os.walk(temporary):
                    Path(directory).chmod(0o750)
                shutil.rmtree(temporary)


if __name__ == "__main__":
    try:
        prepare()
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"[packages] {error}", file=sys.stderr, flush=True)
        sys.exit(1)

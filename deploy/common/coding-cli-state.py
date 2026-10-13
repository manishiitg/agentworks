"""Non-secret successful tool versions and installation policy, outside releases."""
import json
import os
from pathlib import Path
import sys
import tempfile
import time


def main():
    action, filename, policy, version = sys.argv[1:]
    if not version or len(version) > 4096:
        return 1
    path = Path(filename)
    expected = {"schema": 1, "policy": policy, "version": version}
    if action == "check":
        try:
            actual = json.loads(path.read_text())
            recorded_at = actual.pop("recorded_at")
            if actual != expected or not isinstance(recorded_at, (int, float)):
                return 1
            if not 0 < recorded_at <= time.time() + 60:
                return 1
            if time.time() - recorded_at > 14 * 24 * 3600:
                print(f"WARN: {path.stem} tool receipt is older than 14 days; run with DEPLOY_REFRESH_TOOLS=1", file=sys.stderr)
            return 0
        except FileNotFoundError:
            return 2
        except (OSError, ValueError, KeyError, TypeError, AttributeError):
            return 1
    if action != "write":
        return 1
    fd, temporary = tempfile.mkstemp(dir=path.parent, prefix=".tool-")
    try:
        with os.fdopen(fd, "w") as output:
            expected["recorded_at"] = time.time()
            json.dump(expected, output)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    return 0


if __name__ == "__main__":
    sys.exit(main())

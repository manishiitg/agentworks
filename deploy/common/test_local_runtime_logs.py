"""Run the real startup preparation with fixture npm, without any app processes."""
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[2]
SOURCE = (REPO / "agent_go/run_server_with_logging.sh").read_text()


def function(name):
    start = SOURCE.index(name + "() {\n")
    body = start + len(name + "() {\n")
    following = re.search(r"^[a-zA-Z_][a-zA-Z_0-9]*\(\) \{", SOURCE[body:], re.M)
    end = body + following.start() if following else len(SOURCE)
    return SOURCE[start:end]


class LocalRuntimeLogs(unittest.TestCase):
    def test_backend_only_prepares_assets_and_preserves_existing_frontend_log(self):
        for directory in ("logs", "runtime logs"):
            with self.subTest(directory=directory), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                agent, frontend, binaries = (root / name for name in ("agent_go", "frontend", "bin"))
                for path in (agent, frontend, binaries, agent / directory):
                    path.mkdir(parents=True, exist_ok=True)
                (frontend / "package.json").write_text('{"dependencies":{},"devDependencies":{}}')
                (frontend / "package-lock.json").write_text('{"lockfileVersion":3}')
                log = agent / directory / "frontend_debug.log"
                log.write_text("existing Vite log\n")
                npm = binaries / "npm"
                npm.write_text('''#!/bin/bash
echo "fixture npm $*"
mkdir -p node_modules/.bin
printf '#!/bin/sh\nexit 0\n' > node_modules/.bin/vite
chmod +x node_modules/.bin/vite
''')
                npm.chmod(0o755)
                initialization = SOURCE[SOURCE.index('WORKSPACE_PID=""'):]
                assignment = re.search(r'^FRONTEND_LOG_PATH=.*$', initialization, re.M).group()
                start = SOURCE.index("# The agent needs standalone dashboard/preview assets")
                preparation = SOURCE[start:SOURCE.index("\nfi\n", start) + 4]
                script = "\n".join([
                    "set -e", function("frontend_deps_present"), function("ensure_frontend_deps"),
                    assignment, preparation,
                ])
                environment = {**os.environ, "WITH_FRONTEND": "false", "LOG_DIR": directory,
                               "FRONTEND_DIR": str(frontend), "PATH": str(binaries) + os.pathsep + os.environ["PATH"]}
                result = subprocess.run(["bash", "-c", script], cwd=agent, env=environment,
                                        text=True, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("existing Vite log", log.read_text())
                self.assertIn("fixture npm install", log.read_text())
                self.assertIn("fixture npm run prepare:local-assets", log.read_text())
                self.assertTrue((frontend / "node_modules/.agentworks-deps-stamp").is_file())


if __name__ == "__main__":
    unittest.main()

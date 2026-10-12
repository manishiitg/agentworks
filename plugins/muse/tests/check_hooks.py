"""Exercise the executable hook boundary with an isolated fake shared CLI.

This does not claim Muse acceptance or authenticated delivery. Run with python3.
"""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile


PLUGIN = Path(__file__).resolve().parents[1]
SESSION = "4c67acbd-e8c1-4f8a-9c26-9d4feeb403d7"


def main():
    catalog = json.loads((PLUGIN.parents[1] / ".agents/plugins/marketplace.json").read_text())
    entries = {entry["name"]: entry for entry in catalog["plugins"]}
    assert len(entries) == len(catalog["plugins"]), "Duplicate marketplace plugin IDs"
    assert entries["agentworks-muse"]["source"] == {"source": "local", "path": "./plugins/muse"}
    assert entries["agentworks-codex"]["source"] == {"source": "local", "path": "./plugins/codex"}
    with tempfile.TemporaryDirectory(prefix="agentworks-muse-hooks-") as directory:
        scratch = Path(directory)
        cli = scratch / "agentworks"
        cli.write_text(
            "#!" + sys.executable + "\n"
            "import json, os, pathlib, sys\n"
            "assert sys.argv[1:] == ['plugin', 'hook', 'muse']\n"
            "payload = json.load(sys.stdin)\n"
            "pathlib.Path(os.environ['MUSE_TEST_CALL']).write_text(json.dumps(payload))\n"
            "if os.environ.get('MUSE_TEST_FAIL'): sys.exit(1)\n"
            "if os.environ.get('MUSE_TEST_SILENT'): sys.exit(0)\n"
            "print(json.dumps({'decision':'block','reason':'Fixture message; no receipt claimed'}))\n"
        )
        cli.chmod(0o700)
        calls = scratch / "call.json"
        env = dict(os.environ, PATH=str(scratch) + os.pathsep + os.environ["PATH"],
                   MUSE_TEST_CALL=str(calls), PYTHONDONTWRITEBYTECODE="1")

        def invoke(script, payload, extra=None):
            calls.unlink(missing_ok=True)
            raw = payload if isinstance(payload, bytes) else json.dumps(payload).encode()
            result = subprocess.run([sys.executable, str(PLUGIN / "hooks" / script)],
                                    input=raw, capture_output=True, env={**env, **(extra or {})},
                                    timeout=10, check=True)
            assert result.stderr == b"", result.stderr
            return result.stdout, calls.exists()

        stop = {"session_id": SESSION, "hook_event_name": "Stop", "stop_hook_active": False}
        output, called = invoke("stop.py", stop)
        assert called and json.loads(output)["decision"] == "block"
        assert json.loads(calls.read_text()) == stop
        # A real Stop continuation must never consume a second message batch.
        assert invoke("stop.py", {**stop, "stop_hook_active": True}) == (b"", False)
        for malformed in (b"{", b"[]", b"x" * 65537, {**stop, "session_id": ""},
                          {**stop, "session_id": "another-session"},
                          {**stop, "hook_event_name": "UserPromptSubmit"}):
            assert invoke("stop.py", malformed) == (b"", False)
        assert invoke("stop.py", stop, {"MUSE_TEST_SILENT": "1"}) == (b"", True)
        assert invoke("stop.py", stop, {"MUSE_TEST_FAIL": "1"}) == (b"", True)
        end = {**stop, "hook_event_name": "SessionEnd"}
        assert invoke("session_end.py", end) == (b"", True)
        control = {**stop, "hook_event_name": "UserPromptSubmit", "prompt": "/agentworks status"}
        output, called = invoke("prompt_submit.py", control, {"MUSE_TEST_FAIL": "1"})
        assert called and json.loads(output)["decision"] == "block"
        assert "No attachment change was confirmed" in json.loads(output)["reason"]
    print("PASSED: executable hook guards, exact UUID forwarding, silent offline hooks and control refusal")


if __name__ == "__main__":
    main()
